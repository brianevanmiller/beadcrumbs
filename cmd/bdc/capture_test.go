package main

import "testing"

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
