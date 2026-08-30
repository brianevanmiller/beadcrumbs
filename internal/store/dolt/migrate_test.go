package dolt

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// openSchema1 is a ledger that stopped after 001_init.sql. It is the v1.0.0
// snapshot this build migrates; applying every embedded script would already be
// schema 2 and would not prove the rewrite.
func openSchema1(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	repo := fixtureRepo(t)
	loc, err := Discover(ctx, repo)
	if err == nil {
		t.Fatalf("expected %s to have no ledger yet", repo)
	}
	loc = loc.Resolve(false)
	if err := os.MkdirAll(loc.Dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", loc.Dir, err)
	}

	body := schema1Body(t)
	release := acquireProcessLock(loc.Dir, t.Name())
	err = func() error {
		defer release()
		if err := withEngine(ctx, loc.Dir, "", func(db *sql.DB) error {
			_, err := db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+DatabaseName)
			return err
		}); err != nil {
			return err
		}
		return withEngine(ctx, loc.Dir, DatabaseName, func(db *sql.DB) error {
			if err := execScript(ctx, db, body); err != nil {
				return err
			}
			_, err := db.ExecContext(ctx, "CALL DOLT_COMMIT('-A', '--allow-empty', '-m', ?)", "test: schema 1")
			return err
		})
	}()
	if err != nil {
		t.Fatalf("creating a schema-1 ledger: %v", err)
	}
	return openLedger(t, loc, Config{Command: t.Name()})
}

func schema1Body(t *testing.T) string {
	t.Helper()
	ms, err := migrations()
	if err != nil {
		t.Fatalf("reading migrations: %v", err)
	}
	for _, m := range ms {
		if m.version == 1 {
			return m.body
		}
	}
	t.Fatal("this build embeds no 001_init.sql")
	return ""
}

func uuid7(prefix, suffix string) string {
	return prefix + "0199aa11-2233-7444-8555-" + strings.Repeat("0", 12-len(suffix)) + suffix
}

func seedV1Reference(t *testing.T, s *Store, oldID, kind, locator, workspace string) {
	t.Helper()
	ctx := context.Background()
	crumb := uuid7("crb_", "1")
	hash := strings.Repeat("a", 64)
	mustExec(t, s, `INSERT INTO crumbs
		(id, content, content_hash, review_state, confidence, captured_at, redaction_version,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'a fragment', ?, 'candidate', 0.700, UTC_TIMESTAMP(6), '1', 'brian', 'human', NULL, NULL)`,
		crumb, hash)
	mustExec(t, s, `INSERT INTO refs (id, kind, locator, workspace, created_at)
		VALUES (?, ?, ?, ?, UTC_TIMESTAMP(6))`, oldID, kind, locator, workspace)
	mustExec(t, s, `INSERT INTO ref_links (record_kind, record_id, reference_id, relation, created_at)
		VALUES ('crumb', ?, ?, 'subject', UTC_TIMESTAMP(6))`, crumb, oldID)

	insight, revision := uuid7("ins_", "2"), uuid7("rev_", "3")
	mustExec(t, s, `INSERT INTO insights (id, head_revision, created_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 1, UTC_TIMESTAMP(6), 'brian', 'human', NULL, NULL)`, insight)
	mustExec(t, s, `INSERT INTO insight_revisions
		(id, insight_id, revision, title, content, content_hash, class, confidence, created_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, 1, 'a title', 'a body', ?, 'learning', 0.800, UTC_TIMESTAMP(6), 'brian', 'human', NULL, NULL)`,
		revision, insight, strings.Repeat("b", 64))
	proposal, promotion, receipt := uuid7("pp_", "4"), uuid7("prm_", "5"), uuid7("rcp_", "6")
	mustExec(t, s, `INSERT INTO promotion_proposals
		(id, insight_id, revision_id, class, dest_kind, dest_locator, content, content_hash,
		 confidence, policy_version, redaction_version, created_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, ?, 'adr', 'docs', 'docs/adr/', 'rendered', ?, 0.900, '1', '1', UTC_TIMESTAMP(6), 'brian', 'human', NULL, NULL)`,
		proposal, insight, revision, strings.Repeat("c", 64))
	mustExec(t, s, `INSERT INTO promotions
		(id, proposal_id, attempt, status, occurred_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, 1, 'applied', UTC_TIMESTAMP(6), 'brian', 'human', NULL, NULL)`, promotion, proposal)
	mustExec(t, s, `INSERT INTO receipts
		(id, promotion_id, kind, locator, verified, reference_id, recorded_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, 'docs', 'docs/adr/0001.md', 0, ?, UTC_TIMESTAMP(6), 'brian', 'human', NULL, NULL)`,
		receipt, promotion, oldID)
	_ = ctx
}

