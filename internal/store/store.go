package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
	_ "modernc.org/sqlite"
)

// Store provides SQLite persistence for insights, threads, and dependencies.
type Store struct {
	db *sql.DB
}

// NewStore creates a new Store, opening/creating the SQLite database at dbPath.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Run migrations
	if err := RunMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateInsight inserts a new insight into the database.
func (s *Store) CreateInsight(insight *types.Insight) error {
	// Serialize complex fields
	sourceParticipants, err := json.Marshal(insight.Source.Participants)
	if err != nil {
		return fmt.Errorf("failed to marshal source participants: %w", err)
	}

	tags, err := json.Marshal(insight.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}

	approvedBy, err := json.Marshal(insight.EndorsedBy)
	if err != nil {
		return fmt.Errorf("failed to marshal endorsed_by: %w", err)
	}

	// Handle nullable fields - convert empty strings to sql.NullString
	var threadID interface{}
	if insight.ThreadID == "" {
		threadID = nil
	} else {
		threadID = insight.ThreadID
	}

	var authorID interface{}
	if insight.AuthorID == "" {
		authorID = nil
	} else {
		authorID = insight.AuthorID
	}

	_, err = s.db.Exec(`
		INSERT INTO insights (
			id, timestamp, content, summary, type, confidence,
			source_type, source_ref, source_participants,
			thread_id, author_id, endorsed_by, tags, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		insight.ID,
		insight.Timestamp,
		insight.Content,
		insight.Summary,
		insight.Type,
		insight.Confidence,
		insight.Source.Type,
		insight.Source.Ref,
		string(sourceParticipants),
		threadID,
		authorID,
		string(approvedBy),
		string(tags),
		insight.CreatedBy,
		insight.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert insight: %w", err)
	}

	return nil
}

// GetInsight retrieves an insight by ID.
func (s *Store) GetInsight(id string) (*types.Insight, error) {
	var insight types.Insight
	var sourceParticipantsJSON, tagsJSON, endorsedByJSON sql.NullString
	var authorID, threadID sql.NullString

	err := s.db.QueryRow(`
		SELECT
			id, timestamp, content, summary, type, confidence,
			source_type, source_ref, source_participants,
			thread_id, author_id, endorsed_by, tags, created_by, created_at
		FROM insights
		WHERE id = ?
	`, id).Scan(
		&insight.ID,
		&insight.Timestamp,
		&insight.Content,
		&insight.Summary,
		&insight.Type,
		&insight.Confidence,
		&insight.Source.Type,
		&insight.Source.Ref,
		&sourceParticipantsJSON,
		&threadID,
		&authorID,
		&endorsedByJSON,
		&tagsJSON,
		&insight.CreatedBy,
		&insight.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("insight not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query insight: %w", err)
	}

	// Handle nullable fields
	if authorID.Valid {
		insight.AuthorID = authorID.String
	}
	if threadID.Valid {
		insight.ThreadID = threadID.String
	}

	// Deserialize complex fields
	if sourceParticipantsJSON.Valid && sourceParticipantsJSON.String != "" {
		if err := json.Unmarshal([]byte(sourceParticipantsJSON.String), &insight.Source.Participants); err != nil {
			return nil, fmt.Errorf("failed to unmarshal source participants: %w", err)
		}
	}

	if tagsJSON.Valid && tagsJSON.String != "" {
		if err := json.Unmarshal([]byte(tagsJSON.String), &insight.Tags); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
		}
	}

	if endorsedByJSON.Valid && endorsedByJSON.String != "" {
		if err := json.Unmarshal([]byte(endorsedByJSON.String), &insight.EndorsedBy); err != nil {
			return nil, fmt.Errorf("failed to unmarshal endorsed_by: %w", err)
		}
	}

	return &insight, nil
}

// UpdateInsight updates an existing insight.
func (s *Store) UpdateInsight(insight *types.Insight) error {
	// Serialize complex fields
	sourceParticipants, err := json.Marshal(insight.Source.Participants)
	if err != nil {
		return fmt.Errorf("failed to marshal source participants: %w", err)
	}

	tags, err := json.Marshal(insight.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}

	approvedBy, err := json.Marshal(insight.EndorsedBy)
	if err != nil {
		return fmt.Errorf("failed to marshal endorsed_by: %w", err)
	}

	// Handle nullable fields - convert empty strings to nil
	var threadID interface{}
	if insight.ThreadID == "" {
		threadID = nil
	} else {
		threadID = insight.ThreadID
	}

	var authorID interface{}
	if insight.AuthorID == "" {
		authorID = nil
	} else {
		authorID = insight.AuthorID
	}

	result, err := s.db.Exec(`
		UPDATE insights SET
			timestamp = ?,
			content = ?,
			summary = ?,
			type = ?,
			confidence = ?,
			source_type = ?,
			source_ref = ?,
			source_participants = ?,
			thread_id = ?,
			author_id = ?,
			endorsed_by = ?,
			tags = ?,
			created_by = ?,
			created_at = ?
		WHERE id = ?
	`,
		insight.Timestamp,
		insight.Content,
		insight.Summary,
		insight.Type,
		insight.Confidence,
		insight.Source.Type,
		insight.Source.Ref,
		string(sourceParticipants),
		threadID,
		authorID,
		string(approvedBy),
		string(tags),
		insight.CreatedBy,
		insight.CreatedAt,
		insight.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update insight: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("insight not found: %s", insight.ID)
	}

	return nil
}

// DeleteInsight removes an insight from the database.
func (s *Store) DeleteInsight(id string) error {
	result, err := s.db.Exec("DELETE FROM insights WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete insight: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("insight not found: %s", id)
	}

	return nil
}

// ListInsights retrieves insights based on filters.
// Pass empty string for threadID or insightType to skip that filter.
// Pass zero time for since to skip time filter.
func (s *Store) ListInsights(threadID string, insightType types.InsightType, since time.Time) ([]*types.Insight, error) {
	query := "SELECT id, timestamp, content, summary, type, confidence, source_type, source_ref, source_participants, thread_id, author_id, endorsed_by, tags, created_by, created_at FROM insights WHERE 1=1"
	args := []interface{}{}

	if threadID != "" {
		query += " AND thread_id = ?"
		args = append(args, threadID)
	}

	if insightType != "" {
		query += " AND type = ?"
		args = append(args, insightType)
	}

	if !since.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, since)
	}

	query += " ORDER BY timestamp DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query insights: %w", err)
	}
	defer rows.Close()

	var insights []*types.Insight
	for rows.Next() {
		var insight types.Insight
		var sourceParticipantsJSON, tagsJSON sql.NullString
		var endorsedByJSON sql.NullString
		var authorID, threadID sql.NullString

		err := rows.Scan(
			&insight.ID,
			&insight.Timestamp,
			&insight.Content,
			&insight.Summary,
			&insight.Type,
			&insight.Confidence,
			&insight.Source.Type,
			&insight.Source.Ref,
			&sourceParticipantsJSON,
			&threadID,
			&authorID,
			&endorsedByJSON,
			&tagsJSON,
			&insight.CreatedBy,
			&insight.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan insight: %w", err)
		}

		// Handle nullable fields
		if authorID.Valid {
			insight.AuthorID = authorID.String
		}
		if threadID.Valid {
			insight.ThreadID = threadID.String
		}

		// Deserialize complex fields
		if sourceParticipantsJSON.Valid && sourceParticipantsJSON.String != "" {
			if err := json.Unmarshal([]byte(sourceParticipantsJSON.String), &insight.Source.Participants); err != nil {
				return nil, fmt.Errorf("failed to unmarshal source participants: %w", err)
			}
		}

		if tagsJSON.Valid && tagsJSON.String != "" {
			if err := json.Unmarshal([]byte(tagsJSON.String), &insight.Tags); err != nil {
				return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
			}
		}

		if endorsedByJSON.Valid && endorsedByJSON.String != "" {
			if err := json.Unmarshal([]byte(endorsedByJSON.String), &insight.EndorsedBy); err != nil {
				return nil, fmt.Errorf("failed to unmarshal endorsed_by: %w", err)
			}
		}

		insights = append(insights, &insight)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating insights: %w", err)
	}

	return insights, nil
}

// SearchInsights performs a full-text search across insight content and summaries.
func (s *Store) SearchInsights(query string) ([]*types.Insight, error) {
	// Use FTS5 for full-text search
	rows, err := s.db.Query(`
		SELECT i.id, i.timestamp, i.content, i.summary, i.type, i.confidence,
		       i.source_type, i.source_ref, i.source_participants,
		       i.thread_id, i.author_id, i.endorsed_by, i.tags, i.created_by, i.created_at
		FROM insights i
		JOIN insights_fts fts ON i.rowid = fts.rowid
		WHERE insights_fts MATCH ?
		ORDER BY rank
	`, query)
	if err != nil {
		return nil, fmt.Errorf("failed to search insights: %w", err)
	}
	defer rows.Close()

	var insights []*types.Insight
	for rows.Next() {
		var insight types.Insight
		var sourceParticipantsJSON, tagsJSON, endorsedByJSON sql.NullString
		var authorID, threadID sql.NullString

		err := rows.Scan(
			&insight.ID,
			&insight.Timestamp,
			&insight.Content,
			&insight.Summary,
			&insight.Type,
			&insight.Confidence,
			&insight.Source.Type,
			&insight.Source.Ref,
			&sourceParticipantsJSON,
			&threadID,
			&authorID,
			&endorsedByJSON,
			&tagsJSON,
			&insight.CreatedBy,
			&insight.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan insight: %w", err)
		}

		// Handle nullable fields
		if authorID.Valid {
			insight.AuthorID = authorID.String
		}
		if threadID.Valid {
			insight.ThreadID = threadID.String
		}

		// Deserialize complex fields
		if sourceParticipantsJSON.Valid && sourceParticipantsJSON.String != "" {
			if err := json.Unmarshal([]byte(sourceParticipantsJSON.String), &insight.Source.Participants); err != nil {
				return nil, fmt.Errorf("failed to unmarshal source participants: %w", err)
			}
		}

		if tagsJSON.Valid && tagsJSON.String != "" {
			if err := json.Unmarshal([]byte(tagsJSON.String), &insight.Tags); err != nil {
				return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
			}
		}

		if endorsedByJSON.Valid && endorsedByJSON.String != "" {
			if err := json.Unmarshal([]byte(endorsedByJSON.String), &insight.EndorsedBy); err != nil {
				return nil, fmt.Errorf("failed to unmarshal endorsed_by: %w", err)
			}
		}

		insights = append(insights, &insight)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating search results: %w", err)
	}

	return insights, nil
}

// CreateThread inserts a new thread into the database.
func (s *Store) CreateThread(thread *types.InsightThread) error {
	_, err := s.db.Exec(`
		INSERT INTO threads (id, title, status, current_understanding, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		thread.ID,
		thread.Title,
		thread.Status,
		thread.CurrentUnderstanding,
		thread.CreatedAt,
		thread.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert thread: %w", err)
	}

	return nil
}

// GetThread retrieves a thread by ID.
func (s *Store) GetThread(id string) (*types.InsightThread, error) {
	var thread types.InsightThread

	err := s.db.QueryRow(`
		SELECT id, title, status, current_understanding, created_at, updated_at
		FROM threads
		WHERE id = ?
	`, id).Scan(
		&thread.ID,
		&thread.Title,
		&thread.Status,
		&thread.CurrentUnderstanding,
		&thread.CreatedAt,
		&thread.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("thread not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query thread: %w", err)
	}

	return &thread, nil
}

// UpdateThread updates an existing thread.
func (s *Store) UpdateThread(thread *types.InsightThread) error {
	result, err := s.db.Exec(`
		UPDATE threads SET
			title = ?,
			status = ?,
			current_understanding = ?,
			updated_at = ?
		WHERE id = ?
	`,
		thread.Title,
		thread.Status,
		thread.CurrentUnderstanding,
		thread.UpdatedAt,
		thread.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update thread: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("thread not found: %s", thread.ID)
	}

	return nil
}

// ListThreads retrieves threads, optionally filtered by status.
// Pass empty string for status to retrieve all threads.
func (s *Store) ListThreads(status types.ThreadStatus) ([]*types.InsightThread, error) {
	query := "SELECT id, title, status, current_understanding, created_at, updated_at FROM threads"
	args := []interface{}{}

	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}

	query += " ORDER BY updated_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query threads: %w", err)
	}
	defer rows.Close()

	var threads []*types.InsightThread
	for rows.Next() {
		var thread types.InsightThread
		err := rows.Scan(
			&thread.ID,
			&thread.Title,
			&thread.Status,
			&thread.CurrentUnderstanding,
			&thread.CreatedAt,
			&thread.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan thread: %w", err)
		}
		threads = append(threads, &thread)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating threads: %w", err)
	}

	return threads, nil
}

