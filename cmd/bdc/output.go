package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// envelopeVersion is the contract version of the JSON envelope itself, not of
// bdc. It changes only when the envelope's own shape changes.
const envelopeVersion = "1"

// timestampLayout is RFC3339 with microseconds, matching the ledger's
// DATETIME(6) columns.
const timestampLayout = "2006-01-02T15:04:05.000000Z07:00"

// Envelope wraps every command's output. Exactly one of Data and Error is
// non-nil; there is no partial envelope.
type Envelope struct {
	BDC      string     `json:"bdc"`
	Command  string     `json:"command"`
	OK       bool       `json:"ok"`
	Data     any        `json:"data"`
	Warnings []Warning  `json:"warnings"`
	Error    *ErrorBody `json:"error"`
	Meta     Meta       `json:"meta"`
}

// Warning is a non-fatal condition: a degraded adapter, a missing optional
// field, an invariant the database cannot enforce. Code is what a script
// matches; Message is for a human.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorBody carries the machine-readable failure. Details is the structured
// payload a caller acts on, so nothing has to be parsed back out of Message.
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

type Meta struct {
	BDCVersion   string `json:"bdc_version"`
	LedgerSchema int    `json:"ledger_schema"`
	GeneratedAt  string `json:"generated_at"`
}

// result is what a command body returns: the JSON data and its human rendering
// of the same domain result. Human is required — a command with no human
// rendering would break the "human and JSON agree" guarantee by omission.
type result struct {
	Data  any
	Human func(w io.Writer)
}

// emitter renders one invocation's outcome. JSON goes to stdout and prose goes
// to stderr; in human mode the rendered result is the stdout payload. The two
// never mix, which is what makes `bdc ... --json | jq` safe.
type emitter struct {
	jsonMode bool
	quiet    bool
	stdout   io.Writer
	stderr   io.Writer

	warnings []Warning
	schema   func() int
	now      func() time.Time
}

func newEmitter(stdout, stderr io.Writer) *emitter {
	return &emitter{
		stdout:   stdout,
		stderr:   stderr,
		warnings: []Warning{},
		schema:   func() int { return 0 },
		now:      time.Now,
	}
}

// warn accumulates a warning for the envelope. Warnings survive a later failure:
// "the adapter was unavailable" is still true when the write is rejected.
func (e *emitter) warn(code, message string) {
	e.warnings = append(e.warnings, Warning{Code: code, Message: message})
}

// emit renders the outcome and returns the process exit code.
func (e *emitter) emit(command string, res result, err error) int {
	env := Envelope{
		BDC:      envelopeVersion,
		Command:  command,
		Warnings: e.warnings,
		Meta: Meta{
			BDCVersion:   version,
			LedgerSchema: e.schema(),
			GeneratedAt:  e.now().UTC().Format(timestampLayout),
		},
	}
	exit := 0
	if err != nil {
		exit = exitCode(err)
		code, message, details := errorBody(err)
		env.OK = false
		env.Error = &ErrorBody{Code: code, Message: message, Details: details}
	} else {
		env.OK = true
		env.Data = res.Data
	}

	if e.jsonMode {
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(env); encErr != nil {
			fmt.Fprintf(e.stderr, "bdc: cannot encode output: %v\n", encErr)
			return exitStorage
		}
		e.renderWarnings()
		return exit
	}

	if err != nil {
		fmt.Fprintf(e.stderr, "bdc: %s\n", env.Error.Message)
	} else if res.Human != nil {
		res.Human(e.stdout)
	}
	e.renderWarnings()
	return exit
}

func (e *emitter) renderWarnings() {
	if e.quiet {
		return
	}
	for _, w := range e.warnings {
		fmt.Fprintf(e.stderr, "bdc: warning: %s: %s\n", w.Code, w.Message)
	}
}
