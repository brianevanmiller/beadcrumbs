package jsonl_test

import (
	"fmt"
	"os"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/jsonl"
	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// This example demonstrates exporting and importing insights to/from JSONL format.
func ExampleExportInsights() {
	now := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	insights := []*types.Insight{
		{
			ID:         "ins-0001",
			Timestamp:  now,
			Content:    "First insight",
			Summary:    "Summary 1",
			Type:       types.InsightHypothesis,
			Confidence: 0.9,
			Source: types.InsightSource{
				Type:         "human",
				Participants: []string{"alice"},
			},
			Tags:      []string{"test"},
			CreatedAt: now,
		},
		{
			ID:         "ins-0002",
			Timestamp:  now.Add(time.Hour),
			Content:    "Second insight",
			Summary:    "Summary 2",
			Type:       types.InsightDiscovery,
			Confidence: 0.8,
			Source: types.InsightSource{
				Type: "ai-session",
			},
			CreatedAt: now.Add(time.Hour),
		},
	}

	tmpFile := "/tmp/example_insights.jsonl"
	if err := jsonl.ExportInsights(insights, tmpFile); err != nil {
		fmt.Printf("Error exporting: %v\n", err)
		return
	}

	// Read and display the file content
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return
	}

	fmt.Println(string(content))
	os.Remove(tmpFile)
}
