package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var (
	captureType       string
	captureThreadID   string
	captureHypothesis bool
	captureDiscovery  bool
	captureQuestion   bool
	captureFeedback   bool
	capturePivot      bool
	captureDecision   bool
	captureTimestamp  string
	captureAuthor     string
	captureEndorsedBy []string
)

var captureCmd = &cobra.Command{
	Use:   "capture <content>",
	Short: "Capture a new insight",
	Long: `Creates a new insight with the given content and saves it to the store.

The --thread flag is strongly encouraged to associate insights with their
context. It accepts:
  - Thread ID: thr-xxx
  - Bead ID: bd-xxx or bead-xxx (auto-creates/links thread)
  - External ref: linear:TASK-123, github:owner/repo#42

Examples:
  bdc capture --thread bd-a1b2 --decision "We'll use Redis for caching"
  bdc capture --thread linear:ENG-456 --pivot "Need to rethink the data model"
  bdc capture --hypothesis "The bug might be in the auth middleware" --author cc:opus-4.5`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		content := args[0]

		// Determine insight type from flags
		insightType, err := determineInsightType()
		if err != nil {
			return err
		}

		// Parse timestamp if provided
		var timestamp time.Time
		if captureTimestamp != "" {
			timestamp, err = parseTimestamp(captureTimestamp)
			if err != nil {
				return fmt.Errorf("invalid timestamp: %w", err)
			}
		}

		// Create the insight with optional timestamp
		var insight *types.Insight
		if !timestamp.IsZero() {
			insight = types.NewInsightWithTimestamp(content, insightType, timestamp)
		} else {
			insight = types.NewInsight(content, insightType)
		}

		// Set thread ID if provided
		if captureThreadID != "" {
			threadID, err := resolveThreadRef(captureThreadID)
			if err != nil {
				return fmt.Errorf("failed to resolve thread reference: %w", err)
			}
			insight.ThreadID = threadID
		}

		// Set endorsed-by if provided
		if len(captureEndorsedBy) > 0 {
			insight.EndorsedBy = captureEndorsedBy
		}

		// Save to store
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		// Set author if provided (stored directly as string)
		if captureAuthor != "" {
			insight.AuthorID = captureAuthor
			insight.CreatedBy = captureAuthor // Legacy field
		}

		if err := s.CreateInsight(insight); err != nil {
			return fmt.Errorf("failed to save insight: %w", err)
		}

		fmt.Printf("Created insight: %s\n", insight.ID)
		if insight.ThreadID != "" {
			fmt.Printf("  Thread: %s\n", insight.ThreadID)
		}
		if insight.AuthorID != "" {
			fmt.Printf("  Author: %s\n", insight.AuthorID)
		}
		if len(insight.EndorsedBy) > 0 {
			fmt.Printf("  Endorsed by: %s\n", strings.Join(insight.EndorsedBy, ", "))
		}
		return nil
	},
}

func determineInsightType() (types.InsightType, error) {
	// Count how many type flags are set
	count := 0
	var selectedType types.InsightType

	if captureHypothesis {
		count++
		selectedType = types.InsightHypothesis
	}
	if captureDiscovery {
		count++
		selectedType = types.InsightDiscovery
	}
	if captureQuestion {
		count++
		selectedType = types.InsightQuestion
	}
	if captureFeedback {
		count++
		selectedType = types.InsightFeedback
	}
	if capturePivot {
		count++
		selectedType = types.InsightPivot
	}
	if captureDecision {
		count++
		selectedType = types.InsightDecision
	}

	// If --type flag is set, use it
	if captureType != "" {
		if count > 0 {
			return "", fmt.Errorf("cannot use both --type and shorthand flags")
		}
		t := types.InsightType(captureType)
		if !t.IsValid() {
			return "", fmt.Errorf("invalid insight type: %s. Valid types: hypothesis, discovery, question, feedback, pivot, decision", captureType)
		}
		return t, nil
	}

	// If exactly one shorthand flag is set, use it
	if count == 1 {
		return selectedType, nil
	}

	// If multiple shorthand flags are set, error
	if count > 1 {
		return "", fmt.Errorf("only one type flag can be set")
	}

	// Default to discovery if no type is specified
	return types.InsightDiscovery, nil
}