func TestDoctorOnV1LedgerNamesMigrate(t *testing.T) {
	ctx := context.Background()
	s := openSchema1(t)
	report, err := s.Diagnose(ctx)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if report.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", report.SchemaVersion)
	}
	var found ledger.Check
	for _, c := range report.Checks {
		if c.Name == "schema_version" {
			found = c
		}
	}
	if found.Status != StatusFail || !strings.Contains(found.Detail, "bdc migrate") {
		t.Fatalf("schema_version check = %+v, want fail naming bdc migrate", found)
	}
}

func TestMigrateV1RewritesReferenceIDsAndLeavesDoctorClean(t *testing.T) {
	ctx := context.Background()
	s := openSchema1(t)
	old := uuid7("ref_", "9")
	const kind, locator, workspace = "docs", "internal/parse.go", ""
	seedV1Reference(t, s, old, kind, locator, workspace)

	res, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.From != 1 || res.To != 2 || len(res.Applied) != 1 {
		t.Fatalf("migrate result = %+v, want 1 -> 2 with one script", res)
	}

	want := string(ledger.ReferenceIDFor(kind, locator, workspace))
	var got string
	if err := s.DB().QueryRowContext(ctx, `SELECT id FROM refs WHERE kind = ? AND locator = ? AND workspace = ?`,
		kind, locator, workspace).Scan(&got); err != nil {
		t.Fatalf("reading rewritten refs.id: %v", err)
	}
	if got != want {
		t.Fatalf("refs.id = %s, want %s", got, want)
	}
	if n := countRows(t, s.DB(), `SELECT COUNT(*) FROM ref_links WHERE reference_id = '`+want+`'`); n != 1 {
		t.Fatalf("ref_links still point at the old id: %d row(s) at the new one", n)
	}
	if n := countRows(t, s.DB(), `SELECT COUNT(*) FROM receipts WHERE reference_id = '`+want+`'`); n != 1 {
		t.Fatalf("receipts still point at the old id: %d row(s) at the new one", n)
	}

	report, err := s.Diagnose(ctx)
	if err != nil {
		t.Fatalf("diagnose after migrate: %v", err)
	}
	if report.SchemaVersion != 2 || !report.OK {
		t.Fatalf("doctor after migrate: schema=%d ok=%v checks=%v", report.SchemaVersion, report.OK, report.Checks)
	}
	if err := s.Read(ctx, func(snap ledger.Snapshot) error {
		orphans, err := snap.OrphanTargets()
		if err != nil {
			return err
		}
		if len(orphans) != 0 {
			t.Fatalf("migrate left polymorphic orphans: %+v", orphans)
		}
		return nil
	}); err != nil {
		t.Fatalf("orphan scan: %v", err)
	}

	again, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if again.From != 2 || again.To != 2 || len(again.Applied) != 0 {
		t.Fatalf("second migrate applied something: %+v", again)
	}
}

func TestMigrateV1RejectsAmbiguousReferenceIdentity(t *testing.T) {
	s := openSchema1(t)
	seedV1Reference(t, s, uuid7("ref_", "9"), "docs", "internal\u001fparse.go", "")

	_, err := s.Migrate(context.Background())
	if err == nil || !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("migrate error = %v, want invalid reference identity", err)
	}
}
