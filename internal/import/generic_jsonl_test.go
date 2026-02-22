package importer

import (
	"strings"
	"testing"
)

func TestParseGenericJSONLReader_BasicMapping(t *testing.T) {
	jsonl := `{"content":"Found auth bypass","type":"discovery","timestamp":"2024-01-15T10:00:00Z","author":"alice"}
{"content":"Should we add MFA?","type":"question","author":"bob"}
`
	insights, err := ParseGenericJSONLReader(strings.NewReader(jsonl), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 2 {
		t.Fatalf("expected 2 insights, got %d", len(insights))
	}
	if insights[0].Content != "Found auth bypass" {
		t.Errorf("unexpected content: %s", insights[0].Content)
	}
	if string(insights[0].Type) != "discovery" {
		t.Errorf("unexpected type: %s", insights[0].Type)
	}
	if insights[0].AuthorID != "alice" {
		t.Errorf("unexpected author: %s", insights[0].AuthorID)
	}
}

func TestParseGenericJSONLReader_CustomMapping(t *testing.T) {
	jsonl := `{"body":"Custom mapped content","category":"hypothesis","created_at":"2024-03-01"}
`
	mapping := ColumnMapping{
		Content:   "body",
		Type:      "category",
		Timestamp: "created_at",
	}
	insights, err := ParseGenericJSONLReader(strings.NewReader(jsonl), mapping, "custom")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if insights[0].Content != "Custom mapped content" {
		t.Errorf("unexpected content: %s", insights[0].Content)
	}
	if insights[0].Source.Type != "custom" {
		t.Errorf("unexpected source type: %s", insights[0].Source.Type)
	}
}

func TestParseGenericJSONLReader_MalformedLines(t *testing.T) {
	jsonl := `{"content":"Good line 1","type":"discovery"}
this is not json
{"content":"Good line 2","type":"hypothesis"}
`
	insights, err := ParseGenericJSONLReader(strings.NewReader(jsonl), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should skip the malformed line and process the other two
	if len(insights) != 2 {
		t.Fatalf("expected 2 insights (skipping malformed), got %d", len(insights))
	}
}

func TestParseGenericJSONLReader_MissingContent(t *testing.T) {
	jsonl := `{"type":"discovery","author":"alice"}
`
	insights, err := ParseGenericJSONLReader(strings.NewReader(jsonl), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Lines missing content should be skipped
	if len(insights) != 0 {
		t.Fatalf("expected 0 insights (missing content), got %d", len(insights))
	}
}

func TestParseGenericJSONLReader_InvalidType(t *testing.T) {
	jsonl := `{"content":"Some content","type":"not_a_real_type"}
`
	insights, err := ParseGenericJSONLReader(strings.NewReader(jsonl), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	// Should fall back to DetectInsightType default (hypothesis)
	if string(insights[0].Type) != "hypothesis" {
		t.Errorf("expected fallback type 'hypothesis', got: %s", insights[0].Type)
	}
}

func TestParseGenericJSONLReader_EmptyFile(t *testing.T) {
	insights, err := ParseGenericJSONLReader(strings.NewReader(""), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 0 {
		t.Fatalf("expected 0 insights, got %d", len(insights))
	}
}

func TestParseGenericJSONLReader_UnicodeContent(t *testing.T) {
	jsonl := `{"content":"Unicode: 日本語テスト 🚀","type":"discovery"}
`
	insights, err := ParseGenericJSONLReader(strings.NewReader(jsonl), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if !strings.Contains(insights[0].Content, "日本語") {
		t.Errorf("unicode not preserved: %s", insights[0].Content)
	}
}

func TestParseGenericJSONLReader_VariousTimestampFormats(t *testing.T) {
	jsonl := `{"content":"RFC3339","type":"discovery","timestamp":"2024-01-15T10:30:00Z"}
{"content":"Date only","type":"discovery","timestamp":"2024-01-15"}
{"content":"Slack epoch","type":"discovery","timestamp":"1705312200.000000"}
`
	insights, err := ParseGenericJSONLReader(strings.NewReader(jsonl), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 3 {
		t.Fatalf("expected 3 insights, got %d", len(insights))
	}
	// All should have non-zero timestamps
	for i, ins := range insights {
		if ins.Timestamp.IsZero() {
			t.Errorf("insight %d has zero timestamp", i)
		}
	}
}

func TestParseGenericJSONLReader_TagsAsArray(t *testing.T) {
	jsonl := `{"content":"Tagged","type":"discovery","tags":["api","backend"]}
`
	insights, err := ParseGenericJSONLReader(strings.NewReader(jsonl), ColumnMapping{}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if len(insights[0].Tags) != 2 {
		t.Errorf("expected 2 tags, got %d: %v", len(insights[0].Tags), insights[0].Tags)
	}
}
