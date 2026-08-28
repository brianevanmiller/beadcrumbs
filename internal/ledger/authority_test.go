package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
	"github.com/brianevanmiller/beadcrumbs/internal/redact"
)

func humanActor() ledger.Provenance {
	return ledger.Provenance{ActorID: "brian", ActorKind: ledger.ActorHuman}
}

// TestAuthorityHistoryIsAppendOnly is the release gate for authority events.
// A grant that raises or lowers the force of a record must leave the earlier
// grant readable: "an agent made this a working default and a human later made
// it mandatory" is the fact worth keeping.
func TestAuthorityHistoryIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	f := newFixtureWith(t, nil, humanActor())
	crumb := f.capture("prefer bun over npm in this repository", 0.8)
	revision := f.seedInsight("tooling default", crumb.ID)
	target := ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision)}

	first, err := f.L.GrantAuthority(ctx, ledger.GrantAuthority{
		Target: target, Level: ledger.AuthorityAdvisory, Rationale: "worth citing while we try it",
	})
	if err != nil {
		t.Fatalf("advisory: %v", err)
	}
	if first.EffectiveLevel != ledger.AuthorityAdvisory {
		t.Fatalf("effective level = %q, want advisory", first.EffectiveLevel)
	}

	second, err := f.L.GrantAuthority(ctx, ledger.GrantAuthority{
		Target: target, Level: ledger.AuthorityMandatory, Scope: "tooling",
		Rationale: "settled after two sprints",
	})
	if err != nil {
		t.Fatalf("mandatory: %v", err)
	}
	if second.EffectiveLevel != ledger.AuthorityMandatory {
		t.Fatalf("effective level = %q, want mandatory", second.EffectiveLevel)
	}

	if n := f.count(`SELECT COUNT(*) FROM authorities WHERE target_id = ?`, string(revision)); n != 2 {
		t.Fatalf("authorities rows = %d, want 2", n)
	}
	history := targetEvents(t, f, target)
	if len(history) != 2 {
		t.Fatalf("events = %d, want 2", len(history))
	}
	if history[0].Summary != string(ledger.AuthorityAdvisory) ||
		history[0].Rationale != "worth citing while we try it" {
		t.Fatalf("the first grant was rewritten: %+v", history[0])
	}
}

// TestAgentCannotGrantMandatoryAuthority is the ledger half of
// ck_aut_mandatory_human. The refusal is typed and total: nothing is written
// and the request is never quietly downgraded to advisory, which would leave
// the agent believing it had established something binding.
func TestAgentCannotGrantMandatoryAuthority(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("always run migrations before deploy", 0.9)
	revision := f.seedInsight("deploy order", crumb.ID)

	_, err := f.L.GrantAuthority(ctx, ledger.GrantAuthority{
		Target:    ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision)},
		Level:     ledger.AuthorityMandatory,
		Rationale: "an agent decides it is binding",
	})
	if !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("err = %v, want authority denied", err)
	}
	if n := f.count(`SELECT COUNT(*) FROM authorities`); n != 0 {
		t.Fatalf("authorities rows = %d, want none; the grant must not be downgraded either", n)
	}
}

// TestAgentDefaultRequiresRepositoryPolicy covers invariant §2.5.5: an
// agent-set working default is a repository decision, recorded in the ledger it
// governs rather than in a flag.
func TestAgentDefaultRequiresRepositoryPolicy(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("the queue is drained by the worker, not the API", 0.7)
	revision := f.seedInsight("queue ownership", crumb.ID)
	target := ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision)}

	_, err := f.L.GrantAuthority(ctx, ledger.GrantAuthority{
		Target: target, Level: ledger.AuthorityDefault, Rationale: "make it the working default",
	})
	if !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("err = %v, want authority denied while the repository forbids it", err)
	}

	permissive := ledgerWithConfig(t, f, agentActor(), func(c *ledger.RepoConfig) { c.AgentMaySetDefault = true })
	res, err := permissive.GrantAuthority(ctx, ledger.GrantAuthority{
		Target: target, Level: ledger.AuthorityDefault, Rationale: "make it the working default",
	})
	if err != nil {
		t.Fatalf("granting default where the repository permits it: %v", err)
	}
	if res.EffectiveLevel != ledger.AuthorityDefault {
		t.Fatalf("effective level = %q, want default", res.EffectiveLevel)
	}
}

func TestAuthorityRejectsACrumbTarget(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	crumb := f.capture("a fragment carries no force of its own", 0.5)
	_, err := f.L.GrantAuthority(context.Background(), ledger.GrantAuthority{
		Target:    ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(crumb.ID)},
		Level:     ledger.AuthorityAdvisory,
		Rationale: "a Crumb is not a conclusion",
	})
	if !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input", err)
	}
}

// ledgerWithConfig builds a second Ledger over the same store with an altered
// repository policy. The configuration is read once when a Ledger is
// constructed, so changing policy means constructing one — which is also what
// the CLI does on the next invocation.
func ledgerWithConfig(t *testing.T, f *fixture, actor ledger.Provenance, mutate func(*ledger.RepoConfig)) *ledger.Ledger {
	t.Helper()
	cfg, err := ledger.LoadRepoConfig(context.Background(), f.Store)
	if err != nil {
		t.Fatalf("reading repo_config: %v", err)
	}
	mutate(&cfg)
	redactor, err := redact.New(redact.Config{Version: cfg.RedactionVersion, Patterns: cfg.RedactPatterns})
	if err != nil {
		t.Fatalf("building the redactor: %v", err)
	}
	return ledger.New(f.Store, ledger.Options{Actor: actor, Redactor: redactor, Config: cfg})
}
