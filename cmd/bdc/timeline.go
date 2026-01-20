package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var timelineCmd = &cobra.Command{
	Use:   "timeline [thread-id]",
	Short: "Show insights in chronological order",
	Long: `Display insights in chronological timeline format with visual symbols.

Symbols:
  ○ - hypothesis, discovery (speculation and findings)
  ? - question (open uncertainty)
  » - feedback (external input received)
  ● - pivot (direction changed)
  ◆ - decision (committed to approach)

Example:
  bdc timeline              # Show all insights
  bdc timeline thr-7f2a     # Show insights for specific thread`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTimeline,
}

func runTimeline(cmd *cobra.Command, args []string) error {
	st, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()

	var threadID string
	if len(args) > 0 {
		threadID = args[0]
	}

	// Get insights
	insights, err := st.ListInsights(threadID, "", time.Time{})
	if err != nil {
		return fmt.Errorf("failed to list insights: %w", err)
	}

	if len(insights) == 0 {
		if threadID != "" {
			fmt.Printf("No insights found for thread %s\n", threadID)
		} else {
			fmt.Println("No insights found")
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
	// ListInsights returns DESC, so we need to reverse
	reverseInsights(insights)

	// Print each insight
	for _, insight := range insights {
		printInsightLine(st, insight)
	}

	return nil
}

func printInsightLine(st interface {
	GetDependencies(string) ([]*types.Dependency, error)
}, insight *types.Insight) {
	// Choose symbol based on type
	symbol := getInsightSymbol(insight.Type)

	// Format timestamp
	timestamp := insight.Timestamp.Format("2006-01-02 15:04")

	// Use summary if available, otherwise truncate content
	text := insight.Summary
	if text == "" {
		text = insight.Content
		if len(text) > 60 {
			text = text[:57] + "..."
		}
	}

	// Format type with special styling for pivots and decisions
	typeStr := string(insight.Type)
	if insight.Type == types.InsightPivot || insight.Type == types.InsightDecision {
		typeStr = strings.ToUpper(typeStr)
	}

	// Print the main line
	fmt.Printf("%s  %s \"%s\" [%s]\n", timestamp, symbol, text, typeStr)

	// Get and print dependencies
	deps, err := st.GetDependencies(insight.ID)
	if err == nil && len(deps) > 0 {
		for _, dep := range deps {
			fmt.Printf("%s└── %s: %s\n", strings.Repeat(" ", len(timestamp)+2), dep.Type, dep.To)
		}
	}
}

func getInsightSymbol(insightType types.InsightType) string {
	switch insightType {
	case types.InsightQuestion:
		return "?"
	case types.InsightFeedback:
		return "»"
	case types.InsightPivot:
		return "●"
	case types.InsightDecision:
		return "◆"
	default:
		return "○"
	}
}

func reverseInsights(insights []*types.Insight) {
	for i, j := 0, len(insights)-1; i < j; i, j = i+1, j-1 {
		insights[i], insights[j] = insights[j], insights[i]
	}
}

func init() {
	rootCmd.AddCommand(timelineCmd)
}
