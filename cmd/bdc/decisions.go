package main

import (
	"fmt"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var decisionsCmd = &cobra.Command{
	Use:   "decisions [thread-id]",
	Short: "Show decision insights in timeline format",
	Long: `Display only decision-type insights in chronological order.

Decisions represent moments when a commitment was made to a
specific approach or solution.

Example:
  bdc decisions              # Show all decisions
  bdc decisions thr-7f2a     # Show decisions for specific thread`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDecisions,
}

func runDecisions(cmd *cobra.Command, args []string) error {
	st, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()

	var threadID string
	if len(args) > 0 {
		threadID = args[0]
	}

	// Get decision insights
	insights, err := st.ListInsights(threadID, types.InsightDecision, time.Time{})
	if err != nil {
		return fmt.Errorf("failed to list decisions: %w", err)
	}

	if len(insights) == 0 {
		if threadID != "" {
			fmt.Printf("No decisions found for thread %s\n", threadID)
		} else {
			fmt.Println("No decisions found")
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

	// Print each decision
	for _, insight := range insights {
		printInsightLine(st, insight)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(decisionsCmd)
}
