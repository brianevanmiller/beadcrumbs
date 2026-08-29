package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// docsTo builds a plain destination with no declared capabilities: the v1
// default, where nothing is inferred and nothing is required.
func docsTo(locator string, caps ...ledger.Capability) ledger.Destination {
	return ledger.Destination{Kind: "docs", Locator: locator, Capabilities: caps}
}

// seedPromotable synthesises one Insight through the real Harvest path and
// returns both ids, because a proposal names an Insight and a revision together
// and fk_pp_rev is composite precisely so they cannot be mixed.
func seedPromotable(t *testing.T, f *fixture, title string) (ledger.InsightID, ledger.RevisionID) {
	t.Helper()
	crumb := f.capture("a fragment worth promoting: "+title, 0.7)
	revision := f.seedInsight(title, crumb.ID)
	page, err := f.L.Insights(context.Background(), ledger.InsightQuery{})
	if err != nil {
		t.Fatalf("listing insights: %v", err)
	}
	for _, view := range page.Insights {
		if view.HeadRevisionID == revision {
			return view.ID, revision
		}
	}
	t.Fatalf("no Insight has head revision %s", revision)
	return "", ""
}

func propose(t *testing.T, led *ledger.Ledger, c ledger.ProposePromotion) ledger.ProposalResult {
	t.Helper()
	res, err := led.ProposePromotion(context.Background(), c)
	if err != nil {
		t.Fatalf("proposing to %s:%s: %v", c.Destination.Kind, c.Destination.Locator, err)
	}
	return res
}

// TestProposeIsIdempotentByContentHash is the release gate for "idempotent by
// proposal". The second call is answered by uq_pp_hash, not by a lookup this
// package performs first: idempotency is a property of the database.
func TestProposeIsIdempotentByContentHash(t *testing.T) {
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "retry budgets")

	intent := ledger.ProposePromotion{
		InsightID: insight, Class: "learning", Destination: docsTo("docs/learnings.md"),
		Content: "Retry budgets are per-attempt.", Confidence: 0.6,
	}
	first := propose(t, f.L, intent)
	if !first.Created {
		t.Fatal("the first proposal reports created=false")
	}
	second := propose(t, f.L, intent)
	if second.Created {
		t.Fatal("re-proposing identical content reports created=true")
	}
	if second.Proposal.ID != first.Proposal.ID {
		t.Fatalf("idempotent hit returned %s, want %s", second.Proposal.ID, first.Proposal.ID)
	}
	if second.ContentHash != first.ContentHash {
		t.Fatalf("content hash changed between identical proposals: %s vs %s",
			second.ContentHash, first.ContentHash)
	}
	if n := f.count(`SELECT COUNT(*) FROM promotion_proposals`); n != 1 {
		t.Fatalf("promotion_proposals rows = %d, want 1", n)
	}

	// A different destination is a different proposal, not a second attempt.
	intent.Destination = docsTo("docs/decisions.md")
	other := propose(t, f.L, intent)
	if !other.Created || other.Proposal.ID == first.Proposal.ID {
		t.Fatalf("a new destination produced %+v, want a distinct proposal", other.Proposal.ID)
	}
}

// TestProposeWithStricterAuthorityIsNotAnIdempotentHit covers §2.5.9: the
// requested level is inside the hash, because otherwise the same text
// re-proposed as mandatory would return the earlier advisory proposal and the
// stricter request would silently disappear.
func TestProposeWithStricterAuthorityIsNotAnIdempotentHit(t *testing.T) {
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "deploy order")

	intent := ledger.ProposePromotion{
		InsightID: insight, Class: "learning", Destination: docsTo("docs/learnings.md"),
		Content: "Run migrations before deploy.", Confidence: 0.6,
	}
	advisory := propose(t, f.L, intent)

	intent.RequestedAuthority = ledger.AuthorityMandatory
	strict, err := f.L.ProposePromotion(context.Background(), intent)
	// Asking for mandatory is asking for something only a human may grant, so
	// the agent's proposal is recorded and refused rather than merged into the
	// advisory one.
	if !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("err = %v, want authority denied", err)
	}
	if !strict.Created {
		t.Fatal("the stricter request was answered as an idempotent hit")
	}
	if strict.ContentHash == advisory.ContentHash {
		t.Fatal("requested authority is not in the content hash")
	}
	if n := f.count(`SELECT COUNT(*) FROM promotion_proposals`); n != 2 {
		t.Fatalf("promotion_proposals rows = %d, want 2; a blocked proposal is still recorded", n)
	}
}

