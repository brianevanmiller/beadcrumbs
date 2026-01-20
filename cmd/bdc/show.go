package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/store"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show details of an insight or thread",
	Long:  `Shows detailed information about an insight or thread based on the ID prefix (ins- or thr-).`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		// Determine if it's an insight or thread based on prefix
		if strings.HasPrefix(id, "ins-") {
			return showInsight(s, id)
		} else if strings.HasPrefix(id, "thr-") {
			return showThread(s, id)
		} else {
			return fmt.Errorf("invalid ID format: %s (expected ins-xxxx or thr-xxxx)", id)
		}
	},
}

func showInsight(s *store.Store, id string) error {
	// Get the insight
	insight, err := s.GetInsight(id)
	if err != nil {
		return fmt.Errorf("failed to get insight: %w", err)
	}

	// Display insight details
	fmt.Printf("Insight: %s\n", insight.ID)
	fmt.Printf("Type: %s\n", insight.Type)
	fmt.Printf("Timestamp: %s\n", insight.Timestamp.Format("2006-01-02 15:04:05"))
	fmt.Printf("Confidence: %.2f\n", insight.Confidence)
	if insight.ThreadID != "" {
		fmt.Printf("Thread: %s\n", insight.ThreadID)
	}
	fmt.Printf("Created: %s\n", insight.CreatedAt.Format("2006-01-02 15:04:05"))

	fmt.Printf("\nContent:\n%s\n", insight.Content)

	if insight.Summary != "" {
		fmt.Printf("\nSummary: %s\n", insight.Summary)
	}

	// Get dependencies from this insight
	depsFrom, err := s.GetDependencies(id)
	if err == nil && len(depsFrom) > 0 {
		fmt.Printf("\nDependencies (from this insight):\n")
		for _, dep := range depsFrom {
			fmt.Printf("  %s -> %s [%s]\n", dep.From, dep.To, dep.Type)
		}
	}

	// Get dependencies to this insight
	depsTo, err := s.GetDependents(id)
	if err == nil && len(depsTo) > 0 {
		fmt.Printf("\nDependents (to this insight):\n")
		for _, dep := range depsTo {
			fmt.Printf("  %s -> %s [%s]\n", dep.From, dep.To, dep.Type)
		}
	}

	return nil
}

func showThread(s *store.Store, id string) error {
	// Get the thread
	thread, err := s.GetThread(id)
	if err != nil {
		return fmt.Errorf("failed to get thread: %w", err)
	}

	// Display thread details
	fmt.Printf("Thread: %s\n", thread.ID)
	fmt.Printf("Title: %s\n", thread.Title)
	fmt.Printf("Status: %s\n", thread.Status)
	fmt.Printf("Created: %s\n", thread.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("Updated: %s\n", thread.UpdatedAt.Format("2006-01-02 15:04:05"))

	if thread.CurrentUnderstanding != "" {
		fmt.Printf("\nCurrent Understanding:\n%s\n", thread.CurrentUnderstanding)
	}

	// Get insights in this thread
	insights, err := s.ListInsights(id, "", time.Time{})
	if err != nil {
		return fmt.Errorf("failed to get insights: %w", err)
	}

	if len(insights) > 0 {
		fmt.Printf("\nInsights (%d):\n", len(insights))
		for _, insight := range insights {
			fmt.Printf("  %s [%s] %s\n", insight.ID, insight.Type, truncate(insight.Content, 60))
		}
	}

	return nil
}

func init() {
	rootCmd.AddCommand(showCmd)
}
