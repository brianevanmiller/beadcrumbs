package ledger_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// A Crumb is a reusable fragment, not an input that gets consumed. It may be
// considered by several Harvests and support several Insight revisions at once,
// and none of that may change the Crumb itself.
func TestCrumbReusedAcrossHarvestsAndInsights(t *testing.T) {
	f := newFixture(t)
	first := f.capture("the driver fixes the current database at Connect", 0.8)
	second := f.capture("a USE issued afterwards does not survive the statement", 0.6)
	before := f.crumb(first.ID).Crumb

	f.seedHarvest(first.ID, second.ID)
	f.seedHarvest(first.ID)
	f.seedInsight("embedded Dolt selects its database once", first.ID, second.ID)
	f.seedInsight("open two engines in sequence on init", first.ID)

	detail := f.crumb(first.ID)
	if len(detail.Harvests) != 2 {
		t.Fatalf("expected the Crumb to feed 2 Harvests, got %d", len(detail.Harvests))
	}
	if len(detail.Insights) != 2 {
		t.Fatalf("expected the Crumb to support 2 Insight revisions, got %d", len(detail.Insights))
	}
	if detail.Crumb != before {
		t.Fatalf("participation mutated the Crumb:\n  before: %+v\n  after:  %+v", before, detail.Crumb)
	}
	if detail.Crumb.ReviewState != ledger.StateCandidate {
		t.Fatalf("selection moved the review state to %q; selection is a relationship, not a state",
			detail.Crumb.ReviewState)
	}

	// The second Crumb is still independently reusable: neither Harvest nor
	// Insight took ownership of it.
	if got := len(f.crumb(second.ID).Insights); got != 1 {
		t.Fatalf("expected the second Crumb to support 1 revision, got %d", got)
	}
}

// Review is an opinion recorded after the fact. It appends a transition and
// moves the materialised state; it never rewrites what the Crumb was.
func TestReviewAppendsNeverRewrites(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("the watchdog fires when a handle outlives its command", 0.7)

	first, err := f.L.ReviewCrumb(ctx, ledger.ReviewCrumb{
		IDs: []ledger.CrumbID{crumb.ID}, ToState: ledger.StateAccepted, Rationale: "matches the proof",
	})
	if err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if first.Events[0].FromState != ledger.StateCandidate || first.Events[0].ToState != ledger.StateAccepted {
		t.Fatalf("the transition did not record where it came from: %+v", first.Events[0])
	}

	if _, err := f.L.ReviewCrumb(ctx, ledger.ReviewCrumb{
		IDs: []ledger.CrumbID{crumb.ID}, ToState: ledger.StateRejected, Rationale: "superseded by the measured wait",
	}); err != nil {
		t.Fatalf("rejecting: %v", err)
	}

	detail := f.crumb(crumb.ID)
	if detail.Crumb.ReviewState != ledger.StateRejected {
		t.Fatalf("the materialised state is %q, want rejected", detail.Crumb.ReviewState)
	}
	for _, unchanged := range []struct {
		name      string
		got, want any
	}{
		{"content", detail.Crumb.Content, crumb.Content},
		{"content hash", detail.Crumb.ContentHash, crumb.ContentHash},
		{"confidence", detail.Crumb.Confidence, crumb.Confidence},
		{"captured at", detail.Crumb.CapturedAt, crumb.CapturedAt},
		{"provenance", detail.Crumb.Provenance, crumb.Provenance},
		{"redaction version", detail.Crumb.RedactionVersion, crumb.RedactionVersion},
	} {
		if unchanged.got != unchanged.want {
			t.Fatalf("review rewrote the %s: %v -> %v", unchanged.name, unchanged.want, unchanged.got)
		}
	}

	// Both decisions survive. An append-only history that keeps only the latest
	// row is a history that cannot answer "when did we change our mind".
	if len(detail.ReviewEvents) != 2 {
		t.Fatalf("expected 2 review events, got %d", len(detail.ReviewEvents))
	}
	if detail.ReviewEvents[0].Summary != string(ledger.StateAccepted) ||
		detail.ReviewEvents[1].Summary != string(ledger.StateRejected) {
		t.Fatalf("review history is not in order: %+v", detail.ReviewEvents)
	}
	if f.count(`SELECT COUNT(*) FROM crumb_review_events WHERE crumb_id = ?`, string(crumb.ID)) != 2 {
		t.Fatal("the events table does not hold both transitions")
	}
}

