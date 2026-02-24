package store

import (
	"os"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

func TestStoreBasic(t *testing.T) {
	// Create temp database
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	// Create store
	s, err := NewStore(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Test thread creation
	thread := types.NewThread("Test Thread")
	if err := s.CreateThread(thread); err != nil {
		t.Fatalf("Failed to create thread: %v", err)
	}

	// Test thread retrieval
	retrieved, err := s.GetThread(thread.ID)
	if err != nil {
		t.Fatalf("Failed to get thread: %v", err)
	}

	if retrieved.Title != thread.Title {
		t.Errorf("Expected title %s, got %s", thread.Title, retrieved.Title)
	}

	// Test insight creation
	insight := types.NewInsight("Test insight content", types.InsightHypothesis)
	insight.Summary = "Test summary"
	insight.ThreadID = thread.ID

	if err := s.CreateInsight(insight); err != nil {
		t.Fatalf("Failed to create insight: %v", err)
	}

	// Test insight retrieval
	retrievedInsight, err := s.GetInsight(insight.ID)
	if err != nil {
		t.Fatalf("Failed to get insight: %v", err)
	}

	if retrievedInsight.Content != insight.Content {
		t.Errorf("Expected content %s, got %s", insight.Content, retrievedInsight.Content)
	}

	// Test listing insights
	insights, err := s.ListInsights(thread.ID, "", time.Time{}, "")
	if err != nil {
		t.Fatalf("Failed to list insights: %v", err)
	}

	if len(insights) != 1 {
		t.Errorf("Expected 1 insight, got %d", len(insights))
	}

	// Test dependency
	insight2 := types.NewInsight("Second insight", types.InsightDiscovery)
	insight2.ThreadID = thread.ID
	if err := s.CreateInsight(insight2); err != nil {
		t.Fatalf("Failed to create second insight: %v", err)
	}

	dep := types.NewDependency(insight.ID, insight2.ID, types.DepBuildsOn)
	if err := s.AddDependency(dep); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Test getting dependencies
	deps, err := s.GetDependencies(insight.ID)
	if err != nil {
		t.Fatalf("Failed to get dependencies: %v", err)
	}

	if len(deps) != 1 {
		t.Errorf("Expected 1 dependency, got %d", len(deps))
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpfile.Name()) })
	tmpfile.Close()

	s, err := NewStore(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestListInsightsFilterBySourceRef(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Test Thread")
	if err := s.CreateThread(thread); err != nil {
		t.Fatalf("Failed to create thread: %v", err)
	}

	// Create insights with different source refs
	ins1 := types.NewInsight("From Claude session", types.InsightHypothesis)
	ins1.ThreadID = thread.ID
	ins1.Source.Ref = "claude:sess_abc"
	ins1.Source.Type = "ai-session"

	ins2 := types.NewInsight("From Cursor session", types.InsightDiscovery)
	ins2.ThreadID = thread.ID
	ins2.Source.Ref = "cursor:ws_123"
	ins2.Source.Type = "ai-session"

	ins3 := types.NewInsight("No origin set", types.InsightDecision)
	ins3.ThreadID = thread.ID

	for _, ins := range []*types.Insight{ins1, ins2, ins3} {
		if err := s.CreateInsight(ins); err != nil {
			t.Fatalf("Failed to create insight: %v", err)
		}
	}

	// Filter by specific origin
	results, err := s.ListInsights("", "", time.Time{}, "claude:sess_abc")
	if err != nil {
		t.Fatalf("ListInsights failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected 1 insight for claude:sess_abc, got %d", len(results))
	}
	if len(results) > 0 && results[0].ID != ins1.ID {
		t.Errorf("Expected insight %s, got %s", ins1.ID, results[0].ID)
	}

	// Filter by different origin
	results, err = s.ListInsights("", "", time.Time{}, "cursor:ws_123")
	if err != nil {
		t.Fatalf("ListInsights failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected 1 insight for cursor:ws_123, got %d", len(results))
	}

	// Non-existent origin returns empty
	results, err = s.ListInsights("", "", time.Time{}, "nonexistent:xxx")
	if err != nil {
		t.Fatalf("ListInsights failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 insights for nonexistent origin, got %d", len(results))
	}

	// Empty sourceRef returns all
	results, err = s.ListInsights("", "", time.Time{}, "")
	if err != nil {
		t.Fatalf("ListInsights failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("Expected 3 insights with no filter, got %d", len(results))
	}

	// Combine sourceRef with thread filter
	results, err = s.ListInsights(thread.ID, "", time.Time{}, "claude:sess_abc")
	if err != nil {
		t.Fatalf("ListInsights failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected 1 insight for thread+origin filter, got %d", len(results))
	}
}

func TestListOrigins(t *testing.T) {
	s := newTestStore(t)

	thread1 := types.NewThread("Thread 1")
	thread2 := types.NewThread("Thread 2")
	if err := s.CreateThread(thread1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateThread(thread2); err != nil {
		t.Fatal(err)
	}

	// Create insights across origins and threads
	insA1 := types.NewInsight("A insight 1", types.InsightHypothesis)
	insA1.ThreadID = thread1.ID
	insA1.Source.Ref = "claude:sess_aaa"
	insA1.Source.Type = "ai-session"

	insA2 := types.NewInsight("A insight 2", types.InsightDecision)
	insA2.ThreadID = thread2.ID
	insA2.Source.Ref = "claude:sess_aaa"
	insA2.Source.Type = "ai-session"

	insB1 := types.NewInsight("B insight 1", types.InsightDiscovery)
	insB1.ThreadID = thread1.ID
	insB1.Source.Ref = "notion:page_xyz"
	insB1.Source.Type = "human"

	// Insight with no origin — should NOT appear in origins list
	insNone := types.NewInsight("No origin", types.InsightFeedback)
	insNone.ThreadID = thread1.ID

	for _, ins := range []*types.Insight{insA1, insA2, insB1, insNone} {
		if err := s.CreateInsight(ins); err != nil {
			t.Fatalf("Failed to create insight: %v", err)
		}
	}

	origins, err := s.ListOrigins()
	if err != nil {
		t.Fatalf("ListOrigins failed: %v", err)
	}

	if len(origins) != 2 {
		t.Fatalf("Expected 2 origins, got %d", len(origins))
	}

	// Results are ordered by last_activity DESC; both are ~now, so check by content
	originMap := make(map[string]*OriginSummary)
	for _, o := range origins {
		originMap[o.SourceRef] = o
	}

	claudeOrigin, ok := originMap["claude:sess_aaa"]
	if !ok {
		t.Fatal("Missing claude:sess_aaa origin")
	}
	if claudeOrigin.InsightCount != 2 {
		t.Errorf("Expected 2 insights for claude origin, got %d", claudeOrigin.InsightCount)
	}
	// Should have both thread IDs (comma-separated)
	if claudeOrigin.ThreadIDs == "" {
		t.Error("Expected thread IDs for claude origin, got empty")
	}

	notionOrigin, ok := originMap["notion:page_xyz"]
	if !ok {
		t.Fatal("Missing notion:page_xyz origin")
	}
	if notionOrigin.InsightCount != 1 {
		t.Errorf("Expected 1 insight for notion origin, got %d", notionOrigin.InsightCount)
	}
}

func TestListOriginsEmpty(t *testing.T) {
	s := newTestStore(t)

	origins, err := s.ListOrigins()
	if err != nil {
		t.Fatalf("ListOrigins failed: %v", err)
	}
	if len(origins) != 0 {
		t.Errorf("Expected 0 origins on empty db, got %d", len(origins))
	}
}

func TestListOriginsExcludesEmptyThreadIDs(t *testing.T) {
	s := newTestStore(t)

	// Create insight with origin but NO thread (empty thread_id)
	ins := types.NewInsight("Threadless insight", types.InsightHypothesis)
	ins.Source.Ref = "claude:sess_nothrd"
	ins.Source.Type = "ai-session"
	// ThreadID left empty

	if err := s.CreateInsight(ins); err != nil {
		t.Fatal(err)
	}

	origins, err := s.ListOrigins()
	if err != nil {
		t.Fatalf("ListOrigins failed: %v", err)
	}

	if len(origins) != 1 {
		t.Fatalf("Expected 1 origin, got %d", len(origins))
	}

	// ThreadIDs should be empty (not contain empty string or leading comma)
	o := origins[0]
	if o.ThreadIDs != "" {
		t.Errorf("Expected empty ThreadIDs for threadless insight, got %q", o.ThreadIDs)
	}
}

// ============================================================================
// Upsert Operations (PR #10)
// ============================================================================

func TestUpsertInsight(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Test Thread")
	if err := s.CreateThread(thread); err != nil {
		t.Fatal(err)
	}

	// Create via upsert (insert path)
	ins := types.NewInsight("Original content", types.InsightHypothesis)
	ins.ThreadID = thread.ID
	ins.AuthorID = "cc:opus-4.6"
	if err := s.UpsertInsight(ins); err != nil {
		t.Fatalf("UpsertInsight (insert) failed: %v", err)
	}

	// Verify insert
	got, err := s.GetInsight(ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Original content" {
		t.Errorf("got content %q, want %q", got.Content, "Original content")
	}

	// Upsert with changed content (update path)
	ins.Content = "Updated content"
	ins.Type = types.InsightDecision
	if err := s.UpsertInsight(ins); err != nil {
		t.Fatalf("UpsertInsight (update) failed: %v", err)
	}

	got, err = s.GetInsight(ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Updated content" {
		t.Errorf("got content %q after upsert, want %q", got.Content, "Updated content")
	}
	if got.Type != types.InsightDecision {
		t.Errorf("got type %q after upsert, want %q", got.Type, types.InsightDecision)
	}
}

func TestUpsertThread(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Original Title")
	if err := s.UpsertThread(thread); err != nil {
		t.Fatalf("UpsertThread (insert) failed: %v", err)
	}

	got, err := s.GetThread(thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Original Title" {
		t.Errorf("got title %q, want %q", got.Title, "Original Title")
	}

	// Upsert with changed title
	thread.Title = "Updated Title"
	thread.Status = types.ThreadConcluded
	if err := s.UpsertThread(thread); err != nil {
		t.Fatalf("UpsertThread (update) failed: %v", err)
	}

	got, err = s.GetThread(thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Updated Title" {
		t.Errorf("got title %q after upsert, want %q", got.Title, "Updated Title")
	}
	if got.Status != types.ThreadConcluded {
		t.Errorf("got status %q after upsert, want %q", got.Status, types.ThreadConcluded)
	}
}

func TestUpsertDependency(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Test")
	s.CreateThread(thread)

	ins1 := types.NewInsight("A", types.InsightHypothesis)
	ins1.ThreadID = thread.ID
	s.CreateInsight(ins1)

	ins2 := types.NewInsight("B", types.InsightDecision)
	ins2.ThreadID = thread.ID
	s.CreateInsight(ins2)

	dep := types.NewDependency(ins1.ID, ins2.ID, types.DepBuildsOn)
	if err := s.UpsertDependency(dep); err != nil {
		t.Fatalf("UpsertDependency (insert) failed: %v", err)
	}

	// Upsert duplicate — should succeed (ON CONFLICT DO NOTHING)
	if err := s.UpsertDependency(dep); err != nil {
		t.Fatalf("UpsertDependency (duplicate) failed: %v", err)
	}

	// Verify still just 1 dependency
	deps, err := s.GetDependencies(ins1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 {
		t.Errorf("expected 1 dependency after duplicate upsert, got %d", len(deps))
	}
}

// ============================================================================
// External Ref Mapping CRUD (PRs #3, #7)
// ============================================================================

func TestExternalRefMappingCRUD(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Linked Thread")
	if err := s.CreateThread(thread); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	mapping := &ExternalRefMapping{
		ExternalRef: "linear:ENG-456",
		ThreadID:    thread.ID,
		System:      "linear",
		ExternalID:  "ENG-456",
		Metadata:    `{"title":"Fix auth"}`,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Create
	if err := s.CreateExternalRefMapping(mapping); err != nil {
		t.Fatalf("CreateExternalRefMapping failed: %v", err)
	}

	// GetByRef
	got, err := s.GetExternalRefMappingByRef("linear:ENG-456")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("GetExternalRefMappingByRef returned nil")
	}
	if got.ThreadID != thread.ID {
		t.Errorf("got ThreadID %q, want %q", got.ThreadID, thread.ID)
	}
	if got.System != "linear" {
		t.Errorf("got System %q, want %q", got.System, "linear")
	}
	if got.ExternalID != "ENG-456" {
		t.Errorf("got ExternalID %q, want %q", got.ExternalID, "ENG-456")
	}

	// GetByThread
	mappings, err := s.GetExternalRefMappingsByThread(thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping for thread, got %d", len(mappings))
	}
	if mappings[0].ExternalRef != "linear:ENG-456" {
		t.Errorf("got ExternalRef %q, want %q", mappings[0].ExternalRef, "linear:ENG-456")
	}

	// Add another mapping to same thread
	mapping2 := &ExternalRefMapping{
		ExternalRef: "bead:abc1",
		ThreadID:    thread.ID,
		System:      "bead",
		ExternalID:  "abc1",
		Metadata:    "{}",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.CreateExternalRefMapping(mapping2); err != nil {
		t.Fatal(err)
	}

	mappings, err = s.GetExternalRefMappingsByThread(thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 2 {
		t.Errorf("expected 2 mappings for thread, got %d", len(mappings))
	}

	// UpdateMetadata
	if err := s.UpdateExternalRefMappingMetadata("linear:ENG-456", `{"title":"Updated"}`); err != nil {
		t.Fatal(err)
	}

	got, _ = s.GetExternalRefMappingByRef("linear:ENG-456")
	if got.Metadata != `{"title":"Updated"}` {
		t.Errorf("got Metadata %q after update, want %q", got.Metadata, `{"title":"Updated"}`)
	}
}

func TestExternalRefMappingNotFound(t *testing.T) {
	s := newTestStore(t)

	got, err := s.GetExternalRefMappingByRef("nonexistent:ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing ref, got %+v", got)
	}
}

// ============================================================================
// Config Management (PRs #3, #4)
// ============================================================================

func TestConfigGetSet(t *testing.T) {
	s := newTestStore(t)

	// Missing key returns empty string
	val, err := s.GetConfig("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("expected empty string for missing key, got %q", val)
	}

	// Set and get
	if err := s.SetConfig("linear.cli_tool", "schpet"); err != nil {
		t.Fatal(err)
	}
	val, err = s.GetConfig("linear.cli_tool")
	if err != nil {
		t.Fatal(err)
	}
	if val != "schpet" {
		t.Errorf("got %q, want %q", val, "schpet")
	}

	// Overwrite
	if err := s.SetConfig("linear.cli_tool", "finesssee"); err != nil {
		t.Fatal(err)
	}
	val, err = s.GetConfig("linear.cli_tool")
	if err != nil {
		t.Fatal(err)
	}
	if val != "finesssee" {
		t.Errorf("got %q after overwrite, want %q", val, "finesssee")
	}
}

// ============================================================================
// Search, Author, Update, Delete
// ============================================================================

func TestSearchInsights(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Search Test")
	s.CreateThread(thread)

	ins1 := types.NewInsight("Redis caching layer implementation", types.InsightHypothesis)
	ins1.ThreadID = thread.ID
	s.CreateInsight(ins1)

	ins2 := types.NewInsight("PostgreSQL query optimization", types.InsightDiscovery)
	ins2.ThreadID = thread.ID
	s.CreateInsight(ins2)

	ins3 := types.NewInsight("Adding Redis pub/sub for invalidation", types.InsightDecision)
	ins3.ThreadID = thread.ID
	s.CreateInsight(ins3)

	// Search for "Redis" should return 2 results
	results, err := s.SearchInsights("Redis")
	if err != nil {
		t.Fatalf("SearchInsights failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for 'Redis', got %d", len(results))
	}

	// Search for "PostgreSQL" should return 1
	results, err = s.SearchInsights("PostgreSQL")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'PostgreSQL', got %d", len(results))
	}

	// Search for non-matching term
	results, err = s.SearchInsights("MongoDB")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for 'MongoDB', got %d", len(results))
	}
}

func TestListInsightsByAuthor(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Author Test")
	s.CreateThread(thread)

	ins1 := types.NewInsight("AI insight", types.InsightHypothesis)
	ins1.ThreadID = thread.ID
	ins1.AuthorID = "cc:opus-4.6"
	s.CreateInsight(ins1)

	ins2 := types.NewInsight("Human insight", types.InsightFeedback)
	ins2.ThreadID = thread.ID
	ins2.AuthorID = "brian"
	s.CreateInsight(ins2)

	ins3 := types.NewInsight("Another AI insight", types.InsightDecision)
	ins3.ThreadID = thread.ID
	ins3.AuthorID = "cc:opus-4.6"
	s.CreateInsight(ins3)

	// Filter by AI author
	results, err := s.ListInsightsByAuthor("cc:opus-4.6")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 insights for cc:opus-4.6, got %d", len(results))
	}

	// Filter by human author
	results, err = s.ListInsightsByAuthor("brian")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 insight for brian, got %d", len(results))
	}

	// Non-existent author
	results, err = s.ListInsightsByAuthor("nobody")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 insights for nobody, got %d", len(results))
	}
}

func TestUpdateInsight(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Update Test")
	s.CreateThread(thread)

	ins := types.NewInsight("Original content", types.InsightHypothesis)
	ins.ThreadID = thread.ID
	s.CreateInsight(ins)

	// Update non-content fields (type, author, source).
	// Note: changing content/summary triggers the FTS5 UPDATE trigger
	// which has a known compatibility issue with modernc.org/sqlite.
	ins.Type = types.InsightDecision
	ins.AuthorID = "cc:opus-4.6"
	ins.Source.Ref = "claude:sess_update"
	ins.Source.Type = "ai-session"
	if err := s.UpdateInsight(ins); err != nil {
		t.Fatalf("UpdateInsight failed: %v", err)
	}

	got, err := s.GetInsight(ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != types.InsightDecision {
		t.Errorf("type = %q, want %q", got.Type, types.InsightDecision)
	}
	if got.AuthorID != "cc:opus-4.6" {
		t.Errorf("authorID = %q, want %q", got.AuthorID, "cc:opus-4.6")
	}
	if got.Source.Ref != "claude:sess_update" {
		t.Errorf("source.ref = %q, want %q", got.Source.Ref, "claude:sess_update")
	}
}

func TestUpdateInsight_NotFound(t *testing.T) {
	s := newTestStore(t)

	ins := types.NewInsight("Ghost", types.InsightHypothesis)
	err := s.UpdateInsight(ins)
	if err == nil {
		t.Error("expected error updating non-existent insight")
	}
}

func TestDeleteInsight(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Delete Test")
	s.CreateThread(thread)

	ins := types.NewInsight("To be deleted", types.InsightHypothesis)
	ins.ThreadID = thread.ID
	s.CreateInsight(ins)

	// Delete
	if err := s.DeleteInsight(ins.ID); err != nil {
		t.Fatalf("DeleteInsight failed: %v", err)
	}

	// Verify it's gone
	_, err := s.GetInsight(ins.ID)
	if err == nil {
		t.Error("expected error getting deleted insight")
	}
}

func TestDeleteInsight_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.DeleteInsight("ins-nonexistent")
	if err == nil {
		t.Error("expected error deleting non-existent insight")
	}
}

// ============================================================================
// Thread Operations
// ============================================================================

func TestListThreads(t *testing.T) {
	s := newTestStore(t)

	t1 := types.NewThread("Active Thread")
	t2 := types.NewThread("Concluded Thread")
	t2.Status = types.ThreadConcluded
	t3 := types.NewThread("Another Active")

	for _, th := range []*types.InsightThread{t1, t2, t3} {
		if err := s.CreateThread(th); err != nil {
			t.Fatal(err)
		}
	}

	// List all
	all, err := s.ListThreads("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 threads, got %d", len(all))
	}

	// Filter active
	active, err := s.ListThreads(types.ThreadActive)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active threads, got %d", len(active))
	}

	// Filter concluded
	concluded, err := s.ListThreads(types.ThreadConcluded)
	if err != nil {
		t.Fatal(err)
	}
	if len(concluded) != 1 {
		t.Errorf("expected 1 concluded thread, got %d", len(concluded))
	}
}

