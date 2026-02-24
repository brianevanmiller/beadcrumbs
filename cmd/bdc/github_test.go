package main

import (
	"testing"
)

func TestParseGitHubPRRef(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		wantRepo   string
		wantNumber int
	}{
		{
			name:       "standard ref",
			ref:        "owner/repo#42",
			wantRepo:   "owner/repo",
			wantNumber: 42,
		},
		{
			name:       "repo with hyphens",
			ref:        "org/my-cool-repo#1",
			wantRepo:   "org/my-cool-repo",
			wantNumber: 1,
		},
		{
			name:       "missing hash",
			ref:        "owner/repo",
			wantRepo:   "",
			wantNumber: 0,
		},
		{
			name:       "empty string",
			ref:        "",
			wantRepo:   "",
			wantNumber: 0,
		},
		{
			name:       "missing repo",
			ref:        "#42",
			wantRepo:   "",
			wantNumber: 42,
		},
		{
			name:       "PR number zero",
			ref:        "owner/repo#0",
			wantRepo:   "owner/repo",
			wantNumber: 0,
		},
		{
			name:       "non-numeric PR number",
			ref:        "owner/repo#abc",
			wantRepo:   "owner/repo",
			wantNumber: 0,
		},
		{
			name:       "trailing garbage after number rejected",
			ref:        "owner/repo#42abc",
			wantRepo:   "owner/repo",
			wantNumber: 0, // strconv.Atoi rejects "42abc"
		},
		{
			name:       "multiple hash characters",
			ref:        "owner/repo#42#extra",
			wantRepo:   "owner/repo",
			wantNumber: 0, // strconv.Atoi rejects "42#extra"
		},
		{
			name:       "large PR number",
			ref:        "owner/repo#99999",
			wantRepo:   "owner/repo",
			wantNumber: 99999,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, number := parseGitHubPRRef(tc.ref)
			if repo != tc.wantRepo {
				t.Errorf("repo: got %q, want %q", repo, tc.wantRepo)
			}
			if number != tc.wantNumber {
				t.Errorf("number: got %d, want %d", number, tc.wantNumber)
			}
		})
	}
}