// TestIdempotentHitWarnsOnDivergentConfidence covers the other half of §2.5.9:
// confidence and evidence are outside the hash because neither changes the
// artifact, so a divergent one is reported rather than applied.
func TestIdempotentHitWarnsOnDivergentConfidence(t *testing.T) {
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "queue ownership")

	intent := ledger.ProposePromotion{
		InsightID: insight, Class: "learning", Destination: docsTo("docs/learnings.md"),
		Content: "The worker drains the queue.", Confidence: 0.4,
	}
	propose(t, f.L, intent)

	intent.Confidence = 0.9
	intent.Evidence = []ledger.RefSpec{
		{Kind: "docs", Locator: "docs/queue.md", Relation: ledger.RelationEvidence},
	}
	hit := propose(t, f.L, intent)
	if hit.Created {
		t.Fatal("a divergent confidence created a second proposal")
	}
	if !hasNotice(hit.Notices, "proposal_confidence_diverged") {
		t.Fatalf("notices = %+v, want proposal_confidence_diverged", hit.Notices)
	}
	if !hasNotice(hit.Notices, "proposal_evidence_diverged") {
		t.Fatalf("notices = %+v, want proposal_evidence_diverged", hit.Notices)
	}
	if hit.Proposal.Confidence != 0.4 {
		t.Fatalf("stored confidence = %v, want the original 0.4; proposals are immutable", hit.Proposal.Confidence)
	}
	if n := f.count(`SELECT COUNT(*) FROM ref_links WHERE record_kind = 'promotion_proposal'`); n != 0 {
		t.Fatalf("ref_links on the proposal = %d, want 0; an idempotent hit adds no evidence", n)
	}
}

// TestProposeBlockedWhenHumanAuthorityRequired is the release gate for
// "promotion cannot bypass review or authority policy". The blocked proposal is
// still recorded, which is what makes granting authority and retrying a real
// path rather than advice.
func TestProposeBlockedWhenHumanAuthorityRequired(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "release policy")

	intent := ledger.ProposePromotion{
		InsightID: insight, Class: "decision",
		Destination: docsTo("docs/decisions.md", ledger.CapRequiresHumanAuthority),
		Content:     "Releases are cut on Tuesdays.", Confidence: 0.6,
	}
	blocked, err := f.L.ProposePromotion(ctx, intent)
	if !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("err = %v, want authority denied", err)
	}
	var le *ledger.Error
	if !errors.As(err, &le) || le.Code != "authority_required" {
		t.Fatalf("error code = %+v, want authority_required", err)
	}
	if le.Details["proposal_id"] != string(blocked.Proposal.ID) {
		t.Fatalf("details = %+v, want the recorded proposal id", le.Details)
	}
	if n := f.count(`SELECT COUNT(*) FROM promotion_proposals`); n != 1 {
		t.Fatalf("promotion_proposals rows = %d, want 1", n)
	}

	// Recording a receipt is gated by the same rule, or the exit-3 path would
	// be advice an agent could simply ignore.
	if _, err := f.L.RecordPromotion(ctx, ledger.RecordPromotion{
		ProposalID: blocked.Proposal.ID, Locator: "docs/decisions.md",
	}); !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("recording err = %v, want authority denied", err)
	}

	human := ledgerWithConfig(t, f, humanActor(), func(*ledger.RepoConfig) {})
	if _, err := human.GrantAuthority(ctx, ledger.GrantAuthority{
		Target: ledger.RecordRef{Kind: ledger.KindProposal, ID: string(blocked.Proposal.ID)},
		Level:  ledger.AuthorityDefault, Rationale: "reviewed and approved",
	}); err != nil {
		t.Fatalf("granting authority: %v", err)
	}

	retry := propose(t, f.L, intent)
	if retry.Created || retry.Proposal.ID != blocked.Proposal.ID {
		t.Fatalf("the retry produced %+v, want the recorded proposal", retry.Proposal.ID)
	}
	if _, err := f.L.RecordPromotion(ctx, ledger.RecordPromotion{
		ProposalID: blocked.Proposal.ID, Locator: "docs/decisions.md",
	}); err != nil {
		t.Fatalf("recording after the grant: %v", err)
	}
}

