package slack

import (
	"testing"
)

func TestConvertMessages_BasicConversion(t *testing.T) {
	messages := []Message{
		{Type: "message", User: "U001", Text: "We should consider using a message queue for this", Timestamp: "1705312200.000000"},
		{Type: "message", User: "U002", Text: "I found that the API rate limit is 100 req/min", Timestamp: "1705312300.000000"},
	}

	insights := ConvertMessages(messages, ConvertOptions{
		ChannelName: "engineering",
	})

	if len(insights) != 2 {
		t.Fatalf("expected 2 insights, got %d", len(insights))
	}

	if insights[0].Source.Type != "slack-api" {
		t.Errorf("unexpected source type: %s", insights[0].Source.Type)
	}
	if insights[0].Source.Ref != "#engineering/1705312200.000000" {
		t.Errorf("unexpected source ref: %s", insights[0].Source.Ref)
	}
}

func TestConvertMessages_FiltersNoise(t *testing.T) {
	messages := []Message{
		// These match IsSlackNoise exact patterns
		{Type: "message", User: "U001", Text: "sounds good", Timestamp: "1705312200.000000"},
		{Type: "message", User: "U001", Text: "lgtm", Timestamp: "1705312300.000000"},
		{Type: "message", User: "U001", Text: "This is a substantial message about the project architecture", Timestamp: "1705312400.000000"},
	}

	insights := ConvertMessages(messages, ConvertOptions{ChannelName: "general"})

	if len(insights) != 1 {
		t.Fatalf("expected 1 insight (noise filtered), got %d", len(insights))
	}
	if insights[0].Content != "This is a substantial message about the project architecture" {
		t.Errorf("unexpected content: %s", insights[0].Content)
	}
}

func TestConvertMessages_FiltersShortMessages(t *testing.T) {
	messages := []Message{
		{Type: "message", User: "U001", Text: "ok", Timestamp: "1705312200.000000"},
		{Type: "message", User: "U001", Text: "+1", Timestamp: "1705312300.000000"},
		{Type: "message", User: "U001", Text: "This is long enough to be considered an insight message", Timestamp: "1705312400.000000"},
	}

	insights := ConvertMessages(messages, ConvertOptions{})

	if len(insights) != 1 {
		t.Fatalf("expected 1 insight (short filtered), got %d", len(insights))
	}
}

func TestConvertMessages_SkipsNonMessageTypes(t *testing.T) {
	messages := []Message{
		{Type: "channel_join", User: "U001", Text: "joined the channel", Timestamp: "1705312200.000000"},
		{Type: "message", User: "U001", Text: "This is a real message about the project design decisions", Timestamp: "1705312300.000000"},
	}

	insights := ConvertMessages(messages, ConvertOptions{})

	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
}

func TestConvertMessages_UserCacheResolution(t *testing.T) {
	cache := &UserCache{
		cache: map[string]string{
			"U001": "Alice Smith",
			"U002": "Bob Jones",
		},
	}

	messages := []Message{
		{Type: "message", User: "U001", Text: "This is a message from Alice about the design", Timestamp: "1705312200.000000"},
		{Type: "message", User: "U002", Text: "This is a reply from Bob about the implementation", Timestamp: "1705312300.000000"},
	}

	insights := ConvertMessages(messages, ConvertOptions{
		ChannelName: "engineering",
		UserCache:   cache,
	})

	if len(insights) != 2 {
		t.Fatalf("expected 2 insights, got %d", len(insights))
	}
	if insights[0].AuthorID != "Alice Smith" {
		t.Errorf("expected author 'Alice Smith', got: %s", insights[0].AuthorID)
	}
	if insights[1].AuthorID != "Bob Jones" {
		t.Errorf("expected author 'Bob Jones', got: %s", insights[1].AuthorID)
	}
}

func TestConvertMessages_CustomSourceType(t *testing.T) {
	messages := []Message{
		{Type: "message", User: "U001", Text: "A message with custom source type for testing purposes", Timestamp: "1705312200.000000"},
	}

	insights := ConvertMessages(messages, ConvertOptions{
		SourceType: "slack-custom",
	})

	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if insights[0].Source.Type != "slack-custom" {
		t.Errorf("unexpected source type: %s", insights[0].Source.Type)
	}
}

func TestConvertMessages_SourceRefWithoutChannel(t *testing.T) {
	messages := []Message{
		{Type: "message", User: "U001", Text: "A message without channel context for source ref testing", Timestamp: "1705312200.000000"},
	}

	insights := ConvertMessages(messages, ConvertOptions{})

	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	// Without channel name, source ref should be just the timestamp
	if insights[0].Source.Ref != "1705312200.000000" {
		t.Errorf("unexpected source ref: %s", insights[0].Source.Ref)
	}
}

func TestConvertMessages_EmptyInput(t *testing.T) {
	insights := ConvertMessages(nil, ConvertOptions{})
	if len(insights) != 0 {
		t.Fatalf("expected 0 insights, got %d", len(insights))
	}
}
