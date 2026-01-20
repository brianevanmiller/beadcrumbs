package main

import (
	"fmt"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var (
	listThreadID string
	listType     string
	listSince    string
	listAuthor   string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List insights",
	Long: `Lists insights with optional filtering by thread, type, time range, or author.

Examples:
  bdc list                          # List all insights
  bdc list --thread thr-abc1        # Filter by thread
  bdc list --type decision          # Filter by type
  bdc list --since 1w               # Show insights from last week
  bdc list --author brian           # Show insights by author (exact match)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		// Parse the since flag if provided
		var sinceTime time.Time
		if listSince != "" {
			sinceTime, err = parseSince(listSince)
			if err != nil {
				return fmt.Errorf("invalid --since value: %w", err)
			}
		}

		// Get insights based on filters
		var insightType types.InsightType
		if listType != "" {
			insightType = types.InsightType(listType)
			if !insightType.IsValid() {
				return fmt.Errorf("invalid insight type: %s", listType)
			}
		}

		var insights []*types.Insight

		// If author filter is specified, use the author-specific query
		if listAuthor != "" {
			insights, err = s.ListInsightsByAuthor(listAuthor)
			if err != nil {
				return fmt.Errorf("failed to get insights by author: %w", err)
			}

			// Apply additional filters manually
			var filtered []*types.Insight
			for _, insight := range insights {
				// Filter by thread
				if listThreadID != "" && insight.ThreadID != listThreadID {
					continue
				}
				// Filter by type
				if insightType != "" && insight.Type != insightType {
					continue
				}
				// Filter by time
				if !sinceTime.IsZero() && insight.Timestamp.Before(sinceTime) {
					continue
				}
				filtered = append(filtered, insight)
			}
			insights = filtered
		} else {
			insights, err = s.ListInsights(listThreadID, insightType, sinceTime)
			if err != nil {
				return fmt.Errorf("failed to get insights: %w", err)
			}
		}

		if len(insights) == 0 {
			fmt.Println("No insights found")
			return nil
		}

		// Display insights
		for _, insight := range insights {
			timestamp := insight.Timestamp.Format("2006-01-02 15:04")
			typeStr := fmt.Sprintf("[%s]", insight.Type)

			// Build metadata suffix
			var meta []string
			if insight.ThreadID != "" {
				meta = append(meta, fmt.Sprintf("thread: %s", insight.ThreadID))
			}
			if insight.AuthorID != "" {
				meta = append(meta, fmt.Sprintf("by: %s", insight.AuthorID))
			}

			metaStr := ""
			if len(meta) > 0 {
				metaStr = fmt.Sprintf(" (%s)", joinStrings(meta, ", "))
			}

			fmt.Printf("%s %-12s %s%s\n", timestamp, typeStr, truncateStr(insight.Content, 60), metaStr)
		}

		fmt.Printf("\nTotal: %d insights\n", len(insights))
		return nil
	},
}

// parseSince parses a duration string like "1w", "2d", "3h" into a time.Time.
func parseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}

	// Simple parser for common durations
	// Format: <number><unit> where unit is h (hours), d (days), w (weeks), m (months)
	if len(s) < 2 {
		return time.Time{}, fmt.Errorf("invalid duration format")
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]

	var num int
	_, err := fmt.Sscanf(numStr, "%d", &num)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid duration number: %w", err)
	}

	now := time.Now()
	switch unit {
	case 'h':
		return now.Add(-time.Duration(num) * time.Hour), nil
	case 'd':
		return now.Add(-time.Duration(num) * 24 * time.Hour), nil
	case 'w':
		return now.Add(-time.Duration(num) * 7 * 24 * time.Hour), nil
	case 'm':
		return now.AddDate(0, -num, 0), nil
	default:
		return time.Time{}, fmt.Errorf("invalid duration unit: %c (use h, d, w, or m)", unit)
	}
}

// truncateStr shortens a string to maxLen characters (renamed to avoid conflict with author.go)
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// joinStrings joins strings with a separator
func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

func init() {
	rootCmd.AddCommand(listCmd)

	listCmd.Flags().StringVar(&listThreadID, "thread", "", "filter by thread ID")
	listCmd.Flags().StringVar(&listType, "type", "", "filter by insight type")
	listCmd.Flags().StringVar(&listSince, "since", "", "show insights since (e.g., 1w, 2d, 3h)")
	listCmd.Flags().StringVar(&listAuthor, "author", "", "filter by author (exact match)")
}
