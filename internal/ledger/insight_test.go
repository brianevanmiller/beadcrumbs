package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// A revision preserves derivation and prior evidence. The parent chain stays
// walkable, every earlier revision keeps the text and the supporting Crumbs it
// was written with, and evidence only ever accumulates — a revision that could
// shed a Crumb would route around the RESTRICT that protects lineage.
func TestReviseInsightPreservesLineage(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	first := f.capture("DOLT_GC reclaimed 39 MB to 188 KB in 83 ms", 0.8)
	second := f.capture("per-transaction commits grow the journal fast", 0.7)

	harvested, err := f.L.CompleteHarvest(ctx, ledger.CompleteHarvest{
		Mode: ledger.HarvestManual, Crumbs: []ledger.CrumbID{first.ID},
		Title: "GC is scheduled, not hoped for", Content: "bdc gc runs it explicitly.",
		Class: "decision", Confidence: 0.6,
	})
	if err != nil {
		t.Fatalf("harvesting: %v", err)
	}
	insightID := harvested.Insight.ID
	original := *harvested.Revision

	revised, err := f.L.ReviseInsight(ctx, ledger.ReviseInsight{
		InsightID: insightID,
		Content:   "capture and harvest also trigger it once the threshold is crossed.",
		Rationale: "the measured journal growth makes an explicit-only GC insufficient",
		Crumbs:    []ledger.CrumbID{second.ID},
	})
	if err != nil {
		t.Fatalf("revising: %v", err)
	}

	if revised.Revision.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", revised.Revision.Revision)
	}
	if revised.Revision.ParentRevisionID != original.ID {
		t.Fatalf("revision 2 does not name revision 1 as its parent: %+v", revised.Revision)
	}
	if revised.Revision.HarvestID != "" {
		t.Fatalf("a revision is not a Harvest and must not claim one: %+v", revised.Revision)
	}
	if revised.Insight.HeadRevision != 2 {
		t.Fatalf("the head did not move: %+v", revised.Insight)
	}
	// Unset fields carry forward; a revision that restates unchanged metadata
	// is a revision that can get it wrong.
	if revised.Revision.Title != original.Title || revised.Revision.Class != original.Class ||
		revised.Revision.Confidence != original.Confidence {
		t.Fatalf("unset metadata did not carry forward: %+v", revised.Revision)
	}

	// Revision 1 is unchanged: same text, same evidence.
	head, err := f.L.Insight(ctx, insightID, ledger.InsightOptions{Lineage: true})
	if err != nil {
		t.Fatalf("reading the Insight: %v", err)
	}
	if len(head.Revisions) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(head.Revisions))
	}
	if head.Revisions[0] != original {
		t.Fatalf("revision 1 was rewritten:\n  before: %+v\n  after:  %+v", original, head.Revisions[0])
	}
	if head.Revision.Revision != 2 {
		t.Fatalf("show reports revision %d as the head", head.Revision.Revision)
	}

	if len(head.Lineage) != 2 {
		t.Fatalf("expected 2 lineage steps, got %d", len(head.Lineage))
	}
	if got := head.Lineage[0].Crumbs; len(got) != 1 || got[0] != first.ID {
		t.Fatalf("revision 1 lost its evidence: %v", got)
	}
	if got := head.Lineage[1].Crumbs; len(got) != 2 {
		t.Fatalf("revision 2 does not carry prior evidence plus the new Crumb: %v", got)
	}
	if head.Lineage[1].Rationale == "" || head.Lineage[1].ParentID != original.ID {
		t.Fatalf("the lineage step lost the reason or the parent: %+v", head.Lineage[1])
	}

	// Both Crumbs now support the Insight, and neither was consumed.
	if got := len(f.crumb(first.ID).Insights); got != 2 {
		t.Fatalf("the original Crumb supports %d revisions, want 2", got)
	}
	if got := len(f.crumb(second.ID).Insights); got != 1 {
		t.Fatalf("the added Crumb supports %d revisions, want 1", got)
	}

	// Reading an earlier revision reports that revision's own evidence.
	older, err := f.L.Insight(ctx, insightID, ledger.InsightOptions{Revision: 1})
	if err != nil {
		t.Fatalf("reading revision 1: %v", err)
	}
	if len(older.Crumbs) != 1 || older.Crumbs[0].ID != first.ID {
		t.Fatalf("revision 1 reports the wrong supporting set: %+v", older.Crumbs)
	}
}

// A revision has to explain itself. ck_rev_lineage enforces it in the database;
// the ledger says so first, in a sentence a caller can act on.
func TestReviseInsightRequiresARationale(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("the head is a cache and a cache with no check is a lie", 0.6)
	f.seedInsight("head_revision is materialised", crumb.ID)

	insights, err := f.L.Insights(ctx, ledger.InsightQuery{})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	id := insights.Insights[0].ID

	_, err = f.L.ReviseInsight(ctx, ledger.ReviseInsight{
		InsightID: id, Content: "a revision with no reason",
	})
	var le *ledger.Error
	if !errors.As(err, &le) || le.Code != "invalid_rationale" {
		t.Fatalf("expected invalid_rationale, got %v", err)
	}
	if f.count(`SELECT COUNT(*) FROM insight_revisions`) != 1 {
		t.Fatal("a refused revision was written anyway")
	}
}

func TestReviseUnknownInsightIsNotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.L.ReviseInsight(context.Background(), ledger.ReviseInsight{
		InsightID: ledger.NewInsightID(),
		Content:   "body", Rationale: "there is nothing to revise",
	})
	if !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("expected a not-found, got %v", err)
	}
}

