package dolt

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// MigrationResult is what `bdc init` and `bdc doctor` report about schema state.
type MigrationResult = ledger.MigrationResult

// Migrate is `bdc migrate`: it applies every embedded migration above the
// ledger's current version. On a healthy ledger this reports From == To and
// applies nothing. It is what makes a version mismatch a repairable state,
// which is why `bdc doctor` names it as the remediation.
func (s *Store) Migrate(ctx context.Context) (MigrationResult, error) {
	res, err := applyPending(ctx, s.db)
	if err != nil {
		return res, err
	}
	if len(res.Applied) > 0 {
		if err := s.Commit(ctx, fmt.Sprintf("bdc migrate: schema %d -> %d", res.From, res.To)); err != nil {
			return res, err
		}
	}
	return res, nil
}

// applyPending is the single migration applier: `bdc init` and `bdc migrate`
// both go through it, so there is one answer to "what does applying a migration
// mean". Each script records the version itself as its last statement — 001
// inserts schema_meta's singleton row, a later script REPLACEs it — which keeps
// this function from having to know that table's shape, and the version
// assertion afterwards turns a script that forgot into a loud failure instead
// of a silent re-run next time.
func applyPending(ctx context.Context, db *sql.DB) (MigrationResult, error) {
	ms, err := migrations()
	if err != nil {
		return MigrationResult{}, ledger.FailWith(ledger.ErrIntegrity, "integrity_schema_embed", err,
			"embedded schema is unusable")
	}
	current, err := schemaVersion(ctx, db)
	if err != nil {
		return MigrationResult{}, err
	}
	res := MigrationResult{From: current, To: current}
	for _, m := range ms {
		if m.version <= res.To {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return res, ledger.FailWith(ledger.ErrIntegrity, "integrity_migration_failed", err,
				"migration %s failed", m.name)
		}
		applied, err := schemaVersion(ctx, db)
		if err != nil {
			return res, err
		}
		if applied != m.version {
			return res, ledger.Fail(ledger.ErrIntegrity, "integrity_migration_unrecorded",
				"migration %s ran but schema_meta reports version %d", m.name, applied)
		}
		res.To = applied
		res.Applied = append(res.Applied, m.name)
	}
	return res, nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	if m.version == 2 {
		return applySchema2(ctx, db, m.body)
	}
	return execScript(ctx, db, m.body)
}