func TestUpdateThread(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Original")
	if err := s.CreateThread(thread); err != nil {
		t.Fatal(err)
	}

	thread.Title = "Updated Title"
	thread.Status = types.ThreadConcluded
	thread.UpdatedAt = time.Now()
	if err := s.UpdateThread(thread); err != nil {
		t.Fatalf("UpdateThread failed: %v", err)
	}

	got, err := s.GetThread(thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Updated Title" {
		t.Errorf("title = %q, want %q", got.Title, "Updated Title")
	}
	if got.Status != types.ThreadConcluded {
		t.Errorf("status = %q, want %q", got.Status, types.ThreadConcluded)
	}
}

func TestUpdateThread_NotFound(t *testing.T) {
	s := newTestStore(t)
	thread := types.NewThread("Ghost")
	err := s.UpdateThread(thread)
	if err == nil {
		t.Error("expected error updating non-existent thread")
	}
}

// ============================================================================
// Dependency Operations
// ============================================================================

func TestGetDependents(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Deps Test")
	s.CreateThread(thread)

	insA := types.NewInsight("A", types.InsightHypothesis)
	insA.ThreadID = thread.ID
	insB := types.NewInsight("B", types.InsightDiscovery)
	insB.ThreadID = thread.ID
	insC := types.NewInsight("C", types.InsightDecision)
	insC.ThreadID = thread.ID

	for _, ins := range []*types.Insight{insA, insB, insC} {
		s.CreateInsight(ins)
	}

	// A builds-on B, C supersedes B
	s.AddDependency(types.NewDependency(insA.ID, insB.ID, types.DepBuildsOn))
	s.AddDependency(types.NewDependency(insC.ID, insB.ID, types.DepSupersedes))

	// GetDependents of B should return A and C
	dependents, err := s.GetDependents(insB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependents) != 2 {
		t.Errorf("expected 2 dependents of B, got %d", len(dependents))
	}

	// GetDependents of A should return none
	dependents, err = s.GetDependents(insA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependents) != 0 {
		t.Errorf("expected 0 dependents of A, got %d", len(dependents))
	}
}

