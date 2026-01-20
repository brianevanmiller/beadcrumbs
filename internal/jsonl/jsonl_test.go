package jsonl

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

func TestExportImportInsights(t *testing.T) {
	// Create test data
	now := time.Now()
	insights := []*types.Insight{
		{
			ID:         "ins-0001",
			Timestamp:  now,
			Content:    "Test insight 1",
			Summary:    "Summary 1",
			Type:       types.InsightHypothesis,
			Confidence: 0.9,
			Source: types.InsightSource{
				Type:         "human",
				Participants: []string{"alice"},
			},
			ThreadID:  "thr-0001",
			Tags:      []string{"test", "example"},
			CreatedBy: "alice",
			CreatedAt: now,
		},
		{
			ID:         "ins-0002",
			Timestamp:  now.Add(time.Hour),
			Content:    "Test insight 2",
			Summary:    "Summary 2",
			Type:       types.InsightDiscovery,
			Confidence: 0.8,
			Source: types.InsightSource{
				Type: "ai-session",
				Ref:  "session-123",
			},
			CreatedAt: now.Add(time.Hour),
		},
	}

	// Create temp file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "insights.jsonl")

	// Test export
	if err := ExportInsights(insights, filePath); err != nil {
		t.Fatalf("ExportInsights failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("File was not created: %s", filePath)
	}

	// Test import
	imported, err := ImportInsights(filePath)
	if err != nil {
		t.Fatalf("ImportInsights failed: %v", err)
	}

	// Verify data
	if len(imported) != len(insights) {
		t.Fatalf("Expected %d insights, got %d", len(insights), len(imported))
	}

	for i, original := range insights {
		imp := imported[i]
		if imp.ID != original.ID {
			t.Errorf("Insight %d: ID mismatch. Expected %s, got %s", i, original.ID, imp.ID)
		}
		if imp.Content != original.Content {
			t.Errorf("Insight %d: Content mismatch. Expected %s, got %s", i, original.Content, imp.Content)
		}
		if imp.Type != original.Type {
			t.Errorf("Insight %d: Type mismatch. Expected %s, got %s", i, original.Type, imp.Type)
		}
		if imp.Confidence != original.Confidence {
			t.Errorf("Insight %d: Confidence mismatch. Expected %f, got %f", i, original.Confidence, imp.Confidence)
		}
	}
}

func TestExportImportThreads(t *testing.T) {
	// Create test data
	now := time.Now()
	threads := []*types.InsightThread{
		{
			ID:                   "thr-0001",
			Title:                "Test thread 1",
			Status:               types.ThreadActive,
			CurrentUnderstanding: "We are exploring the auth bug",
			CreatedAt:            now,
			UpdatedAt:            now,
		},
		{
			ID:        "thr-0002",
			Title:     "Test thread 2",
			Status:    types.ThreadConcluded,
			CreatedAt: now.Add(time.Hour),
			UpdatedAt: now.Add(2 * time.Hour),
		},
	}

	// Create temp file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "threads.jsonl")

	// Test export
	if err := ExportThreads(threads, filePath); err != nil {
		t.Fatalf("ExportThreads failed: %v", err)
	}

	// Test import
	imported, err := ImportThreads(filePath)
	if err != nil {
		t.Fatalf("ImportThreads failed: %v", err)
	}

	// Verify data
	if len(imported) != len(threads) {
		t.Fatalf("Expected %d threads, got %d", len(threads), len(imported))
	}

	for i, original := range threads {
		imp := imported[i]
		if imp.ID != original.ID {
			t.Errorf("Thread %d: ID mismatch. Expected %s, got %s", i, original.ID, imp.ID)
		}
		if imp.Title != original.Title {
			t.Errorf("Thread %d: Title mismatch. Expected %s, got %s", i, original.Title, imp.Title)
		}
		if imp.Status != original.Status {
			t.Errorf("Thread %d: Status mismatch. Expected %s, got %s", i, original.Status, imp.Status)
		}
	}
}

func TestExportImportDependencies(t *testing.T) {
	// Create test data
	now := time.Now()
	deps := []*types.Dependency{
		{
			From:      "ins-0001",
			To:        "ins-0002",
			Type:      types.DepBuildsOn,
			CreatedAt: now,
		},
		{
			From:      "ins-0002",
			To:        "bead-0001",
			Type:      types.DepSpawns,
			CreatedAt: now.Add(time.Hour),
		},
	}

	// Create temp file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "deps.jsonl")

	// Test export
	if err := ExportDependencies(deps, filePath); err != nil {
		t.Fatalf("ExportDependencies failed: %v", err)
	}

	// Test import
	imported, err := ImportDependencies(filePath)
	if err != nil {
		t.Fatalf("ImportDependencies failed: %v", err)
	}

	// Verify data
	if len(imported) != len(deps) {
		t.Fatalf("Expected %d dependencies, got %d", len(deps), len(imported))
	}

	for i, original := range deps {
		imp := imported[i]
		if imp.From != original.From {
			t.Errorf("Dependency %d: From mismatch. Expected %s, got %s", i, original.From, imp.From)
		}
		if imp.To != original.To {
			t.Errorf("Dependency %d: To mismatch. Expected %s, got %s", i, original.To, imp.To)
		}
		if imp.Type != original.Type {
			t.Errorf("Dependency %d: Type mismatch. Expected %s, got %s", i, original.Type, imp.Type)
		}
	}
}

func TestExportEmptyData(t *testing.T) {
	tmpDir := t.TempDir()

	// Test empty insights
	filePath := filepath.Join(tmpDir, "empty_insights.jsonl")
	if err := ExportInsights([]*types.Insight{}, filePath); err != nil {
		t.Fatalf("ExportInsights with empty data failed: %v", err)
	}
	imported, err := ImportInsights(filePath)
	if err != nil {
		t.Fatalf("ImportInsights with empty file failed: %v", err)
	}
	if len(imported) != 0 {
		t.Errorf("Expected 0 insights, got %d", len(imported))
	}

	// Test empty threads
	filePath = filepath.Join(tmpDir, "empty_threads.jsonl")
	if err := ExportThreads([]*types.InsightThread{}, filePath); err != nil {
		t.Fatalf("ExportThreads with empty data failed: %v", err)
	}
	importedThreads, err := ImportThreads(filePath)
	if err != nil {
		t.Fatalf("ImportThreads with empty file failed: %v", err)
	}
	if len(importedThreads) != 0 {
		t.Errorf("Expected 0 threads, got %d", len(importedThreads))
	}

	// Test empty dependencies
	filePath = filepath.Join(tmpDir, "empty_deps.jsonl")
	if err := ExportDependencies([]*types.Dependency{}, filePath); err != nil {
		t.Fatalf("ExportDependencies with empty data failed: %v", err)
	}
	importedDeps, err := ImportDependencies(filePath)
	if err != nil {
		t.Fatalf("ImportDependencies with empty file failed: %v", err)
	}
	if len(importedDeps) != 0 {
		t.Errorf("Expected 0 dependencies, got %d", len(importedDeps))
	}
}

func TestImportNonExistentFile(t *testing.T) {
	_, err := ImportInsights("/nonexistent/path/insights.jsonl")
	if err == nil {
		t.Fatal("Expected error when importing from nonexistent file")
	}

	_, err = ImportThreads("/nonexistent/path/threads.jsonl")
	if err == nil {
		t.Fatal("Expected error when importing from nonexistent file")
	}

	_, err = ImportDependencies("/nonexistent/path/deps.jsonl")
	if err == nil {
		t.Fatal("Expected error when importing from nonexistent file")
	}
}

func TestImportMalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "malformed.jsonl")

	// Write malformed JSON
	content := `{"id": "ins-0001", "content": "valid"}
{"id": "ins-0002", "content": "invalid" this is broken}
{"id": "ins-0003", "content": "also valid"}`

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Import should fail
	_, err := ImportInsights(filePath)
	if err == nil {
		t.Fatal("Expected error when importing malformed JSON")
	}
}

