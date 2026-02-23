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
