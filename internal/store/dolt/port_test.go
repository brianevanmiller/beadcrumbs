package dolt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// The storage port is the seam the whole product crosses, so it is tested at
// that seam and nowhere past it. One ledger carries the whole sequence because
// the reads are only interesting against data the writes actually placed.

func TestStorePortRoundTrip(t *testing.T) {
	ctx := context.Background()
	var store ledger.Store = schemaFixture(t)
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	human := ledger.Provenance{ActorID: "brian", ActorKind: ledger.ActorHuman}
	agent := ledger.Provenance{
		ActorID: "claude", ActorKind: ledger.ActorAgent,
		ActorModel: "claude-opus-5", SessionID: "sess-1",
	}

	crumbA, crumbB := ledger.NewCrumbID(), ledger.NewCrumbID()
	harvestID := ledger.NewHarvestID()
	insightID, revision1 := ledger.NewInsightID(), ledger.NewRevisionID()
	proposalID := ledger.NewProposalID()
	referenceID := ledger.ReferenceIDFor("beads", "bdc-7ah", "")

	if err := store.Write(ctx, func(tx ledger.Tx) error {
		for i, id := range []ledger.CrumbID{crumbA, crumbB} {
			if err := tx.InsertCrumb(ledger.Crumb{
				ID: id, Content: "fragment", ContentHash: hash64(byte('a' + i)),
				ReviewState: ledger.StateCandidate, Confidence: 0.7,
				CapturedAt:       at.Add(time.Duration(i) * time.Minute),
				RedactionVersion: "1", Provenance: agent,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("capturing crumbs: %v", err)
	}

	// A read outside the write transaction proves the commit landed and that
	// DECIMAL, DATETIME(6), and ENUM all survive the round trip.
	var crumbs []ledger.CrumbRow
	if err := store.Read(ctx, func(snap ledger.Snapshot) error {
		var err error
		crumbs, err = snap.Crumbs(ledger.CrumbQuery{States: []ledger.ReviewState{ledger.StateCandidate}})
		return err
	}); err != nil {
		t.Fatalf("listing crumbs: %v", err)
	}
	if len(crumbs) != 2 {
		t.Fatalf("expected 2 candidate crumbs, got %d", len(crumbs))
	}
	// Newest first.
	if crumbs[0].ID != crumbB {
		t.Fatalf("expected the newest crumb first, got %s", crumbs[0].ID)
	}
	if got := crumbs[0].Confidence; got != 0.7 {
		t.Fatalf("confidence round-tripped as %v, want 0.7", got)
	}
	if got := crumbs[0].CapturedAt; !got.Equal(at.Add(time.Minute)) {
		t.Fatalf("captured_at round-tripped as %s, want %s", got, at.Add(time.Minute))
	}
	if crumbs[0].Provenance != agent {
		t.Fatalf("provenance round-tripped as %+v", crumbs[0].Provenance)
	}

	// Review moves the materialised state and appends the event. They travel
	// together so a list can never disagree with the history behind it.
	if err := store.Write(ctx, func(tx ledger.Tx) error {
		return tx.AppendCrumbReview(ledger.CrumbReviewEvent{
			ID: ledger.NewReviewEventID(), CrumbID: crumbA,
			FromState: ledger.StateCandidate, ToState: ledger.StateAccepted,
			Rationale: "worth keeping", OccurredAt: at.Add(time.Hour), Provenance: human,
		})
	}); err != nil {
		t.Fatalf("reviewing: %v", err)
	}

	if err := store.Write(ctx, func(tx ledger.Tx) error {
		if err := tx.InsertHarvest(ledger.Harvest{
			ID: harvestID, Mode: ledger.HarvestManual, Outcome: ledger.HarvestCompleted,
			CrumbsConsidered: 2, CrumbsSelected: 1, PolicyVersion: "1", RedactionVersion: "1",
			StartedAt: at, FinishedAt: at.Add(2 * time.Hour), Provenance: human,
		}, []ledger.HarvestCrumb{
			{CrumbID: crumbA, Role: ledger.RoleSelected},
			{CrumbID: crumbB, Role: ledger.RoleConsidered},
		}); err != nil {
			return err
		}
		return tx.InsertRevision(ledger.InsightRevision{
			ID: revision1, InsightID: insightID, Revision: 1,
			Title: "a learning", Content: "the body", ContentHash: hash64('c'),
			Class: "learning", Confidence: 0.8, HarvestID: harvestID,
			CreatedAt: at.Add(2 * time.Hour), Provenance: human,
		}, []ledger.CrumbID{crumbA})
	}); err != nil {
		t.Fatalf("harvesting: %v", err)
	}

	// Revision 2 exercises the lineage constraint and the head update, which is
	// the only place the materialised head can go wrong.
	revision2 := ledger.NewRevisionID()
	if err := store.Write(ctx, func(tx ledger.Tx) error {
		if err := tx.InsertRevision(ledger.InsightRevision{
			ID: revision2, InsightID: insightID, Revision: 2,
			Title: "a sharper learning", Content: "a better body", ContentHash: hash64('d'),
			Class: "learning", Confidence: 0.9, Rationale: "clarified",
			ParentRevisionID: revision1, CreatedAt: at.Add(3 * time.Hour), Provenance: human,
		}, []ledger.CrumbID{crumbA}); err != nil {
			return err
		}
		return tx.SetInsightHead(insightID, 2)
	}); err != nil {
		t.Fatalf("revising: %v", err)
	}

	if err := store.Write(ctx, func(tx ledger.Tx) error {
		got, created, err := tx.UpsertReference(ledger.Reference{
			ID: referenceID, Kind: "beads", Locator: "bdc-7ah", CreatedAt: at,
		})
		if err != nil {
			return err
		}
		// The same identity a second time is the same Reference, not a
		// duplicate-key error: that is what makes attaching one idempotent.
		// created is the write result, not an id comparison — the deterministic
		// id equals the existing one on a hit.
		again, createdAgain, err := tx.UpsertReference(ledger.Reference{
			ID: ledger.ReferenceIDFor("beads", "bdc-7ah", ""), Kind: "beads", Locator: "bdc-7ah",
			Label: "the epic", FetchedAt: at, CreatedAt: at,
		})
		if err != nil {
			return err
		}
		if got != referenceID || again != referenceID || !created || createdAgain {
			return errors.New("upsert minted a second Reference for one identity")
		}
		if err := tx.LinkReference(ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision2)},
			referenceID, ledger.RelationEvidence); err != nil {
			return err
		}
		// Linking twice is one fact stated twice.
		return tx.LinkReference(ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision2)},
			referenceID, ledger.RelationEvidence)
	}); err != nil {
		t.Fatalf("referencing: %v", err)
	}

	if err := store.Write(ctx, func(tx ledger.Tx) error {
		if err := tx.AppendValidation(ledger.Validation{
			ID: ledger.NewValidationID(), Target: ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision2)},
			Verdict: ledger.VerdictSupported, Rationale: "checked against the source",
			OccurredAt: at.Add(4 * time.Hour), Provenance: human,
		}); err != nil {
			return err
		}
		return tx.AppendAuthority(ledger.Authority{
			ID: ledger.NewAuthorityID(), Target: ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision2)},
			Level: ledger.AuthorityMandatory, Rationale: "a human decided",
			OccurredAt: at.Add(5 * time.Hour), Provenance: human,
		})
	}); err != nil {
		t.Fatalf("judging: %v", err)
	}

	// Idempotency is answered by uq_pp_hash, so the second propose is a hit.
	if err := store.Write(ctx, func(tx ledger.Tx) error {
		p := ledger.Proposal{
			ID: proposalID, InsightID: insightID, RevisionID: revision2, Class: "adr",
			DestKind: "docs", DestLocator: "docs/adr/", Content: "rendered", ContentHash: hash64('e'),
			Confidence: 0.9, RequestedAuthority: ledger.AuthorityAdvisory,
			Capabilities:  []ledger.Capability{ledger.CapStableAnchor, ledger.CapAppendOnly},
			PolicyVersion: "1", RedactionVersion: "1", CreatedAt: at.Add(6 * time.Hour),
			Provenance: human,
		}
		got, created, err := tx.UpsertProposal(p)
		if err != nil || !created || got != proposalID {
			return orElse(err, "first propose must create the proposal")
		}
		p.ID = ledger.NewProposalID()
		got, created, err = tx.UpsertProposal(p)
		if err != nil {
			return err
		}
		if created || got != proposalID {
			return errors.New("re-proposing the same content hash must be an idempotent hit")
		}
		return nil
	}); err != nil {
		t.Fatalf("proposing: %v", err)
	}

	// A failed attempt then a successful retry: the whole reason `promote fail`
	// exists is that attempt 2 must be reachable.
	promotion2 := ledger.NewPromotionID()
	if err := store.Write(ctx, func(tx ledger.Tx) error {
		if err := tx.AppendPromotion(ledger.Promotion{
			ID: ledger.NewPromotionID(), ProposalID: proposalID, Attempt: 1,
			Status: ledger.PromotionFailed, Detail: "the destination was unreachable",
			OccurredAt: at.Add(7 * time.Hour), Provenance: human,
		}); err != nil {
			return err
		}
		if err := tx.AppendPromotion(ledger.Promotion{
			ID: promotion2, ProposalID: proposalID, Attempt: 2,
			Status: ledger.PromotionApplied, OccurredAt: at.Add(8 * time.Hour), Provenance: human,
		}); err != nil {
			return err
		}
		return tx.InsertReceipt(ledger.Receipt{
			ID: ledger.NewReceiptID(), PromotionID: promotion2, Kind: "docs",
			Locator: "docs/adr/0007-a-learning.md", Anchor: "abc123", Verified: true,
			ReferenceID: referenceID, RecordedAt: at.Add(8 * time.Hour), Provenance: human,
		})
	}); err != nil {
		t.Fatalf("promoting: %v", err)
	}

	if err := store.Read(ctx, func(snap ledger.Snapshot) error {
		insights, err := snap.Insights(ledger.InsightQuery{Classes: []string{"learning"}})
		if err != nil {
			return err
		}
		if len(insights) != 1 || insights[0].HeadRevision != 2 || insights[0].Title != "a sharper learning" {
			t.Fatalf("head revision join is wrong: %+v", insights)
		}
		// The verdict and authority filters read the latest event, which is the
		// only reading of "this Insight is supported" the product ever gives.
		supported, err := snap.Insights(ledger.InsightQuery{
			Verdicts:        []ledger.Verdict{ledger.VerdictSupported},
			AuthorityLevels: []ledger.AuthorityLevel{ledger.AuthorityMandatory},
		})
		if err != nil {
			return err
		}
		if len(supported) != 1 {
			t.Fatalf("expected the supported+mandatory Insight, got %d", len(supported))
		}
		unreviewed, err := snap.Insights(ledger.InsightQuery{Verdicts: []ledger.Verdict{ledger.VerdictUnreviewed}})
		if err != nil {
			return err
		}
		if len(unreviewed) != 0 {
			t.Fatalf("a validated Insight must not read as unreviewed")
		}

		revisions, err := snap.Revisions(insightID)
		if err != nil {
			return err
		}
		if len(revisions) != 2 || revisions[0].Revision != 1 || revisions[1].ParentRevisionID != revision1 {
			t.Fatalf("lineage is wrong: %+v", revisions)
		}

		links, err := snap.CrumbLinks(crumbA)
		if err != nil {
			return err
		}
		if len(links.Harvests) != 1 || len(links.Revisions) != 2 {
			t.Fatalf("crumb links are wrong: %+v", links)
		}

		refs, err := snap.References(ledger.ReferenceQuery{
			Target: &ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision2)},
		})
		if err != nil {
			return err
		}
		if len(refs) != 1 || refs[0].Label != "the epic" {
			t.Fatalf("references are wrong: %+v", refs)
		}

		refLinks, err := snap.ReferenceLinks(ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision2)})
		if err != nil {
			return err
		}
		if len(refLinks) != 1 {
			t.Fatalf("linking twice created %d links", len(refLinks))
		}

		proposals, err := snap.Proposals(ledger.PromotionQuery{
			InsightID: insightID, Statuses: []ledger.PromotionStatus{ledger.PromotionApplied},
		})
		if err != nil {
			return err
		}
		if len(proposals) != 1 {
			t.Fatalf("status filter reads the latest attempt; got %d proposals", len(proposals))
		}
		if got := proposals[0].Capabilities; len(got) != 2 || got[0] != ledger.CapAppendOnly {
			t.Fatalf("capabilities round-tripped as %v, want canonical order", got)
		}

		attempts, receipts, err := snap.Attempts([]ledger.ProposalID{proposalID})
		if err != nil {
			return err
		}
		if len(attempts) != 2 || attempts[1].Attempt != 2 || len(receipts) != 1 {
			t.Fatalf("attempts are wrong: %+v / %+v", attempts, receipts)
		}

		events, err := snap.Events(ledger.EventQuery{})
		if err != nil {
			return err
		}
		if len(events) != 3 {
			t.Fatalf("expected review + validation + authority, got %d", len(events))
		}
		if events[0].Kind != ledger.EventReview || events[2].Kind != ledger.EventAuthority {
			t.Fatalf("events are not in time order: %+v", events)
		}

		orphans, err := snap.OrphanTargets()
		if err != nil {
			return err
		}
		if len(orphans) != 0 {
			t.Fatalf("a consistent ledger reported orphans: %+v", orphans)
		}
		drift, err := snap.HeadRevisionDrift()
		if err != nil {
			return err
		}
		if len(drift) != 0 {
			t.Fatalf("head drift on a consistent ledger: %+v", drift)
		}

		counts, err := snap.Counts(ledger.CountQuery{})
		if err != nil {
			return err
		}
		if counts.CrumbsByState[ledger.StateAccepted] != 1 ||
			counts.CrumbsByState[ledger.StateCandidate] != 1 ||
			counts.Insights != 1 || counts.Revisions != 2 || counts.Proposals != 1 ||
			counts.PromotionsByStatus[ledger.PromotionApplied] != 1 {
			t.Fatalf("counts are wrong: %+v", counts)
		}

		cfg, err := snap.Config()
		if err != nil {
			return err
		}
		if cfg[ledger.ConfigHarvestAuto] != "0" {
			t.Fatalf("automatic harvesting must be seeded off, got %q", cfg[ledger.ConfigHarvestAuto])
		}
		return nil
	}); err != nil {
		t.Fatalf("reading back: %v", err)
	}

	// The one thing the database cannot do for us: a pruned Crumb must leave no
	// polymorphic row behind, because neither column carries a foreign key.
	if err := store.Write(ctx, func(tx ledger.Tx) error {
		if err := tx.LinkReference(ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(crumbB)},
			referenceID, ledger.RelationSource); err != nil {
			return err
		}
		return tx.AppendValidation(ledger.Validation{
			ID: ledger.NewValidationID(), Target: ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(crumbB)},
			Verdict: ledger.VerdictDisputed, Rationale: "not reproducible",
			OccurredAt: at.Add(9 * time.Hour), Provenance: human,
		})
	}); err != nil {
		t.Fatalf("attaching to the crumb: %v", err)
	}
	if err := store.Write(ctx, func(tx ledger.Tx) error {
		n, err := tx.DeleteCrumbs([]ledger.CrumbID{crumbB})
		if err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("expected to prune 1 crumb, pruned %d", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("pruning: %v", err)
	}
	if err := store.Read(ctx, func(snap ledger.Snapshot) error {
		orphans, err := snap.OrphanTargets()
		if err != nil {
			return err
		}
		if len(orphans) != 0 {
			t.Fatalf("prune left polymorphic orphans: %+v", orphans)
		}
		return nil
	}); err != nil {
		t.Fatalf("scanning after prune: %v", err)
	}
}

// TestWriteRollsBackOnError: the transaction boundary is the whole promise that
// an interruption cannot leave a partial operation.
func TestWriteRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	var store ledger.Store = schemaFixture(t)
	sentinel := errors.New("the caller changed its mind")

	err := store.Write(ctx, func(tx ledger.Tx) error {
		if err := tx.InsertCrumb(ledger.Crumb{
			ID: ledger.NewCrumbID(), Content: "half a fact", ContentHash: hash64('f'),
			ReviewState: ledger.StateCandidate, Confidence: 0.5,
			CapturedAt: time.Now(), RedactionVersion: "1",
			Provenance: ledger.Provenance{ActorID: "brian", ActorKind: ledger.ActorHuman},
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the caller's error to survive, got %v", err)
	}

	var crumbs []ledger.CrumbRow
	if err := store.Read(ctx, func(snap ledger.Snapshot) error {
		var err error
		crumbs, err = snap.Crumbs(ledger.CrumbQuery{})
		return err
	}); err != nil {
		t.Fatalf("reading after rollback: %v", err)
	}
	if len(crumbs) != 0 {
		t.Fatalf("rollback left %d crumbs behind", len(crumbs))
	}
}

	// TestMigrateIsIdempotent: a healthy ledger reports no work. The value of the
	// check is that a drifted ledger would report some.
	func TestMigrateIsIdempotent(t *testing.T) {
		ctx := context.Background()
		store := schemaFixture(t)

		res, err := store.Migrate(ctx)
		if err != nil {
			t.Fatalf("migrate: %v", err)
		}
		want := CurrentSchemaVersion()
		if res.From != want || res.To != want || len(res.Applied) != 0 {
			t.Fatalf("a current ledger migrated something: %+v", res)
		}
	if v, err := store.SchemaVersion(ctx); err != nil || v != CurrentSchemaVersion() {
		t.Fatalf("schema version is %d (err %v), want %d", v, err, CurrentSchemaVersion())
	}
}

// TestWriteRejectsUnmintedID: ids are minted by the ledger, so a bare or
// mistyped one is a bug in a write path, not user input — and the row it would
// create is unreachable by its own show command.
func TestWriteRejectsUnmintedID(t *testing.T) {
	ctx := context.Background()
	var store ledger.Store = schemaFixture(t)

	err := store.Write(ctx, func(tx ledger.Tx) error {
		return tx.InsertCrumb(ledger.Crumb{
			ID: "not-an-id", Content: "x", ContentHash: hash64('g'),
			ReviewState: ledger.StateCandidate, Confidence: 0.5, CapturedAt: time.Now(),
			RedactionVersion: "1",
			Provenance:       ledger.Provenance{ActorID: "brian", ActorKind: ledger.ActorHuman},
		})
	})
	if !errors.Is(err, ledger.ErrIntegrity) {
		t.Fatalf("expected an integrity error, got %v", err)
	}
}

func hash64(b byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = b
	}
	return string(out)
}

func orElse(err error, msg string) error {
	if err != nil {
		return err
	}
	return errors.New(msg)
}
