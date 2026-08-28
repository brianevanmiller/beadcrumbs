package dolt

import (
	"context"
	"path/filepath"
	"testing"
)

// TestBackupRestoreRoundTrip: a backup that carried only the working set would
// give up the reason Dolt was chosen, so this asserts records, committed
// history, and schema version all cross the round trip.
func TestBackupRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	const rows = 5

	source := fixtureRepo(t)
	sourceLoc := initLedger(t, source, false)
	sourceStore := openLedger(t, sourceLoc, Config{})
	seedRows(t, sourceStore, rows)

	wantRows := countRows(t, sourceStore.DB(), "SELECT COUNT(*) FROM round_trip")
	// Records is every row in the ledger, schema_meta and repo_config included,
	// so it is measured the same way on both sides rather than assumed equal to
	// the fixture table's row count.
	wantRecords, err := countRecords(ctx, sourceStore.DB())
	if err != nil {
		t.Fatalf("counting source records: %v", err)
	}
	wantCommits := countRows(t, sourceStore.DB(), "SELECT COUNT(*) FROM dolt_log")
	wantSchema, err := sourceStore.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if wantCommits < rows {
		t.Fatalf("only %d commits for %d rows; per-write commits are not happening", wantCommits, rows)
	}

	dest := filepath.Join(t.TempDir(), "backup")
	result, err := sourceStore.Backup(ctx, dest)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if result.Bytes <= 0 {
		t.Fatalf("backup reported %d bytes", result.Bytes)
	}
	if result.SchemaVersion != wantSchema {
		t.Fatalf("backup reports schema %d, want %d", result.SchemaVersion, wantSchema)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	target := fixtureRepo(t)
	targetLoc, _ := Discover(ctx, target)
	restored, err := Restore(ctx, targetLoc, dest, RestoreOptions{})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.SchemaVersion != wantSchema {
		t.Fatalf("restore reports schema %d, want %d", restored.SchemaVersion, wantSchema)
	}
	if restored.Records != wantRecords {
		t.Fatalf("restore reports %d records, want %d", restored.Records, wantRecords)
	}

	targetStore := openLedger(t, targetLoc, Config{})
	if got := countRows(t, targetStore.DB(), "SELECT COUNT(*) FROM round_trip"); got != wantRows {
		t.Fatalf("restored ledger has %d rows, want %d", got, wantRows)
	}
	if got := countRows(t, targetStore.DB(), "SELECT COUNT(*) FROM dolt_log"); got != wantCommits {
		t.Fatalf("restored ledger has %d commits, want %d; history did not survive", got, wantCommits)
	}
	// The restored ledger is a working ledger, not just readable bytes.
	if _, err := targetStore.DB().ExecContext(ctx,
		"INSERT INTO round_trip (id, note) VALUES (?, ?)", rows, "after-restore"); err != nil {
		t.Fatalf("restored ledger rejects writes: %v", err)
	}
	if err := targetStore.Commit(ctx, "test: after restore"); err != nil {
		t.Fatalf("commit after restore: %v", err)
	}
}
