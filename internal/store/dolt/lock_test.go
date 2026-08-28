package dolt

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSecondOpenInProcessPanics: embedded Dolt holds an exclusive lock for the
// engine's whole lifetime, so a second Open in the same process cannot be
// contention — it is a bug in our code. Crash, don't trash.
func TestSecondOpenInProcessPanics(t *testing.T) {
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)
	openLedger(t, loc, Config{Command: "first"})

	defer func() {
		p := recover()
		if p == nil {
			t.Fatal("a second Open returned instead of panicking")
		}
		msg, ok := p.(string)
		if !ok || !strings.Contains(msg, "second dolt.Open") {
			t.Fatalf("panic does not name the violated invariant: %v", p)
		}
		if !ok || !strings.Contains(msg, "first") {
			t.Fatalf("panic does not name the command still holding the handle: %v", p)
		}
	}()
	_, _ = Open(context.Background(), loc, Config{Command: "second"})
}

// TestHeldEngineWatchdogFires: "no engine across agent turns" is unenforceable
// as a convention, so it is a live assertion that reports in production builds.
func TestHeldEngineWatchdogFires(t *testing.T) {
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)

	violations := make(chan Violation, 1)
	store := openLedger(t, loc, Config{
		Command:     "capture",
		MaxOpenHold: 50 * time.Millisecond,
		OnViolation: func(v Violation) { violations <- v },
	})

	select {
	case v := <-violations:
		if v.Kind != "engine_held_too_long" {
			t.Fatalf("violation kind is %q", v.Kind)
		}
		if v.Command != "capture" {
			t.Fatalf("violation does not name the command: %+v", v)
		}
		if v.Dir != loc.Dir {
			t.Fatalf("violation names %s, want %s", v.Dir, loc.Dir)
		}
		if v.Held < 50*time.Millisecond {
			t.Fatalf("violation reports a hold of %s, shorter than the limit", v.Held)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watchdog never fired")
	}

	// Closing stops the watchdog: a closed handle must not keep reporting.
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case v := <-violations:
		t.Fatalf("the watchdog fired again after Close: %+v", v)
	case <-time.After(200 * time.Millisecond):
	}
}

// A closed handle must release the process guard, or the next command in the
// same process would panic on a legitimate Open.
func TestCloseReleasesTheProcessGuard(t *testing.T) {
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)

	first, err := Open(context.Background(), loc, Config{Command: "first"})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Close is idempotent, and the second call must not double-release.
	if err := first.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	openLedger(t, loc, Config{Command: "second"})
}
