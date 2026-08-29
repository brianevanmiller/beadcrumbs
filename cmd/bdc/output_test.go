package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// "Human and JSON output represent the same result" is a claim about one
// result, so it is tested on one result: each command body runs exactly once
// and the outcome it produced is rendered twice. Running the command a second
// time in the other mode would prove something weaker and, for a write, would
// be a different result — a second harvest, a second attempt.
func TestHumanAndJSONAgree(t *testing.T) {
	f := newFixture(t)
	for _, s := range contractSteps() {
		t.Run(s.name, func(t *testing.T) {
			out := f.renderBoth(t, s)
			f.bind(t, s, out.jsonText)

			if out.exit != s.exit {
				t.Fatalf("exit code %d, want %d\n%s", out.exit, s.exit, out.jsonText)
			}

			// stdout is a machine surface under --json and a human one without;
			// it is never both. This is the separation `bd` gets wrong, and the
			// only reason `bdc ... --json | jq` is safe.
			var env map[string]any
			dec := json.NewDecoder(strings.NewReader(out.jsonText))
			dec.UseNumber()
			if err := dec.Decode(&env); err != nil {
				t.Fatalf("--json stdout is not one envelope: %v\n%s", err, out.jsonText)
			}
			if dec.More() {
				t.Error("--json stdout carried more than one JSON value")
			}
			if strings.Contains(out.jsonStderr, "{") {
				t.Errorf("JSON leaked onto stderr: %q", out.jsonStderr)
			}
			if json.Valid([]byte(out.humanText)) && strings.HasPrefix(strings.TrimSpace(out.humanText), "{") {
				t.Errorf("human stdout is JSON: %q", out.humanText)
			}

			if s.exit != exitOK {
				// A failure renders as one diagnostic on stderr and nothing on
				// stdout: a script that only reads stdout sees no result, which
				// is the truth.
				if strings.TrimSpace(out.humanText) != "" {
					t.Errorf("a failed command wrote to stdout: %q", out.humanText)
				}
				message, _ := lookup(env, "error.message")
				if message == nil || !strings.Contains(out.humanStderr, fmt.Sprint(message)) {
					t.Errorf("human stderr %q does not carry the JSON error message %v",
						out.humanStderr, message)
				}
				code, _ := lookup(env, "error.code")
				if fmt.Sprint(code) != s.errCode {
					t.Errorf("error.code is %v, want %s", code, s.errCode)
				}
				return
			}

			for _, path := range s.facts {
				v, ok := lookup(env["data"], path)
				if !ok {
					t.Errorf("data has no %s", path)
					continue
				}
				want := fmt.Sprint(v)
				if !strings.Contains(out.humanText, want) {
					t.Errorf("the human rendering never mentions %s (%q):\n%s",
						path, want, out.humanText)
				}
			}
		})
	}
}

// TestWarningsSurviveIntoBothRenderings: a degraded adapter or a redacted field
// is reported the same way whichever surface the caller reads. The redaction
// warning is the one that matters — the caller has to learn that what it asked
// to store is not what was stored.
func TestWarningsSurviveIntoBothRenderings(t *testing.T) {
	f := newFixture(t)
	f.renderBoth(t, step{name: "init", args: []string{"init"}})
	out := f.renderBoth(t, step{name: "capture.redacted", args: []string{"capture",
		"The failing run logged " + awsExampleKey + " into the build output."}})

	var env struct {
		Warnings []Warning `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out.jsonText), &env); err != nil {
		t.Fatalf("decoding the capture envelope: %v", err)
	}
	for _, w := range env.Warnings {
		if w.Code != "redacted" {
			continue
		}
		if strings.Contains(w.Message, awsExampleKey) {
			t.Fatal("the warning quoted the secret it redacted")
		}
		if !strings.Contains(out.humanStderr, w.Message) {
			t.Errorf("the human rendering dropped the warning %q:\n%s", w.Message, out.humanStderr)
		}
		if strings.Contains(out.jsonText, awsExampleKey) || strings.Contains(out.humanText, awsExampleKey) {
			t.Fatal("the stored content still carries the secret")
		}
		return
	}
	t.Fatalf("storing text with a secret shape produced no redaction warning: %v", env.Warnings)
}

// TestEmptyCollectionsAreNeverNull: an empty list is `[]`, never `null`. A
// caller that has to distinguish "no proposals" from "this field is missing"
// before it can iterate has been handed a bug, not a contract.
func TestEmptyCollectionsAreNeverNull(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{
		{"init"},
		{"crumb", "list"},
		{"insight", "list"},
		{"reference", "list"},
		{"promote", "list"},
		{"context"},
		{"handoff"},
		{"prime"},
	} {
		out := f.run(t, step{args: args}, true)
		if strings.Contains(out.stdout, ": null") && !strings.Contains(out.stdout, `"error": null`) {
			t.Errorf("`bdc %s` emitted a null where a value was declared:\n%s",
				strings.Join(args, " "), out.stdout)
		}
		if strings.Contains(out.stdout, `"warnings": null`) {
			t.Errorf("`bdc %s` emitted a null warnings list", strings.Join(args, " "))
		}
	}
}

// renderings is one command body's outcome rendered both ways.
type renderings struct {
	jsonText    string
	jsonStderr  string
	humanText   string
	humanStderr string
	exit        int
}

// renderBoth runs the step once and renders the recorded result twice. It
// mirrors run() rather than calling it, because run() renders once by design:
// the point here is that both renderings come from the same result value.
func (f *fixture) renderBoth(t *testing.T, s step) renderings {
	t.Helper()
	args := []string{"-C", f.dir, "--actor", "tester", "--actor-kind", "human", "--json"}
	args = append(args, f.resolve(t, s.args)...)

	var jsonOut, jsonErr bytes.Buffer
	a := newApp(&jsonOut, &jsonErr)
	root := a.newRootCommand()
	root.SetArgs(args)
	root.SetOut(&jsonOut)
	root.SetErr(&jsonErr)

	err := asLedgerError(closeAfter(a.closer, func() error {
		return root.ExecuteContext(context.Background())
	}))
	exit := a.out.emit(a.command, a.result, err)

	var humanOut, humanErr bytes.Buffer
	a.out.jsonMode = false
	a.out.stdout = &humanOut
	a.out.stderr = &humanErr
	if human := a.out.emit(a.command, a.result, err); human != exit {
		t.Errorf("the same result exited %d as JSON and %d as prose", exit, human)
	}

	return renderings{
		jsonText: jsonOut.String(), jsonStderr: jsonErr.String(),
		humanText: humanOut.String(), humanStderr: humanErr.String(),
		exit: exit,
	}
}