// AddDependency creates a new dependency relationship.
func (s *Store) AddDependency(dep *types.Dependency) error {
	_, err := s.db.Exec(`
		INSERT INTO dependencies (from_id, to_id, type, created_at)
		VALUES (?, ?, ?, ?)
	`,
		dep.From,
		dep.To,
		dep.Type,
		dep.CreatedAt,
	)

	if err != nil {
		// Check if this is a duplicate key error
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("dependency already exists: %s -> %s (%s)", dep.From, dep.To, dep.Type)
		}
		return fmt.Errorf("failed to insert dependency: %w", err)
	}

	return nil
}

// GetDependencies retrieves all dependencies where fromID is the source.
func (s *Store) GetDependencies(fromID string) ([]*types.Dependency, error) {
	rows, err := s.db.Query(`
		SELECT from_id, to_id, type, created_at
		FROM dependencies
		WHERE from_id = ?
		ORDER BY created_at
	`, fromID)
	if err != nil {
		return nil, fmt.Errorf("failed to query dependencies: %w", err)
	}
	defer rows.Close()

	var deps []*types.Dependency
	for rows.Next() {
		var dep types.Dependency
		err := rows.Scan(&dep.From, &dep.To, &dep.Type, &dep.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan dependency: %w", err)
		}
		deps = append(deps, &dep)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating dependencies: %w", err)
	}

	return deps, nil
}

