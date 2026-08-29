package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// Exit codes are a contract a caller may branch on forever, so they are tested
// as a closed table rather than by example: every error kind the ledger can
// raise appears here with its code and its declared JSON prefixes, and a new
// kind that nobody adds to this table fails the build.
//
// §3.2 of the plan is the source of both columns.
var errorClasses = []struct {
	kind     error
	exit     int
	prefixes []string
}{
	{ledger.ErrInvalidInput, exitUsage, []string{"invalid_"}},
	{ledger.ErrNotFound, exitNotFound, []string{"not_found"}},
	{ledger.ErrPolicyDenied, exitDenied, []string{"policy_denied"}},
	{ledger.ErrAuthorityDenied, exitDenied, []string{"authority_denied", "authority_required"}},
	{ledger.ErrBusy, exitBusy, []string{"ledger_busy"}},
	{ledger.ErrNoLedger, exitNoLedger, []string{"no_ledger"}},
	{ledger.ErrIntegrity, exitStorage, []string{"storage_", "integrity_"}},
	{ledger.ErrRedaction, exitRedaction, []string{"redaction_failed"}},
	{ledger.ErrAdapter, exitAdapter, []string{"adapter_"}},
}

func TestExitCodeForEachErrorClass(t *testing.T) {
	t.Run("every kind maps to its code", func(t *testing.T) {
		for _, c := range errorClasses {
			err := ledger.Fail(c.kind, c.prefixes[0]+"example", "example failure")
			if got := exitCode(err); got != c.exit {
				t.Errorf("%v exits %d, want %d", c.kind, got, c.exit)
			}
			code, message, _ := errorBody(err)
			if code != c.prefixes[0]+"example" || message != "example failure" {
				t.Errorf("%v produced (%q, %q)", c.kind, code, message)
			}
		}
	})

	t.Run("the table covers every kind the ledger declares", func(t *testing.T) {
		// A kind with no entry above would fall through exitCode's default and
		// be reported as a storage error — a wrong exit code that no test would
		// otherwise notice.
		for _, kind := range []error{
			ledger.ErrInvalidInput, ledger.ErrNotFound, ledger.ErrPolicyDenied,
			ledger.ErrAuthorityDenied, ledger.ErrBusy, ledger.ErrNoLedger,
			ledger.ErrIntegrity, ledger.ErrRedaction, ledger.ErrAdapter,
		} {
			found := false
			for _, c := range errorClasses {
				if errors.Is(kind, c.kind) {
					found = true
				}
			}
			if !found {
				t.Errorf("%v has no exit code in this table", kind)
			}
		}
		if len(exitCodes) != len(errorClasses) {
			t.Errorf("cmd/bdc maps %d kinds; this table declares %d", len(exitCodes), len(errorClasses))
		}
	})

	t.Run("success and unclassified failures", func(t *testing.T) {
		if got := exitCode(nil); got != exitOK {
			t.Errorf("no error exits %d, want 0", got)
		}
		// An error from outside the ledger cannot be classified, and saying so
		// is more honest than guessing a code that a caller would branch on.
		plain := errors.New("something outside the ledger failed")
		if got := exitCode(plain); got != exitStorage {
			t.Errorf("an unclassified error exits %d, want %d", got, exitStorage)
		}
		code, _, _ := errorBody(plain)
		if code != "storage_unclassified" {
			t.Errorf("an unclassified error reported code %q", code)
		}
	})

	t.Run("a Cobra failure is a usage error", func(t *testing.T) {
		err := asLedgerError(errors.New("unknown flag: --nope"))
		if got := exitCode(err); got != exitUsage {
			t.Errorf("a flag failure exits %d, want %d", got, exitUsage)
		}
		code, _, _ := errorBody(err)
		if code != "invalid_usage" {
			t.Errorf("a flag failure reported code %q", code)
		}
		// A ledger error that reaches the same path keeps its own code.
		original := ledger.Fail(ledger.ErrNotFound, "not_found", "no crumb")
		if asLedgerError(original) != error(original) {
			t.Error("asLedgerError rewrapped a ledger error")
		}
	})

	t.Run("every failing invocation in the contract uses a declared pair", func(t *testing.T) {
		// The end-to-end half: the golden table exercises these codes against a
		// real ledger, and this asserts the pairs it expects are ones §3.2
		// actually declares.
		for _, s := range contractSteps() {
			if s.exit == exitOK {
				continue
			}
			legal := false
			for _, c := range errorClasses {
				if c.exit != s.exit {
					continue
				}
				for _, p := range c.prefixes {
					if strings.HasPrefix(s.errCode, p) {
						legal = true
					}
				}
			}
			if !legal {
				t.Errorf("%s expects exit %d with code %q, which §3.2 does not pair",
					s.name, s.exit, s.errCode)
			}
		}
	})
}

// TestErrorDetailsSurviveIntoTheEnvelope: `error.details` is the payload a
// caller acts on. The authority-blocked propose is the case that matters — the
// proposal was recorded, and without its id in the envelope a human cannot
// grant authority and retry against it.
func TestErrorDetailsSurviveIntoTheEnvelope(t *testing.T) {
	err := ledger.Fail(ledger.ErrAuthorityDenied, "authority_required", "a human must decide").
		WithDetails(map[string]any{"proposal_id": "pp_1", "created": true})
	code, message, details := errorBody(err)
	if code != "authority_required" || message != "a human must decide" {
		t.Fatalf("errorBody returned (%q, %q)", code, message)
	}
	if fmt.Sprint(details["proposal_id"]) != "pp_1" || details["created"] != true {
		t.Fatalf("details were lost: %v", details)
	}
}

// A Cobra failure reaches error.message exactly once and never carries the
// rejected value: a value mistyped into the wrong flag can be a token, and
// --json output is routinely logged and pasted.
func TestUsageErrorsCarryNeitherTheValueNorTheMessageTwice(t *testing.T) {
	const secret = "sk-ant-notarealkey"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a rejected value is reported as its flag",
			in:   `invalid argument "` + secret + `" for "--limit" flag: strconv.ParseInt: parsing "` + secret + `": invalid syntax`,
			want: `invalid argument for "--limit" flag`,
		},
		{
			name: "anything else is passed through once",
			in:   `required flag(s) "class" not set`,
			want: `required flag(s) "class" not set`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, message, _ := errorBody(usageError(errors.New(c.in)))
			if message != c.want {
				t.Errorf("error.message is %q, want %q", message, c.want)
			}
			if strings.Contains(message, secret) {
				t.Errorf("the rejected value reached error.message: %q", message)
			}
		})
	}
}
