package ledger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// A Harvest synthesises: it names the Crumbs it weighed, records their roles,
// and writes revision 1 of a new Insight from the selected ones. The Crumbs
// themselves are untouched — they are inputs, not raw material.
func TestHarvestSynthesisesSelectedCrumbs(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	selected := f.capture("the driver fixes the current database at Connect", 0.8)
	swept := f.capture("a linked worktree resolves the same git-common-dir", 0.4)

	res, err := f.L.CompleteHarvest(ctx, ledger.CompleteHarvest{
		Mode:  ledger.HarvestManual,
		Since: time.Now().UTC().Add(-time.Hour),
		Crumbs: []ledger.CrumbID{
			selected.ID,
		},
		Title: "embedded Dolt selects its database once",
		Content: "Init opens two engines in sequence: the database cannot be selected " +
			"before it exists, and a USE does not survive the statement.",
		Class:      "learning",
		Confidence: 0.75,
	})
	if err != nil {
		t.Fatalf("harvesting: %v", err)
	}

	if res.Harvest.Outcome != ledger.HarvestCompleted || res.Harvest.FailureCode != "" {
		t.Fatalf("expected a completed harvest with no failure code, got %+v", res.Harvest)
	}
	if res.Harvest.CrumbsSelected != 1 || res.Harvest.CrumbsConsidered != 2 {
		t.Fatalf("expected 1 of 2 selected, got %d of %d",
			res.Harvest.CrumbsSelected, res.Harvest.CrumbsConsidered)
	}
	if res.Harvest.PolicyVersion == "" || res.Harvest.RedactionVersion == "" {
		t.Fatalf("a harvest records the policy and redaction versions that judged it: %+v", res.Harvest)
	}
	if res.Revision == nil || res.Revision.Revision != 1 {
		t.Fatalf("expected revision 1, got %+v", res.Revision)
	}
	if res.Revision.HarvestID != res.Harvest.ID {
		t.Fatalf("the revision does not name the Harvest that produced it: %+v", res.Revision)
	}
	if res.Revision.ParentRevisionID != "" || res.Revision.Rationale != "" {
		t.Fatalf("revision 1 has no parent and needs no rationale: %+v", res.Revision)
	}
	if res.Insight == nil || res.Insight.HeadRevision != 1 {
		t.Fatalf("expected a new Insight at head 1, got %+v", res.Insight)
	}

	// The selected Crumb supports the revision; the swept one was only weighed.
	if got := len(f.crumb(selected.ID).Insights); got != 1 {
		t.Fatalf("expected the selected Crumb to support 1 revision, got %d", got)
	}
	if got := len(f.crumb(swept.ID).Insights); got != 0 {
		t.Fatalf("a Crumb in the window was selected without being named: %d revisions", got)
	}
	roles := map[ledger.CrumbID]ledger.HarvestRole{}
	for _, id := range []ledger.CrumbID{selected.ID, swept.ID} {
		for _, link := range f.crumb(id).Harvests {
			if link.HarvestID == res.Harvest.ID {
				roles[id] = link.Role
			}
		}
	}
	if roles[selected.ID] != ledger.RoleSelected || roles[swept.ID] != ledger.RoleConsidered {
		t.Fatalf("roles are wrong: %+v", roles)
	}

	// Selection is a relationship, not a state.
	if state := f.crumb(selected.ID).Crumb.ReviewState; state != ledger.StateCandidate {
		t.Fatalf("synthesis moved the review state to %q", state)
	}
}

