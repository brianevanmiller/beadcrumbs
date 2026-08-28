package beads_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// `--json` does not guarantee JSON on failure: list, show, comments, and info
// print plain text on stderr and exit 1. A failure is therefore identified by
// its exit code alone, and neither stream is ever parsed or pattern-matched.
func TestPlainTextFailureIsNeverParsed(t *testing.T) {
	assertBounded := func(t *testing.T, err error, forbidden ...string) {
		t.Helper()
		if err == nil {
			t.Fatal("a failing bd produced no error")
		}
		if !errors.Is(err, ledger.ErrAdapter) {
			t.Fatalf("a bd failure escaped as something other than an adapter error: %v", err)
		}
		if !strings.Contains(err.Error(), "bd show") {
			t.Fatalf("the error does not name the command: %v", err)
		}
		for _, text := range forbidden {
			if strings.Contains(err.Error(), text) {
				t.Fatalf("the error quotes bd's own output %q: %v", text, err)
			}
		}
		var adapterErr *ledger.Error
		if !errors.As(err, &adapterErr) || adapterErr.Details["exit_code"] != 1 {
			t.Fatalf("the error carries no exit code: %+v", err)
		}
	}

	t.Run("real bd, unknown issue", func(t *testing.T) {
		repo := workspace(t)
		adapter, av := beads.Detect(ctx(), repo)
		if adapter == nil {
			t.Fatalf("no adapter: %+v", av)
		}
		_, err := adapter.Resolve(ctx(), "tst-nosuchissue")
		// bd prints prose on stderr and a JSON error on stdout for this case;
		// neither may reach the user through this adapter.
		assertBounded(t, err, "no issue found", "no issues found")
	})

	t.Run("plain text on both streams", func(t *testing.T) {
		stubBD(t, `
version) echo '{"version":"1.2.2"}';;
where) echo '{"prefix":"tst"}';;
show) echo "Error: no beads database found"; echo "Error: no beads database found" >&2; exit 1;;`)
		adapter, av := beads.Detect(ctx(), "/fixture/repo")
		if adapter == nil {
			t.Fatalf("no adapter: %+v", av)
		}
		_, err := adapter.Resolve(ctx(), "tst-1")
		assertBounded(t, err, "no beads database found")
	})
}

// The floor is about the JSON contract these shapes were measured against, so
// below it the adapter is disabled with its own reason rather than left to
// guess.
func TestVersionBelowFloorDisablesAdapter(t *testing.T) {
	cases := []struct {
		version string
		reason  string
	}{
		{`{"version":"1.2.1"}`, beads.ReasonBelowFloor},
		{`{"version":"1.1.9"}`, beads.ReasonBelowFloor},
		{`{"version":"0.9.0"}`, beads.ReasonBelowFloor},
		{`{"version":"1.2.2"}`, beads.ReasonOK},
		{`{"version":"1.2.10"}`, beads.ReasonOK},
		{`{"version":"1.10.0"}`, beads.ReasonOK},
		{`{"version":"2.0.0-dev"}`, beads.ReasonOK},
		{`{"schema_version":1}`, beads.ReasonNoVersion},
		{`bd version 1.2.2`, beads.ReasonNoVersion},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			stubBD(t, `
version) echo '`+tc.version+`';;
where) echo '{"prefix":"tst"}';;`)

			adapter, av := beads.Detect(ctx(), "/fixture/repo")
			if av.Reason != tc.reason {
				t.Fatalf("bd %s detected as %s, expected %s", tc.version, av.Reason, tc.reason)
			}
			if (adapter != nil) != (tc.reason == beads.ReasonOK) {
				t.Fatalf("adapter presence disagrees with reason %s", av.Reason)
			}
			// A disabled adapter must not become a non-nil interface holding
			// nil, which the ledger would call and crash on.
			if tc.reason != beads.ReasonOK && beads.Enricher(adapter) != nil {
				t.Fatal("a disabled adapter was still handed to the ledger as an enricher")
			}
		})
	}
}

// Unknown fields are additive and ignored; a missing expected field is the
// failure signal. Guessing at either is how an adapter silently invents data.
func TestUnknownFieldsAreTolerated(t *testing.T) {
	// The stub runs with a PATH holding only itself, so its bodies use shell
	// builtins alone — one line of JSON, echoed.
	const withUnknowns = `[{"id":"tst-1","title":"Known","status":"open","priority":1,` +
		`"issue_type":"decision","molecule_kind":"swarm","gate":{"kind":"all-children"},` +
		`"unknown_scalar":7,"comment_count":2,"dependencies":[{"id":"tst-2","title":"Parent",` +
		`"dependency_type":"discovered-from","swarm_role":"lead"}]}]`

	t.Run("additive fields", func(t *testing.T) {
		stubBD(t, `
version) echo '{"version":"1.2.2"}';;
where) echo '{"prefix":"tst"}';;
show) echo '`+withUnknowns+`';;`)
		adapter, av := beads.Detect(ctx(), "/fixture/repo")
		if adapter == nil {
			t.Fatalf("no adapter: %+v", av)
		}
		issue, err := adapter.Resolve(ctx(), "tst-1")
		if err != nil {
			t.Fatalf("unknown fields failed the read: %v", err)
		}
		if issue.ID != "tst-1" || issue.Title != "Known" || issue.CommentCount != 2 {
			t.Fatalf("known fields were lost: %+v", issue)
		}
		if len(issue.Dependencies) != 1 || issue.Dependencies[0].Relation != "discovered-from" {
			t.Fatalf("the hydrated relation was lost: %+v", issue.Dependencies)
		}
	})

	t.Run("missing expected field", func(t *testing.T) {
		stubBD(t, `
version) echo '{"version":"1.2.2"}';;
where) echo '{"prefix":"tst"}';;
show) echo '[{"title":"No id here","status":"open"}]';;`)
		adapter, _ := beads.Detect(ctx(), "/fixture/repo")
		_, err := adapter.Resolve(ctx(), "tst-1")
		if err == nil {
			t.Fatal("an issue with no id was accepted")
		}
		if !errors.Is(err, ledger.ErrAdapter) || !strings.Contains(err.Error(), "id") {
			t.Fatalf("expected a bounded adapter error naming the field, got %v", err)
		}
	})

	t.Run("output that is not JSON at all", func(t *testing.T) {
		stubBD(t, `
version) echo '{"version":"1.2.2"}';;
where) echo '{"prefix":"tst"}';;
show) echo "tst-1  Known  open";;`)
		adapter, _ := beads.Detect(ctx(), "/fixture/repo")
		_, err := adapter.Resolve(ctx(), "tst-1")
		if !errors.Is(err, ledger.ErrAdapter) {
			t.Fatalf("expected a bounded adapter error, got %v", err)
		}
		if strings.Contains(err.Error(), "tst-1  Known") {
			t.Fatalf("the error quotes the unparsed output: %v", err)
		}
	})
}
