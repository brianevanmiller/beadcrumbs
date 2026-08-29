package beads_test

import (
	"errors"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// Enrichment is optional, so nothing this package does may fail a core write.
// The adapter tier of that guarantee is: detection never errors, a disabled
// adapter is absent rather than nil-in-an-interface, and every failure that
// does surface is a ledger.ErrAdapter — the one kind the CLI renders as a
// warning against a single Reference.
func TestEnrichFailureIsWarningNotError(t *testing.T) {
	t.Run("no bd leaves the ledger with no enricher", func(t *testing.T) {
		repo := gitRepo(t)
		t.Setenv("PATH", t.TempDir())

		adapter, av := beads.Detect(ctx(), repo)
		if av.Present {
			t.Fatal("detection claimed a bd it could not find")
		}
		// The typed nil must not become a non-nil ledger.Enricher: the ledger
		// checks for a nil interface to decide whether enrichment exists at all.
		if beads.Enricher(adapter) != nil {
			t.Fatal("an absent bd was handed to the ledger as an enricher")
		}
	})

	t.Run("every wrapper degrades to an adapter error", func(t *testing.T) {
		// A bd that answers detection and then fails everything, which is what
		// a mid-command workspace loss or a forward schema skew looks like.
		stubBD(t, `
version) echo '{"version":"1.2.2"}';;
where) echo '{"prefix":"tst"}';;`)
		adapter, av := beads.Detect(ctx(), "/fixture/repo")
		if adapter == nil {
			t.Fatalf("no adapter: %+v", av)
		}

		calls := map[string]func() error{
			"Resolve":    func() error { _, err := adapter.Resolve(ctx(), "tst-1"); return err },
			"List":       func() error { _, err := adapter.List(ctx(), beads.Filter{}); return err },
			"Comments":   func() error { _, err := adapter.Comments(ctx(), "tst-1"); return err },
			"AddComment": func() error { _, err := adapter.AddComment(ctx(), "tst-1", "note"); return err },
			"Create":     func() error { _, err := adapter.Create(ctx(), beads.NewIssue{Title: "t"}); return err },
			"Link":       func() error { return adapter.Link(ctx(), "tst-1", "tst-2", "discovered-from") },
			"Workspace":  func() error { _, err := adapter.Workspace(ctx()); return err },
			"Enrich": func() error {
				_, _, _, err := adapter.Enrich(ctx(), "tst-1", "")
				return err
			},
		}
		for name, call := range calls {
			err := call()
			if err == nil {
				t.Fatalf("%s succeeded against a bd that fails everything", name)
			}
			if !errors.Is(err, ledger.ErrAdapter) {
				t.Fatalf("%s failed with a kind the CLI would not treat as a warning: %v", name, err)
			}
		}
	})

	t.Run("a workspace bd cannot describe does not enrich from the wrong one", func(t *testing.T) {
		stubBD(t, `
version) echo '{"version":"1.2.2"}';;
where) echo '{"prefix":"tst"}';;
show) echo '[{"id":"tst-1","title":"Local"}]';;`)
		adapter, _ := beads.Detect(ctx(), "/fixture/repo")

		// bd context fails, so the adapter cannot prove this Reference is one of
		// its own. Refusing is the degradation; enriching would cache a
		// plausible, wrong title.
		if _, _, _, err := adapter.Enrich(ctx(), "tst-1", "some-other-project"); !errors.Is(err, ledger.ErrAdapter) {
			t.Fatalf("expected a bounded refusal, got %v", err)
		}
		// A Reference with no recorded workspace is still enriched, because
		// nothing about it contradicts this one.
		if _, _, _, err := adapter.Enrich(ctx(), "tst-1", ""); err != nil {
			t.Fatalf("a workspace-less Reference was refused: %v", err)
		}
	})

	t.Run("real bd, unknown issue", func(t *testing.T) {
		repo := workspace(t)
		adapter, av := beads.Detect(ctx(), repo)
		if adapter == nil {
			t.Fatalf("no adapter: %+v", av)
		}
		_, _, _, err := adapter.Enrich(ctx(), "tst-nosuchissue", "")
		if !errors.Is(err, ledger.ErrAdapter) {
			t.Fatalf("expected an adapter error, got %v", err)
		}
		for _, kind := range []error{ledger.ErrIntegrity, ledger.ErrRedaction, ledger.ErrInvalidInput, ledger.ErrNotFound} {
			if errors.Is(err, kind) {
				t.Fatalf("an absent tracker issue surfaced as %v, which is not a warning: %v", kind, err)
			}
		}
	})
}
