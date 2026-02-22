package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	"github.com/brianevanmiller/beadcrumbs/internal/linear"
	"github.com/brianevanmiller/beadcrumbs/internal/store"
	"github.com/spf13/cobra"
)

// getLinearConfig reads Linear configuration with env var override.
// Precedence: BDC_LINEAR_API_KEY > LINEAR_API_KEY (env) > linear.api_key (config)
func getLinearConfig(s *store.Store) (configTool, configPath, apiKey string) {
	configTool, _ = s.GetConfig("linear.cli_tool")
	configPath, _ = s.GetConfig("linear.cli_path")
	apiKey, _ = s.GetConfig("linear.api_key")

	if envKey := os.Getenv("LINEAR_API_KEY"); envKey != "" {
		apiKey = envKey
	}
	if envKey := os.Getenv("BDC_LINEAR_API_KEY"); envKey != "" {
		apiKey = envKey
	}
	return
}

var linearCmd = &cobra.Command{
	Use:   "linear",
	Short: "Manage Linear integration",
	Long: `Commands for interacting with Linear issues from beadcrumbs.

Supported CLI tools (auto-detected):
  @schpet/linear-cli  (binary: linear)       — recommended
  linear-cli (Rust)   (binary: linear-cli)
  Linearis            (binary: linearis)`,
}

var linearSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Detect and configure Linear CLI integration",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		fmt.Println("Checking for Linear CLI tools...")

		apiKey, _ := s.GetConfig("linear.api_key")
		adapters := linear.DetectAll(apiKey)

		if len(adapters) == 0 {
			fmt.Println("\nNo Linear CLI detected. To enable Linear integration, install one:")
			fmt.Println("  brew install schpet/tap/linear        (recommended)")
			fmt.Println("  cargo install linear-cli              (Rust alternative)")
			fmt.Println("  npm install -g czottmann/linearis     (Node alternative)")
			fmt.Println("\nThen run: bdc linear setup")
			return nil
		}

		// Show all detected tools
		for _, a := range adapters {
			authStatus := "not authenticated"
			if err := a.CheckAuth(); err == nil {
				authStatus = "authenticated"
			}
			fmt.Printf("  Found: %s (%s) — %s\n", a.Name(), a.BinPath(), authStatus)
		}

		// Use the first detected tool as default
		chosen := adapters[0]
		if err := s.SetConfig("linear.cli_tool", chosen.Name()); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Printf("\nStored %s as default Linear tool.\n", chosen.Name())

		return nil
	},
}

var linearStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Linear integration status",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		// Show config
		configTool, configPath, apiKey := getLinearConfig(s)
		autoPush, _ := s.GetConfig("linear.auto_push")

		fmt.Println("Linear Integration Status")
		fmt.Println("─────────────────────────")

		// Config
		if configTool != "" {
			fmt.Printf("Configured tool: %s\n", configTool)
		} else {
			fmt.Println("Configured tool: (auto-detect)")
		}
		if configPath != "" {
			fmt.Printf("Binary path: %s\n", configPath)
		}
		if apiKey != "" {
			source := "config"
			if os.Getenv("BDC_LINEAR_API_KEY") != "" {
				source = "BDC_LINEAR_API_KEY env"
			} else if os.Getenv("LINEAR_API_KEY") != "" {
				source = "LINEAR_API_KEY env"
			}
			if len(apiKey) > 12 {
				fmt.Printf("API key: %s...%s (%s)\n", apiKey[:8], apiKey[len(apiKey)-4:], source)
			} else {
				fmt.Printf("API key: (set, %s)\n", source)
			}
		}
		if autoPush == "false" {
			fmt.Println("Auto-push on close: disabled")
		} else {
			fmt.Println("Auto-push on close: enabled (default)")
		}

		// Detection
		fmt.Println()
		adapters := linear.DetectAll(apiKey)
		if len(adapters) == 0 {
			fmt.Println("No Linear CLI tools detected on PATH.")
		} else {
			fmt.Println("Detected CLI tools:")
			for _, a := range adapters {
				authStatus := "not authenticated"
				if err := a.CheckAuth(); err == nil {
					authStatus = "authenticated"
				}
				fmt.Printf("  %s (%s) — %s\n", a.Name(), a.BinPath(), authStatus)
			}
		}

		// Linked threads
		fmt.Println()
		threads, _ := listLinearLinkedThreads(s)
		if len(threads) == 0 {
			fmt.Println("No threads linked to Linear issues.")
		} else {
			fmt.Println("Linked threads:")
			for _, t := range threads {
				fmt.Printf("  %s → %s  [%s]\n", t.mapping.ExternalID, t.thread.ID, t.thread.Status)
			}
		}

		return nil
	},
}

