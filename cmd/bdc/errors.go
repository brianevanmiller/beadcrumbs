package main

import (
	"errors"

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
func usageError(err error) error {
	return ledger.FailWith(ledger.ErrInvalidInput, "invalid_usage", err, "%s", err.Error())
}
