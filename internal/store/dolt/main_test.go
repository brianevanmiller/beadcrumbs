package dolt

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// holdEnv turns the test binary into a process that opens a ledger and holds the
// directory lock. "Locked by another process" is not reachable in-process — a
// second Open panics by design — so the only honest way to test it is a second
// process, and re-executing the test binary is the cheapest one available.
const holdEnv = "BDC_TEST_HOLD_LEDGER"

// restoreEnv and restoreSrcEnv turn the test binary into a process that runs a
// restore, so the parent can kill it partway through.
const (
	restoreEnv    = "BDC_TEST_RESTORE"
	restoreSrcEnv = "BDC_TEST_RESTORE_SRC"
)

// writeEnv turns the test binary into one short-lived writer: open, one bounded
// transaction, close. Concurrency is between processes because that is the only
// place it exists — the engine's lock is per directory, and a second Open in one
// process panics by design.
const (
	writeEnv    = "BDC_TEST_WRITE"
	writeTagEnv = "BDC_TEST_WRITE_TAG"
)

// killEnv turns the test binary into a process that writes inside a transaction
// and then waits to be killed, so the parent can prove an interrupted
// transaction leaves nothing behind.
const killEnv = "BDC_TEST_KILL_MID_TX"

func TestMain(m *testing.M) {
	if dir := os.Getenv(holdEnv); dir != "" {
		os.Exit(runHolder(dir))
	}
	if dir := os.Getenv(restoreEnv); dir != "" {
		os.Exit(runRestorer(dir, os.Getenv(restoreSrcEnv)))
	}
	if dir := os.Getenv(writeEnv); dir != "" {
		os.Exit(runWriter(dir, os.Getenv(writeTagEnv)))
	}
	if dir := os.Getenv(killEnv); dir != "" {
		os.Exit(runKilled(dir))
	}
	os.Exit(m.Run())
}

func runHolder(dir string) int {
	store, err := Open(context.Background(), Location{Dir: dir, Stealth: true}, Config{
		MaxOpenWait: 5 * time.Second,
		MaxOpenHold: time.Hour, // the point of this process is to hold it
		Command:     "test-holder",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "holder:", err)
		return 1
	}
	defer store.Close()
	fmt.Println("READY")
	// Blocks until the parent closes our stdin, which is how it says "let go".
	_, _ = io.Copy(io.Discard, os.Stdin)
	return 0
}

func runRestorer(dir, src string) int {
	if _, err := Restore(context.Background(), Location{Dir: dir, Stealth: true}, src, RestoreOptions{Force: true}); err != nil {
		fmt.Fprintln(os.Stderr, "restorer:", err)
		return 1
	}
	return 0
}

// runWriter is one whole `bdc` invocation's worth of storage work: open the
// ledger, write one Crumb in one transaction, close. It waits for the lock
// rather than failing, which is what a real command does.
func runWriter(dir, tag string) int {
	store, err := Open(context.Background(), Location{Dir: dir, Stealth: true}, Config{
		MaxOpenWait: 2 * time.Minute,
		MaxOpenHold: time.Minute,
		Command:     "test-writer",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "writer:", err)
		return 1
	}
	defer store.Close()
	if err := store.Write(context.Background(), func(tx ledger.Tx) error {
		return tx.InsertCrumb(testCrumb(tag))
	}); err != nil {
		fmt.Fprintln(os.Stderr, "writer:", err)
		return 1
	}
	return 0
}

// runKilled writes inside a transaction and then blocks forever. The parent
// kills it with SIGKILL, which is the only way to interrupt a process between
// the writes and the commit.
func runKilled(dir string) int {
	store, err := Open(context.Background(), Location{Dir: dir, Stealth: true}, Config{
		MaxOpenWait: 30 * time.Second,
		MaxOpenHold: time.Hour,
		Command:     "test-killed",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "killed:", err)
		return 1
	}
	defer store.Close()
	_ = store.Write(context.Background(), func(tx ledger.Tx) error {
		for i := 0; i < 3; i++ {
			if err := tx.InsertCrumb(testCrumb(fmt.Sprintf("partial-%d", i))); err != nil {
				return err
			}
		}
		fmt.Println("WROTE")
		select {}
	})
	return 0
}

// testCrumb is one valid Crumb, distinct per tag: uq_crumbs_hash_session is on
// (content_hash, session_id), so two writers must not mint the same pair.
func testCrumb(tag string) ledger.Crumb {
	sum := sha256.Sum256([]byte(tag))
	return ledger.Crumb{
		ID: ledger.NewCrumbID(), Content: "written by " + tag,
		ContentHash: hex.EncodeToString(sum[:]), ReviewState: ledger.StateCandidate,
		Confidence: 0.5, CapturedAt: time.Now().UTC().Truncate(time.Microsecond),
		RedactionVersion: "1",
		Provenance:       ledger.Provenance{ActorID: "tester", ActorKind: ledger.ActorHuman},
	}
}

// holdLedger starts a second process holding dir's lock and returns once it is
// confirmed held. The lock is released at test cleanup.
func holdLedger(t *testing.T, dir string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), holdEnv+"="+dir)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("holder stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("holder stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the holder: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- strings.TrimSpace(line)
	}()
	select {
	case line := <-ready:
		if line != "READY" {
			t.Fatalf("holder did not take the lock, said %q", line)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("holder never reported READY")
	}
}

// fixtureRepo is a fresh Git repository with one commit, which every ledger test
// needs because discovery is structural.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	git(t, repo, "init", "-q", ".")
	git(t, repo, "config", "user.email", "fixture@example.test")
	git(t, repo, "config", "user.name", "fixture")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "root")
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("resolving the fixture path: %v", err)
	}
	return resolved
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return out
}

// fakeLedger creates only the marker that makes ledgerExists true. Discovery is
// pure path resolution and never opens an engine, so its tests do not need one.
func fakeLedger(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dbPath(dir), 0o700); err != nil {
		t.Fatalf("creating the fake ledger: %v", err)
	}
}

// initLedger creates a real ledger in repo and returns its Location.
func initLedger(t *testing.T, repo string, visible bool) Location {
	t.Helper()
	loc, err := Discover(context.Background(), repo)
	if err == nil {
		t.Fatalf("expected %s to have no ledger yet", repo)
	}
	loc = loc.Resolve(visible)
	if _, err := Init(context.Background(), loc, InitOptions{Visible: visible}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return loc
}

// openLedger opens a real ledger and closes it at cleanup, so the process-level
// open guard is never left tripped for the next test.
func openLedger(t *testing.T, loc Location, cfg Config) *Store {
	t.Helper()
	if cfg.Command == "" {
		cfg.Command = t.Name()
	}
	store, err := Open(context.Background(), loc, cfg)
	if err != nil {
		t.Fatalf("open %s: %v", loc.Dir, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// seedRows writes a scratch table with rows and one Dolt commit per row, which
// is what grows the journal and what backup and restore have to carry.
func seedRows(t *testing.T, store *Store, rows int) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.DB().ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec("CREATE TABLE IF NOT EXISTS round_trip (id INT PRIMARY KEY, note VARCHAR(64))")
	for i := 0; i < rows; i++ {
		exec("INSERT INTO round_trip (id, note) VALUES (?, ?)", i, fmt.Sprintf("row-%d", i))
		if err := store.Commit(ctx, fmt.Sprintf("test: row %d", i)); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
}

func countRows(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
