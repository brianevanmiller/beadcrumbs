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
	insights, err := s.ListInsights(thread.ID, "", time.Time{})
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
