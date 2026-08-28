package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Args[1:]))
}

// run is main without os.Exit, so the lifecycle guarantees below are testable.
// No command body may call os.Exit: that would skip the close.
func run(stdout, stderr io.Writer, args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := newApp(stdout, stderr)
	root := a.newRootCommand()
	root.SetArgs(args)
	// Help is prose, but it is also the requested result, so it goes to stdout.
	// Only --json makes stdout a machine surface, and help is never JSON.
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := closeAfter(a.closer, func() error { return root.ExecuteContext(ctx) })
	if err == nil && !a.bodyRan {
		// --help and shell completion print their own output and are not
		// domain results; wrapping them in an envelope would be noise.
		return exitOK
	}
	return a.out.emit(a.command, a.result, asLedgerError(err))
}

// closeAfter runs fn and then closes the ledger, unconditionally: on success, on
// error, and on panic (close first, then re-panic so the crash is not hidden).
// The closer is resolved lazily because the store is opened inside fn.
func closeAfter(closer func() io.Closer, fn func() error) (err error) {
	defer func() {
		c := closer()
		if p := recover(); p != nil {
			if c != nil {
				_ = c.Close()
			}
			panic(p)
		}
		if c == nil {
			return
		}
		if closeErr := c.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return fn()
}