// Moving a Crumb back to candidate would erase a decision rather than record
// one, which is the single thing an append-only history must not permit.
func TestReviewRefusesToMoveBackToCandidate(t *testing.T) {
	f := newFixture(t)
	crumb := f.capture("worth keeping", 0.5)
	_, err := f.L.ReviewCrumb(context.Background(), ledger.ReviewCrumb{
		IDs: []ledger.CrumbID{crumb.ID}, ToState: ledger.StateCandidate, Rationale: "undo",
	})
	if !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("expected an invalid-input error, got %v", err)
	}
}

// A review of several Crumbs is one decision, so a bad id in the batch leaves
// none of them reviewed.
func TestReviewOfAMissingCrumbWritesNothing(t *testing.T) {
	f := newFixture(t)
	crumb := f.capture("the first one", 0.5)
	missing := ledger.NewCrumbID()

	_, err := f.L.ReviewCrumb(context.Background(), ledger.ReviewCrumb{
		IDs:     []ledger.CrumbID{crumb.ID, missing},
		ToState: ledger.StateAccepted, Rationale: "batch",
	})
	if !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("expected a not-found error, got %v", err)
	}
	if got := f.crumb(crumb.ID).Crumb.ReviewState; got != ledger.StateCandidate {
		t.Fatalf("a rolled-back batch still moved a Crumb to %q", got)
	}
	if f.count(`SELECT COUNT(*) FROM crumb_review_events`) != 0 {
		t.Fatal("a rolled-back batch left review events behind")
	}
}

// ref_links.record_id and validations.target_id are polymorphic and carry no
// foreign key, so nothing but prune can clean them. A pruned Crumb must leave
// nothing behind at the head.
func TestPruneRemovesDependentLinksAndValidations(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("a fragment with a reference", 0.4, "beads:bdc-7ah.16@subject")

	f.write(func(tx ledger.Tx) error {
		return tx.AppendValidation(ledger.Validation{
			ID:      ledger.NewValidationID(),
			Target:  ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(crumb.ID)},
			Verdict: ledger.VerdictSupported, Rationale: "confirmed by the proof",
			OccurredAt: time.Now().UTC().Truncate(time.Microsecond), Provenance: f.Actor,
		})
	})
	if f.count(`SELECT COUNT(*) FROM ref_links WHERE record_id = ?`, string(crumb.ID)) != 1 {
		t.Fatal("the fixture did not attach a reference link")
	}

	res, err := f.L.PruneCrumbs(ctx, ledger.PruneCrumbs{
		IDs: []ledger.CrumbID{crumb.ID}, Confirmed: true,
	})
	if err != nil {
		t.Fatalf("pruning: %v", err)
	}
	if res.Pruned != 1 || len(res.Blocked) != 0 {
		t.Fatalf("expected one pruned Crumb and no blockage, got %+v", res)
	}

	for _, check := range []struct {
		name, query string
	}{
		{"crumbs", `SELECT COUNT(*) FROM crumbs WHERE id = ?`},
		{"ref_links", `SELECT COUNT(*) FROM ref_links WHERE record_id = ?`},
		{"validations", `SELECT COUNT(*) FROM validations WHERE target_id = ?`},
		{"crumb_review_events", `SELECT COUNT(*) FROM crumb_review_events WHERE crumb_id = ?`},
	} {
		if n := f.count(check.query, string(crumb.ID)); n != 0 {
			t.Fatalf("prune left %d row(s) in %s", n, check.name)
		}
	}

	// The Reference itself survives: it is shared identity, not the Crumb's
	// property, and deleting it would break every other record that names it.
	if f.count(`SELECT COUNT(*) FROM refs`) != 1 {
		t.Fatal("prune deleted the Reference, which other records may also name")
	}

	report, err := f.L.Doctor(ctx)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	for _, c := range report.Checks {
		if c.Name == "polymorphic_targets" && c.Status != ledger.StatusOK {
			t.Fatalf("prune left a polymorphic orphan: %s", c.Detail)
		}
	}
}