// parseTimestamp parses a timestamp string in various formats.
// Supports RFC3339, common date formats, and relative time expressions.
func parseTimestamp(ts string) (time.Time, error) {
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t, nil
	}

	// Try RFC3339Nano
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t, nil
	}

	// Try common date-time formats
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"Jan 2, 2006 3:04 PM",
		"Jan 2, 2006",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, ts); err == nil {
			return t, nil
		}
	}

	// Try relative time expressions (e.g., "2h ago", "1d ago")
	if strings.HasSuffix(ts, " ago") {
		durationStr := strings.TrimSuffix(ts, " ago")
		// Handle day/week shortcuts
		durationStr = strings.ReplaceAll(durationStr, "d", "h")
		durationStr = strings.ReplaceAll(durationStr, "w", "h")

		// Parse as if it were hours for d/w
		if strings.Contains(ts, "d ago") {
			parts := strings.Split(strings.TrimSuffix(ts, " ago"), "d")
			if len(parts) > 0 {
				var days int
				if _, err := fmt.Sscanf(parts[0], "%d", &days); err == nil {
					return time.Now().Add(-time.Duration(days) * 24 * time.Hour), nil
				}
			}
		}
		if strings.Contains(ts, "w ago") {
			parts := strings.Split(strings.TrimSuffix(ts, " ago"), "w")
			if len(parts) > 0 {
				var weeks int
				if _, err := fmt.Sscanf(parts[0], "%d", &weeks); err == nil {
					return time.Now().Add(-time.Duration(weeks) * 7 * 24 * time.Hour), nil
				}
			}
		}

		// Try standard duration
		if d, err := time.ParseDuration(durationStr); err == nil {
			return time.Now().Add(-d), nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognized timestamp format: %s", ts)
}

// resolveThreadRef resolves a thread reference to a thread ID.
// Accepts: thr-xxx (thread ID), bd-xxx (bead ID), or external:ref format.
func resolveThreadRef(ref string) (string, error) {
	// Direct thread ID
	if strings.HasPrefix(ref, "thr-") {
		return ref, nil
	}

	// For now, just return the ref as-is for other formats.
	// In Phase 4, we'll add proper resolution for bead IDs and external refs.
	// This allows the feature to work while we build out the full integration.
	return ref, nil
}

func init() {
	rootCmd.AddCommand(captureCmd)

	captureCmd.Flags().StringVar(&captureType, "type", "", "insight type (hypothesis|discovery|question|feedback|pivot|decision)")
	captureCmd.Flags().StringVar(&captureThreadID, "thread", "", "thread/bead/external ref to associate with")
	captureCmd.Flags().BoolVar(&captureHypothesis, "hypothesis", false, "mark as hypothesis (speculation before evidence)")
	captureCmd.Flags().BoolVar(&captureDiscovery, "discovery", false, "mark as discovery (evidence-based finding)")
	captureCmd.Flags().BoolVar(&captureQuestion, "question", false, "mark as question (open uncertainty)")
	captureCmd.Flags().BoolVar(&captureFeedback, "feedback", false, "mark as feedback (external input received)")
	captureCmd.Flags().BoolVar(&capturePivot, "pivot", false, "mark as pivot (direction changed)")
	captureCmd.Flags().BoolVar(&captureDecision, "decision", false, "mark as decision (committed to approach)")
	captureCmd.Flags().StringVar(&captureTimestamp, "timestamp", "", "when the insight occurred (RFC3339, date, or relative like '2h ago')")
	captureCmd.Flags().StringVar(&captureAuthor, "author", "", "who captured this insight (e.g., 'brian', 'cc:opus-4.5')")
	captureCmd.Flags().StringSliceVar(&captureEndorsedBy, "endorsed-by", nil, "who endorsed this insight (repeatable)")
}
