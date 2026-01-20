// Package importer provides functions to import insights from various sources.
package importer

import (
	"regexp"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// Common patterns for insight type detection
var (
	discoveryPatterns = []string{"found:", "discovered:", "noticed:", "realized:", "identified:"}
	decisionPatterns  = []string{"decision:", "decided:", "let's go with", "we'll use", "going with", "chose to", "will use"}
	pivotPatterns     = []string{"actually", "wait,", "but actually", "turns out", "however,", "on second thought"}
	questionPattern   = regexp.MustCompile(`\?[\s]*$`)
)

// ParseAISession parses an AI session transcript and extracts insights.
// The content should be a plain text conversation between human and AI.
// Uses current time for timestamps.
func ParseAISession(content string) ([]*types.Insight, error) {
	return ParseAISessionWithTimestamp(content, time.Time{})
}

// ParseAISessionWithTimestamp parses an AI session transcript with an optional base timestamp.
// If baseTimestamp is provided (non-zero), all insights will use that timestamp.
// Otherwise, the current time is used.
// This is useful for importing historical conversations.
func ParseAISessionWithTimestamp(content string, baseTimestamp time.Time) ([]*types.Insight, error) {
	var insights []*types.Insight

	// If no base timestamp provided, use current time
	now := time.Now()
	insightTimestamp := baseTimestamp
	if insightTimestamp.IsZero() {
		insightTimestamp = now
	}

	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip very short lines (likely not substantive)
		if len(line) < 10 {
			continue
		}

		// Detect insight type based on content
		insightType := detectInsightType(line)

		// Create insight
		insight := &types.Insight{
			ID:         types.GenerateID("ins"),
			Timestamp:  insightTimestamp, // When the insight occurred (historical)
			Content:    line,
			Summary:    truncate(line, 80),
			Type:       insightType,
			Confidence: 0.7, // Default confidence for auto-extracted
			Source: types.InsightSource{
				Type: "ai-session",
			},
			CreatedAt: now, // When we imported it (always now)
		}

		insights = append(insights, insight)
	}

	return insights, nil
}

// ParseConversation parses a conversation with explicit turn markers.
// Supports formats like "Human: ...", "AI: ...", "User: ...", "Assistant: ..."
// Uses current time for timestamps.
func ParseConversation(content string) ([]*types.Insight, error) {
	return ParseConversationWithTimestamp(content, time.Time{})
}

// ParseConversationWithTimestamp parses a conversation with explicit turn markers
// and an optional base timestamp.
// If baseTimestamp is provided (non-zero), all insights will use that timestamp.
func ParseConversationWithTimestamp(content string, baseTimestamp time.Time) ([]*types.Insight, error) {
	var insights []*types.Insight

	// If no base timestamp provided, use current time
	now := time.Now()
	insightTimestamp := baseTimestamp
	if insightTimestamp.IsZero() {
		insightTimestamp = now
	}

	// Pattern to match conversation turns
	turnPattern := regexp.MustCompile(`(?i)^(human|user|ai|assistant|claude|gpt):\s*(.+)`)

	lines := strings.Split(content, "\n")
	var currentTurn strings.Builder
	var currentSpeaker string

	flushTurn := func() {
		if currentTurn.Len() > 0 && currentSpeaker != "" {
			text := strings.TrimSpace(currentTurn.String())
			if len(text) >= 10 {
				insightType := detectInsightType(text)

				participant := currentSpeaker
				if strings.EqualFold(participant, "human") || strings.EqualFold(participant, "user") {
					participant = "human"
				} else {
					participant = "ai-agent"
				}

				insight := &types.Insight{
					ID:         types.GenerateID("ins"),
					Timestamp:  insightTimestamp, // When the insight occurred
					Content:    text,
					Summary:    truncate(text, 80),
					Type:       insightType,
					Confidence: 0.7,
					Source: types.InsightSource{
						Type:         "ai-session",
						Participants: []string{participant},
					},
					CreatedAt: now, // When we imported it
				}

				insights = append(insights, insight)
			}
			currentTurn.Reset()
		}
	}

	for _, line := range lines {
		if matches := turnPattern.FindStringSubmatch(line); matches != nil {
			flushTurn()
			currentSpeaker = matches[1]
			currentTurn.WriteString(matches[2])
		} else if currentSpeaker != "" {
			// Continuation of current turn
			currentTurn.WriteString(" ")
			currentTurn.WriteString(strings.TrimSpace(line))
		}
	}
	flushTurn()

	return insights, nil
}

// detectInsightType determines the type of insight based on content patterns.
func detectInsightType(text string) types.InsightType {
	lower := strings.ToLower(text)

	// Check for questions first
	if questionPattern.MatchString(text) {
		return types.InsightQuestion
	}

	// Check for decisions
	for _, pattern := range decisionPatterns {
		if strings.Contains(lower, pattern) {
			return types.InsightDecision
		}
	}

	// Check for pivots
	for _, pattern := range pivotPatterns {
		if strings.Contains(lower, pattern) {
			return types.InsightPivot
		}
	}

	// Check for discoveries
	for _, pattern := range discoveryPatterns {
		if strings.Contains(lower, pattern) {
			return types.InsightDiscovery
		}
	}

	// Default to hypothesis
	return types.InsightHypothesis
}

// truncate shortens a string to maxLen characters, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