// TestPolicyClassAlwaysRequiresHuman covers invariant §2.5.4: the class's own
// requirement holds regardless of what the destination declares.
func TestPolicyClassAlwaysRequiresHuman(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "review policy")

	intent := ledger.ProposePromotion{
		InsightID: insight, Class: "policy", Destination: docsTo("docs/policies.md"),
		Content: "Every PR needs one reviewer.", Confidence: 0.8,
	}
	blocked, err := f.L.ProposePromotion(ctx, intent)
	if !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("err = %v, want authority denied for a policy-class proposal", err)
	}
	if blocked.AuthorityRequired != ledger.RequireHuman {
		t.Fatalf("authority required = %q, want human", blocked.AuthorityRequired)
	}

	// The same proposal from a human is the same proposal — the hash does not
	// cover the actor — and a human proposing it is the human decision.
	human := ledgerWithConfig(t, f, humanActor(), func(*ledger.RepoConfig) {})
	allowed := propose(t, human, intent)
	if allowed.Created || allowed.Proposal.ID != blocked.Proposal.ID {
		t.Fatalf("the human proposal produced %+v, want the recorded proposal", allowed.Proposal.ID)
	}

	// A permissive destination does not relax the class.
	intent.Destination = docsTo("docs/other-policies.md", ledger.CapStableAnchor)
	if _, err := f.L.ProposePromotion(ctx, intent); !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("err = %v, want authority denied whatever the destination declares", err)
	}
}

// TestAttemptsAreIndependentPerDestination is the release gate for "promotion
// attempts independent by destination": one destination failing has zero
// lifecycle effect on any other.
func TestAttemptsAreIndependentPerDestination(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "cache invalidation")

	base := ledger.ProposePromotion{
		InsightID: insight, Class: "learning",
		Content: "Invalidate on write, not on read.", Confidence: 0.7,
	}
	base.Destination = docsTo("docs/learnings.md", ledger.CapStableAnchor)
	toDocs := propose(t, f.L, base)
	base.Destination = docsTo("bdc-42")
	base.Destination.Kind = "beads"
	toBeads := propose(t, f.L, base)

	if _, err := f.L.FailPromotion(ctx, ledger.FailPromotion{
		ProposalID: toBeads.Proposal.ID, Detail: "bd exited 1: no such issue",
	}); err != nil {
		t.Fatalf("failing the beads attempt: %v", err)
	}
	applied, err := f.L.RecordPromotion(ctx, ledger.RecordPromotion{
		ProposalID: toDocs.Proposal.ID, Locator: "docs/learnings.md",
		Anchor: "9447d97", Verified: true,
	})
	if err != nil {
		t.Fatalf("recording the docs attempt: %v", err)
	}
	if !applied.Durable {
		t.Fatal("a stable-anchor destination with an anchor reported durable=false")
	}

	views, err := f.L.Promotions(ctx, ledger.PromotionQuery{InsightID: insight})
	if err != nil {
		t.Fatalf("listing promotions: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("proposals = %d, want 2", len(views))
	}
	for _, v := range views {
		switch v.ID {
		case toDocs.Proposal.ID:
			if v.Status != ledger.PromotionApplied || v.Receipt == nil {
				t.Fatalf("docs proposal = %s with receipt %v, want applied with a receipt", v.Status, v.Receipt)
			}
			if !v.Durable {
				t.Fatal("the docs receipt is not reported durable")
			}
		case toBeads.Proposal.ID:
			if v.Status != ledger.PromotionFailed || v.Receipt != nil {
				t.Fatalf("beads proposal = %s with receipt %v, want failed with none", v.Status, v.Receipt)
			}
			if len(v.Attempts) != 1 || v.Attempts[0].Attempt != 1 {
				t.Fatalf("beads attempts = %+v, want exactly attempt 1", v.Attempts)
			}
		default:
			t.Fatalf("unexpected proposal %s", v.ID)
		}
	}
}