// The database refuses to prune a Crumb that supports an Insight (fk_ic_crumb
// RESTRICT), but a foreign-key violation aborts the whole transaction and loses
// the per-id answer. The ledger therefore checks first and reports blockage per
// Crumb; the constraint is the backstop, not the check.
func TestPruneBlocksCrumbsThatSupportAnInsight(t *testing.T) {
	f := newFixture(t)
	supporting := f.capture("this one became evidence", 0.7)
	loose := f.capture("this one did not", 0.3)
	f.seedInsight("an insight that cites one Crumb", supporting.ID)

	res, err := f.L.PruneCrumbs(context.Background(), ledger.PruneCrumbs{
		IDs: []ledger.CrumbID{supporting.ID, loose.ID}, Confirmed: true,
	})
	if err != nil {
		t.Fatalf("pruning: %v", err)
	}
	if res.Pruned != 1 || len(res.PrunedIDs) != 1 || res.PrunedIDs[0] != loose.ID {
		t.Fatalf("expected only the unsupported Crumb to be pruned, got %+v", res)
	}
	if len(res.Blocked) != 1 || res.Blocked[0].CrumbID != supporting.ID {
		t.Fatalf("expected the supporting Crumb to be blocked, got %+v", res.Blocked)
	}
	if res.Blocked[0].Code != "supports_insight" || len(res.Blocked[0].Revisions) != 1 {
		t.Fatalf("blockage does not name the lineage that caused it: %+v", res.Blocked[0])
	}
	if f.count(`SELECT COUNT(*) FROM crumbs WHERE id = ?`, string(supporting.ID)) != 1 {
		t.Fatal("a blocked Crumb was deleted anyway")
	}
}

// Prune is destructive and unrecoverable at the head, so it never runs on a
// default selection and never runs unconfirmed.
func TestPruneRefusesUnconfirmedAndUnboundedSelections(t *testing.T) {
	f := newFixture(t)
	crumb := f.capture("still a candidate", 0.5)

	for _, tc := range []struct {
		name string
		cmd  ledger.PruneCrumbs
	}{
		{"unconfirmed", ledger.PruneCrumbs{IDs: []ledger.CrumbID{crumb.ID}}},
		{"no selection", ledger.PruneCrumbs{Confirmed: true}},
		{"non-candidate state", ledger.PruneCrumbs{IDs: []ledger.CrumbID{crumb.ID}, State: ledger.StateAccepted, Confirmed: true}},
	} {
		if _, err := f.L.PruneCrumbs(context.Background(), tc.cmd); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Fatalf("%s: expected an invalid-input error, got %v", tc.name, err)
		}
	}
	if f.count(`SELECT COUNT(*) FROM crumbs`) != 1 {
		t.Fatal("a refused prune deleted something")
	}
}

