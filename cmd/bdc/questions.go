package main

import (
	"fmt"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var (
	unresolvedOnly bool
)

var questionsCmd = &cobra.Command{
	Use:   "questions [thread-id]",
	Short: "Show question insights in timeline format",
	Long: `Display only question-type insights in chronological order.

Questions represent open questions or areas of uncertainty that
were identified during the discovery process.

Use --unresolved to show only questions that have not been superseded
by later insights.

Example:
  bdc questions                    # Show all questions
  bdc questions --unresolved       # Show only unresolved questions
  bdc questions thr-7f2a           # Show questions for specific thread`,
	Args: cobra.MaximumNArgs(1),
	RunE: runQuestions,
}

func init() {
	rootCmd.AddCommand(questionsCmd)
	questionsCmd.Flags().BoolVar(&unresolvedOnly, "unresolved", false, "Show only unresolved questions")
}

func runQuestions(cmd *cobra.Command, args []string) error {
	st, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()

	var threadID string
	if len(args) > 0 {
		threadID = args[0]
	}

	// Get question insights
	insights, err := st.ListInsights(threadID, types.InsightQuestion, time.Time{})
	if err != nil {
		return fmt.Errorf("failed to list questions: %w", err)
	}

	// Filter to unresolved if requested
	if unresolvedOnly {
		insights, err = filterUnresolved(st, insights)
		if err != nil {
			return fmt.Errorf("failed to filter unresolved questions: %w", err)
		}
	}

	if len(insights) == 0 {
		if threadID != "" && unresolvedOnly {
			fmt.Printf("No unresolved questions found for thread %s\n", threadID)
		} else if threadID != "" {
			fmt.Printf("No questions found for thread %s\n", threadID)
		} else if unresolvedOnly {
			fmt.Println("No unresolved questions found")
		} else {
			fmt.Println("No questions found")
		}
		return nil
	}

	// Print header if filtering by thread
	if threadID != "" {
		thread, err := st.GetThread(threadID)
		if err == nil {
			fmt.Printf("Thread: %s\n", thread.Title)
			if unresolvedOnly {
				fmt.Println("(showing unresolved only)")
			}
			fmt.Println()
		}
	} else if unresolvedOnly {
		fmt.Println("Unresolved questions:")
		fmt.Println()
	}

	// Sort insights by timestamp (oldest first for timeline view)
	reverseInsights(insights)

	// Print each question
	for _, insight := range insights {
		printInsightLine(st, insight)
	}

	return nil
}

// filterUnresolved filters insights to only those that have not been superseded.
func filterUnresolved(st interface {
	GetDependents(string) ([]*types.Dependency, error)
}, insights []*types.Insight) ([]*types.Insight, error) {
	var unresolved []*types.Insight

	for _, insight := range insights {
		// Get dependents (insights that reference this one)
		deps, err := st.GetDependents(insight.ID)
		if err != nil {
			return nil, err
		}

		// Check if any dependent supersedes this insight
		isSuperseded := false
		for _, dep := range deps {
			if dep.Type == types.DepSupersedes {
				isSuperseded = true
				break
			}
		}

		if !isSuperseded {
			unresolved = append(unresolved, insight)
		}
	}

	return unresolved, nil
}
