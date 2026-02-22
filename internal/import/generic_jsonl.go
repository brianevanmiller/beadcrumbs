package importer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// ParseGenericJSONL reads a JSONL file where each line is a JSON object with arbitrary keys,
// mapped to insight fields via ColumnMapping.
func ParseGenericJSONL(filePath string, mapping ColumnMapping, sourceType string) ([]*types.Insight, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open JSONL file: %w", err)
	}
	defer f.Close()

	return ParseGenericJSONLReader(f, mapping, sourceType)
}

// ParseGenericJSONLReader reads generic JSONL from an io.Reader.
func ParseGenericJSONLReader(r io.Reader, mapping ColumnMapping, sourceType string) ([]*types.Insight, error) {
	if sourceType == "" {
		sourceType = "jsonl"
	}

	contentKey := mapping.Content
	if contentKey == "" {
		contentKey = "content"
	}
	typeKey := mapping.Type
	if typeKey == "" {
		typeKey = "type"
	}
	tsKey := mapping.Timestamp
	if tsKey == "" {
		tsKey = "timestamp"
	}
	authorKey := mapping.Author
	if authorKey == "" {
		authorKey = "author"
	}
	summaryKey := mapping.Summary
	if summaryKey == "" {
		summaryKey = "summary"
	}
	sourceRefKey := mapping.SourceRef
	if sourceRefKey == "" {
		sourceRefKey = "source_ref"
	}
	tagsKey := mapping.Tags
	if tagsKey == "" {
		tagsKey = "tags"
	}

	now := time.Now()
	var insights []*types.Insight
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping malformed JSONL line %d: %v\n", lineNum, err)
			continue
		}

		// Extract content (required)
		content := getStringField(obj, contentKey)
		if content == "" {
			continue
		}

		// Determine insight type
		insightType := DetectInsightType(content)
		if typeStr := getStringField(obj, typeKey); typeStr != "" {
			candidate := types.InsightType(strings.ToLower(typeStr))
			if candidate.IsValid() {
				insightType = candidate
			}
		}

		// Parse timestamp
		timestamp := now
		if tsStr := getStringField(obj, tsKey); tsStr != "" {
			if parsed, err := ParseTimestamp(tsStr); err == nil {
				timestamp = parsed
			}
		}

		// Author
		authorID := getStringField(obj, authorKey)

		// Summary
		summary := Truncate(content, 80)
		if s := getStringField(obj, summaryKey); s != "" {
			summary = s
		}

		// Source ref
		sourceRef := getStringField(obj, sourceRefKey)

		// Tags
		var tags []string
		tagsRaw := getStringField(obj, tagsKey)
		if tagsRaw != "" {
			// If it's a JSON array string like ["a","b"], try parsing
			if strings.HasPrefix(tagsRaw, "[") {
				var arr []string
				if err := json.Unmarshal([]byte(tagsRaw), &arr); err == nil {
					tags = arr
				}
			}
			if tags == nil {
				for _, t := range strings.Split(tagsRaw, ",") {
					if tag := strings.TrimSpace(t); tag != "" {
						tags = append(tags, tag)
					}
				}
			}
		}
		// Also check if tags field is natively an array in the JSON
		if tags == nil {
			if rawVal, ok := obj[tagsKey]; ok {
				if arr, ok := rawVal.([]interface{}); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok {
							tags = append(tags, s)
						}
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

	if err := scanner.Err(); err != nil {
		return insights, fmt.Errorf("error reading JSONL: %w", err)
	}

	return insights, nil
}

// getStringField extracts a string value from a map, handling type conversion.
func getStringField(obj map[string]interface{}, key string) string {
	val, ok := obj[key]
	if !ok {
		// Try case-insensitive lookup
		lowerKey := strings.ToLower(key)
		for k, v := range obj {
			if strings.ToLower(k) == lowerKey {
				val = v
				ok = true
				break
			}
		}
		if !ok {
			return ""
		}
	}

	switch v := val.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%g", v)
	case bool:
		return fmt.Sprintf("%t", v)
	case nil:
		return ""
	default:
		// For complex types (arrays, objects), marshal to JSON string
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}
