package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	importer "github.com/brianevanmiller/beadcrumbs/internal/import"
	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import <file-or-directory>",
	Short: "Import insights from a file or directory",
	Long: `Import insights from various sources:
  - AI session transcripts (text files with conversation)
  - Slack exports (directory with JSON files)

The format is auto-detected, or you can specify it with flags.

Examples:
  bdc import session.txt                          # Auto-detect format
  bdc import session.txt --ai-session             # Force AI session format
  bdc import slack-export/ --slack                # Import Slack export
  bdc import session.txt --thread=thr-xxx         # Add to existing thread
  bdc import session.txt --timestamp="2024-01-15" # Set timestamp for all insights
  bdc import session.txt --dry-run                # Preview without saving`,
	Args: cobra.ExactArgs(1),
	RunE: runImport,
}

var (
	importThread    string
	importAISession bool
	importSlack     bool
	importDryRun    bool
	importTimestamp string
	importAuto      bool
	importQuiet     bool
)

func init() {
	importCmd.Flags().StringVar(&importThread, "thread", "", "add imported insights to this thread")
	importCmd.Flags().BoolVar(&importAISession, "ai-session", false, "force AI session format")
	importCmd.Flags().BoolVar(&importSlack, "slack", false, "force Slack export format")
	importCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "preview without saving")
	importCmd.Flags().StringVar(&importTimestamp, "timestamp", "", "set timestamp for all insights (RFC3339 or date)")
	importCmd.Flags().BoolVar(&importAuto, "auto", false, "auto-import from JSONL (for git hooks)")
	importCmd.Flags().BoolVar(&importQuiet, "quiet", false, "suppress output (for hooks)")
	rootCmd.AddCommand(importCmd)
}

func runImport(cmd *cobra.Command, args []string) error {
	path := args[0]

	// Parse timestamp if provided
	var baseTimestamp time.Time
	if importTimestamp != "" {
		var err error
		baseTimestamp, err = parseImportTimestamp(importTimestamp)
		if err != nil {
			return fmt.Errorf("invalid timestamp: %w", err)
		}
	}

	// Determine format
	format := detectFormat(path)
	if importAISession {
		format = "ai-session"
	} else if importSlack {
		format = "slack"
	}

	// Parse insights
	var insights []*types.Insight
	var err error

	switch format {
	case "ai-session":
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("failed to read file: %w", readErr)
		}

		// Try conversation format first, fall back to plain
		// Use timestamp-aware functions
		insights, err = importer.ParseConversationWithTimestamp(string(content), baseTimestamp)
		if err != nil || len(insights) == 0 {
			insights, err = importer.ParseAISessionWithTimestamp(string(content), baseTimestamp)
		}

	case "slack":
		info, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("failed to stat path: %w", statErr)
		}

		if info.IsDir() {
			insights, err = importer.ParseSlackExport(path)
		} else {
			// Single JSON file
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("failed to read file: %w", readErr)
			}
			insights, err = importer.ParseSlackJSON(content)
		}

		// Note: Slack imports preserve their original timestamps automatically
		// The --timestamp flag is only used to override if explicitly provided
		if !baseTimestamp.IsZero() {
			for _, insight := range insights {
				insight.Timestamp = baseTimestamp
			}
		}

	default:
		return fmt.Errorf("unknown format: %s (use --ai-session or --slack)", format)
	}

	if err != nil {
		return fmt.Errorf("failed to parse: %w", err)
	}

	if len(insights) == 0 {
		if !importQuiet {
			fmt.Println("No insights extracted from the input.")
		}
		return nil
	}

	// Set thread if specified
	if importThread != "" {
		for _, insight := range insights {
			insight.ThreadID = importThread
		}
	}

	// Print extracted insights (unless quiet)
	if !importQuiet {
		fmt.Printf("Extracted %d insights:\n\n", len(insights))
		for i, insight := range insights {
			symbol := getInsightSymbol(insight.Type)
			typeStr := string(insight.Type)
			if insight.Type == types.InsightPivot || insight.Type == types.InsightDecision {
				typeStr = strings.ToUpper(typeStr)
			}

			timestampStr := ""
			if !baseTimestamp.IsZero() {
				timestampStr = fmt.Sprintf(" @ %s", insight.Timestamp.Format("2006-01-02"))
			}

			fmt.Printf("  %d. %s \"%s\" [%s]%s\n", i+1, symbol, truncateContent(insight.Content, 60), typeStr, timestampStr)
		}
		fmt.Println()
	}

	// Save if not dry run
	if importDryRun {
		if !importQuiet {
			fmt.Println("Dry run - no insights saved.")
		}
		return nil
	}

	// Get store
	s, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()

	// Verify thread exists if specified
	if importThread != "" {
		_, err := s.GetThread(importThread)
		if err != nil {
			return fmt.Errorf("thread %s not found: %w", importThread, err)
		}
	}

	// Save insights
	saved := 0
	for _, insight := range insights {
		if err := s.CreateInsight(insight); err != nil {
			if !importQuiet {
				fmt.Printf("Warning: failed to save insight: %v\n", err)
			}
			continue
		}
		saved++
	}

	if !importQuiet {
		fmt.Printf("Saved %d insights.\n", saved)
	}
	return nil
}

// detectFormat tries to determine the format from the path.
func detectFormat(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "unknown"
	}

	// Directory = Slack export
	if info.IsDir() {
		return "slack"
	}

	// Check extension
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return "slack"
	case ".txt", ".md", ".log":
		return "ai-session"
	default:
		// Try to peek at content
		content, err := os.ReadFile(path)
		if err != nil {
			return "unknown"
		}

		// If it starts with [ or {, probably JSON
		trimmed := strings.TrimSpace(string(content))
		if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
			return "slack"
		}

		return "ai-session"
	}
}

func truncateContent(s string, maxLen int) string {
	// Replace newlines with spaces
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")

	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// parseImportTimestamp parses various timestamp formats.
func parseImportTimestamp(ts string) (time.Time, error) {
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t, nil
	}

	// Try common formats
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"Jan 2, 2006",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, ts); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognized timestamp format: %s", ts)
}