// A revision may change what it says about itself, and the earlier revision
// keeps what it said.
func TestReviseInsightOverridesMetadataItIsGiven(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("a mapping-class proposal carries two subject references", 0.7)
	f.seedInsight("mapping needs two subjects", crumb.ID)

	page, err := f.L.Insights(ctx, ledger.InsightQuery{})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	id := page.Insights[0].ID

	confidence := 0.95
	revised, err := f.L.ReviseInsight(ctx, ledger.ReviseInsight{
		InsightID: id, Title: "mapping arity is a policy", Content: "two subjects, always.",
		Class: "policy", Confidence: &confidence,
		Rationale: "the arity rule is enforced, so it is policy rather than a learning",
	})
	if err != nil {
		t.Fatalf("revising: %v", err)
	}
	if revised.Revision.Class != "policy" || revised.Revision.Confidence != 0.95 ||
		revised.Revision.Title != "mapping arity is a policy" {
		t.Fatalf("the revision did not take the values it was given: %+v", revised.Revision)
	}

	detail, err := f.L.Insight(ctx, id, ledger.InsightOptions{Revision: 1})
	if err != nil {
		t.Fatalf("reading revision 1: %v", err)
	}
	if detail.Revision.Class != "learning" || detail.Revision.Title != "mapping needs two subjects" {
		t.Fatalf("revision 1 was rewritten: %+v", detail.Revision)
	}
}

// A listing reports the head of each Insight and the total the filter matched,
// so a caller can tell "these are all of them" from "these are the first N".
func TestInsightListFiltersAndReportsTheTotal(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for i, title := range []string{"first", "second", "third"} {
		crumb := f.capture(title+" fragment", 0.5)
		class := "learning"
		if i == 2 {
			class = "adr"
		}
		if _, err := f.L.CompleteHarvest(ctx, ledger.CompleteHarvest{
			Mode: ledger.HarvestManual, Crumbs: []ledger.CrumbID{crumb.ID},
			Title: title, Content: "body", Class: class, Confidence: 0.5,
		}); err != nil {
			t.Fatalf("harvesting %s: %v", title, err)
		}
	}

	page, err := f.L.Insights(ctx, ledger.InsightQuery{Limit: 2})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if page.Total != 3 || len(page.Insights) != 2 {
		t.Fatalf("expected 2 of 3, got %d of %d", len(page.Insights), page.Total)
	}
	// Absence of a judgement means unreviewed and advisory, the same reading
	// every other part of the product gives an empty history.
	for _, view := range page.Insights {
		if view.Verdict != ledger.VerdictUnreviewed || view.Authority != ledger.AuthorityAdvisory {
			t.Fatalf("an unjudged Insight reads as %s/%s", view.Verdict, view.Authority)
		}
		if view.Title == "" || view.HeadRevisionID == "" {
			t.Fatalf("a listing without the head's title is unusable: %+v", view)
		}
	}

	filtered, err := f.L.Insights(ctx, ledger.InsightQuery{Classes: []string{"adr"}})
	if err != nil {
		t.Fatalf("filtering: %v", err)
	}
	if filtered.Total != 1 || filtered.Insights[0].Class != "adr" {
		t.Fatalf("the class filter returned %d rows: %+v", filtered.Total, filtered.Insights)
	}

	if _, err := f.L.Insights(ctx, ledger.InsightQuery{Classes: []string{"folklore"}}); err == nil {
		t.Fatal("an unknown class filtered silently instead of failing")
	}
}

// `bdc insight show` is one read of everything attached to a revision.
func TestInsightShowReportsEverythingAttachedToTheRevision(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("the locator is opaque to core", 0.8)
	harvested, err := f.L.CompleteHarvest(ctx, ledger.CompleteHarvest{
		Mode: ledger.HarvestManual, Crumbs: []ledger.CrumbID{crumb.ID},
		Title: "references are tracker-neutral", Content: "no tracker column exists anywhere.",
		Class: "technical-ontology", Confidence: 0.7,
	})
	if err != nil {
		t.Fatalf("harvesting: %v", err)
	}
	spec, err := ledger.ParseRefSpec("docs:docs/adr/0001-references.md@subject", ledger.RelationSubject)
	if err != nil {
		t.Fatalf("parsing the reference: %v", err)
	}
	if _, err := f.L.AttachReference(ctx, ledger.AttachReference{
		Target: ledger.RecordRef{Kind: ledger.KindRevision, ID: string(harvested.Revision.ID)},
		Ref:    spec,
	}); err != nil {
		t.Fatalf("attaching: %v", err)
	}

	detail, err := f.L.Insight(ctx, harvested.Insight.ID, ledger.InsightOptions{})
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if detail.Insight.ID != harvested.Insight.ID || detail.Revision.ID != harvested.Revision.ID {
		t.Fatalf("show reported the wrong records: %+v", detail)
	}
	if len(detail.Crumbs) != 1 || detail.Crumbs[0].ID != crumb.ID {
		t.Fatalf("the supporting set is wrong: %+v", detail.Crumbs)
	}
	if len(detail.References) != 1 || detail.References[0].Locator != "docs/adr/0001-references.md" {
		t.Fatalf("the reference did not come back: %+v", detail.References)
	}
	// Empty is a list, not a null: a caller that ranges over the JSON must not
	// have to check for one.
	if detail.Validations == nil || detail.Authorities == nil || detail.Proposals == nil {
		t.Fatalf("an empty attachment came back as null: %+v", detail)
	}
	if detail.Lineage != nil {
		t.Fatal("lineage was rendered without being asked for")
	}

	if _, err := f.L.Insight(ctx, harvested.Insight.ID, ledger.InsightOptions{Revision: 7}); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("expected a not-found for a revision that does not exist, got %v", err)
	}
}
