// Package summary provides shared formatting for beadcrumbs thread summaries.
// Used by all integrations (Linear, GitHub, etc.) to produce consistent markdown.
package summary

import (
	"fmt"
	"strings"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// FormatSummary formats thread insights as a markdown summary comment.
// This is the canonical format used by all integrations (Linear, GitHub PR, etc.).
func FormatSummary(thread *types.InsightThread, insights []*types.Insight) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("## Beadcrumbs Summary \u2014 Thread `%s`\n\n", thread.ID))
	sb.WriteString(fmt.Sprintf("**%s**\n\n", thread.Title))

	// Group by type
	var decisions, discoveries, pivots, feedback []*types.Insight
	typeCounts := make(map[types.InsightType]int)
	for _, ins := range insights {
		typeCounts[ins.Type]++
		switch ins.Type {
		case types.InsightDecision:
			decisions = append(decisions, ins)
		case types.InsightDiscovery:
			discoveries = append(discoveries, ins)
		case types.InsightPivot:
			pivots = append(pivots, ins)
		case types.InsightFeedback:
			feedback = append(feedback, ins)
		}
	}

	if len(decisions) > 0 {
		sb.WriteString("### Decisions\n\n")
		for _, d := range decisions {
			sb.WriteString(fmt.Sprintf("- **%s**\n", d.Content))
		}
		sb.WriteString("\n")
	}

	if len(discoveries) > 0 {
		sb.WriteString("### Discoveries\n\n")
		for _, d := range discoveries {
			sb.WriteString(fmt.Sprintf("- %s\n", d.Content))
		}
		sb.WriteString("\n")
	}

	if len(pivots) > 0 {
		sb.WriteString("### Pivots\n\n")
		for _, p := range pivots {
			sb.WriteString(fmt.Sprintf("- %s\n", p.Content))
		}
		sb.WriteString("\n")
	}

	if len(feedback) > 0 {
		sb.WriteString("### Feedback\n\n")
		for _, f := range feedback {
			sb.WriteString(fmt.Sprintf("- %s\n", f.Content))
		}
		sb.WriteString("\n")
	}

	if thread.CurrentUnderstanding != "" {
		sb.WriteString("### Summary\n\n")
		sb.WriteString(thread.CurrentUnderstanding + "\n\n")
	}

	// Footer: divider + per-type breakdown + attribution
	sb.WriteString("---\n\n")

	var parts []string
	parts = append(parts, fmt.Sprintf("%d insights", len(insights)))
	typeOrder := []types.InsightType{
		types.InsightDecision,
		types.InsightDiscovery,
		types.InsightHypothesis,
		types.InsightPivot,
		types.InsightQuestion,
		types.InsightFeedback,
	}
	pluralLabels := map[types.InsightType]string{
		types.InsightDecision:   "decisions",
		types.InsightDiscovery:  "discoveries",
		types.InsightHypothesis: "hypotheses",
		types.InsightPivot:      "pivots",
		types.InsightQuestion:   "questions",
		types.InsightFeedback:   "feedback",
	}
	for _, t := range typeOrder {
		if c, ok := typeCounts[t]; ok && c > 0 {
			label := pluralLabels[t]
			parts = append(parts, fmt.Sprintf("%d %s", c, label))
		}
	}
	sb.WriteString(fmt.Sprintf("*%s*\n", strings.Join(parts, " \u00b7 ")))
	sb.WriteString("*Tracked by [beadcrumbs](https://github.com/brianevanmiller/beadcrumbs)*\n")

	return sb.String()
}