// A Harvest that captures its own Crumbs is the reason capture and synthesis
// are one operation: a candidate persisted this way carries the policy and
// redaction versions that judged it, which ck_crumbs_harvest_policy enforces.
func TestHarvestCapturesCandidatesUnderItsPolicy(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	res, err := f.L.CompleteHarvest(ctx, ledger.CompleteHarvest{
		Mode: ledger.HarvestAutomatic,
		Captures: []ledger.CaptureCrumb{
			{Content: "the watchdog names the command that held the engine", Confidence: 0.6},
			{Content: "15 s of backoff is safe only because a command is one transaction", Confidence: 0.5},
		},
		Title:      "lock discipline is a live assertion",
		Content:    "Three checks fire in production builds, not just in tests.",
		Class:      "decision",
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("harvesting: %v", err)
	}
	if len(res.CrumbsCaptured) != 2 {
		t.Fatalf("expected 2 captured Crumbs, got %d", len(res.CrumbsCaptured))
	}
	for _, crumb := range res.CrumbsCaptured {
		if crumb.HarvestID != res.Harvest.ID {
			t.Fatalf("captured Crumb %s does not name its Harvest", crumb.ID)
		}
		if crumb.PolicyVersion != res.Harvest.PolicyVersion || crumb.PolicyVersion == "" {
			t.Fatalf("captured Crumb %s carries policy version %q, harvest carries %q",
				crumb.ID, crumb.PolicyVersion, res.Harvest.PolicyVersion)
		}
		if crumb.ReviewState != ledger.StateCandidate {
			t.Fatalf("a harvested Crumb is a candidate, got %q", crumb.ReviewState)
		}
		// A Crumb the Harvest captured for its conclusion is selected by it.
		if got := len(f.crumb(crumb.ID).Insights); got != 1 {
			t.Fatalf("captured Crumb %s supports %d revisions, want 1", crumb.ID, got)
		}
	}
	if res.Harvest.CrumbsSelected != 2 || res.Harvest.CrumbsConsidered != 2 {
		t.Fatalf("expected 2 of 2 selected, got %d of %d",
			res.Harvest.CrumbsSelected, res.Harvest.CrumbsConsidered)
	}
}

// Automatic harvesting inherits the transcript-shape refusal, because the
// captures it persists are session material and redaction says nothing about
// the product boundary that keeps transcripts out of the ledger.
func TestAutomaticHarvestRefusesTranscriptShapedCaptures(t *testing.T) {
	f := newFixture(t)
	_, err := f.L.CompleteHarvest(context.Background(), ledger.CompleteHarvest{
		Mode:     ledger.HarvestAutomatic,
		Captures: []ledger.CaptureCrumb{{Content: transcriptFixture, Confidence: 0.5}},
	})
	var le *ledger.Error
	if !errors.As(err, &le) || le.Code != "invalid_transcript_shape" {
		t.Fatalf("expected a transcript-shape refusal, got %v", err)
	}
	if f.count(`SELECT COUNT(*) FROM crumbs`) != 0 {
		t.Fatal("a refused automatic harvest still captured a Crumb")
	}
	assertHarvestOutcome(t, f, ledger.HarvestFailed, "invalid_input")
}

// A Harvest that weighs Crumbs and concludes nothing is the durable-completion
// step: it records what was considered so a later session can tell reviewed
// ground from unexamined ground.
func TestHarvestWithoutSynthesisRecordsWhatItWeighed(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("restore renames the live directory aside", 0.6)

	res, err := f.L.CompleteHarvest(ctx, ledger.CompleteHarvest{
		Mode: ledger.HarvestManual, Crumbs: []ledger.CrumbID{crumb.ID},
	})
	if err != nil {
		t.Fatalf("harvesting: %v", err)
	}
	if res.Insight != nil || res.Revision != nil {
		t.Fatalf("a harvest with no synthesis produced an Insight: %+v", res)
	}
	if res.Harvest.CrumbsConsidered != 1 || res.Harvest.CrumbsSelected != 0 {
		t.Fatalf("expected 1 considered and 0 selected, got %d and %d",
			res.Harvest.CrumbsConsidered, res.Harvest.CrumbsSelected)
	}
	if f.count(`SELECT COUNT(*) FROM insights`) != 0 {
		t.Fatal("a harvest with no synthesis wrote an Insight")
	}
}

// A dry run is an aborted Harvest, and the ledger says so. Recording it is the
// point: "we looked and did not commit" is an outcome worth remembering, and it
// is the only way `aborted` is reachable from a healthy process.
func TestHarvestDryRunAbortsAndRecordsTheOutcome(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("prune removes rows from the head, not from history", 0.7)

	res, err := f.L.CompleteHarvest(ctx, ledger.CompleteHarvest{
		Mode: ledger.HarvestManual, Crumbs: []ledger.CrumbID{crumb.ID},
		Title: "prune is retention", Content: "committed history retains a pruned Crumb.",
		Class: "policy", Confidence: 0.9, DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Harvest.Outcome != ledger.HarvestAborted || res.Harvest.FailureCode != "dry_run" {
		t.Fatalf("expected an aborted dry run, got %+v", res.Harvest)
	}
	if res.Harvest.CrumbsSelected != 1 {
		t.Fatalf("a dry run reports what it would select, got %d", res.Harvest.CrumbsSelected)
	}
	if res.Insight != nil || res.Revision != nil {
		t.Fatal("a dry run minted an Insight; a preview must not report records that do not exist")
	}
	if f.count(`SELECT COUNT(*) FROM insights`) != 0 || f.count(`SELECT COUNT(*) FROM insight_revisions`) != 0 {
		t.Fatal("a dry run wrote an Insight")
	}
	if f.count(`SELECT COUNT(*) FROM harvest_crumbs`) != 0 {
		t.Fatal("an aborted harvest recorded crumb roles; the failure row carries no content")
	}
}

// A Harvest that fails inside its transaction rolls back entirely, so the
// `failed` outcome is written by a second transaction that carries the failure
// code and nothing else.
func TestHarvestFailureRecordsTheFailureCode(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	real := f.capture("the aside copy is removed only after the reopen verifies", 0.6)
	missing := ledger.NewCrumbID()

	_, err := f.L.CompleteHarvest(ctx, ledger.CompleteHarvest{
		Mode: ledger.HarvestManual, Crumbs: []ledger.CrumbID{real.ID, missing},
		Title: "restore is atomic", Content: "every step before the first rename is discardable.",
		Class: "decision", Confidence: 0.8,
	})
	if !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("expected a not-found, got %v", err)
	}
	harvest := assertHarvestOutcome(t, f, ledger.HarvestFailed, "crumb_not_found")
	if f.count(`SELECT COUNT(*) FROM insight_revisions`) != 0 {
		t.Fatal("a failed harvest wrote a revision")
	}
	if f.count(`SELECT COUNT(*) FROM harvest_crumbs`) != 0 {
		t.Fatal("a failed harvest recorded crumb roles")
	}

	// The error names the recorded row, so a caller can find what it did.
	var le *ledger.Error
	if !errors.As(err, &le) || le.Details["harvest_id"] != harvest {
		t.Fatalf("the failure does not name the recorded harvest: %+v", le)
	}
}

// The three synthesis fields travel together, and a synthesis needs evidence.
// Both are refused before anything is attempted, so neither records a Harvest.
func TestHarvestRefusesIncompleteSynthesis(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	crumb := f.capture("stealth mode needs no ignore file", 0.5)

	for _, tc := range []struct {
		name string
		in   ledger.CompleteHarvest
		code string
	}{
		{
			name: "title without content or class",
			in:   ledger.CompleteHarvest{Crumbs: []ledger.CrumbID{crumb.ID}, Title: "half a synthesis"},
			code: "invalid_usage",
		},
		{
			name: "synthesis with no Crumbs",
			in: ledger.CompleteHarvest{
				Title: "a conclusion from nothing", Content: "body", Class: "learning", Confidence: 0.5,
			},
			code: "invalid_selection",
		},
		{
			name: "nothing to weigh",
			in:   ledger.CompleteHarvest{},
			code: "invalid_selection",
		},
		{
			name: "unknown class",
			in: ledger.CompleteHarvest{
				Crumbs: []ledger.CrumbID{crumb.ID},
				Title:  "t", Content: "body", Class: "folklore", Confidence: 0.5,
			},
			code: "invalid_class",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.L.CompleteHarvest(ctx, tc.in)
			var le *ledger.Error
			if !errors.As(err, &le) || le.Code != tc.code {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
		})
	}
	if f.count(`SELECT COUNT(*) FROM harvests`) != 0 {
		t.Fatal("a rejected argument list recorded a Harvest; nothing was attempted")
	}
}

// assertHarvestOutcome reads the one `harvests` row straight out of the store,
// because the point of the failure path is what it persisted, not what it
// returned.
func assertHarvestOutcome(t *testing.T, f *fixture, outcome ledger.HarvestOutcome, code string) string {
	t.Helper()
	var (
		id          string
		gotOutcome  string
		gotCode     string
		considered  int
		selected    int
		policy      string
		redaction   string
		gotStarted  time.Time
		gotFinished time.Time
	)
	err := f.Store.DB().QueryRowContext(context.Background(),
		`SELECT id, CAST(outcome AS CHAR), COALESCE(failure_code,''), crumbs_considered,
			crumbs_selected, policy_version, redaction_version, started_at, finished_at
		 FROM harvests`).
		Scan(&id, &gotOutcome, &gotCode, &considered, &selected, &policy, &redaction,
			&gotStarted, &gotFinished)
	if err != nil {
		t.Fatalf("reading the harvests row: %v", err)
	}
	if gotOutcome != string(outcome) || gotCode != code {
		t.Fatalf("expected outcome %s/%s, got %s/%s", outcome, code, gotOutcome, gotCode)
	}
	if policy == "" || redaction == "" {
		t.Fatalf("the failure row lost its policy or redaction version: %q / %q", policy, redaction)
	}
	if gotFinished.Before(gotStarted) {
		t.Fatalf("finished_at %s precedes started_at %s", gotFinished, gotStarted)
	}
	return id
}