func TestImportWithEmptyLines(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "with_empty_lines.jsonl")

	// Write JSONL with empty lines
	now := time.Now()
	content := `{"id":"ins-0001","timestamp":"` + now.Format(time.RFC3339Nano) + `","content":"First","summary":"S1","type":"hypothesis","confidence":1.0,"source":{"type":"human"},"created_at":"` + now.Format(time.RFC3339Nano) + `"}

{"id":"ins-0002","timestamp":"` + now.Format(time.RFC3339Nano) + `","content":"Second","summary":"S2","type":"discovery","confidence":1.0,"source":{"type":"human"},"created_at":"` + now.Format(time.RFC3339Nano) + `"}

`

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Import should succeed and skip empty lines
	imported, err := ImportInsights(filePath)
	if err != nil {
		t.Fatalf("ImportInsights failed: %v", err)
	}

	if len(imported) != 2 {
		t.Errorf("Expected 2 insights (empty lines skipped), got %d", len(imported))
	}
}

func TestExportInvalidPath(t *testing.T) {
	insights := []*types.Insight{
		{
			ID:      "ins-0001",
			Content: "Test",
			Type:    types.InsightHypothesis,
		},
	}

	// Try to export to invalid path (directory that doesn't exist)
	err := ExportInsights(insights, "/nonexistent/directory/file.jsonl")
	if err == nil {
		t.Fatal("Expected error when exporting to invalid path")
	}
}
