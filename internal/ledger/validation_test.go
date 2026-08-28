package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// TestValidationHistoryIsAppendOnly is the release gate for "review,
// validation, authority, rejection, invalidation, and supersession append
// events". A verdict that reversed an earlier one has to leave the earlier one
// readable, because the reversal is only meaningful next to what it reversed.
func TestValidationHistoryIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("the retry budget is per-attempt, not per-request", 0.6)
	target := ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(crumb.ID)}

	first, err := f.L.RecordValidation(ctx, ledger.RecordValidation{
		Target: target, Verdict: ledger.VerdictSupported, Rationale: "matches the client config",
	})
	if err != nil {
		t.Fatalf("first verdict: %v", err)
	}
	if first.EffectiveVerdict != ledger.VerdictSupported {
		t.Fatalf("effective verdict = %q, want supported", first.EffectiveVerdict)
	}

	second, err := f.L.RecordValidation(ctx, ledger.RecordValidation{
		Target: target, Verdict: ledger.VerdictDisputed, Rationale: "the server overrides it",
		Evidence: []ledger.RefSpec{{Kind: "docs", Locator: "docs/retries.md", Relation: ledger.RelationEvidence}},
	})
	if err != nil {
		t.Fatalf("second verdict: %v", err)
	}
	if second.EffectiveVerdict != ledger.VerdictDisputed {
		t.Fatalf("effective verdict = %q, want disputed", second.EffectiveVerdict)
	}

	if n := f.count(`SELECT COUNT(*) FROM validations WHERE target_id = ?`, string(crumb.ID)); n != 2 {
		t.Fatalf("validations rows = %d, want 2", n)
	}
	history := targetEvents(t, f, target)
	if len(history) != 2 {
		t.Fatalf("events = %d, want 2", len(history))
	}
	if history[0].ID != string(first.Validation.ID) || history[0].Summary != string(ledger.VerdictSupported) {
		t.Fatalf("the first verdict was rewritten: %+v", history[0])
	}
	if history[0].Rationale != "matches the client config" {
		t.Fatalf("first rationale = %q, want the original", history[0].Rationale)
	}

	// The Crumb's own lifecycle is a separate axis and a verdict never moves it.
	if got := f.crumb(crumb.ID).Crumb.ReviewState; got != ledger.StateCandidate {
		t.Fatalf("review state = %q, want candidate; validation must not touch Crumb lifecycle", got)
	}
}

func TestValidationRequiresRationale(t *testing.T) {
	f := newFixture(t)
	crumb := f.capture("a fragment", 0.5)
	_, err := f.L.RecordValidation(context.Background(), ledger.RecordValidation{
		Target:  ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(crumb.ID)},
		Verdict: ledger.VerdictSupported,
	})
	if !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input", err)
	}
	if n := f.count(`SELECT COUNT(*) FROM validations`); n != 0 {
		t.Fatalf("validations rows = %d, want none", n)
	}
}

// TestSupersededVerdictNeedsItsSuccessor is the Go half of ck_val_supersede: a
// superseded record that does not say what replaced it is a dead end.
func TestSupersededVerdictNeedsItsSuccessor(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("an early conclusion", 0.5)
	first := f.seedInsight("first take", crumb.ID)
	second := f.seedInsight("second take", crumb.ID)
	target := ledger.RecordRef{Kind: ledger.KindRevision, ID: string(first)}

	_, err := f.L.RecordValidation(ctx, ledger.RecordValidation{
		Target: target, Verdict: ledger.VerdictSuperseded, Rationale: "replaced",
	})
	if !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input for a superseded verdict with no successor", err)
	}

	res, err := f.L.RecordValidation(ctx, ledger.RecordValidation{
		Target: target, Verdict: ledger.VerdictSuperseded, Rationale: "replaced",
		SupersededBy: ledger.RecordRef{Kind: ledger.KindRevision, ID: string(second)},
		Evidence:     []ledger.RefSpec{{Kind: "docs", Locator: "docs/second.md", Relation: ledger.RelationEvidence}},
	})
	if err != nil {
		t.Fatalf("superseding: %v", err)
	}
	if res.Validation.SupersededBy.ID != string(second) {
		t.Fatalf("superseded_by = %q, want %q", res.Validation.SupersededBy.ID, second)
	}
}

// TestUnsupportedVerdictWithoutEvidenceIsANotice covers invariant §2.5.8:
// "when one exists" is not machine-checkable, so the absence is reported and
// never enforced.
func TestUnsupportedVerdictWithoutEvidenceIsANotice(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("a claim nobody has checked", 0.4)
	target := ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(crumb.ID)}

	res, err := f.L.RecordValidation(ctx, ledger.RecordValidation{
		Target: target, Verdict: ledger.VerdictRejected, Rationale: "contradicted in review",
	})
	if err != nil {
		t.Fatalf("rejecting: %v", err)
	}
	if !hasNotice(res.Notices, "validation_without_evidence") {
		t.Fatalf("notices = %+v, want validation_without_evidence", res.Notices)
	}

	withEvidence, err := f.L.RecordValidation(ctx, ledger.RecordValidation{
		Target: target, Verdict: ledger.VerdictRejected, Rationale: "contradicted in review",
		Evidence: []ledger.RefSpec{{Kind: "docs", Locator: "docs/adr-3.md", Relation: ledger.RelationEvidence}},
	})
	if err != nil {
		t.Fatalf("rejecting with evidence: %v", err)
	}
	if hasNotice(withEvidence.Notices, "validation_without_evidence") {
		t.Fatalf("notices = %+v, want none when evidence was cited", withEvidence.Notices)
	}
	// The evidence hangs off the validation, not off the Crumb: the reason for
	// this verdict belongs to this verdict.
	if n := f.count(`SELECT COUNT(*) FROM ref_links WHERE record_kind = 'validation' AND record_id = ?`,
		string(withEvidence.Validation.ID)); n != 1 {
		t.Fatalf("ref_links on the validation = %d, want 1", n)
	}
}

func TestValidationRejectsUnknownTarget(t *testing.T) {
	f := newFixture(t)
	_, err := f.L.RecordValidation(context.Background(), ledger.RecordValidation{
		Target:  ledger.RecordRef{Kind: ledger.KindCrumb, ID: "crb_00000000-0000-7000-8000-000000000000"},
		Verdict: ledger.VerdictSupported, Rationale: "no such Crumb",
	})
	if !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
}

// targetEvents reads one record's whole append-only history, oldest first.
func targetEvents(t *testing.T, f *fixture, target ledger.RecordRef) []ledger.EventRow {
	t.Helper()
	var rows []ledger.EventRow
	err := f.Store.Read(context.Background(), func(snap ledger.Snapshot) error {
		var err error
		rows, err = snap.Events(ledger.EventQuery{Targets: []ledger.RecordRef{target}})
		return err
	})
	if err != nil {
		t.Fatalf("reading events for %s: %v", target, err)
	}
	return rows
}

func hasNotice(notices []ledger.Notice, code string) bool {
	for _, n := range notices {
		if n.Code == code {
			return true
		}
	}
	return false
}