// applySchema2 is 002: ALTERs that may already have landed, a Go rewrite of
// refs.id from the natural key, then the FKs and schema_meta REPLACE. Dolt DDL
// typically auto-commits, so the rewrite is a function of identity and every
// ALTER is skipped when its effect is already visible — a failed run leaves
// version 1 and bdc migrate is re-runnable.
func applySchema2(ctx context.Context, db *sql.DB, script string) error {
	stmts, err := splitStatements(script)
	if err != nil {
		return err
	}
	rewrote := false
	for _, stmt := range stmts {
		switch {
		case isAddHarnessColumn(stmt):
			table := addColumnTable(stmt)
			exists, err := columnExists(ctx, db, table, "harness")
			if err != nil {
				return err
			}
			if exists {
				continue
			}
		case isDropForeignKey(stmt):
			name := foreignKeyName(stmt)
			exists, err := constraintExists(ctx, db, name)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
		case isAddForeignKey(stmt):
			// The rewrite has to run with the FKs down, after the DROP and
			// before the ADD. Once, not per ADD CONSTRAINT: a second call
			// would restage ids that already match.
			if !rewrote {
				if err := rewriteReferenceIDs(ctx, db); err != nil {
					return err
				}
				rewrote = true
			}
			name := foreignKeyName(stmt)
			exists, err := constraintExists(ctx, db, name)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

// rewriteReferenceIDs is Go because Go is the source of truth for ReferenceIDFor.
// A SQL SHA2 that disagreed would mint ids the writer never produces. The map
// is computed from the current (kind, locator, workspace); applying it twice is
// a no-op once every id already matches.
func rewriteReferenceIDs(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT id, kind, locator, workspace FROM refs`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type mapping struct{ old, neu string }
	var maps []mapping
	for rows.Next() {
		var old, kind, locator, workspace string
		if err := rows.Scan(&old, &kind, &locator, &workspace); err != nil {
			return err
		}
		neu := string(ledger.ReferenceIDFor(kind, locator, workspace))
		if old != neu {
			maps = append(maps, mapping{old: old, neu: neu})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(maps) == 0 {
		return nil
	}

	// Stage through a throwaway prefix so two rows cannot collide mid-update
	// when one row's new id is another's old id. CHAR(40) still holds:
	// "tmp_" + 36-character UUID.
	for _, m := range maps {
		stage := "tmp_" + m.neu[len(ledger.PrefixReference):]
		if _, err := db.ExecContext(ctx, `UPDATE refs SET id = ? WHERE id = ?`, stage, m.old); err != nil {
			return fmt.Errorf("staging refs.id %s: %w", m.old, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE ref_links SET reference_id = ? WHERE reference_id = ?`, stage, m.old); err != nil {
			return fmt.Errorf("staging ref_links.reference_id %s: %w", m.old, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE receipts SET reference_id = ? WHERE reference_id = ?`, stage, m.old); err != nil {
			return fmt.Errorf("staging receipts.reference_id %s: %w", m.old, err)
		}
	}
	for _, m := range maps {
		stage := "tmp_" + m.neu[len(ledger.PrefixReference):]
		if _, err := db.ExecContext(ctx, `UPDATE refs SET id = ? WHERE id = ?`, m.neu, stage); err != nil {
			return fmt.Errorf("rewriting refs.id %s: %w", m.old, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE ref_links SET reference_id = ? WHERE reference_id = ?`, m.neu, stage); err != nil {
			return fmt.Errorf("rewriting ref_links.reference_id %s: %w", m.old, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE receipts SET reference_id = ? WHERE reference_id = ?`, m.neu, stage); err != nil {
			return fmt.Errorf("rewriting receipts.reference_id %s: %w", m.old, err)
		}
	}
	return nil
}

func isAddHarnessColumn(stmt string) bool {
	s := strings.ToLower(compactSQL(stmt))
	return strings.HasPrefix(s, "alter table ") && strings.Contains(s, " add column harness ")
}

func isDropForeignKey(stmt string) bool {
	s := strings.ToLower(compactSQL(stmt))
	return strings.HasPrefix(s, "alter table ") && strings.Contains(s, " drop foreign key ")
}

func isAddForeignKey(stmt string) bool {
	s := strings.ToLower(compactSQL(stmt))
	return strings.HasPrefix(s, "alter table ") && strings.Contains(s, " add constraint ") && strings.Contains(s, " foreign key ")
}

func addColumnTable(stmt string) string {
	fields := strings.Fields(compactSQL(stmt))
	if len(fields) >= 3 {
		return fields[2]
	}
	return ""
}

func foreignKeyName(stmt string) string {
	fields := strings.Fields(compactSQL(stmt))
	for i, f := range fields {
		if strings.EqualFold(f, "constraint") && i+1 < len(fields) {
			return fields[i+1]
		}
		if strings.EqualFold(f, "key") && i > 0 && strings.EqualFold(fields[i-1], "foreign") && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

func compactSQL(stmt string) string {
	return strings.Join(strings.Fields(stmt), " ")
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?`,
		DatabaseName, table, column).Scan(&n)
	return n > 0, err
}

func constraintExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.table_constraints
		 WHERE table_schema = ? AND constraint_name = ? AND constraint_type = 'FOREIGN KEY'`,
		DatabaseName, name).Scan(&n)
	return n > 0, err
}

// execScript applies one migration script statement by statement. Statement-wise
// application is what lets an already-open engine migrate: MultiStatements is a
// connector-level setting fixed at Connect, and the engine a command holds is
// not opened with it.
func execScript(ctx context.Context, db *sql.DB, script string) error {
	stmts, err := splitStatements(script)
	if err != nil {
		return err
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

// splitStatements splits a migration on unquoted semicolons, dropping `--` line
// comments. It handles the SQL the migrations actually contain and refuses
// anything it cannot split honestly — a mis-split migration would apply half a
// schema, so guessing is worse than failing.
func splitStatements(script string) ([]string, error) {
	if strings.Contains(script, "/*") {
		return nil, fmt.Errorf("migration uses a block comment, which the statement splitter does not parse")
	}
	var (
		out     []string
		cur     strings.Builder
		quote   rune // 0, '\'', '"', or '`'
		comment bool
		escaped bool
	)
	for _, r := range script {
		switch {
		case comment:
			if r == '\n' {
				comment = false
				cur.WriteRune(r)
			}
			continue
		case quote != 0:
			cur.WriteRune(r)
			switch {
			case escaped:
				escaped = false
			case r == '\\' && quote != '`':
				escaped = true
			case r == quote:
				quote = 0
			}
			continue
		case r == '\'' || r == '"' || r == '`':
			quote = r
			cur.WriteRune(r)
			continue
		case r == '-' && strings.HasSuffix(cur.String(), "-"):
			// Two hyphens outside a quote start a line comment; drop the first,
			// which is already in the buffer.
			s := cur.String()
			cur.Reset()
			cur.WriteString(s[:len(s)-1])
			comment = true
			continue
		case r == ';':
			if stmt := strings.TrimSpace(cur.String()); stmt != "" {
				out = append(out, stmt)
			}
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	if quote != 0 {
		return nil, fmt.Errorf("migration ends inside a quoted literal")
	}
	if tail := strings.TrimSpace(cur.String()); tail != "" {
		return nil, fmt.Errorf("migration ends without a semicolon: %s", firstLine(tail))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("migration contains no statements")
	}
	return out, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 80 {
		line = line[:80] + "…"
	}
	return line
}
