package importer

import (
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// ----------------------------------------------------------------------------
// parseSlackTimestamp
// ----------------------------------------------------------------------------

func TestParseSlackTimestamp(t *testing.T) {
	t.Run("valid epoch.micro format", func(t *testing.T) {
		// 1609459200 = 2021-01-01 00:00:00 UTC
		ts := parseSlackTimestamp("1609459200.000100")
		want := time.Unix(1609459200, 0)
		if !ts.Equal(want) {
			t.Errorf("parseSlackTimestamp(\"1609459200.000100\") = %v, want %v", ts, want)
		}
	})

	t.Run("empty string falls back to current time", func(t *testing.T) {
		before := time.Now()
		ts := parseSlackTimestamp("")
		after := time.Now()
		if ts.Before(before) || ts.After(after) {
			t.Errorf("parseSlackTimestamp(\"\") = %v, not within [%v, %v]", ts, before, after)
		}
	})

	t.Run("invalid string falls back to current time", func(t *testing.T) {
		before := time.Now()
		ts := parseSlackTimestamp("invalid")
		after := time.Now()
		if ts.Before(before) || ts.After(after) {
			t.Errorf("parseSlackTimestamp(\"invalid\") = %v, not within [%v, %v]", ts, before, after)
		}
	})

	t.Run("epoch only without dot falls back correctly", func(t *testing.T) {
		// Just an epoch with no "." — parts[0] is the whole string, should parse fine
		ts := parseSlackTimestamp("1609459200")
		want := time.Unix(1609459200, 0)
		if !ts.Equal(want) {
			t.Errorf("parseSlackTimestamp(\"1609459200\") = %v, want %v", ts, want)
		}
	})
}

// ----------------------------------------------------------------------------
// isSlackNoise
// ----------------------------------------------------------------------------

func TestIsSlackNoise(t *testing.T) {
	noiseTests := []struct {
		name  string
		text  string
		noise bool
	}{
		// Exact noise matches
		{name: "ok", text: "ok", noise: true},
		{name: "okay", text: "okay", noise: true},
		{name: "thanks", text: "thanks", noise: true},
		{name: "lgtm", text: "lgtm", noise: true},
		{name: "+1", text: "+1", noise: true},
		{name: "sounds good", text: "sounds good", noise: true},
		{name: "cool", text: "cool", noise: true},
		{name: "nice", text: "nice", noise: true},
		{name: "great", text: "great", noise: true},
		{name: "got it", text: "got it", noise: true},
		{name: "will do", text: "will do", noise: true},
		{name: "on it", text: "on it", noise: true},
		{name: "thank you", text: "thank you", noise: true},

		// Case-insensitive noise
		{name: "OK uppercase", text: "OK", noise: true},
		{name: "LGTM uppercase", text: "LGTM", noise: true},

		// Colon-wrapped emoji (reaction-style)
		{name: "thumbsup emoji", text: ":thumbsup:", noise: true},
		{name: "white_check_mark emoji", text: ":white_check_mark:", noise: true},
		{name: "100 emoji", text: ":100:", noise: true},

		// Substantive messages that are NOT noise
		{name: "substantive message", text: "I think we should use Redis for this", noise: false},
		{name: "longer ok sentence", text: "ok let me explain why the current approach fails", noise: false},
		{name: "decision text", text: "decided: we'll use Postgres for all relational data", noise: false},
		{name: "question text", text: "What should the TTL be for anonymous sessions?", noise: false},
	}

	for _, tc := range noiseTests {
		t.Run(tc.name, func(t *testing.T) {
			got := isSlackNoise(tc.text)
			if got != tc.noise {
				t.Errorf("isSlackNoise(%q) = %v, want %v", tc.text, got, tc.noise)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// ParseSlackJSON — valid input
// ----------------------------------------------------------------------------

func TestParseSlackJSON_Valid(t *testing.T) {
	// Includes:
	//  - a noise message ("ok") that should be filtered
	//  - a short message under 10 chars that should be filtered
	//  - a non-"message" type that should be filtered
	//  - three substantive messages of varying types
	jsonInput := []byte(`[
		{"type": "message", "user": "U001", "text": "ok", "ts": "1609459200.000001"},
		{"type": "message", "user": "U002", "text": "hi", "ts": "1609459201.000001"},
		{"type": "channel_join", "user": "U003", "text": "U003 has joined the channel", "ts": "1609459202.000001"},
		{"type": "message", "user": "U001", "text": "decided: we will use Redis for the session store because of TTL support", "ts": "1609459203.000001"},
		{"type": "message", "user": "U002", "text": "found: the current implementation leaks connections after restart", "ts": "1609459204.000001"},
		{"type": "message", "user": "U003", "text": "I think we should add integration tests before deploying this to production", "ts": "1609459205.000001"}
	]`)

	insights, err := ParseSlackJSON(jsonInput)
	if err != nil {
		t.Fatalf("ParseSlackJSON returned unexpected error: %v", err)
	}

	// "ok" → noise filtered
	// "hi" → too short (< 10)
	// channel_join → non-message type filtered
	// remaining 3 messages pass
	wantCount := 3
	if len(insights) != wantCount {
		t.Fatalf("ParseSlackJSON returned %d insights, want %d", len(insights), wantCount)
	}

	// Verify all insights have correct confidence and source type
	for i, ins := range insights {
		if ins.Confidence != 0.6 {
			t.Errorf("insight[%d].Confidence = %v, want 0.6", i, ins.Confidence)
		}
		if ins.Source.Type != "slack" {
			t.Errorf("insight[%d].Source.Type = %q, want \"slack\"", i, ins.Source.Type)
		}
		if ins.ID == "" {
			t.Errorf("insight[%d].ID is empty", i)
		}
	}

	// Spot-check types
	typeChecks := []struct {
		idx      int
		wantType types.InsightType
	}{
		{0, types.InsightDecision},
		{1, types.InsightDiscovery},
		{2, types.InsightHypothesis},
	}
	for _, tc := range typeChecks {
		if insights[tc.idx].Type != tc.wantType {
			t.Errorf("insight[%d].Type = %q, want %q", tc.idx, insights[tc.idx].Type, tc.wantType)
		}
	}

	// Verify Source.Ref is populated with the Slack timestamp
	if insights[0].Source.Ref == "" {
		t.Error("insight[0].Source.Ref is empty, want Slack timestamp string")
	}

	// Verify Timestamp is parsed from "ts" field — epoch 1609459203 = 2021-01-01 00:00:03 UTC
	wantTs := time.Unix(1609459203, 0)
	if !insights[0].Timestamp.Equal(wantTs) {
		t.Errorf("insight[0].Timestamp = %v, want %v", insights[0].Timestamp, wantTs)
	}

	// Verify participant from user field
	if len(insights[0].Source.Participants) == 0 || insights[0].Source.Participants[0] != "U001" {
		t.Errorf("insight[0].Source.Participants = %v, want [\"U001\"]", insights[0].Source.Participants)
	}
}

// ----------------------------------------------------------------------------
// ParseSlackJSON — empty type treated as "message"
// ----------------------------------------------------------------------------

func TestParseSlackJSON_EmptyTypeIncluded(t *testing.T) {
	// The source treats msg.Type == "" the same as "message"
	jsonInput := []byte(`[
		{"type": "", "user": "U001", "text": "This is a substantive message worth keeping here", "ts": "1609459200.000001"}
	]`)

	insights, err := ParseSlackJSON(jsonInput)
	if err != nil {
		t.Fatalf("ParseSlackJSON returned error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight for empty type, got %d", len(insights))
	}
}

// ----------------------------------------------------------------------------
// ParseSlackJSON — malformed JSON returns error
// ----------------------------------------------------------------------------

func TestParseSlackJSON_MalformedJSON(t *testing.T) {
	bad := []byte(`{ not valid json at all `)

	_, err := ParseSlackJSON(bad)
	if err == nil {
		t.Error("ParseSlackJSON with malformed JSON should return an error, got nil")
	}
}

// ----------------------------------------------------------------------------
// ParseSlackJSON — empty array returns empty slice (no error)
// ----------------------------------------------------------------------------

func TestParseSlackJSON_EmptyArray(t *testing.T) {
	insights, err := ParseSlackJSON([]byte(`[]`))
	if err != nil {
		t.Fatalf("ParseSlackJSON([]) returned error: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("ParseSlackJSON([]) returned %d insights, want 0", len(insights))
	}
}
