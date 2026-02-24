package main

import (
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

func TestInferSourceType(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		author   string
		expected string
	}{
		// AI tool prefixes
		{"claude origin", "claude:sess_abc", "", "ai-session"},
		{"cursor origin", "cursor:ws_123", "", "ai-session"},
		{"codex origin", "codex:run-456", "", "ai-session"},
		{"warp origin", "warp:session-789", "", "ai-session"},
		{"gemini origin", "gemini:conv-xyz", "", "ai-session"},
		{"zed origin", "zed:workspace-id", "", "ai-session"},
		{"opencode origin", "opencode:sess-abc", "", "ai-session"},

		// Human origins
		{"notion origin", "notion:page-xyz", "", "human"},
		{"slack origin", "slack:C0123-1234567", "", "human"},
		{"basecamp origin", "basecamp:12345", "", "human"},
		{"unknown system", "jira:PROJ-123", "", "human"},

		// Author-based fallback
		{"cc author no AI origin", "notion:page-xyz", "cc:opus-4.6", "ai-session"},
		{"cc author with AI origin", "claude:sess_abc", "cc:opus-4.6", "ai-session"},
		{"human author", "notion:page-xyz", "brian", "human"},
		{"no author no AI prefix", "slack:thread-1", "", "human"},

		// Edge cases
		{"prefix only", "claude:", "", "ai-session"},
		{"prefix substring mismatch", "claudeX:something", "", "human"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferSourceType(tt.origin, tt.author)
			if got != tt.expected {
				t.Errorf("inferSourceType(%q, %q) = %q, want %q", tt.origin, tt.author, got, tt.expected)
			}
		})
	}
}

// resetCaptureFlags resets all global capture type flags to their defaults.
func resetCaptureFlags() {
	captureType = ""
	captureHypothesis = false
	captureDiscovery = false
	captureQuestion = false
	captureFeedback = false
	capturePivot = false
	captureDecision = false
}

func TestDetermineInsightType(t *testing.T) {
	tests := []struct {
		name      string
		setup     func()
		expected  types.InsightType
		wantErr   bool
	}{
		{
			name:     "default is discovery",
			setup:    func() {},
			expected: types.InsightDiscovery,
		},
		{
			name:     "hypothesis flag",
			setup:    func() { captureHypothesis = true },
			expected: types.InsightHypothesis,
		},
		{
			name:     "discovery flag",
			setup:    func() { captureDiscovery = true },
			expected: types.InsightDiscovery,
		},
		{
			name:     "question flag",
			setup:    func() { captureQuestion = true },
			expected: types.InsightQuestion,
		},
		{
			name:     "feedback flag",
			setup:    func() { captureFeedback = true },
			expected: types.InsightFeedback,
		},
		{
			name:     "pivot flag",
			setup:    func() { capturePivot = true },
			expected: types.InsightPivot,
		},
		{
			name:     "decision flag",
			setup:    func() { captureDecision = true },
			expected: types.InsightDecision,
		},
		{
			name:     "type flag hypothesis",
			setup:    func() { captureType = "hypothesis" },
			expected: types.InsightHypothesis,
		},
		{
			name:     "type flag decision",
			setup:    func() { captureType = "decision" },
			expected: types.InsightDecision,
		},
		{
			name:    "multiple flags error",
			setup:   func() { captureHypothesis = true; captureDecision = true },
			wantErr: true,
		},
		{
			name:    "type flag plus shorthand error",
			setup:   func() { captureType = "hypothesis"; captureDecision = true },
			wantErr: true,
		},
		{
			name:    "invalid type flag",
			setup:   func() { captureType = "invalid" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCaptureFlags()
			tt.setup()

			got, err := determineInsightType()
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got type=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("got %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseTimestamp_RFC3339(t *testing.T) {
	got, err := parseTimestamp("2024-06-15T10:00:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
	if !got.Equal(expected) {
		t.Errorf("got %v, want %v", got, expected)
	}
}

func TestParseTimestamp_DateOnly(t *testing.T) {
	got, err := parseTimestamp("2024-06-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Year() != 2024 || got.Month() != 6 || got.Day() != 15 {
		t.Errorf("got %v, want 2024-06-15", got)
	}
}

func TestParseTimestamp_RelativeDay(t *testing.T) {
	got, err := parseTimestamp("3d ago")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Now().Add(-3 * 24 * time.Hour)
	diff := expected.Sub(got).Abs()
	if diff > 5*time.Second {
		t.Errorf("got %v, expected within 5s of %v", got, expected)
	}
}

func TestParseTimestamp_Invalid(t *testing.T) {
	_, err := parseTimestamp("garbage")
	if err == nil {
		t.Error("expected error for invalid timestamp")
	}
}
