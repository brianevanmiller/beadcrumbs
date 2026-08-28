package ledger_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
	"github.com/brianevanmiller/beadcrumbs/internal/redact"
	"github.com/brianevanmiller/beadcrumbs/internal/store/dolt"
)

// These tests run against a real embedded Dolt ledger in a fresh Git repository.
// The invariants under test — redaction before persist, append-only review,
// prune blocked by lineage — are all enforced jointly by the ledger and the
// schema, so a fake store would prove only that the Go half agrees with itself.

type fixture struct {
	t     *testing.T
	L     *ledger.Ledger
	Store *dolt.Store
	Actor ledger.Provenance
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, nil, agentActor())
}

// newFixtureWith injects a Redactor, which is how the redaction-abort paths are
// reached: the real rule set has no input that both parses and cannot be
// replaced, and inventing one would test the rules rather than the sequence.
func newFixtureWith(t *testing.T, redactor ledger.Redactor, actor ledger.Provenance) *fixture {
	t.Helper()
	ctx := context.Background()
	repo := fixtureRepo(t)

	loc, err := dolt.Discover(ctx, repo)
	if err == nil {
		t.Fatalf("expected %s to have no ledger yet", repo)
	}
	loc = loc.Resolve(false)
	if _, err := dolt.Init(ctx, loc, dolt.InitOptions{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := dolt.Open(ctx, loc, dolt.Config{Command: "test"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg, err := ledger.LoadRepoConfig(ctx, store)
	if err != nil {
		t.Fatalf("reading repo_config: %v", err)
	}
	if redactor == nil {
		r, err := redact.New(redact.Config{Version: cfg.RedactionVersion, Patterns: cfg.RedactPatterns})
		if err != nil {
			t.Fatalf("building the redactor: %v", err)
		}
		redactor = r
	}
	return &fixture{
		t:     t,
		Store: store,
		Actor: actor,
		L:     ledger.New(store, ledger.Options{Actor: actor, Redactor: redactor, Config: cfg}),
	}
}

func agentActor() ledger.Provenance {
	return ledger.Provenance{
		ActorID: "claude", ActorKind: ledger.ActorAgent,
		ActorModel: "claude-opus-5", SessionID: "sess-1",
	}
}

func (f *fixture) capture(content string, confidence float64, refs ...string) ledger.Crumb {
	f.t.Helper()
	specs := make([]ledger.RefSpec, 0, len(refs))
	for _, raw := range refs {
		spec, err := ledger.ParseRefSpec(raw, ledger.RelationSubject)
		if err != nil {
			f.t.Fatalf("parsing %q: %v", raw, err)
		}
		specs = append(specs, spec)
	}
	res, err := f.L.CaptureCrumb(context.Background(), ledger.CaptureCrumb{
		Content: content, Confidence: confidence, References: specs,
	})
	if err != nil {
		f.t.Fatalf("capturing %q: %v", content, err)
	}
	return res.Crumb
}

func (f *fixture) crumb(id ledger.CrumbID) ledger.CrumbDetail {
	f.t.Helper()
	detail, err := f.L.Crumb(context.Background(), id)
	if err != nil {
		f.t.Fatalf("reading crumb %s: %v", id, err)
	}
	return detail
}

// write runs a raw storage transaction. It exists for the few tests that need a
// record no ledger operation writes yet — a validation, an orphaned link — and
// never for Harvests or Insights, which go through the real operations.
func (f *fixture) write(fn func(tx ledger.Tx) error) {
	f.t.Helper()
	if err := f.Store.Write(context.Background(), fn); err != nil {
		f.t.Fatalf("write: %v", err)
	}
}

// seedInsight synthesises one Insight from the given Crumbs through the real
// operation, so every test that needs an Insight exercises the path that
// creates one in production. Synthesis is always a Harvest: revision 1 has no
// other author.
func (f *fixture) seedInsight(title string, crumbs ...ledger.CrumbID) ledger.RevisionID {
	f.t.Helper()
	res, err := f.L.CompleteHarvest(context.Background(), ledger.CompleteHarvest{
		Mode: ledger.HarvestManual, Crumbs: crumbs,
		Title: title, Content: "body", Class: "learning", Confidence: 0.6,
	})
	if err != nil {
		f.t.Fatalf("synthesising %q: %v", title, err)
	}
	return res.Revision.ID
}

// seedHarvest records one completed Harvest that weighed the given Crumbs and
// concluded nothing. Weighing a Crumb neither consumes nor mutates it, which is
// the property most of these tests are about.
func (f *fixture) seedHarvest(crumbs ...ledger.CrumbID) ledger.HarvestID {
	f.t.Helper()
	res, err := f.L.CompleteHarvest(context.Background(), ledger.CompleteHarvest{
		Mode: ledger.HarvestManual, Crumbs: crumbs,
	})
	if err != nil {
		f.t.Fatalf("harvesting: %v", err)
	}
	return res.Harvest.ID
}

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.Store.DB().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
	return n
}

// fixtureRepo is a fresh Git repository with one commit. Discovery is
// structural, so every ledger test needs a real repository rather than a
// directory.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"config", "user.email", "fixture@example.test"},
		{"config", "user.name", "fixture"},
		{"commit", "-q", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	// macOS puts TempDir under a symlink, and discovery asserts that cwd is
	// inside the resolved repository root.
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("resolving the fixture path: %v", err)
	}
	return resolved
}