// TestFailedAttemptThenRetryApplies is the reason FailPromotion exists: without
// it a destination outage strands a proposal at `proposed` forever and the
// `failed` status is unreachable.
func TestFailedAttemptThenRetryApplies(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "timeout defaults")

	proposal := propose(t, f.L, ledger.ProposePromotion{
		InsightID: insight, Class: "learning", Destination: docsTo("docs/learnings.md"),
		Content: "Default timeouts are 30s.", Confidence: 0.5,
	})
	failed, err := f.L.FailPromotion(ctx, ledger.FailPromotion{
		ProposalID: proposal.Proposal.ID, Detail: "the write host was unreachable",
	})
	if err != nil {
		t.Fatalf("failing: %v", err)
	}
	if failed.Promotion.Attempt != 1 || failed.Promotion.Status != ledger.PromotionFailed {
		t.Fatalf("first attempt = %+v, want attempt 1 failed", failed.Promotion)
	}

	applied, err := f.L.RecordPromotion(ctx, ledger.RecordPromotion{
		ProposalID: proposal.Proposal.ID, Locator: "docs/learnings.md",
	})
	if err != nil {
		t.Fatalf("retrying: %v", err)
	}
	if applied.Promotion.Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", applied.Promotion.Attempt)
	}
	if applied.Durable {
		t.Fatal("a destination with no stable-anchor reported durable=true")
	}
	if !hasNotice(applied.Notices, "receipt_not_durable") {
		t.Fatalf("notices = %+v, want receipt_not_durable", applied.Notices)
	}
	if n := f.count(`SELECT COUNT(*) FROM promotions WHERE proposal_id = ?`, string(proposal.Proposal.ID)); n != 2 {
		t.Fatalf("promotions rows = %d, want 2", n)
	}
}

// TestRejectionIsNotAFailure keeps the two terminal outcomes distinguishable: a
// rejection is a decision not to write, a failure is a write that did not land,
// and a retry reads them differently.
func TestRejectionIsNotAFailure(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "logging format")

	proposal := propose(t, f.L, ledger.ProposePromotion{
		InsightID: insight, Class: "learning", Destination: docsTo("docs/learnings.md"),
		Content: "Log in JSON.", Confidence: 0.5,
	})
	rejected, err := f.L.RejectPromotion(ctx, ledger.RejectPromotion{
		ProposalID: proposal.Proposal.ID, Rationale: "already documented elsewhere",
	})
	if err != nil {
		t.Fatalf("rejecting: %v", err)
	}
	if rejected.Promotion.Status != ledger.PromotionRejected {
		t.Fatalf("status = %q, want rejected", rejected.Promotion.Status)
	}
	if rejected.Promotion.Detail != "already documented elsewhere" {
		t.Fatalf("detail = %q, want the rationale", rejected.Promotion.Detail)
	}

	if _, err := f.L.RejectPromotion(ctx, ledger.RejectPromotion{
		ProposalID: proposal.Proposal.ID,
	}); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input for a rejection with no rationale", err)
	}
}