type linkedThread struct {
	thread  *store.ThreadSummary
	mapping *store.ExternalRefMapping
}

// listLinearLinkedThreads finds all threads linked to Linear issues.
func listLinearLinkedThreads(s *store.Store) ([]linkedThread, error) {
	// Query all linear mappings
	rows, err := s.DB().Query(`
		SELECT m.external_ref, m.thread_id, m.system, m.external_id, m.metadata, m.created_at, m.updated_at,
		       t.title, t.status
		FROM external_ref_mappings m
		JOIN threads t ON t.id = m.thread_id
		WHERE m.system = 'linear'
		ORDER BY m.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []linkedThread
	for rows.Next() {
		var m store.ExternalRefMapping
		var ts store.ThreadSummary
		if err := rows.Scan(&m.ExternalRef, &m.ThreadID, &m.System, &m.ExternalID, &m.Metadata, &m.CreatedAt, &m.UpdatedAt,
			&ts.Title, &ts.Status); err != nil {
			return nil, err
		}
		ts.ID = m.ThreadID
		result = append(result, linkedThread{thread: &ts, mapping: &m})
	}
	return result, rows.Err()
}

var linearPushCmd = &cobra.Command{
	Use:   "push <thread-id>",
	Short: "Push thread summary to linked Linear issue",
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

		var linearMapping *store.ExternalRefMapping
		for _, m := range mappings {
			if m.System == "linear" {
				linearMapping = m
				break
			}
		}
		if linearMapping == nil {
			return fmt.Errorf("thread %s is not linked to a Linear issue", threadID)
		}

		configTool, configPath, apiKey := getLinearConfig(s)

		adapter, err := linear.Detect(configTool, configPath, apiKey)
		if err != nil {
			return fmt.Errorf("linear CLI not available: %w", err)
		}

		insights, err := s.ListInsights(threadID, "", time.Time{})
		if err != nil {
			return fmt.Errorf("failed to get insights: %w", err)
		}
		if len(insights) == 0 {
			return fmt.Errorf("no insights in thread %s", threadID)
		}

		body := formatLinearSummary(thread, insights)
		if err := adapter.AddComment(linearMapping.ExternalID, body); err != nil {
			return fmt.Errorf("failed to post comment: %w", err)
		}

		fmt.Printf("Posted summary to Linear %s\n", linearMapping.ExternalID)
		return nil
	},
}

var linearLinkCmd = &cobra.Command{
	Use:   "link <thread-id> <issue-id>",
	Short: "Link an existing thread to a Linear issue",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		threadID := args[0]
		issueID := args[1]

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
		ref := issueID
		if !strings.Contains(ref, ":") {
			ref = "linear:" + ref
		}

		extRef, err := beads.ParseExternalRef(ref)
		if err != nil {
			return fmt.Errorf("invalid Linear reference: %w", err)
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

		fmt.Printf("Linked thread %s to Linear %s\n", threadID, extRef.ID)
		return nil
	},
}

var linearConfigCmd = &cobra.Command{
	Use:   "config <key> [value]",
	Short: "Get or set Linear integration config",
	Long: `Get or set Linear integration configuration.

Keys:
  cli_tool    Which CLI adapter to use (schpet, finesssee, linearis)
  cli_path    Override binary path (skips detection)
  api_key     API key passed as LINEAR_API_KEY env var to the CLI
  auto_push   Post summary comment on thread close (true/false)

API key precedence (highest wins):
  1. BDC_LINEAR_API_KEY env var
  2. LINEAR_API_KEY env var
  3. linear.api_key config (this command)

Config is per-repository. Each project can have its own API key.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		key := "linear." + args[0]

		if len(args) == 1 {
			// Get
			value, err := s.GetConfig(key)
			if err != nil {
				return err
			}
			if value == "" {
				fmt.Printf("%s: (not set)\n", args[0])
			} else {
				// Mask API key for display
				if args[0] == "api_key" && len(value) > 12 {
					fmt.Printf("%s: %s...%s\n", args[0], value[:8], value[len(value)-4:])
				} else {
					fmt.Printf("%s: %s\n", args[0], value)
				}
			}
		} else {
			// Set
			if err := s.SetConfig(key, args[1]); err != nil {
				return err
			}
			if args[0] == "api_key" {
				fmt.Printf("Set %s\n", args[0])
			} else {
				fmt.Printf("Set %s = %s\n", args[0], args[1])
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(linearCmd)
	linearCmd.AddCommand(linearSetupCmd)
	linearCmd.AddCommand(linearStatusCmd)
	linearCmd.AddCommand(linearPushCmd)
	linearCmd.AddCommand(linearLinkCmd)
	linearCmd.AddCommand(linearConfigCmd)
}
