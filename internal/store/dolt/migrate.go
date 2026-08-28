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
// ledger's current version. v1 ships exactly one, so on a healthy ledger this
// reports From == To and applies nothing. It is what makes a version mismatch a
// repairable state, which is why `bdc doctor` names it as the remediation.
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
		if err := execScript(ctx, db, m.body); err != nil {
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
