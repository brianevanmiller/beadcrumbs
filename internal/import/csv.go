package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// ColumnMapping defines how CSV/JSONL columns map to insight fields.
type ColumnMapping struct {
	Content   string // Column name for insight content (required)
	Type      string // Column name for insight type (optional)
	Timestamp string // Column name for timestamp (optional)
	Author    string // Column name for author (optional)
	Summary   string // Column name for summary (optional)
	SourceRef string // Column name for source reference (optional)
	Tags      string // Column name for tags (optional, comma-separated)
}

// ParseCSV reads a CSV file and converts rows to insights using the column mapping.
func ParseCSV(filePath string, mapping ColumnMapping, sourceType string) ([]*types.Insight, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer f.Close()

	return ParseCSVReader(f, mapping, sourceType)
}

// ParseCSVReader reads CSV from an io.Reader and converts rows to insights.
func ParseCSVReader(r io.Reader, mapping ColumnMapping, sourceType string) ([]*types.Insight, error) {
	if sourceType == "" {
		sourceType = "csv"
	}

	reader := csv.NewReader(r)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	// Read header row
	headers, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil // Empty file
		}
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	// Build column index (case-insensitive)
	colIndex := make(map[string]int)
	for i, h := range headers {
		colIndex[strings.ToLower(strings.TrimSpace(h))] = i
	}

	// Verify content column exists
	// Resolve column name: use mapping if provided, otherwise try default name
	resolveCol := func(mapped, defaultName string) int {
		name := mapped
		if name == "" {
			name = defaultName
		}
		if idx, ok := colIndex[strings.ToLower(name)]; ok {
			return idx
		}
		return -1
	}

	contentIdx := resolveCol(mapping.Content, "content")
	if contentIdx < 0 {
		return nil, fmt.Errorf("content column %q not found in CSV headers: %v", mapping.Content, headers)
	}

	typeIdx := resolveCol(mapping.Type, "type")
	tsIdx := resolveCol(mapping.Timestamp, "timestamp")
	authorIdx := resolveCol(mapping.Author, "author")
	summaryIdx := resolveCol(mapping.Summary, "summary")
	sourceRefIdx := resolveCol(mapping.SourceRef, "source_ref")
	tagsIdx := resolveCol(mapping.Tags, "tags")

	now := time.Now()
	var insights []*types.Insight

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // Skip malformed rows
		}

		// Extract content
		if contentIdx >= len(record) {
			continue
		}
		content := strings.TrimSpace(record[contentIdx])
		if content == "" {
			continue
		}

		// Determine insight type
		insightType := DetectInsightType(content)
		if typeIdx >= 0 && typeIdx < len(record) {
			candidate := types.InsightType(strings.ToLower(strings.TrimSpace(record[typeIdx])))
			if candidate.IsValid() {
				insightType = candidate
			}
		}

		// Parse timestamp
		timestamp := now
		if tsIdx >= 0 && tsIdx < len(record) {
			if parsed, err := ParseTimestamp(strings.TrimSpace(record[tsIdx])); err == nil {
				timestamp = parsed
			}
		}

		// Author
		var authorID string
		if authorIdx >= 0 && authorIdx < len(record) {
			authorID = strings.TrimSpace(record[authorIdx])
		}

		// Summary
		summary := Truncate(content, 80)
		if summaryIdx >= 0 && summaryIdx < len(record) {
			if s := strings.TrimSpace(record[summaryIdx]); s != "" {
				summary = s
			}
		}

		// Source ref
		var sourceRef string
		if sourceRefIdx >= 0 && sourceRefIdx < len(record) {
			sourceRef = strings.TrimSpace(record[sourceRefIdx])
		}

		// Tags
		var tags []string
		if tagsIdx >= 0 && tagsIdx < len(record) {
			raw := strings.TrimSpace(record[tagsIdx])
			if raw != "" {
				for _, t := range strings.Split(raw, ",") {
					if tag := strings.TrimSpace(t); tag != "" {
						tags = append(tags, tag)
					}
				}
			}
		}

		insight := &types.Insight{
			ID:         types.GenerateID("ins"),
			Timestamp:  timestamp,
			Content:    content,
			Summary:    summary,
			Type:       insightType,
			Confidence: 0.7,
			Source: types.InsightSource{
				Type: sourceType,
				Ref:  sourceRef,
			},
			AuthorID:  authorID,
			Tags:      tags,
			CreatedAt: now,
		}

		insights = append(insights, insight)
	}

	return insights, nil
}
