package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// SlackMessage represents a message in Slack export format.
type SlackMessage struct {
	Type      string `json:"type"`
	User      string `json:"user"`
	Text      string `json:"text"`
	Timestamp string `json:"ts"`
	ThreadTS  string `json:"thread_ts,omitempty"`
}

// ParseSlackExport parses a Slack export directory and extracts insights.
// Slack exports have JSON files per channel with message arrays.
func ParseSlackExport(dirPath string) ([]*types.Insight, error) {
	var allInsights []*types.Insight

	// Find all JSON files in the directory
	files, err := filepath.Glob(filepath.Join(dirPath, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to glob JSON files: %w", err)
	}

	for _, file := range files {
		insights, err := parseSlackFile(file)
		if err != nil {
			// Log but continue with other files
			continue
		}
		allInsights = append(allInsights, insights...)
	}

	// Sort by timestamp
	sort.Slice(allInsights, func(i, j int) bool {
		return allInsights[i].Timestamp.Before(allInsights[j].Timestamp)
	})

	return allInsights, nil
}

// ParseSlackJSON parses a single Slack JSON file (array of messages).
func ParseSlackJSON(content []byte) ([]*types.Insight, error) {
	var messages []SlackMessage
	if err := json.Unmarshal(content, &messages); err != nil {
		return nil, fmt.Errorf("failed to parse Slack JSON: %w", err)
	}

	return messagesToInsights(messages)
}

func parseSlackFile(filePath string) ([]*types.Insight, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	return ParseSlackJSON(content)
}

func messagesToInsights(messages []SlackMessage) ([]*types.Insight, error) {
	var insights []*types.Insight

	for _, msg := range messages {
		// Skip non-message types
		if msg.Type != "message" && msg.Type != "" {
			continue
		}

		// Skip empty or very short messages
		text := strings.TrimSpace(msg.Text)
		if len(text) < 10 {
			continue
		}

		// Skip common noise
		if isSlackNoise(text) {
			continue
		}

		// Parse timestamp
		ts := parseSlackTimestamp(msg.Timestamp)

		// Detect insight type
		insightType := detectInsightType(text)

		insight := &types.Insight{
			ID:         types.GenerateID("ins"),
			Timestamp:  ts,
			Content:    text,
			Summary:    truncate(text, 80),
			Type:       insightType,
			Confidence: 0.6, // Lower confidence for Slack (more noise)
			Source: types.InsightSource{
				Type:         "slack",
				Ref:          msg.Timestamp, // Slack timestamp as reference
				Participants: []string{msg.User},
			},
			CreatedAt: time.Now(),
		}

		insights = append(insights, insight)
	}

	return insights, nil
}

// parseSlackTimestamp converts Slack's "epoch.micro" format to time.Time.
func parseSlackTimestamp(ts string) time.Time {
	parts := strings.Split(ts, ".")
	if len(parts) == 0 {
		return time.Now()
	}

	epoch, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Now()
	}

	return time.Unix(epoch, 0)
}

// isSlackNoise detects common Slack messages that aren't substantive.
func isSlackNoise(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))

	noisePatterns := []string{
		"ok",
		"okay",
		"thanks",
		"thank you",
		"👍",
		"lgtm",
		"sounds good",
		"+1",
		"cool",
		"nice",
		"great",
		"got it",
		"will do",
		"on it",
	}

	for _, pattern := range noisePatterns {
		if lower == pattern {
			return true
		}
	}

	// Skip messages that are just reactions or emoji
	if strings.HasPrefix(text, ":") && strings.HasSuffix(text, ":") {
		return true
	}

	return false
}