// GetDependents retrieves all dependencies where toID is the target.
func (s *Store) GetDependents(toID string) ([]*types.Dependency, error) {
	rows, err := s.db.Query(`
		SELECT from_id, to_id, type, created_at
		FROM dependencies
		WHERE to_id = ?
		ORDER BY created_at
	`, toID)
	if err != nil {
		return nil, fmt.Errorf("failed to query dependents: %w", err)
	}
	defer rows.Close()

	var deps []*types.Dependency
	for rows.Next() {
		var dep types.Dependency
		err := rows.Scan(&dep.From, &dep.To, &dep.Type, &dep.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan dependency: %w", err)
		}
		deps = append(deps, &dep)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating dependents: %w", err)
	}

	return deps, nil
}

// ListInsightsByAuthor retrieves all insights by an author (exact match on author_id).
func (s *Store) ListInsightsByAuthor(authorID string) ([]*types.Insight, error) {
	query := `
		SELECT id, timestamp, content, summary, type, confidence,
		       source_type, source_ref, source_participants,
		       thread_id, author_id, endorsed_by, tags, created_by, created_at
		FROM insights
		WHERE author_id = ?
		ORDER BY timestamp DESC
	`

	rows, err := s.db.Query(query, authorID)
	if err != nil {
		return nil, fmt.Errorf("failed to query insights by author: %w", err)
	}
	defer rows.Close()

	var insights []*types.Insight
	for rows.Next() {
		var insight types.Insight
		var sourceParticipantsJSON, tagsJSON, endorsedByJSON sql.NullString
		var authorID, threadID sql.NullString

		err := rows.Scan(
			&insight.ID,
			&insight.Timestamp,
			&insight.Content,
			&insight.Summary,
			&insight.Type,
			&insight.Confidence,
			&insight.Source.Type,
			&insight.Source.Ref,
			&sourceParticipantsJSON,
			&threadID,
			&authorID,
			&endorsedByJSON,
			&tagsJSON,
			&insight.CreatedBy,
			&insight.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan insight: %w", err)
		}

		// Handle nullable fields
		if authorID.Valid {
			insight.AuthorID = authorID.String
		}
		if threadID.Valid {
			insight.ThreadID = threadID.String
		}

		// Deserialize complex fields
		if sourceParticipantsJSON.Valid && sourceParticipantsJSON.String != "" {
			if err := json.Unmarshal([]byte(sourceParticipantsJSON.String), &insight.Source.Participants); err != nil {
				return nil, fmt.Errorf("failed to unmarshal source participants: %w", err)
			}
		}

		if tagsJSON.Valid && tagsJSON.String != "" {
			if err := json.Unmarshal([]byte(tagsJSON.String), &insight.Tags); err != nil {
				return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
			}
		}

		if endorsedByJSON.Valid && endorsedByJSON.String != "" {
			if err := json.Unmarshal([]byte(endorsedByJSON.String), &insight.EndorsedBy); err != nil {
				return nil, fmt.Errorf("failed to unmarshal endorsed_by: %w", err)
			}
		}

		insights = append(insights, &insight)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating insights: %w", err)
	}

	return insights, nil
}

// ============================================================================
// Config Management
// ============================================================================

// GetConfig retrieves a configuration value by key.
func (s *Store) GetConfig(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil // Return empty string for missing keys
	}
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}
	return value, nil
}

// SetConfig sets a configuration value.
func (s *Store) SetConfig(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO config (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("failed to set config: %w", err)
	}
	return nil
}

// Verify checks the database integrity.
func (s *Store) Verify() error {
	// Run integrity check
	var result string
	err := s.db.QueryRow("PRAGMA integrity_check").Scan(&result)
	if err != nil {
		return fmt.Errorf("failed to run integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("database integrity check failed: %s", result)
	}

	// Verify all tables exist
	tables := []string{"threads", "insights", "dependencies", "config"}
	for _, table := range tables {
		var count int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to check table %s: %w", table, err)
		}
		if count == 0 {
			return fmt.Errorf("table missing: %s", table)
		}
	}

	return nil
}
