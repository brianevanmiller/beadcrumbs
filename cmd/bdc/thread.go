package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	gh "github.com/brianevanmiller/beadcrumbs/internal/github"
	"github.com/brianevanmiller/beadcrumbs/internal/linear"
	"github.com/brianevanmiller/beadcrumbs/internal/store"
	"github.com/brianevanmiller/beadcrumbs/internal/summary"
	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var (
	threadStatus    string
	threadLinearRef string
	threadBeadRef   string
	threadGitHubRef string
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

		// Link to Linear issue if --linear flag is set
		if threadLinearRef != "" {
			if err := linkThreadToLinear(s, thread, threadLinearRef); err != nil {
				return err
			}
		}

		// Link to bead if --bead flag is set
		if threadBeadRef != "" {
			if err := linkThreadToBead(s, thread, threadBeadRef); err != nil {
				return err
			}
		}

		// Link to GitHub PR if --github flag is set
		if threadGitHubRef != "" {
			if err := linkThreadToGitHub(s, thread, threadGitHubRef); err != nil {
				return err
			}
		}

		return nil
	},
}

// linkThreadToLinear links a thread to a Linear issue and optionally enriches the title.
func linkThreadToLinear(s *store.Store, thread *types.InsightThread, linearRef string) error {
	// Normalize: accept both "ENG-456" and "linear:ENG-456"
	ref := linearRef
	if !strings.Contains(ref, ":") {
		ref = "linear:" + ref
	}

	extRef, err := beads.ParseExternalRef(ref)
	if err != nil {
		return fmt.Errorf("invalid Linear reference: %w", err)
	}

	// Try to fetch issue title from Linear CLI to enrich thread title
	configTool, configPath, apiKey := getLinearConfig(s)

	adapter, adapterErr := linear.Detect(configTool, configPath, apiKey)
	if adapterErr == nil {
		issue, fetchErr := adapter.ViewIssue(extRef.ID)
		if fetchErr == nil && issue.Title != "" {
			// Enrich thread title with issue title
			thread.Title = fmt.Sprintf("%s: %s", issue.ID, issue.Title)
			thread.UpdatedAt = time.Now()
			s.UpdateThread(thread)
			fmt.Printf("  Title: %s\n", thread.Title)
		}
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
		return fmt.Errorf("failed to link Linear issue: %w", err)
	}
	fmt.Printf("  Linked to Linear: %s\n", extRef.ID)
	return nil
}

// linkThreadToBead links a thread to a bead via an external ref mapping.
func linkThreadToBead(s *store.Store, thread *types.InsightThread, beadRef string) error {
	if !beads.IsBeadID(beadRef) {
		return fmt.Errorf("invalid bead reference: %s (expected bd-xxx or bead-xxx)", beadRef)
	}
	ref := beads.BeadIDToExternalRef(beadRef)

	extRef, err := beads.ParseExternalRef(ref)
	if err != nil {
		return fmt.Errorf("invalid bead reference: %w", err)
	}

	// Check for existing mapping
	existing, _ := s.GetExternalRefMappingByRef(ref)
	if existing != nil {
		if existing.ThreadID == thread.ID {
			fmt.Printf("  Already linked to %s\n", beads.FormatExternalRef(extRef))
			return nil
		}
		return fmt.Errorf("reference %s is already linked to thread %s", ref, existing.ThreadID)
	}

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
		return fmt.Errorf("failed to link bead: %w", err)
	}
	fmt.Printf("  Linked to %s\n", beads.FormatExternalRef(extRef))
	return nil
}

