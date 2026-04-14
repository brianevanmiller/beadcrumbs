package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var pivotsOrigin string

var pivotsCmd = &cobra.Command{
	Use:   "pivots [thread-id]",
	Short: "Show pivot insights in timeline format",
	Long: `Display only pivot-type insights in chronological order.

Pivots represent moments when understanding changed direction or
a significant realization altered the approach.

Example:
  bdc pivots              # Show all pivots
  bdc pivots thr-7f2a     # Show pivots for specific thread`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPivots,
}

func runPivots(cmd *cobra.Command, args []string) error {
	st, err := getReadOnlyStore()
	if err != nil {
		return err
	}
	defer closeStore()

	var threadID string
	if len(args) > 0 {
		threadID = args[0]
	}

	// Get pivot insights
	insights, err := st.ListInsights(threadID, types.InsightPivot, time.Time{}, pivotsOrigin)
	if err != nil {
		return fmt.Errorf("failed to list pivots: %w", err)
	}

	if len(insights) == 0 {
		if jsonOutput {
			fmt.Println("[]")
			return nil
		}
		if threadID != "" {
			fmt.Printf("No pivots found for thread %s\n", threadID)
		} else {
			fmt.Println("No pivots found")
		}
		return nil
	}

	// Sort insights by timestamp (oldest first for timeline view)
	reverseInsights(insights)

	if jsonOutput {
		out, err := json.MarshalIndent(insights, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}

	// Print header if filtering by thread
	if threadID != "" {
		thread, err := st.GetThread(threadID)
		if err == nil {
			fmt.Printf("Thread: %s\n", thread.Title)
			fmt.Println()
		}
	}

	// Print each pivot
	for _, insight := range insights {
		printInsightLine(st, insight)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(pivotsCmd)
	pivotsCmd.Flags().StringVar(&pivotsOrigin, "origin", "", "filter by origin (exact match)")
}
