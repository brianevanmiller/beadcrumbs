package dolt

import (
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// concurrentWriters is the release gate's figure (plan §6, "8 processes, zero
// loss, bounded wait"): enough that the lock is contended for most of the run,
// few enough that the test stays inside a normal `go test` budget.
const concurrentWriters = 8

// TestConcurrentShortLivedWriters is the release gate for "concurrent readers
// and writers behave deterministically". Embedded Dolt takes an exclusive lock
// on the ledger directory, so concurrency is between processes and the property
// to prove is that contention costs time and nothing else: every writer waits,
// every writer lands, and none of them reports the ledger busy.
func TestConcurrentShortLivedWriters(t *testing.T) {
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}

	started := time.Now()
	var wg sync.WaitGroup
	failures := make(chan string, concurrentWriters)
	for i := 0; i < concurrentWriters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(exe)
			cmd.Env = append(os.Environ(),
				writeEnv+"="+loc.Dir, writeTagEnv+"="+tagFor(i))
			out, err := cmd.CombinedOutput()
			if err != nil {
				failures <- string(out)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for out := range failures {
		t.Fatalf("a writer failed rather than waiting for the lock:\n%s", out)
	}
	t.Logf("%d serialised writers took %s", concurrentWriters, time.Since(started).Round(time.Millisecond))

	store := openLedger(t, loc, Config{Command: "check"})
	if got := countRows(t, store.DB(), "SELECT COUNT(*) FROM crumbs"); got != concurrentWriters {
		t.Fatalf("%d of %d concurrent writes landed", got, concurrentWriters)
	}
	// One Dolt commit per write transaction, so a lost commit is as visible as
	// a lost row: the history is what backup and restore carry.
	if got := countRows(t, store.DB(),
		"SELECT COUNT(*) FROM dolt_log WHERE message = 'bdc test-writer'"); got != concurrentWriters {
		t.Fatalf("%d of %d writers left a commit", got, concurrentWriters)
	}
	if got := countRows(t, store.DB(), "SELECT COUNT(DISTINCT content_hash) FROM crumbs"); got != concurrentWriters {
		t.Fatalf("%d distinct rows from %d writers: a write overwrote another", got, concurrentWriters)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func tagFor(i int) string { return string(rune('a' + i)) }
