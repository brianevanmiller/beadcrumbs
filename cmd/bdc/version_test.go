package main

import "testing"

func TestShortCommit(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"long hash", "abcdef1234567890abcdef", "abcdef123456"},
		{"exactly 12", "abcdef123456", "abcdef123456"},
		{"short hash", "abcdef", "abcdef"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortCommit(tt.input)
			if got != tt.want {
				t.Errorf("shortCommit(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveCommitHash_FallsBackToEmpty(t *testing.T) {
	// With no ldflags set and running in test context (no vcs info),
	// resolveCommitHash should return empty or a hash (never panic)
	hash := resolveCommitHash()
	// Just verify it doesn't panic — the value depends on build context
	_ = hash
}
