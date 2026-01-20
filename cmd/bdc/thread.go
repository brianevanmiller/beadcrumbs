package main

import (
	"fmt"

	"time"

	"github.com/spf13/cobra"
	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

var (
	threadStatus string
)

var threadCmd = &cobra.Command{
	Use:   "thread",
	Short: "Manage insight threads",
	Long:  `Create, view, and manage insight threads.`,
}

var threadNewCmd = &cobra.Command{
	Use:   "new <title>",
	Short: "Create a new thread",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := args[0]

		// Create the thread
		thread := types.NewThread(title)

		// Save to store
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		if err := s.CreateThread(thread); err != nil {
			return fmt.Errorf("failed to save thread: %w", err)
		}

		fmt.Printf("Created thread: %s\n", thread.ID)
		return nil
	},
}

var threadShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show thread details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		threadID := args[0]

		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		// Get the thread
		thread, err := s.GetThread(threadID)
		if err != nil {
			return fmt.Errorf("failed to get thread: %w", err)
		}
		if thread == nil {
			return fmt.Errorf("thread not found: %s", threadID)
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
		insights, err := s.ListInsights(threadID, "", time.Time{})
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
	},
}

var threadListCmd = &cobra.Command{
	Use:   "list",
	Short: "List threads",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		// Get threads
		var threads []*types.InsightThread
		var status types.ThreadStatus
		if threadStatus != "" {
			status = types.ThreadStatus(threadStatus)
		}
		threads, err = s.ListThreads(status)

		if err != nil {
			return fmt.Errorf("failed to get threads: %w", err)
		}

		if len(threads) == 0 {
			fmt.Println("No threads found")
			return nil
		}

		// Display threads
		for _, thread := range threads {
			fmt.Printf("%s [%s] %s\n", thread.ID, thread.Status, thread.Title)
		}

		return nil
	},
}

var threadCloseCmd = &cobra.Command{
	Use:   "close <id>",
	Short: "Close a thread",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		threadID := args[0]

		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		// Get the thread
		thread, err := s.GetThread(threadID)
		if err != nil {
			return fmt.Errorf("failed to get thread: %w", err)
		}
		if thread == nil {
			return fmt.Errorf("thread not found: %s", threadID)
		}

		// Determine the new status
		newStatus := types.ThreadConcluded
		if threadStatus != "" {
			newStatus = types.ThreadStatus(threadStatus)
			if newStatus != types.ThreadConcluded && newStatus != types.ThreadAbandoned {
				return fmt.Errorf("invalid status: %s. Use 'concluded' or 'abandoned'", threadStatus)
			}
		}

		// Update the thread status
		thread.Status = newStatus
		thread.UpdatedAt = time.Now()
		if err := s.UpdateThread(thread); err != nil {
			return fmt.Errorf("failed to update thread status: %w", err)
		}

		fmt.Printf("Thread %s closed with status: %s\n", threadID, newStatus)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(threadCmd)
	threadCmd.AddCommand(threadNewCmd)
	threadCmd.AddCommand(threadShowCmd)
	threadCmd.AddCommand(threadListCmd)
	threadCmd.AddCommand(threadCloseCmd)

	threadListCmd.Flags().StringVar(&threadStatus, "status", "", "filter by status (active|concluded|abandoned)")
	threadCloseCmd.Flags().StringVar(&threadStatus, "status", "concluded", "status to set (concluded|abandoned)")
}
