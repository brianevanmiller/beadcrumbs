package dolt

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Violation is an invariant breach detected at runtime, in production builds.
// It is not a user error: it names a bug in how Beadcrumbs held the engine.
type Violation struct {
	Kind    string        // "engine_held_too_long"
	Command string        // the invocation that held it
	Dir     string        // the ledger directory
	Held    time.Duration // how long the handle had been open when the check fired
}

func (v Violation) String() string {
	return fmt.Sprintf("beadcrumbs: invariant violation: %s: command=%s ledger=%s held=%s",
		v.Kind, v.Command, v.Dir, v.Held.Round(time.Millisecond))
}

// The process-level open guard. Embedded Dolt holds an exclusive lock on the
// database directory for the life of the engine, so at most one handle may be
// live per process. A second Open is a programming error, not contention.
var (
	openMu      sync.Mutex
	openDir     string
	openCommand string
	openLive    bool
)

func acquireProcessLock(dir, command string) (release func()) {
	openMu.Lock()
	defer openMu.Unlock()
	if openLive {
		// Crash, don't trash: continuing would either deadlock on the directory
		// lock or interleave two sessions inside one bounded transaction.
		panic(fmt.Sprintf("beadcrumbs: second dolt.Open in this process: %q wants %s while %q still holds %s",
			command, dir, openCommand, openDir))
	}
	openLive, openDir, openCommand = true, dir, command

	var once sync.Once
	return func() {
		once.Do(func() {
			openMu.Lock()
			openLive, openDir, openCommand = false, "", ""
			openMu.Unlock()
		})
	}
}

// watchdog reports if a handle outlives Config.MaxOpenHold. It does not close
// the engine: tearing a live transaction down would trade a reported invariant
// breach for a silent one.
type watchdog struct {
	done chan struct{}
	once sync.Once
}

func startWatchdog(cfg Config, dir string) *watchdog {
	w := &watchdog{done: make(chan struct{})}
	started := time.Now()
	report := cfg.OnViolation
	if report == nil {
		report = func(v Violation) { fmt.Fprintln(os.Stderr, v) }
	}
	timer := time.NewTimer(cfg.MaxOpenHold)
	go func() {
		defer timer.Stop()
		select {
		case <-w.done:
		case <-timer.C:
			report(Violation{
				Kind:    "engine_held_too_long",
				Command: cfg.Command,
				Dir:     dir,
				Held:    time.Since(started),
			})
		}
	}()
	return w
}

func (w *watchdog) stop() { w.once.Do(func() { close(w.done) }) }