// linkThreadToGitHub links a thread to a GitHub PR and optionally enriches the title.
func linkThreadToGitHub(s *store.Store, thread *types.InsightThread, githubRef string) error {
	// Normalize: accept "owner/repo#42", "github:owner/repo#42", or "gh:owner/repo#42"
	ref := githubRef
	if !strings.Contains(ref, ":") {
		ref = "github:" + ref
	}

	extRef, err := beads.ParseExternalRef(ref)
	if err != nil {
		return fmt.Errorf("invalid GitHub reference: %w", err)
	}

	// Try to fetch PR title from gh CLI to enrich thread title
	ghCli, ghErr := gh.Detect()
	if ghErr == nil {
		repo, prNumber := parseGitHubPRRef(extRef.ID)
		if prNumber > 0 {
			pr, fetchErr := ghCli.ViewPR(fmt.Sprintf("%d", prNumber), repo)
			if fetchErr == nil && pr != nil && pr.Title != "" {
				thread.Title = fmt.Sprintf("%s#%d: %s", repo, prNumber, pr.Title)
				thread.UpdatedAt = time.Now()
				s.UpdateThread(thread)
				fmt.Printf("  Title: %s\n", thread.Title)
			}
		}
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
		return fmt.Errorf("failed to link GitHub PR: %w", err)
	}
	fmt.Printf("  Linked to GitHub: %s\n", extRef.ID)
	return nil
}

// parseGitHubPRRef parses "owner/repo#42" into repo and PR number.
func parseGitHubPRRef(ref string) (repo string, number int) {
	parts := strings.SplitN(ref, "#", 2)
	if len(parts) != 2 {
		return "", 0
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return parts[0], 0
	}
	return parts[0], n
}

var threadLinkCmd = &cobra.Command{
	Use:   "link <thread-id> <ref>",
	Short: "Link a thread to an external reference",
	Long: `Link an existing thread to an external tracker reference.

Accepts any external ref format:
  linear:ENG-456       Linear issue
  bd-abc1 or bead:abc1 Beads task
  github:owner/repo#42 GitHub issue
  jira:PROJ-123        Jira issue
  notion:page-id       Notion page

Examples:
  bdc thread link thr-xxxx linear:ENG-456
  bdc thread link thr-xxxx bd-abc1
  bdc thread link thr-xxxx github:myorg/myrepo#42`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		threadID := args[0]
		ref := args[1]

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

		// Normalize: if it looks like a bead ID, convert to external ref format
		if beads.IsBeadID(ref) {
			ref = beads.BeadIDToExternalRef(ref)
		}

		extRef, err := beads.ParseExternalRef(ref)
		if err != nil {
			return fmt.Errorf("invalid reference: %w", err)
		}

		// Check for existing mapping
		existing, _ := s.GetExternalRefMappingByRef(ref)
		if existing != nil {
			if existing.ThreadID == threadID {
				fmt.Printf("Thread %s is already linked to %s\n", threadID, beads.FormatExternalRef(extRef))
				return nil
			}
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

		fmt.Printf("Linked thread %s to %s\n", threadID, beads.FormatExternalRef(extRef))
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

		// Show linked external refs
		mappings, _ := s.GetExternalRefMappingsByThread(threadID)
		for _, m := range mappings {
			fmt.Printf("Linked: %s (%s)\n", m.ExternalID, m.System)
		}

		if thread.CurrentUnderstanding != "" {
			fmt.Printf("\nCurrent Understanding:\n%s\n", thread.CurrentUnderstanding)
		}

		// Get insights in this thread
		insights, err := s.ListInsights(threadID, "", time.Time{}, "")
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

		// Push summaries to integrations on conclude
		if newStatus == types.ThreadConcluded {
			pushLinearSummaryOnClose(s, thread)
			pushGitHubSummaryOnClose(s, thread)
		}

		return nil
	},
}

// pushLinearSummaryOnClose posts a summary comment to the linked Linear issue.
func pushLinearSummaryOnClose(s *store.Store, thread *types.InsightThread) {
	// Check if auto-push is disabled
	autoPush, _ := s.GetConfig("linear.auto_push")
	if autoPush == "false" {
		return
	}

	// Get Linear mappings for this thread
	mappings, err := s.GetExternalRefMappingsByThread(thread.ID)
	if err != nil || len(mappings) == 0 {
		return
	}

	// Find Linear mapping
	var linearMapping *store.ExternalRefMapping
	for _, m := range mappings {
		if m.System == "linear" {
			linearMapping = m
			break
		}
	}
	if linearMapping == nil {
		return
	}

	// Get adapter
	configTool, configPath, apiKey := getLinearConfig(s)

	adapter, err := linear.Detect(configTool, configPath, apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Linear CLI not available, skipping comment push.\n")
		return
	}

	// Gather insights
	insights, err := s.ListInsights(thread.ID, "", time.Time{}, "")
	if err != nil || len(insights) == 0 {
		return
	}

	// Format and post
	body := summary.FormatSummary(thread, insights)
	if err := adapter.AddComment(linearMapping.ExternalID, body); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to post summary to Linear %s: %v\n", linearMapping.ExternalID, err)
		return
	}

	fmt.Printf("Posted insight summary to Linear %s\n", linearMapping.ExternalID)
}