// TestMappingClassRequiresTwoSubjects is the arity rule: mapping is the one
// class whose validity depends on two external things, and no new relation type
// is needed to say so.
func TestMappingClassRequiresTwoSubjects(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "account mapping")

	intent := ledger.ProposePromotion{
		InsightID: insight, Class: "mapping", Destination: docsTo("docs/mappings.md"),
		Content: "Salesforce Account maps to the Foundry Customer object.", Confidence: 0.7,
		Evidence: []ledger.RefSpec{
			{Kind: "sf", Locator: "Account", Relation: ledger.RelationSubject},
		},
	}
	if _, err := f.L.ProposePromotion(ctx, intent); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input for a mapping with one subject", err)
	}
	// Evidence is not a subject: the arity rule counts what the mapping relates,
	// not what supports it.
	intent.Evidence = append(intent.Evidence,
		ledger.RefSpec{Kind: "docs", Locator: "docs/ontology.md", Relation: ledger.RelationEvidence})
	if _, err := f.L.ProposePromotion(ctx, intent); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input; evidence does not satisfy the subject arity", err)
	}

	intent.Evidence = append(intent.Evidence,
		ledger.RefSpec{Kind: "foundry", Locator: "Customer", Relation: ledger.RelationSubject})
	res := propose(t, f.L, intent)
	if !res.Created {
		t.Fatal("a mapping with two subjects was refused")
	}
	if n := f.count(`SELECT COUNT(*) FROM ref_links WHERE record_kind = 'promotion_proposal' AND record_id = ?`,
		string(res.Proposal.ID)); n != 3 {
		t.Fatalf("ref_links on the proposal = %d, want 3", n)
	}
}

// TestReceiptLocatorMayDifferFromTheProposal is the ADR-numbering case: the
// repository decides where a record lands, and the receipt records what
// actually happened rather than what was asked for.
func TestReceiptLocatorMayDifferFromTheProposal(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	insight, _ := seedPromotable(t, f, "storage engine")

	proposal := propose(t, f.L, ledger.ProposePromotion{
		InsightID: insight, Class: "adr", Destination: docsTo("docs/adr/NNNN-storage.md"),
		Content: "We use embedded Dolt.", Confidence: 0.9,
	})
	res, err := f.L.RecordPromotion(ctx, ledger.RecordPromotion{
		ProposalID: proposal.Proposal.ID, Locator: "docs/adr/0007-storage.md",
		Anchor: "c3208be", Verified: true,
	})
	if err != nil {
		t.Fatalf("recording: %v", err)
	}
	if res.Receipt.Locator != "docs/adr/0007-storage.md" {
		t.Fatalf("receipt locator = %q, want the written one", res.Receipt.Locator)
	}
	if res.Receipt.ReferenceID == "" {
		t.Fatal("the receipt minted no Reference for the written record")
	}
	if n := f.count(`SELECT COUNT(*) FROM refs WHERE locator = ?`, "docs/adr/0007-storage.md"); n != 1 {
		t.Fatalf("refs rows for the written locator = %d, want 1", n)
	}
}

// grant makes one human authority grant and fails the test if the ledger
// refuses it.
func grant(t *testing.T, f *fixture, c ledger.GrantAuthority) {
	t.Helper()
	human := ledgerWithConfig(t, f, humanActor(), func(*ledger.RepoConfig) {})
	if c.Rationale == "" {
		c.Rationale = "confirming this is the settled default for day-to-day use"
	}
	if _, err := human.GrantAuthority(context.Background(), c); err != nil {
		t.Fatalf("granting %s on %s: %v", c.Level, c.Target, err)
	}
}