func TestListAllDependencies(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("All Deps Test")
	s.CreateThread(thread)

	insA := types.NewInsight("A", types.InsightHypothesis)
	insA.ThreadID = thread.ID
	insB := types.NewInsight("B", types.InsightDiscovery)
	insB.ThreadID = thread.ID
	insC := types.NewInsight("C", types.InsightDecision)
	insC.ThreadID = thread.ID

	for _, ins := range []*types.Insight{insA, insB, insC} {
		s.CreateInsight(ins)
	}

	s.AddDependency(types.NewDependency(insA.ID, insB.ID, types.DepBuildsOn))
	s.AddDependency(types.NewDependency(insB.ID, insC.ID, types.DepSupersedes))
	s.AddDependency(types.NewDependency(insA.ID, insC.ID, types.DepContradicts))

	deps, err := s.ListAllDependencies()
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 3 {
		t.Errorf("expected 3 total dependencies, got %d", len(deps))
	}
}

func TestListAllDependencies_Empty(t *testing.T) {
	s := newTestStore(t)

	deps, err := s.ListAllDependencies()
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 0 {
		t.Errorf("expected 0 dependencies on empty db, got %d", len(deps))
	}
}

func TestDuplicateDependency(t *testing.T) {
	s := newTestStore(t)

	thread := types.NewThread("Dup Dep Test")
	s.CreateThread(thread)

	insA := types.NewInsight("A", types.InsightHypothesis)
	insA.ThreadID = thread.ID
	insB := types.NewInsight("B", types.InsightDiscovery)
	insB.ThreadID = thread.ID

	s.CreateInsight(insA)
	s.CreateInsight(insB)

	dep := types.NewDependency(insA.ID, insB.ID, types.DepBuildsOn)
	if err := s.AddDependency(dep); err != nil {
		t.Fatal(err)
	}

	// Adding same dependency again should error
	err := s.AddDependency(dep)
	if err == nil {
		t.Error("expected error on duplicate dependency")
	}
}

// ============================================================================
// Verify
// ============================================================================

func TestVerify(t *testing.T) {
	s := newTestStore(t)

	if err := s.Verify(); err != nil {
		t.Fatalf("Verify failed on fresh store: %v", err)
	}
}
