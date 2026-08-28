package dolt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// backupOf builds a ledger with the given rows and returns a backup URL for it.
func backupOf(t *testing.T, rows int) (dest string, commits int) {
	t.Helper()
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)
	store, err := Open(context.Background(), loc, Config{Command: "seed"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	seedRows(t, store, rows)
	commits = countRows(t, store.DB(), "SELECT COUNT(*) FROM dolt_log")

	dest = filepath.Join(t.TempDir(), "backup")
	if _, err := store.Backup(context.Background(), dest); err != nil {
		t.Fatalf("backup: %v", err)
	}
	return dest, commits
}

// TestRestoreSwapIsAtomic: the first rename is the commit point, so a restore
// either replaces the ledger completely or leaves the previous one working.
// Nothing in between is a legal outcome.
func TestRestoreSwapIsAtomic(t *testing.T) {
	ctx := context.Background()
	dest, _ := backupOf(t, 4)

	// A live ledger with different content, so a partial swap would be visible.
	target := fixtureRepo(t)
	targetLoc := initLedger(t, target, false)
	existing, err := Open(ctx, targetLoc, Config{Command: "seed-target"})
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	seedRows(t, existing, 9)
	if err := existing.Close(); err != nil {
		t.Fatalf("close target: %v", err)
	}

	// Refusing without --force is what makes the destructive step deliberate.
	if _, err := Restore(ctx, targetLoc, dest, RestoreOptions{}); err == nil {
		t.Fatal("restore over an existing ledger succeeded without --force")
	}
	if store := openLedger(t, targetLoc, Config{Command: "check-refused"}); countRows(t, store.DB(), "SELECT COUNT(*) FROM round_trip") != 9 {
		t.Fatal("the refused restore changed the ledger")
	} else if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := Restore(ctx, targetLoc, dest, RestoreOptions{Force: true}); err != nil {
		t.Fatalf("forced restore: %v", err)
	}
	swapped := openLedger(t, targetLoc, Config{Command: "check-swapped"})
	if got := countRows(t, swapped.DB(), "SELECT COUNT(*) FROM round_trip"); got != 4 {
		t.Fatalf("restored ledger has %d rows, want the backup's 4", got)
	}
	if leftovers := restoreLeftovers(targetLoc); len(leftovers) != 0 {
		t.Fatalf("a completed restore left %v behind", leftovers)
	}
	if err := swapped.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// A restore that fails before the swap must leave the live ledger untouched.
	if _, err := Restore(ctx, targetLoc, filepath.Join(t.TempDir(), "absent"), RestoreOptions{Force: true}); err == nil {
		t.Fatal("restore from a nonexistent backup succeeded")
	}
	survivor := openLedger(t, targetLoc, Config{Command: "check-survivor"})
	if got := countRows(t, survivor.DB(), "SELECT COUNT(*) FROM round_trip"); got != 4 {
		t.Fatalf("a failed restore left %d rows, want the previous 4", got)
	}
	if leftovers := restoreLeftovers(targetLoc); len(leftovers) != 0 {
		t.Fatalf("a failed restore left %v behind", leftovers)
	}
}

// TestKilledRestoreLeavesOriginalIntact covers both halves of the guarantee: a
// process killed mid-restore leaves a recoverable ledger on disk, and whatever
// it leaves behind is reported rather than silently ignored.
func TestKilledRestoreLeavesOriginalIntact(t *testing.T) {
	ctx := context.Background()
	dest, _ := backupOf(t, 3)

	target := fixtureRepo(t)
	targetLoc := initLedger(t, target, false)
	original, err := Open(ctx, targetLoc, Config{Command: "seed-target"})
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	seedRows(t, original, 7)
	if err := original.Close(); err != nil {
		t.Fatalf("close target: %v", err)
	}

	// Time an uninterrupted restore so the kill lands inside the real work
	// rather than at an arbitrary point outside it.
	reference := fixtureRepo(t)
	referenceLoc, _ := Discover(ctx, reference)
	started := time.Now()
	if _, err := Restore(ctx, referenceLoc, dest, RestoreOptions{}); err != nil {
		t.Fatalf("reference restore: %v", err)
	}
	killAfter := time.Since(started) / 2

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), restoreEnv+"="+targetLoc.Dir, restoreSrcEnv+"="+dest)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the restorer: %v", err)
	}
	time.Sleep(killAfter)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	// Exactly one of two states is legal: the live ledger works, or it is gone
	// and doctor names the copy that still holds the data.
	leftovers := restoreLeftovers(targetLoc)
	if ledgerExists(targetLoc.Dir) {
		store := openLedger(t, targetLoc, Config{Command: "check-killed"})
		rows := countRows(t, store.DB(), "SELECT COUNT(*) FROM round_trip")
		if rows != 7 && rows != 3 {
			t.Fatalf("live ledger has %d rows, neither the original 7 nor the backup's 3", rows)
		}
		report, err := store.Diagnose(ctx)
		if err != nil {
			t.Fatalf("diagnose: %v", err)
		}
		assertLeftoversReported(t, report, leftovers)
		if err := store.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		return
	}

	var recovered bool
	for _, path := range leftovers {
		if ledgerExists(path) {
			recovered = true
		}
	}
	if !recovered {
		t.Fatalf("no live ledger at %s and no recoverable copy in %v", targetLoc.Dir, leftovers)
	}
	assertLeftoversReported(t, DiagnoseUnopened(targetLoc, nil), leftovers)
}

func assertLeftoversReported(t *testing.T, report StoreReport, leftovers []string) {
	t.Helper()
	check := findCheck(t, report, "restore_leftovers")
	if len(leftovers) == 0 {
		if check.Status != StatusOK {
			t.Fatalf("no leftovers on disk but the check says %q: %s", check.Status, check.Detail)
		}
		return
	}
	if check.Status != StatusWarn {
		t.Fatalf("leftovers %v on disk but the check says %q", leftovers, check.Status)
	}
	for _, path := range leftovers {
		if !strings.Contains(check.Detail, path) {
			t.Fatalf("the check does not name %s: %s", path, check.Detail)
		}
	}
}
