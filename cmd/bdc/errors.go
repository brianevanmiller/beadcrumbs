package main

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// The stable exit codes. A caller may branch on these forever; they are part of
// the CLI contract, not an implementation detail.
const (
	exitOK        = 0
	exitUsage     = 1
	exitNotFound  = 2
	exitDenied    = 3
	exitBusy      = 4
	exitNoLedger  = 5
	exitStorage   = 6
	exitRedaction = 7
	exitAdapter   = 8
)

// exitCodes maps each ledger error kind to its one exit code. An error that
// matches none of them is a defect somewhere above, and reporting it as a
// storage error is the honest answer: something failed and we cannot classify it.
var exitCodes = []struct {
	kind error
	code int
}{
	{ledger.ErrInvalidInput, exitUsage},
	{ledger.ErrNotFound, exitNotFound},
	{ledger.ErrPolicyDenied, exitDenied},
	{ledger.ErrAuthorityDenied, exitDenied},
	{ledger.ErrBusy, exitBusy},
	{ledger.ErrNoLedger, exitNoLedger},
	{ledger.ErrIntegrity, exitStorage},
	{ledger.ErrRedaction, exitRedaction},
	{ledger.ErrAdapter, exitAdapter},
}

func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	for _, m := range exitCodes {
		if errors.Is(err, m.kind) {
			return m.code
		}
	}
	return exitStorage
}

// errorBody extracts the JSON error payload. An unclassified error still gets a
// code, so the envelope shape never varies.
func errorBody(err error) (code, message string, details map[string]any) {
	var le *ledger.Error
	if errors.As(err, &le) {
		return le.Code, le.Error(), le.Details
	}
	return "storage_unclassified", err.Error(), nil
}

// asLedgerError classifies an error that came out of Cobra rather than out of a
// command body. Every error Beadcrumbs raises is a *ledger.Error, so anything
// else is an argument or flag problem Cobra rejected, which is exit 1.
func asLedgerError(err error) error {
	if err == nil {
		return nil
	}
	var le *ledger.Error
	if errors.As(err, &le) {
		return err
	}
	return usageError(err)
}

// usageError wraps a flag or argument failure from Cobra so it carries the same
// code and exit status as a validation failure raised inside a command body.
//
// The cause is deliberately not attached. ledger.Error renders "<message>:
// <cause>", and here the message *is* the cause, so keeping it printed every
// usage error twice: `required flag(s) "class" not set: required flag(s)
// "class" not set`.
func usageError(err error) error {
	return ledger.Fail(ledger.ErrInvalidInput, "invalid_usage", "%s", withoutFlagValue(err.Error()))
}

// invalidArgument matches Cobra's `invalid argument "<value>" for "<flag>"
// flag: <parse error>`, whose trailing parse error repeats the value.
var invalidArgument = regexp.MustCompile(`^invalid argument ".*" for "([^"]*)" flag: `)

// withoutFlagValue names the flag a value was rejected for without echoing the
// value. Everything Beadcrumbs persists goes through the redactor and every
// rejection names its rule rather than its match; the usage surface has to hold
// the same line, because a mistyped flag's value can be a token and --json
// output is routinely logged and pasted.
func withoutFlagValue(message string) string {
	if m := invalidArgument.FindStringSubmatch(message); m != nil {
		return fmt.Sprintf("invalid argument for %q flag", m[1])
	}
	return message
}
