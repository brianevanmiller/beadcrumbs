// Package jsonl provides JSONL export/import functionality for beadcrumbs data structures.
package jsonl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// ExportInsights writes insights to a JSONL file (one JSON object per line).
func ExportInsights(insights []*types.Insight, filePath string) error {
	return writeJSONL(insights, filePath)
}

// ExportThreads writes insight threads to a JSONL file (one JSON object per line).
func ExportThreads(threads []*types.InsightThread, filePath string) error {
	return writeJSONL(threads, filePath)
}

// ExportDependencies writes dependencies to a JSONL file (one JSON object per line).
func ExportDependencies(deps []*types.Dependency, filePath string) error {
	return writeJSONL(deps, filePath)
}

// ImportInsights reads insights from a JSONL file.
func ImportInsights(filePath string) ([]*types.Insight, error) {
	items, err := readJSONL(filePath, func() interface{} {
		return &types.Insight{}
	})
	if err != nil {
		return nil, fmt.Errorf("failed to import insights: %w", err)
	}

	insights := make([]*types.Insight, len(items))
	for i, item := range items {
		insights[i] = item.(*types.Insight)
	}
	return insights, nil
}

// ImportThreads reads insight threads from a JSONL file.
func ImportThreads(filePath string) ([]*types.InsightThread, error) {
	items, err := readJSONL(filePath, func() interface{} {
		return &types.InsightThread{}
	})
	if err != nil {
		return nil, fmt.Errorf("failed to import threads: %w", err)
	}

	threads := make([]*types.InsightThread, len(items))
	for i, item := range items {
		threads[i] = item.(*types.InsightThread)
	}
	return threads, nil
}

// ImportDependencies reads dependencies from a JSONL file.
func ImportDependencies(filePath string) ([]*types.Dependency, error) {
	items, err := readJSONL(filePath, func() interface{} {
		return &types.Dependency{}
	})
	if err != nil {
		return nil, fmt.Errorf("failed to import dependencies: %w", err)
	}

	deps := make([]*types.Dependency, len(items))
	for i, item := range items {
		deps[i] = item.(*types.Dependency)
	}
	return deps, nil
}

// writeJSONL is a generic JSONL writer that writes a slice of items to a file,
// with one JSON object per line.
func writeJSONL(data interface{}, filePath string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filePath, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	encoder := json.NewEncoder(writer)

	// Handle slice types by using reflection-free type assertion patterns
	switch items := data.(type) {
	case []*types.Insight:
		for _, item := range items {
			if err := encoder.Encode(item); err != nil {
				return fmt.Errorf("failed to encode insight: %w", err)
			}
		}
	case []*types.InsightThread:
		for _, item := range items {
			if err := encoder.Encode(item); err != nil {
				return fmt.Errorf("failed to encode thread: %w", err)
			}
		}
	case []*types.Dependency:
		for _, item := range items {
			if err := encoder.Encode(item); err != nil {
				return fmt.Errorf("failed to encode dependency: %w", err)
			}
		}
	default:
		return fmt.Errorf("unsupported data type for JSONL export")
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush writer: %w", err)
	}

	return nil
}

// readJSONL is a generic JSONL reader that reads a file line by line,
// decoding each line as a JSON object using the provided factory function.
func readJSONL(filePath string, factory func() interface{}) ([]interface{}, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	var items []interface{}
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		// Skip empty lines
		if len(line) == 0 {
			continue
		}

		item := factory()
		if err := json.Unmarshal(line, item); err != nil {
			return nil, fmt.Errorf("failed to decode JSON at line %d: %w", lineNum, err)
		}
		items = append(items, item)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file %s: %w", filePath, err)
	}

	return items, nil
}
