package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// Migration represents a single database migration.
type Migration struct {
	Name string
	Func func(*sql.DB) error
}

// migrationsList contains all migrations in order.
var migrationsList = []Migration{
	{"001_initial_schema", migrateInitialSchema},
	{"002_insights_author_id", migrateInsightsAuthorID},
	{"003_insights_endorsed_by", migrateInsightsEndorsedBy},
	{"004_config_table", migrateConfigTable},
	{"005_external_ref_mappings", migrateExternalRefMappings},
	{"006_migrate_bead_thread_ids", migrateBeadThreadIDs},
	{"007_insights_source_ref_index", migrateInsightsSourceRefIndex},
	{"008_insights_content_hash", migrateInsightsContentHash},
}

// RunMigrations runs all database migrations.
func RunMigrations(db *sql.DB) error {
	for _, migration := range migrationsList {
		if err := migration.Func(db); err != nil {
			return fmt.Errorf("migration %s failed: %w", migration.Name, err)
		}
	}
	return nil
}

// migrateInitialSchema creates the initial tables for beadcrumbs storage.
func migrateInitialSchema(db *sql.DB) error {
	// Create threads table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS threads (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			status TEXT NOT NULL,
			current_understanding TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create threads table: %w", err)
	}

	// Create insights table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS insights (
			id TEXT PRIMARY KEY,
			timestamp DATETIME NOT NULL,
			content TEXT NOT NULL,
			summary TEXT,
			type TEXT NOT NULL,
			confidence REAL NOT NULL,
			source_type TEXT NOT NULL,
			source_ref TEXT,
			source_participants TEXT,
			thread_id TEXT,
			tags TEXT,
			created_by TEXT,
			created_at DATETIME NOT NULL,
			FOREIGN KEY (thread_id) REFERENCES threads(id) ON DELETE SET NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create insights table: %w", err)
	}

	// Create dependencies table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS dependencies (
			from_id TEXT NOT NULL,
			to_id TEXT NOT NULL,
			type TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			PRIMARY KEY (from_id, to_id, type)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create dependencies table: %w", err)
	}

	// Create indexes for efficient queries
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_insights_thread_id ON insights(thread_id)",
		"CREATE INDEX IF NOT EXISTS idx_insights_type ON insights(type)",
		"CREATE INDEX IF NOT EXISTS idx_insights_timestamp ON insights(timestamp)",
		"CREATE INDEX IF NOT EXISTS idx_insights_created_at ON insights(created_at)",
		"CREATE INDEX IF NOT EXISTS idx_threads_status ON threads(status)",
		"CREATE INDEX IF NOT EXISTS idx_dependencies_from ON dependencies(from_id)",
		"CREATE INDEX IF NOT EXISTS idx_dependencies_to ON dependencies(to_id)",
		// Full-text search index for insights content and summary
		"CREATE VIRTUAL TABLE IF NOT EXISTS insights_fts USING fts5(id, content, summary, content=insights, content_rowid=rowid)",
		// Trigger to keep FTS index in sync
		"CREATE TRIGGER IF NOT EXISTS insights_fts_insert AFTER INSERT ON insights BEGIN INSERT INTO insights_fts(rowid, id, content, summary) VALUES (new.rowid, new.id, new.content, new.summary); END",
		"CREATE TRIGGER IF NOT EXISTS insights_fts_delete AFTER DELETE ON insights BEGIN DELETE FROM insights_fts WHERE rowid = old.rowid; END",
		"CREATE TRIGGER IF NOT EXISTS insights_fts_update AFTER UPDATE ON insights BEGIN UPDATE insights_fts SET id = new.id, content = new.content, summary = new.summary WHERE rowid = new.rowid; END",
	}

	for _, idx := range indexes {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// migrateInsightsAuthorID adds the author_id column to insights table.
func migrateInsightsAuthorID(db *sql.DB) error {
	// Check if column already exists
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('insights')
		WHERE name = 'author_id'
	`).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check for author_id column: %w", err)
	}
	if count > 0 {
		return nil // Already migrated
	}

	_, err = db.Exec(`ALTER TABLE insights ADD COLUMN author_id TEXT`)
	if err != nil {
		return fmt.Errorf("failed to add author_id column: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_insights_author_id ON insights(author_id)`)
	if err != nil {
		return fmt.Errorf("failed to create author_id index: %w", err)
	}

	return nil
}

