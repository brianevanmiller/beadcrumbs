package dolt

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
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
	if leftovers := leftoversOf(t, targetLoc); len(leftovers) != 0 {
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
	if leftovers := leftoversOf(t, targetLoc); len(leftovers) != 0 {
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
	leftovers := leftoversOf(t, targetLoc)
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

// leftoversOf reads the interrupted-restore copies on disk. An unreadable
// parent is a test failure rather than an empty answer, which is the same
// distinction the check itself now makes.
func leftoversOf(t *testing.T, loc Location) []string {
	t.Helper()
	out, err := restoreLeftovers(loc)
	if err != nil {
		t.Fatalf("reading restore leftovers for %s: %v", loc.Dir, err)
	}
	return out
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

// TestRestoreRefusesWhileAnotherProcessHoldsTheLedger: the swap renames the
// live directory and then deletes it, and POSIX lets that happen under an open
// handle. A holder must therefore make the restore exit 4 rather than survive
// pointing at inodes nothing will ever read again.
func TestRestoreRefusesWhileAnotherProcessHoldsTheLedger(t *testing.T) {
	ctx := context.Background()
	dest, _ := backupOf(t, 4)

	target := fixtureRepo(t)
	targetLoc := initLedger(t, target, false)
	existing, err := Open(ctx, targetLoc, Config{Command: "seed-target"})
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	seedRows(t, existing, 6)
	if err := existing.Close(); err != nil {
		t.Fatalf("close target: %v", err)
	}

	holdLedger(t, targetLoc.Dir)
	if _, err := Restore(ctx, targetLoc, dest, RestoreOptions{Force: true}); !errors.Is(err, ledger.ErrBusy) {
		t.Fatalf("restore err = %v, want ledger busy while another process holds %s", err, targetLoc.Dir)
	}
	if leftovers := leftoversOf(t, targetLoc); len(leftovers) != 0 {
		t.Fatalf("the refused restore left %v behind", leftovers)
	}
}

// TestRestoreRefusesANewerSchema: the check has to happen while the staging
// copy is still discardable. Afterwards the previous ledger has been removed,
// so a mismatch is only reportable — and doctor's remediation cannot undo it.
func TestRestoreRefusesANewerSchema(t *testing.T) {
	ctx := context.Background()

	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)
	source, err := Open(ctx, loc, Config{Command: "seed-future"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	seedRows(t, source, 2)
	future := CurrentSchemaVersion() + 1
	if _, err := source.DB().ExecContext(ctx,
		`REPLACE INTO schema_meta (id, version, bdc_version, applied_at) VALUES (1, ?, '9.9.9', UTC_TIMESTAMP(6))`,
		future); err != nil {
		t.Fatalf("recording a future schema version: %v", err)
	}
	if err := source.Commit(ctx, "test: future schema"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "backup")
	if _, err := source.Backup(ctx, dest); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	target := fixtureRepo(t)
	targetLoc := initLedger(t, target, false)
	existing, err := Open(ctx, targetLoc, Config{Command: "seed-target"})
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	seedRows(t, existing, 5)
	if err := existing.Close(); err != nil {
		t.Fatalf("close target: %v", err)
	}

	if _, err := Restore(ctx, targetLoc, dest, RestoreOptions{Force: true}); !errors.Is(err, ledger.ErrIntegrity) {
		t.Fatalf("restore err = %v, want an integrity error for schema version %d", err, future)
	}
	survivor := openLedger(t, targetLoc, Config{Command: "check-survivor"})
	if got := countRows(t, survivor.DB(), "SELECT COUNT(*) FROM round_trip"); got != 5 {
		t.Fatalf("the refused restore left %d rows, want the previous 5", got)
	}
	if leftovers := leftoversOf(t, targetLoc); len(leftovers) != 0 {
		t.Fatalf("the refused restore left %v behind", leftovers)
	}
}
