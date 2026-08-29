package dolt

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// TestPrunedCrumbRemainsInDoltHistory documents the guarantee rather than
// pretending to erase. Every write transaction ends in a Dolt commit, and
// DOLT_GC reclaims the journal without rewriting committed history, so
// `bdc crumb prune` removes a Crumb from the head and not from the past.
//
// Two consequences the product states rather than implies: prune is retention,
// not erasure, and a secret that survives redaction is permanent. This test is
// what keeps README's claim honest — if a future change did erase history, it
// would fail here rather than quietly change what the product promises.
func TestPrunedCrumbRemainsInDoltHistory(t *testing.T) {
	ctx := context.Background()
	store := schemaFixture(t)
	id := ledger.NewCrumbID()
	const content = "a fragment that will be pruned from the head"

	if err := store.Write(ctx, func(tx ledger.Tx) error {
		return tx.InsertCrumb(ledger.Crumb{
			ID: id, Content: content, ContentHash: hash64('h'),
			ReviewState: ledger.StateCandidate, Confidence: 0.5,
			CapturedAt:       time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
			RedactionVersion: "1",
			Provenance:       ledger.Provenance{ActorID: "brian", ActorKind: ledger.ActorHuman},
		})
	}); err != nil {
		t.Fatalf("capturing: %v", err)
	}

	if err := store.Write(ctx, func(tx ledger.Tx) error {
		n, err := tx.DeleteCrumbs([]ledger.CrumbID{id})
		if n != 1 {
			t.Fatalf("prune removed %d rows, want 1", n)
		}
		return err
	}); err != nil {
		t.Fatalf("pruning: %v", err)
	}

	if n := countRows(t, store.DB(), `SELECT COUNT(*) FROM crumbs WHERE id = '`+string(id)+`'`); n != 0 {
		t.Fatalf("the pruned Crumb is still at the head: %d row(s)", n)
	}

	// dolt_history_crumbs is every committed version of the table. The Crumb is
	// still in it, content and all.
	var found string
	err := store.DB().QueryRowContext(ctx,
		`SELECT content FROM dolt_history_crumbs WHERE id = ? LIMIT 1`, string(id)).Scan(&found)
	if err != nil {
		t.Fatalf("the pruned Crumb is not in Dolt history, so prune is being read as an erase: %v", err)
	}
	if !strings.Contains(found, content) {
		t.Fatalf("history holds %q, not the captured content", found)
	}

	// GC reclaims the journal and still does not rewrite history, which is the
	// half a reader is most likely to assume otherwise.
	if _, err := store.GC(ctx); err != nil {
		t.Fatalf("gc: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx,
		`SELECT content FROM dolt_history_crumbs WHERE id = ? LIMIT 1`, string(id)).Scan(&found); err != nil {
		t.Fatalf("gc removed the Crumb from history: %v", err)
	}
}
