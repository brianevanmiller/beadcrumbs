// Package ledger owns the Beadcrumbs domain: lifecycle invariants, append-only
// behavior, provenance requirements, and the stable result and error types the
// CLI renders. Nothing in this package names a storage concept.
//
// This file is the whole error vocabulary. Every failure that reaches a user
// crosses it, because the exit-code table in cmd/bdc/errors.go maps these nine
// kinds and nothing else.
package ledger

import (
	"errors"
	"fmt"
)

// The nine error kinds. Each maps to exactly one CLI exit code; the mapping
// lives in cmd/bdc/errors.go and is asserted by TestExitCodeForEachErrorClass.
var (
	ErrInvalidInput    = errors.New("invalid input")
	ErrNotFound        = errors.New("not found")
	ErrPolicyDenied    = errors.New("policy denied")
	ErrAuthorityDenied = errors.New("authority denied")
	ErrBusy            = errors.New("ledger busy")
	ErrNoLedger        = errors.New("no ledger")
	ErrIntegrity       = errors.New("integrity error")
	ErrRedaction       = errors.New("redaction failed")
	ErrAdapter         = errors.New("adapter error")
)

// Error is the only error type Beadcrumbs constructs. Kind selects the exit
// code, Code is the machine-readable JSON error.code, and Details is the
// structured payload a caller needs to act — never free-form prose parsed back
// out of Message.
type Error struct {
	Kind    error
	Code    string
	Message string
	Details map[string]any

	cause error
}

// Error is the message and nothing else. The JSON envelope's error.message is
// built from it, and that is a stable contract: splicing the cause in would
// publish go-mysql-server's own text — which interpolates key values and
// changes between upstream releases — into a field callers match on. The cause
// stays reachable through errors.Is and errors.As.
func (e *Error) Error() string { return e.Message }

// Unwrap exposes both the kind and the cause so errors.Is matches either.
func (e *Error) Unwrap() []error {
	if e.cause == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.cause}
}

// Fail builds an error of the given kind. Code must be the JSON error code from
// the CLI contract's exit-code table, not a sentence.
func Fail(kind error, code, format string, a ...any) *Error {
	return &Error{Kind: kind, Code: code, Message: fmt.Sprintf(format, a...)}
}

// FailWith is Fail with an underlying cause preserved for errors.Is. The cause
// is never part of what a caller reads: not the JSON error code, and not the
// message either.
func FailWith(kind error, code string, cause error, format string, a ...any) *Error {
	return &Error{Kind: kind, Code: code, Message: fmt.Sprintf(format, a...), cause: cause}
}

// WithDetails attaches the structured payload. Chainable at the construction site.
func (e *Error) WithDetails(d map[string]any) *Error {
	e.Details = d
	return e
}

// NotFound is the one error shape common enough to deserve a constructor: every
// read that resolves a user-supplied id produces it, and the JSON error code is
// a single value in the CLI contract's exit-code table.
func NotFound(what, id string) *Error {
	return Fail(ErrNotFound, "not_found", "no %s with id %s", what, id)
}
