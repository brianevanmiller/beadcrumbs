package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

type recordingCloser struct {
	closes int
	err    error
}

func (c *recordingCloser) Close() error {
	c.closes++
	return c.err
}

// TestCloseRunsOnErrorPanicAndSignal: embedded Dolt holds an exclusive directory
// lock for the engine's whole life, so a missed Close leaves the repository's
// ledger unusable until the process dies. Close is therefore unconditional, and
// this asserts every path that could skip it.
func TestCloseRunsOnErrorPanicAndSignal(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c := &recordingCloser{}
		if err := closeAfter(closerOf(c), func() error { return nil }); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertClosedOnce(t, c)
	})

	t.Run("error", func(t *testing.T) {
		c := &recordingCloser{}
		want := errors.New("command failed")
		if err := closeAfter(closerOf(c), func() error { return want }); !errors.Is(err, want) {
			t.Fatalf("error was swallowed: %v", err)
		}
		assertClosedOnce(t, c)
	})

	t.Run("close error surfaces when the command succeeded", func(t *testing.T) {
		c := &recordingCloser{err: errors.New("close failed")}
		if err := closeAfter(closerOf(c), func() error { return nil }); !errors.Is(err, c.err) {
			t.Fatalf("a close failure was lost: %v", err)
		}
	})

	t.Run("close error never masks the command error", func(t *testing.T) {
		c := &recordingCloser{err: errors.New("close failed")}
		want := errors.New("command failed")
		if err := closeAfter(closerOf(c), func() error { return want }); !errors.Is(err, want) {
			t.Fatalf("the close failure masked the command failure: %v", err)
		}
	})

	t.Run("panic", func(t *testing.T) {
		c := &recordingCloser{}
		func() {
			defer func() {
				if recover() == nil {
					t.Error("the panic was swallowed instead of re-raised")
				}
			}()
			_ = closeAfter(closerOf(c), func() error { panic("boom") })
		}()
		assertClosedOnce(t, c)
	})

	t.Run("no ledger open", func(t *testing.T) {
		if err := closeAfter(func() io.Closer { return nil }, func() error { return nil }); err != nil {
			t.Fatalf("unexpected error with no ledger open: %v", err)
		}
	})

	t.Run("signal", func(t *testing.T) {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()

		c := &recordingCloser{}
		err := closeAfter(closerOf(c), func() error {
			if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
				t.Fatalf("cannot signal this process: %v", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				t.Fatal("SIGTERM did not cancel the command context")
				return nil
			}
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected the cancelled context, got %v", err)
		}
		assertClosedOnce(t, c)
	})
}

// closerOf mirrors app.closer: the ledger is opened inside the command, so the
// closer is resolved lazily rather than passed in.
func closerOf(c *recordingCloser) func() io.Closer {
	return func() io.Closer { return c }
}

func assertClosedOnce(t *testing.T, c *recordingCloser) {
	t.Helper()
	if c.closes != 1 {
		t.Fatalf("Close ran %d times, want exactly 1", c.closes)
	}
}

// app.closer must not hand back a typed nil: a nil *dolt.Store inside an
// io.Closer would pass a nil check and then panic on Close.
func TestCloserIsNilWhenNoStoreIsOpen(t *testing.T) {
	a := newApp(io.Discard, io.Discard)
	if c := a.closer(); c != nil {
		t.Fatalf("closer returned %#v with no store open", c)
	}
}
