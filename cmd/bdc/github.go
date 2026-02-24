package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	gh "github.com/brianevanmiller/beadcrumbs/internal/github"
	"github.com/brianevanmiller/beadcrumbs/internal/store"
	"github.com/brianevanmiller/beadcrumbs/internal/summary"
	"github.com/spf13/cobra"
)

var githubCmd = &cobra.Command{
	Use:   "github",
	Short: "Manage GitHub PR integration",
	Long: `Commands for posting beadcrumbs summaries to GitHub pull requests.

Requires the GitHub CLI (gh) to be installed and authenticated.
Install: https://cli.github.com

Quick start:
  bdc github setup                    # Detect gh CLI and check auth
  bdc github config auto_push true    # Enable auto-posting on thread close
  bdc thread new "My work" --github owner/repo#42  # Link thread to PR`,
}

var githubSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Detect and verify GitHub CLI integration",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Checking for GitHub CLI (gh)...")

		ghCli, err := gh.Detect()
		if err != nil {
			fmt.Println("\n  gh CLI not found on PATH.")
			fmt.Println("  Install: https://cli.github.com")
			fmt.Println("  Then run: bdc github setup")
			return nil
		}

		fmt.Printf("  Found: gh (%s)\n", ghCli.BinPath())

		authStatus := "not authenticated"
		if err := ghCli.CheckAuth(); err == nil {
			authStatus = "authenticated"
		}
		fmt.Printf("  Status: %s\n", authStatus)

		if authStatus == "not authenticated" {
			fmt.Println("\n  Run: gh auth login")
			fmt.Println("  Then run: bdc github setup")
			return nil
		}

		fmt.Println("\n  GitHub CLI is ready.")
		fmt.Println("  To enable auto-posting summaries to PRs on thread close:")
		fmt.Println("    bdc github config auto_push true")
		return nil
	},
}

var githubStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show GitHub integration status",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		autoPush, _ := s.GetConfig("github.auto_push")
		autoDetect, _ := s.GetConfig("github.auto_detect")

		fmt.Println("GitHub Integration Status")
		fmt.Println("\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

		// Config
		if autoPush == "true" {
			fmt.Println("Auto-push on close: enabled")
		} else {
			fmt.Println("Auto-push on close: disabled (default)")
		}
		if autoDetect == "false" {
			fmt.Println("Auto-detect PR: disabled")
		} else {
			fmt.Println("Auto-detect PR: enabled (default)")
		}

		// Detection
		fmt.Println()
		ghCli, err := gh.Detect()
		if err != nil {
			fmt.Println("gh CLI: not found")
			fmt.Println("  Install: https://cli.github.com")
		} else {
			authStatus := "not authenticated"
			if err := ghCli.CheckAuth(); err == nil {
				authStatus = "authenticated"
			}
			fmt.Printf("gh CLI: %s \u2014 %s\n", ghCli.BinPath(), authStatus)
		}

		// Linked threads
		fmt.Println()
		threads, _ := listGitHubLinkedThreads(s)
		if len(threads) == 0 {
			fmt.Println("No threads linked to GitHub PRs.")
		} else {
			fmt.Println("Linked threads:")
			for _, t := range threads {
				fmt.Printf("  %s \u2192 %s  [%s]\n", t.mapping.ExternalID, t.thread.ID, t.thread.Status)
			}
		}

		return nil
	},
}

type ghLinkedThread struct {
	thread  *store.ThreadSummary
	mapping *store.ExternalRefMapping
}

