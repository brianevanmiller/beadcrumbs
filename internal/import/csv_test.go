package importer

import (
	"strings"
	"testing"
)

func TestParseCSVReader_BasicMapping(t *testing.T) {
	csv := `content,type,timestamp,author
"Found a bug in auth flow",discovery,2024-01-15T10:00:00Z,alice
"Should we switch to OAuth?",question,2024-01-15T11:00:00Z,bob
`
	insights, err := ParseCSVReader(strings.NewReader(csv), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 2 {
		t.Fatalf("expected 2 insights, got %d", len(insights))
	}
	if insights[0].Content != "Found a bug in auth flow" {
		t.Errorf("unexpected content: %s", insights[0].Content)
	}
	if string(insights[0].Type) != "discovery" {
		t.Errorf("unexpected type: %s", insights[0].Type)
	}
	if insights[0].AuthorID != "alice" {
		t.Errorf("unexpected author: %s", insights[0].AuthorID)
	}
	if insights[1].Content != "Should we switch to OAuth?" {
		t.Errorf("unexpected content: %s", insights[1].Content)
	}
	if string(insights[1].Type) != "question" {
		t.Errorf("unexpected type: %s", insights[1].Type)
	}
}

func TestParseCSVReader_CustomMapping(t *testing.T) {
	csv := `body,category,created_at
"Important discovery about caching",discovery,2024-03-01
"We decided to use Redis",decision,2024-03-02
`
	mapping := ColumnMapping{
		Content:   "body",
		Type:      "category",
		Timestamp: "created_at",
	}
	insights, err := ParseCSVReader(strings.NewReader(csv), mapping, "custom-source")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 2 {
		t.Fatalf("expected 2 insights, got %d", len(insights))
	}
	if insights[0].Content != "Important discovery about caching" {
		t.Errorf("unexpected content: %s", insights[0].Content)
	}
	if insights[0].Source.Type != "custom-source" {
		t.Errorf("unexpected source type: %s", insights[0].Source.Type)
	}
}

func TestParseCSVReader_MinimalMapping(t *testing.T) {
	csv := `content
"Just some content here"
"Another insight to import"
`
	insights, err := ParseCSVReader(strings.NewReader(csv), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 2 {
		t.Fatalf("expected 2 insights, got %d", len(insights))
	}
	// Type defaults to hypothesis via DetectInsightType when no type column
	if string(insights[0].Type) != "hypothesis" {
		t.Errorf("expected default type 'hypothesis', got: %s", insights[0].Type)
	}
}

func TestParseCSVReader_MissingContentColumn(t *testing.T) {
	csv := `title,category
"Some title","question"
`
	_, err := ParseCSVReader(strings.NewReader(csv), ColumnMapping{}, "test")
	if err == nil {
		t.Fatal("expected error for missing content column")
	}
}

func TestParseCSVReader_CaseInsensitiveHeaders(t *testing.T) {
	csv := `Content,Type,Author
"Case test content",hypothesis,charlie
`
	insights, err := ParseCSVReader(strings.NewReader(csv), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if insights[0].Content != "Case test content" {
		t.Errorf("unexpected content: %s", insights[0].Content)
	}
}

func TestParseCSVReader_InvalidType(t *testing.T) {
	csv := `content,type
"Some content","invalid_type"
`
	insights, err := ParseCSVReader(strings.NewReader(csv), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	// Invalid type should fall back to DetectInsightType default (hypothesis)
	if string(insights[0].Type) != "hypothesis" {
		t.Errorf("expected fallback type 'hypothesis', got: %s", insights[0].Type)
	}
}

func TestParseCSVReader_EmptyFile(t *testing.T) {
	csv := `content,type
`
	insights, err := ParseCSVReader(strings.NewReader(csv), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 0 {
		t.Fatalf("expected 0 insights, got %d", len(insights))
	}
}

func TestParseCSVReader_UnicodeContent(t *testing.T) {
	csv := `content,type
"Unicode: Ünïcödé 日本語 emoji 🎉",discovery
`
	insights, err := ParseCSVReader(strings.NewReader(csv), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if !strings.Contains(insights[0].Content, "日本語") {
		t.Errorf("unicode content not preserved: %s", insights[0].Content)
	}
}

func TestParseCSVReader_TagsParsing(t *testing.T) {
	csv := `content,tags
"Tagged content","api,backend,auth"
`
	insights, err := ParseCSVReader(strings.NewReader(csv), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if len(insights[0].Tags) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(insights[0].Tags), insights[0].Tags)
	}
}