// A listing reports the total the filter matched, not the size of the page:
// a caller has to be able to tell "these are all of them" from "these are the
// first two".
func TestCrumbListPagesAndReportsTheTotal(t *testing.T) {
	f := newFixture(t)
	for _, text := range []string{"first", "second", "third", "fourth"} {
		f.capture(text, 0.5)
	}
	page, err := f.L.Crumbs(context.Background(), ledger.CrumbQuery{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if page.Total != 4 || len(page.Crumbs) != 2 {
		t.Fatalf("expected 2 of 4, got %d of %d", len(page.Crumbs), page.Total)
	}
	if page.Crumbs[0].Content != "third" || page.Crumbs[1].Content != "second" {
		t.Fatalf("paging is not newest-first: %q, %q", page.Crumbs[0].Content, page.Crumbs[1].Content)
	}
}

// Repeated automatic capture of the same fragment within one session is a
// duplicate, and uq_crumbs_hash_session exists to collapse it. Answering with
// the Crumb already held is what that key is for.
func TestRepeatedCaptureInOneSessionDeduplicates(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	const text = "the journal grows one commit per transaction"

	first, err := f.L.CaptureCrumb(ctx, ledger.CaptureCrumb{Content: text, Confidence: 0.6})
	if err != nil {
		t.Fatalf("first capture: %v", err)
	}
	second, err := f.L.CaptureCrumb(ctx, ledger.CaptureCrumb{Content: text, Confidence: 0.6})
	if err != nil {
		t.Fatalf("second capture: %v", err)
	}
	if !second.Deduplicated || second.Crumb.ID != first.Crumb.ID {
		t.Fatalf("expected the second capture to return %s, got %+v", first.Crumb.ID, second)
	}
	if f.count(`SELECT COUNT(*) FROM crumbs`) != 1 {
		t.Fatal("the duplicate was written anyway")
	}
}

func TestParseRefSpec(t *testing.T) {
	cases := []struct {
		arg      string
		kind     string
		locator  string
		relation ledger.Relation
		wantErr  bool
	}{
		{arg: "beads:bdc-7ah.16", kind: "beads", locator: "bdc-7ah.16", relation: ledger.RelationSubject},
		{arg: "beads:bdc-7ah.16@evidence", kind: "beads", locator: "bdc-7ah.16", relation: ledger.RelationEvidence},
		{arg: "docs:docs/adr/0007.md@source", kind: "docs", locator: "docs/adr/0007.md", relation: ledger.RelationSource},
		// A locator that contains an @ keeps it: an unrecognised suffix is part
		// of the locator, not a silent parse failure.
		{arg: "npm:react@18.2.0", kind: "npm", locator: "react@18.2.0", relation: ledger.RelationSubject},
		// The kind ends at the first colon, because a locator routinely holds one.
		{arg: "web:https://example.test/a:b", kind: "web", locator: "https://example.test/a:b", relation: ledger.RelationSubject},
		{arg: "no-colon", wantErr: true},
		{arg: "beads:", wantErr: true},
		{arg: ":locator", wantErr: true},
	}
	for _, tc := range cases {
		spec, err := ledger.ParseRefSpec(tc.arg, ledger.RelationSubject)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q: expected an error, got %+v", tc.arg, spec)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tc.arg, err)
		}
		if spec.Kind != tc.kind || spec.Locator != tc.locator || spec.Relation != tc.relation {
			t.Fatalf("%q parsed as %+v", tc.arg, spec)
		}
	}
}

func TestCaptureRejectsInvalidConfidence(t *testing.T) {
	f := newFixture(t)
	for _, c := range []float64{-0.1, 1.5, 0.12345} {
		_, err := f.L.CaptureCrumb(context.Background(), ledger.CaptureCrumb{
			Content: "a fragment", Confidence: c,
		})
		if !errors.Is(err, ledger.ErrInvalidInput) {
			t.Fatalf("confidence %v: expected an invalid-input error, got %v", c, err)
		}
	}
}

func TestCaptureRequiresContent(t *testing.T) {
	f := newFixture(t)
	_, err := f.L.CaptureCrumb(context.Background(), ledger.CaptureCrumb{
		Content: strings.Repeat(" \n", 4), Confidence: 0.5,
	})
	if !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("expected an invalid-input error, got %v", err)
	}
}
