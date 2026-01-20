package main

import (
	"fmt"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

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
	st, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()

	var threadID string
	if len(args) > 0 {
		threadID = args[0]
	}

	// Get pivot insights
	insights, err := st.ListInsights(threadID, types.InsightPivot, time.Time{})
	if err != nil {
		return fmt.Errorf("failed to list pivots: %w", err)
	}

	if len(insights) == 0 {
		if threadID != "" {
			fmt.Printf("No pivots found for thread %s\n", threadID)
		} else {
			fmt.Println("No pivots found")
		}
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

	// Sort insights by timestamp (oldest first for timeline view)
	reverseInsights(insights)

	// Print each pivot
	for _, insight := range insights {
		printInsightLine(st, insight)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(pivotsCmd)
}
