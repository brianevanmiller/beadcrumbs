package slack

import (
	"fmt"
	"strings"
	"time"

	importer "github.com/brianevanmiller/beadcrumbs/internal/import"
	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// ConvertOptions configures the message-to-insight conversion.
type ConvertOptions struct {
	ChannelName string
	UserCache   *UserCache
	SourceType  string // defaults to "slack-api"
}

// ConvertMessages transforms Slack API messages into beadcrumbs insights.
// Filters noise, detects insight types, resolves user names, and groups thread replies.
func ConvertMessages(messages []Message, opts ConvertOptions) []*types.Insight {
	sourceType := opts.SourceType
	if sourceType == "" {
		sourceType = "slack-api"
	}

	now := time.Now()
	var insights []*types.Insight

	for _, msg := range messages {
		// Skip non-message types
		if msg.Type != "message" && msg.Type != "" {
			continue
		}

		text := strings.TrimSpace(msg.Text)

		// Skip empty or very short messages
		if len(text) < 10 {
			continue
		}

		// Skip noise
		if importer.IsSlackNoise(text) {
			continue
		}

		// Parse timestamp
		ts := importer.ParseSlackTimestamp(msg.Timestamp)

		// Detect insight type
		insightType := importer.DetectInsightType(text)

		// Resolve user name
		var authorID string
		var participants []string
		if msg.User != "" {
			if opts.UserCache != nil {
				authorID = opts.UserCache.Resolve(msg.User)
			} else {
				authorID = msg.User
			}
			participants = []string{authorID}
		}

		// Build source ref
		sourceRef := msg.Timestamp
		if opts.ChannelName != "" {
			sourceRef = fmt.Sprintf("#%s/%s", opts.ChannelName, msg.Timestamp)
		}

		insight := &types.Insight{
			ID:         types.GenerateID("ins"),
			Timestamp:  ts,
			Content:    text,
			Summary:    importer.Truncate(text, 80),
			Type:       insightType,
			Confidence: 0.6, // Lower confidence for Slack messages
			Source: types.InsightSource{
				Type:         sourceType,
				Ref:          sourceRef,
				Participants: participants,
			},
			AuthorID:  authorID,
			CreatedAt: now,
		}

		insights = append(insights, insight)
	}

	return insights
}
