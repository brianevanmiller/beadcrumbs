package importer

import (
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// ----------------------------------------------------------------------------
// detectInsightType
// ----------------------------------------------------------------------------

func TestDetectInsightType(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantType types.InsightType
	}{
		// Questions — ends with "?"
		{
			name:     "question ends with question mark",
			text:     "What should we use for caching?",
			wantType: types.InsightQuestion,
		},
		{
			name:     "question with trailing whitespace after mark",
			text:     "Should we deploy on Friday? ",
			wantType: types.InsightQuestion,
		},
		// Decisions
		{
			name:     "decided prefix",
			text:     "decided: we'll use Redis for the caching layer",
			wantType: types.InsightDecision,
		},
		{
			name:     "let's go with phrasing",
			text:     "let's go with option A here",
			wantType: types.InsightDecision,
		},
		{
			name:     "we'll use phrasing",
			text:     "we'll use Postgres for persistence",
			wantType: types.InsightDecision,
		},
		{
			name:     "going with phrasing",
			text:     "going with the simpler approach for now",
			wantType: types.InsightDecision,
		},
		// Pivots
		{
			name:     "actually keyword",
			text:     "actually, that won't work at all because of the rate limits",
			wantType: types.InsightPivot,
		},
		{
			name:     "wait comma keyword",
			text:     "wait, there's a better way to handle this",
			wantType: types.InsightPivot,
		},
		{
			name:     "turns out keyword",
			text:     "turns out the library doesn't support streaming",
			wantType: types.InsightPivot,
		},
		// Discoveries
		{
			name:     "found colon prefix",
			text:     "found: the API returns 404 for deleted items",
			wantType: types.InsightDiscovery,
		},
		{
			name:     "noticed colon prefix",
			text:     "noticed: latency spikes at noon on weekdays",
			wantType: types.InsightDiscovery,
		},
		{
			name:     "discovered colon prefix",
			text:     "discovered: there is an undocumented rate limit",
			wantType: types.InsightDiscovery,
		},
		// Default — hypothesis
		{
			name:     "no pattern match defaults to hypothesis",
			text:     "The caching layer needs work before launch",
			wantType: types.InsightHypothesis,
		},
		{
			name:     "plain statement is hypothesis",
			text:     "We might want to revisit the database schema later",
			wantType: types.InsightHypothesis,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectInsightType(tc.text)
			if got != tc.wantType {
				t.Errorf("detectInsightType(%q) = %q, want %q", tc.text, got, tc.wantType)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// truncate
// ----------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "short string unchanged",
			input:  "hello",
			maxLen: 20,
			want:   "hello",
		},
		{
			name:   "exact length unchanged",
			input:  "exactly ten!",
			maxLen: 12,
			want:   "exactly ten!",
		},
		{
			name:   "long string gets ellipsis",
			input:  "This is a longer string that exceeds the max length",
			maxLen: 20,
			want:   "This is a longer ..." ,
		},
		{
			name:   "empty string unchanged",
			input:  "",
			maxLen: 10,
			want:   "",
		},
		{
			name:   "one over max length gets truncated",
			input:  "12345678901",
			maxLen: 10,
			want:   "1234567...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.input, tc.maxLen)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
			}
			// Result must never exceed maxLen
			if len(got) > tc.maxLen {
				t.Errorf("truncate result length %d exceeds maxLen %d", len(got), tc.maxLen)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// ParseAISession
// ----------------------------------------------------------------------------

func TestParseAISession(t *testing.T) {
	content := `
We need to figure out what caching strategy makes sense here.

decided: we'll use Redis for session caching because it supports TTL natively

found: the existing in-memory cache leaks memory after about 1000 entries

actually, the memory leak is in the eviction policy, not the cache itself

What is the expected TTL for anonymous users?

ok

short
`
	insights, err := ParseAISession(content)
	if err != nil {
		t.Fatalf("ParseAISession returned unexpected error: %v", err)
	}

	// Blank lines and short lines ("ok", "short") are skipped.
	// Substantive lines that survive the len>=10 filter:
	// 1. "We need to figure out..."      → hypothesis
	// 2. "decided: we'll use Redis..."   → decision
	// 3. "found: the existing..."        → discovery
	// 4. "actually, the memory leak..."  → pivot
	// 5. "What is the expected TTL..."   → question
	wantCount := 5
	if len(insights) != wantCount {
		t.Fatalf("ParseAISession returned %d insights, want %d", len(insights), wantCount)
	}

	// Verify confidence and source type on every insight
	for i, ins := range insights {
		if ins.Confidence != 0.7 {
			t.Errorf("insight[%d].Confidence = %v, want 0.7", i, ins.Confidence)
		}
		if ins.Source.Type != "ai-session" {
			t.Errorf("insight[%d].Source.Type = %q, want \"ai-session\"", i, ins.Source.Type)
		}
		if ins.ID == "" {
			t.Errorf("insight[%d].ID is empty", i)
		}
	}

	// Spot-check specific types
	typeChecks := []struct {
		idx      int
		wantType types.InsightType
	}{
		{0, types.InsightHypothesis},
		{1, types.InsightDecision},
		{2, types.InsightDiscovery},
		{3, types.InsightPivot},
		{4, types.InsightQuestion},
	}
	for _, tc := range typeChecks {
		if insights[tc.idx].Type != tc.wantType {
			t.Errorf("insight[%d].Type = %q, want %q", tc.idx, insights[tc.idx].Type, tc.wantType)
		}
	}
}

// ----------------------------------------------------------------------------
// ParseConversation
// ----------------------------------------------------------------------------

func TestParseConversation(t *testing.T) {
	content := `Human: What database should we use for this project?
AI: I recommend Postgres because of its JSONB support and strong consistency guarantees.
It also has excellent tooling in the Go ecosystem.
Human: decided: let's go with Postgres then.
AI: Great choice. We should also think about connection pooling.`

	insights, err := ParseConversation(content)
	if err != nil {
		t.Fatalf("ParseConversation returned unexpected error: %v", err)
	}

	// Expect 4 turns — each speaker utterance becomes one insight
	wantCount := 4
	if len(insights) != wantCount {
		t.Fatalf("ParseConversation returned %d insights, want %d", len(insights), wantCount)
	}

	// Verify participant assignment
	participantChecks := []struct {
		idx         int
		wantParticipant string
	}{
		{0, "human"},
		{1, "ai-agent"},
		{2, "human"},
		{3, "ai-agent"},
	}
	for _, tc := range participantChecks {
		ins := insights[tc.idx]
		if len(ins.Source.Participants) == 0 {
			t.Errorf("insight[%d] has no participants", tc.idx)
			continue
		}
		if ins.Source.Participants[0] != tc.wantParticipant {
			t.Errorf("insight[%d].Source.Participants[0] = %q, want %q",
				tc.idx, ins.Source.Participants[0], tc.wantParticipant)
		}
	}

	// Human turn 2 ("decided: let's go with Postgres then.") should be a decision
	if insights[2].Type != types.InsightDecision {
		t.Errorf("insight[2].Type = %q, want %q", insights[2].Type, types.InsightDecision)
	}

	// AI multi-line turn should be merged — verify continuation text is included
	aiTurnContent := insights[1].Content
	if !strings.Contains(aiTurnContent, "JSONB") {
		t.Errorf("AI turn content missing expected text; got: %q", aiTurnContent)
	}
	if !strings.Contains(aiTurnContent, "ecosystem") {
		t.Errorf("AI turn content missing continuation; got: %q", aiTurnContent)
	}

	// Source type must be ai-session for all
	for i, ins := range insights {
		if ins.Source.Type != "ai-session" {
			t.Errorf("insight[%d].Source.Type = %q, want \"ai-session\"", i, ins.Source.Type)
		}
	}
}

func TestParseConversation_AlternativeSpeakerLabels(t *testing.T) {
	content := `User: We should cache aggressively to reduce database load.
Assistant: Agreed. Let's use Redis with a one-hour TTL as our default.`

	insights, err := ParseConversation(content)
	if err != nil {
		t.Fatalf("ParseConversation returned unexpected error: %v", err)
	}
	if len(insights) != 2 {
		t.Fatalf("expected 2 insights, got %d", len(insights))
	}

	if insights[0].Source.Participants[0] != "human" {
		t.Errorf("User speaker should map to \"human\", got %q", insights[0].Source.Participants[0])
	}
	if insights[1].Source.Participants[0] != "ai-agent" {
		t.Errorf("Assistant speaker should map to \"ai-agent\", got %q", insights[1].Source.Participants[0])
	}
}

// ----------------------------------------------------------------------------
// ParseAISessionWithTimestamp
// ----------------------------------------------------------------------------

func TestParseAISessionWithTimestamp_ExplicitTimestamp(t *testing.T) {
	fixedTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)

	content := "decided: we will deploy on Friday after the feature freeze ends"

	insights, err := ParseAISessionWithTimestamp(content, fixedTime)
	if err != nil {
		t.Fatalf("ParseAISessionWithTimestamp returned error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}

	if !insights[0].Timestamp.Equal(fixedTime) {
		t.Errorf("Timestamp = %v, want %v", insights[0].Timestamp, fixedTime)
	}
}

func TestParseAISessionWithTimestamp_ZeroTimestampUsesNow(t *testing.T) {
	before := time.Now()

	content := "decided: we will deploy on Friday after the feature freeze ends"

	insights, err := ParseAISessionWithTimestamp(content, time.Time{})
	if err != nil {
		t.Fatalf("ParseAISessionWithTimestamp returned error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}

	after := time.Now()
	ts := insights[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Errorf("Timestamp %v not within expected range [%v, %v]", ts, before, after)
	}
}