// A human grant on the Insight revision, made for an unrelated reason, does not
// let an agent promote a policy-class proposal it authored: §2.5.4 says `policy`
// always requires a human, and a grant that never saw this proposal's content is
// not that decision.
func TestRevisionGrantDoesNotAuthorizePolicyPromotion(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	insight, revision := seedPromotable(t, f, "how the store is used")

	grant(t, f, ledger.GrantAuthority{
		Target: ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision)},
		Level:  ledger.AuthorityDefault,
	})

	intent := ledger.ProposePromotion{
		InsightID: insight, Class: "policy", Destination: docsTo("docs/policy/fresh.md"),
		Content: "agent-authored policy change, never reviewed by a human", Confidence: 0.6,
	}
	blocked, err := f.L.ProposePromotion(ctx, intent)
	if !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("propose err = %v, want authority denied: a revision grant is not a decision about this proposal", err)
	}
	if _, err := f.L.RecordPromotion(ctx, ledger.RecordPromotion{
		ProposalID: blocked.Proposal.ID, Locator: "docs/policy/fresh.md",
	}); !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("record err = %v, want authority denied", err)
	}

	// The grant the exit-3 message actually prescribes does unblock it.
	grant(t, f, ledger.GrantAuthority{
		Target: ledger.RecordRef{Kind: ledger.KindProposal, ID: string(blocked.Proposal.ID)},
		Level:  ledger.AuthorityDefault, Rationale: "read the content and approved it",
	})
	if _, err := f.L.RecordPromotion(ctx, ledger.RecordPromotion{
		ProposalID: blocked.Proposal.ID, Locator: "docs/policy/fresh.md",
	}); err != nil {
		t.Fatalf("recording after a grant on the proposal: %v", err)
	}
}

// A grant narrowed with `--scope` or `--destination` covers only what it names,
// and a narrowing the ledger cannot interpret never widens.
func TestNarrowedGrantCoversOnlyWhatItNames(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	insight, revision := seedPromotable(t, f, "where decisions are written")
	revisionRef := ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision)}

	adr := ledger.ProposePromotion{
		InsightID: insight, Class: "decision",
		Destination: docsTo("docs/adr/0001.md", ledger.CapStableAnchor),
		Content:     "Decisions live in the ADR tree.", Confidence: 0.7,
		RequestedAuthority: ledger.AuthorityMandatory,
	}
	blocked, err := f.L.ProposePromotion(ctx, adr)
	if !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("propose err = %v, want authority denied", err)
	}

	// A grant scoped to something the ledger cannot interpret, pointed at a
	// different destination, does not reach this one.
	grant(t, f, ledger.GrantAuthority{
		Target: revisionRef, Level: ledger.AuthorityDefault,
		Scope: "wiki-only", DestinationKind: "wiki", DestinationLocator: "https://example.test/other",
	})
	if _, err := f.L.ProposePromotion(ctx, adr); !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("propose err = %v, want authority denied: the grant names another destination", err)
	}

	// Naming this destination, with no scope, is the grant that covers it.
	grant(t, f, ledger.GrantAuthority{
		Target: revisionRef, Level: ledger.AuthorityDefault,
		DestinationKind: "docs", DestinationLocator: "docs/adr/0001.md",
		Rationale: "this conclusion may be written to the ADR tree",
	})
	if _, err := f.L.ProposePromotion(ctx, adr); err != nil {
		t.Fatalf("propose after a matching destination grant: %v", err)
	}
	if _, err := f.L.RecordPromotion(ctx, ledger.RecordPromotion{
		ProposalID: blocked.Proposal.ID, Locator: "docs/adr/0001.md", Anchor: "sha:1",
	}); err != nil {
		t.Fatalf("recording after a matching destination grant: %v", err)
	}

	// The same grant does not reach a second destination.
	other := adr
	other.Destination = docsTo("docs/adr/0002.md", ledger.CapStableAnchor)
	if _, err := f.L.ProposePromotion(ctx, other); !errors.Is(err, ledger.ErrAuthorityDenied) {
		t.Fatalf("propose err = %v, want authority denied for a destination no grant names", err)
	}
}
