package dolt

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// TestTwoClonesSameReferencePullWithoutConstraintViolation is the v1.0.1
// acceptance criterion: two clones that independently create the same Reference
// identity produce the same refs.id, so DOLT_PULL does not leave a unique-key
// violation on uq_refs_identity.
//
// Independent inits do not share Dolt history. B is cloned from a pushed common
// ancestor. created_at is equalized so the unique-key fix is not mixed with a
// cell conflict — merge policy for unequal cache cells is v1.1.0.
func TestTwoClonesSameReferencePullWithoutConstraintViolation(t *testing.T) {
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatalf("creating the remote: %v", err)
	}
	remoteURL := "file://" + remote

	locA := initLedger(t, fixtureRepo(t), false)
	a := openLedger(t, locA, Config{Command: t.Name() + "-a-push"})
	mustExec(t, a, `CALL DOLT_REMOTE('add', 'origin', ?)`, remoteURL)
	if _, err := a.DB().ExecContext(ctx, `CALL DOLT_PUSH('origin', 'main')`); err != nil {
		t.Fatalf("pushing the ancestor: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("closing A after ancestor push: %v", err)
	}

	locB := Location{Dir: filepath.Join(t.TempDir(), "clone-b"), Stealth: true}
	if err := os.MkdirAll(locB.Dir, 0o700); err != nil {
		t.Fatalf("creating B: %v", err)
	}
	release := acquireProcessLock(locB.Dir, t.Name()+"-clone")
	if err := withEngine(ctx, locB.Dir, "", func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `CALL DOLT_CLONE(?, ?)`, remoteURL, DatabaseName)
		return err
	}); err != nil {
		release()
		t.Fatalf("cloning B: %v", err)
	}
	release()

	created := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	const kind, locator, workspace = "docs", "internal/parse.go", ""

	a = openLedger(t, locA, Config{Command: t.Name() + "-a-write"})
	insertNamedReference(t, a, kind, locator, workspace, created)
	if _, err := a.DB().ExecContext(ctx, `CALL DOLT_PUSH('origin', 'main')`); err != nil {
		t.Fatalf("pushing A's reference: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("closing A: %v", err)
	}

	b := openLedger(t, locB, Config{Command: t.Name() + "-b"})
	insertNamedReference(t, b, kind, locator, workspace, created)
	if _, err := b.DB().ExecContext(ctx, `CALL DOLT_PULL('origin')`); err != nil {
		t.Fatalf("pulling into B: %v", err)
	}

	if n := countRows(t, b.DB(), `SELECT COUNT(*) FROM refs WHERE kind = 'docs' AND locator = 'internal/parse.go' AND workspace = ''`); n != 1 {
		t.Fatalf("refs after pull = %d, want 1", n)
	}
	var violations int
	err := b.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM dolt_constraint_violations`).Scan(&violations)
	if err != nil && !isMissingTable(err) {
		t.Fatalf("reading constraint violations: %v", err)
	}
	if violations != 0 {
		t.Fatalf("dolt_constraint_violations = %d, want 0", violations)
	}
	var conflicts int
	err = b.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM dolt_conflicts`).Scan(&conflicts)
	if err != nil && !isMissingTable(err) {
		t.Fatalf("reading conflicts: %v", err)
	}
	if conflicts != 0 {
		t.Fatalf("dolt_conflicts = %d, want 0", conflicts)
	}
}

func insertNamedReference(t *testing.T, s *Store, kind, locator, workspace string, created time.Time) {
	t.Helper()
	ctx := context.Background()
	id := ledger.ReferenceIDFor(kind, locator, workspace)
	if err := s.Write(ctx, func(tx ledger.Tx) error {
		_, _, err := tx.UpsertReference(ledger.Reference{
			ID: id, Kind: kind, Locator: locator, Workspace: workspace, CreatedAt: created,
		})
		return err
	}); err != nil {
		t.Fatalf("inserting reference: %v", err)
	}
}