// listGitHubLinkedThreads finds all threads linked to GitHub PRs.
func listGitHubLinkedThreads(s *store.Store) ([]ghLinkedThread, error) {
	rows, err := s.DB().Query(`
		SELECT m.external_ref, m.thread_id, m.system, m.external_id, m.metadata, m.created_at, m.updated_at,
		       t.title, t.status
		FROM external_ref_mappings m
		JOIN threads t ON t.id = m.thread_id
		WHERE m.system = 'github'
		ORDER BY m.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ghLinkedThread
	for rows.Next() {
		var m store.ExternalRefMapping
		var ts store.ThreadSummary
		if err := rows.Scan(&m.ExternalRef, &m.ThreadID, &m.System, &m.ExternalID, &m.Metadata, &m.CreatedAt, &m.UpdatedAt,
			&ts.Title, &ts.Status); err != nil {
			return nil, err
		}
		ts.ID = m.ThreadID
		result = append(result, ghLinkedThread{thread: &ts, mapping: &m})
	}
	return result, rows.Err()
}

var githubPushCmd = &cobra.Command{
	Use:   "push <thread-id>",
	Short: "Push thread summary to linked GitHub PR",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		threadID := args[0]

		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		thread, err := s.GetThread(threadID)
		if err != nil {
			return fmt.Errorf("failed to get thread: %w", err)
		}
		if thread == nil {
			return fmt.Errorf("thread not found: %s", threadID)
		}

		mappings, err := s.GetExternalRefMappingsByThread(threadID)
		if err != nil {
			return fmt.Errorf("failed to get mappings: %w", err)
		}

		var ghMapping *store.ExternalRefMapping
		for _, m := range mappings {
			if m.System == "github" {
				ghMapping = m
				break
			}
		}
		if ghMapping == nil {
			return fmt.Errorf("thread %s is not linked to a GitHub PR", threadID)
		}

		ghCli, err := gh.Detect()
		if err != nil {
			return fmt.Errorf("gh CLI not available: %w", err)
		}

		insights, err := s.ListInsights(threadID, "", time.Time{}, "")
		if err != nil {
			return fmt.Errorf("failed to get insights: %w", err)
		}
		if len(insights) == 0 {
			return fmt.Errorf("no insights in thread %s", threadID)
		}

		repo, prNumber := parseGitHubPRRef(ghMapping.ExternalID)
		if prNumber == 0 {
			return fmt.Errorf("invalid PR reference: %s", ghMapping.ExternalID)
		}

		body := summary.FormatSummary(thread, insights)
		if err := ghCli.AddComment(repo, prNumber, body); err != nil {
			return fmt.Errorf("failed to post comment: %w", err)
		}

		fmt.Printf("Posted summary to GitHub %s#%d\n", repo, prNumber)
		return nil
	},
}

var githubLinkCmd = &cobra.Command{
	Use:   "link <thread-id> <owner/repo#number>",
	Short: "Link an existing thread to a GitHub PR",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		threadID := args[0]
		prRef := args[1]

		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		// Verify thread exists
		thread, err := s.GetThread(threadID)
		if err != nil {
			return fmt.Errorf("failed to get thread: %w", err)
		}
		if thread == nil {
			return fmt.Errorf("thread not found: %s", threadID)
		}

		// Normalize ref
		ref := prRef
		if !strings.Contains(ref, ":") {
			ref = "github:" + ref
		}

		extRef, err := beads.ParseExternalRef(ref)
		if err != nil {
			return fmt.Errorf("invalid GitHub reference: %w", err)
		}

		// Check for existing mapping
		existing, _ := s.GetExternalRefMappingByRef(ref)
		if existing != nil {
			return fmt.Errorf("reference %s is already linked to thread %s", ref, existing.ThreadID)
		}

		// Create the mapping
		now := time.Now()
		mapping := &store.ExternalRefMapping{
			ExternalRef: ref,
			ThreadID:    thread.ID,
			System:      extRef.System,
			ExternalID:  extRef.ID,
			Metadata:    "{}",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.CreateExternalRefMapping(mapping); err != nil {
			return fmt.Errorf("failed to create mapping: %w", err)
		}

		fmt.Printf("Linked thread %s to GitHub %s\n", threadID, extRef.ID)
		return nil
	},
}

var githubConfigCmd = &cobra.Command{
	Use:   "config <key> [value]",
	Short: "Get or set GitHub integration config",
	Long: `Get or set GitHub integration configuration.

Keys:
  auto_push     Post summary comment on thread close (true/false, default: false)
  auto_detect   Auto-detect PR from current branch (true/false, default: true)

Config is per-repository.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		key := "github." + args[0]

		if len(args) == 1 {
			// Get
			value, err := s.GetConfig(key)
			if err != nil {
				return err
			}
			if value == "" {
				fmt.Printf("%s: (not set)\n", args[0])
			} else {
				fmt.Printf("%s: %s\n", args[0], value)
			}
		} else {
			// Set
			if err := s.SetConfig(key, args[1]); err != nil {
				return err
			}
			fmt.Printf("Set %s = %s\n", args[0], args[1])
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(githubCmd)
	githubCmd.AddCommand(githubSetupCmd)
	githubCmd.AddCommand(githubStatusCmd)
	githubCmd.AddCommand(githubPushCmd)
	githubCmd.AddCommand(githubLinkCmd)
	githubCmd.AddCommand(githubConfigCmd)
}