// migrateInsightsEndorsedBy adds the endorsed_by column to insights table.
func migrateInsightsEndorsedBy(db *sql.DB) error {
	// Check if column already exists
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('insights')
		WHERE name = 'endorsed_by'
	`).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check for endorsed_by column: %w", err)
	}
	if count > 0 {
		return nil // Already migrated
	}

	_, err = db.Exec(`ALTER TABLE insights ADD COLUMN endorsed_by TEXT DEFAULT '[]'`)
	if err != nil {
		return fmt.Errorf("failed to add endorsed_by column: %w", err)
	}

	return nil
}

// migrateConfigTable creates the config table for key-value storage.
func migrateConfigTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create config table: %w", err)
	}

	return nil
}

// migrateExternalRefMappings creates the external_ref_mappings table
// for linking threads to external issue trackers (Linear, GitHub, Jira, etc.).
func migrateExternalRefMappings(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS external_ref_mappings (
			external_ref TEXT PRIMARY KEY,
			thread_id    TEXT NOT NULL,
			system       TEXT NOT NULL,
			external_id  TEXT NOT NULL,
			metadata     TEXT DEFAULT '{}',
			created_at   DATETIME NOT NULL,
			updated_at   DATETIME NOT NULL,
			FOREIGN KEY (thread_id) REFERENCES threads(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create external_ref_mappings table: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ext_ref_thread_id ON external_ref_mappings(thread_id)`)
	if err != nil {
		return fmt.Errorf("failed to create ext_ref thread_id index: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ext_ref_system ON external_ref_mappings(system)`)
	if err != nil {
		return fmt.Errorf("failed to create ext_ref system index: %w", err)
	}

	return nil
}

// migrateBeadThreadIDs converts orphaned bead IDs used as thread_id values
// into proper threads with external_ref_mappings entries.
// Previously, --thread bd-abc1 set insight.ThreadID = "bd-abc1" directly
// without creating a real thread. This migration fixes those orphans.
func migrateBeadThreadIDs(db *sql.DB) error {
	// Find all unique bead IDs used as thread_id
	rows, err := db.Query(`
		SELECT DISTINCT thread_id FROM insights
		WHERE thread_id LIKE 'bd-%' OR thread_id LIKE 'bead-%'
	`)
	if err != nil {
		return fmt.Errorf("failed to query bead thread IDs: %w", err)
	}
	defer rows.Close()

	var beadIDs []string
	for rows.Next() {
		var beadID string
		if err := rows.Scan(&beadID); err != nil {
			return fmt.Errorf("failed to scan bead thread ID: %w", err)
		}
		beadIDs = append(beadIDs, beadID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating bead thread IDs: %w", err)
	}

	if len(beadIDs) == 0 {
		return nil // Nothing to migrate
	}

	// Migrate each bead ID within a transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()

	for _, beadID := range beadIDs {
		// Normalize bead ID to external ref format
		var externalID string
		if strings.HasPrefix(beadID, "bead-") {
			externalID = beadID[5:]
		} else if strings.HasPrefix(beadID, "bd-") {
			externalID = beadID[3:]
		} else {
			continue
		}
		externalRef := "bead:" + externalID

		// Check if a thread already exists for this bead (shouldn't, but be safe)
		var existingCount int
		err := tx.QueryRow(`SELECT COUNT(*) FROM external_ref_mappings WHERE external_ref = ?`, externalRef).Scan(&existingCount)
		if err != nil {
			return fmt.Errorf("failed to check existing mapping for %s: %w", beadID, err)
		}
		if existingCount > 0 {
			continue // Already migrated
		}

		// Create a real thread
		threadID := types.GenerateID("thr")
		_, err = tx.Exec(`
			INSERT INTO threads (id, title, status, current_understanding, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?)
		`, threadID, beadID, "active", now, now)
		if err != nil {
			return fmt.Errorf("failed to create thread for bead %s: %w", beadID, err)
		}

		// Create external ref mapping
		_, err = tx.Exec(`
			INSERT INTO external_ref_mappings (external_ref, thread_id, system, external_id, metadata, created_at, updated_at)
			VALUES (?, ?, 'bead', ?, '{}', ?, ?)
		`, externalRef, threadID, externalID, now, now)
		if err != nil {
			return fmt.Errorf("failed to create mapping for bead %s: %w", beadID, err)
		}

		// Update all insights to use the new thread ID
		_, err = tx.Exec(`UPDATE insights SET thread_id = ? WHERE thread_id = ?`, threadID, beadID)
		if err != nil {
			return fmt.Errorf("failed to update insights for bead %s: %w", beadID, err)
		}
	}

	return tx.Commit()
}

// migrateInsightsSourceRefIndex adds an index on source_ref for origin-based queries.
func migrateInsightsSourceRefIndex(db *sql.DB) error {
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_insights_source_ref ON insights(source_ref)`)
	if err != nil {
		return fmt.Errorf("failed to create source_ref index: %w", err)
	}
	return nil
}

// migrateInsightsContentHash adds the content_hash column to the insights table
// and backfills existing rows by computing hashes from their substantive fields.
func migrateInsightsContentHash(db *sql.DB) error {
	// Check if column already exists.
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('insights')
		WHERE name = 'content_hash'
	`).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check for content_hash column: %w", err)
	}
	if count > 0 {
		return nil // Already migrated.
	}

	_, err = db.Exec(`ALTER TABLE insights ADD COLUMN content_hash TEXT`)
	if err != nil {
		return fmt.Errorf("failed to add content_hash column: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_insights_content_hash ON insights(content_hash)`)
	if err != nil {
		return fmt.Errorf("failed to create content_hash index: %w", err)
	}

	// Backfill existing rows.
	rows, err := db.Query(`SELECT id, content, type, COALESCE(thread_id, ''), COALESCE(author_id, '') FROM insights WHERE content_hash IS NULL`)
	if err != nil {
		return fmt.Errorf("failed to query insights for backfill: %w", err)
	}
	defer rows.Close()

	type row struct {
		id   string
		ins  types.Insight
	}
	var toUpdate []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.ins.Content, &r.ins.Type, &r.ins.ThreadID, &r.ins.AuthorID); err != nil {
			return fmt.Errorf("failed to scan insight for backfill: %w", err)
		}
		toUpdate = append(toUpdate, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating insights for backfill: %w", err)
	}

	for _, r := range toUpdate {
		hash := r.ins.ComputeContentHash()
		if _, err := db.Exec(`UPDATE insights SET content_hash = ? WHERE id = ?`, hash, r.id); err != nil {
			return fmt.Errorf("failed to backfill content_hash for insight %s: %w", r.id, err)
		}
	}

	return nil
}
