package store

import (
	"database/sql"
	"fmt"
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
