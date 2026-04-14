package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var feedbackCmd = &cobra.Command{
	Use:   "feedback [thread-id]",
	Short: "Show feedback insights in timeline format",
	Long: `Display only feedback-type insights in chronological order.

Feedback represents external input received from others—code reviews,
user testing, stakeholder requests, AI critiques, or team discussions.

Examples:
  bdc feedback              # Show all feedback
  bdc feedback thr-7f2a     # Show feedback for specific thread
  bdc feedback --since 1w   # Show feedback from the last week`,
	Args: cobra.MaximumNArgs(1),
	RunE: runFeedback,
}

var feedbackSince string
var feedbackOrigin string

func runFeedback(cmd *cobra.Command, args []string) error {
	st, err := getReadOnlyStore()
	if err != nil {
		return err
	}
	defer closeStore()

	var threadID string
	if len(args) > 0 {
		threadID = args[0]
	}

	// Parse --since if provided
	var since time.Time
	if feedbackSince != "" {
		since, err = parseSince(feedbackSince)
		if err != nil {
			return fmt.Errorf("invalid --since value: %w", err)
		}
	}

	// Get feedback insights
	insights, err := st.ListInsights(threadID, types.InsightFeedback, since, feedbackOrigin)
	if err != nil {
		return fmt.Errorf("failed to list feedback: %w", err)
	}

	if len(insights) == 0 {
		if jsonOutput {
			fmt.Println("[]")
			return nil
		}
		if threadID != "" {
			fmt.Printf("No feedback found for thread %s\n", threadID)
		} else {
			fmt.Println("No feedback found")
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

	// Print each feedback
	for _, insight := range insights {
		printInsightLine(st, insight)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(feedbackCmd)
	feedbackCmd.Flags().StringVar(&feedbackSince, "since", "", "show feedback since (e.g., 1w, 2d, 3h)")
	feedbackCmd.Flags().StringVar(&feedbackOrigin, "origin", "", "filter by origin (exact match)")
}