// pushGitHubSummaryOnClose posts a summary comment to the linked GitHub PR.
func pushGitHubSummaryOnClose(s *store.Store, thread *types.InsightThread) {
	// Check if auto-push is enabled (opt-in, defaults to false)
	autoPush, _ := s.GetConfig("github.auto_push")
	if autoPush != "true" {
		return
	}

	// Detect gh CLI early — needed for both auto-detect and posting
	ghCli, err := gh.Detect()
	if err != nil {
		return // gh not installed, silently skip
	}

	// Get mappings for this thread
	mappings, err := s.GetExternalRefMappingsByThread(thread.ID)
	if err != nil {
		return
	}

	// Find explicit GitHub mapping
	var ghMapping *store.ExternalRefMapping
	for _, m := range mappings {
		if m.System == "github" {
			ghMapping = m
			break
		}
	}

	var prRepo string
	var prNumber int

	if ghMapping != nil {
		// Parse "owner/repo#42" from ExternalID
		prRepo, prNumber = parseGitHubPRRef(ghMapping.ExternalID)
	} else {
		// Auto-detect PR from current branch if not explicitly disabled
		autoDetect, _ := s.GetConfig("github.auto_detect")
		if autoDetect == "false" {
			return
		}

		pr, err := ghCli.CurrentBranchPR()
		if err != nil || pr == nil {
			return // no PR for current branch, silently skip
		}
		prRepo = pr.Repo
		prNumber = pr.Number

		// Persist the auto-detected mapping for traceability (only if repo info available)
		if pr.Repo != "" {
			ref := fmt.Sprintf("github:%s#%d", pr.Repo, pr.Number)
			now := time.Now()
			mapping := &store.ExternalRefMapping{
				ExternalRef: ref,
				ThreadID:    thread.ID,
				System:      "github",
				ExternalID:  fmt.Sprintf("%s#%d", pr.Repo, pr.Number),
				Metadata:    `{"auto_detected":true}`,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			s.CreateExternalRefMapping(mapping) // Best-effort
		}
	}

	if prNumber == 0 {
		return
	}

	// Gather insights
	insights, err := s.ListInsights(thread.ID, "", time.Time{}, "")
	if err != nil || len(insights) == 0 {
		return
	}

	// Format and post
	body := summary.FormatSummary(thread, insights)
	if err := ghCli.AddComment(prRepo, prNumber, body); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to post summary to GitHub %s#%d: %v\n", prRepo, prNumber, err)
		return
	}

	fmt.Printf("Posted insight summary to GitHub %s#%d\n", prRepo, prNumber)
}

func init() {
	rootCmd.AddCommand(threadCmd)
	threadCmd.AddCommand(threadNewCmd)
	threadCmd.AddCommand(threadShowCmd)
	threadCmd.AddCommand(threadListCmd)
	threadCmd.AddCommand(threadCloseCmd)
	threadCmd.AddCommand(threadLinkCmd)

	threadNewCmd.Flags().StringVar(&threadLinearRef, "linear", "", "link to Linear issue (e.g., ENG-456)")
	threadNewCmd.Flags().StringVar(&threadBeadRef, "bead", "", "link to bead task (e.g., bd-abc1)")
	threadNewCmd.Flags().StringVar(&threadGitHubRef, "github", "", "link to GitHub PR (e.g., owner/repo#42)")
	threadListCmd.Flags().StringVar(&threadStatus, "status", "", "filter by status (active|concluded|abandoned)")
	threadCloseCmd.Flags().StringVar(&threadStatus, "status", "concluded", "status to set (concluded|abandoned)")
}
