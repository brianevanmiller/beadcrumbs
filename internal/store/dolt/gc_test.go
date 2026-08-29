package dolt

import (
	"context"
	"testing"
)

// TestGCReclaimsJournal: per-transaction commits grow the chunk journal fast
// (the proof reached 39 MB at 4,910 rows), so GC has to be a real operation and
// not a hope. This asserts the journal both grows and is reclaimed.
func TestGCReclaimsJournal(t *testing.T) {
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)
	store := openLedger(t, loc, Config{})

	seedRows(t, store, 60)

	grown := journalBytes(loc.Dir)
	if grown <= 0 {
		t.Fatalf("journal is %d bytes after 60 committed writes; the measurement is wrong", grown)
	}

	result, err := store.GC(context.Background())
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if result.AfterBytes >= result.BeforeBytes {
		t.Fatalf("gc reclaimed nothing: %d -> %d bytes", result.BeforeBytes, result.AfterBytes)
	}
	if journalBytes(loc.Dir) >= grown {
		t.Fatalf("journal is still %d bytes, was %d before gc", journalBytes(loc.Dir), grown)
	}

	// GC reclaims the journal; it must not cost the data or the history.
	if got := countRows(t, store.DB(), "SELECT COUNT(*) FROM round_trip"); got != 60 {
		t.Fatalf("gc lost rows: %d of 60 remain", got)
	}
	if got := countRows(t, store.DB(), "SELECT COUNT(*) FROM dolt_log"); got < 60 {
		t.Fatalf("gc lost history: %d commits remain", got)
	}
}
