package github

import (
	"strings"
	"testing"
)

func TestCLIError_Error(t *testing.T) {
	tests := []struct {
		name        string
		err         *CLIError
		wantContain string
	}{
		{
			name:        "with stderr",
			err:         &CLIError{Command: "gh pr view", ExitCode: 1, Stderr: "not found"},
			wantContain: "not found",
		},
		{
			name:        "without stderr",
			err:         &CLIError{Command: "gh pr view 42", ExitCode: 2},
			wantContain: "gh pr view 42",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			if !strings.Contains(msg, tc.wantContain) {
				t.Errorf("Error() = %q; want it to contain %q", msg, tc.wantContain)
			}
		})
	}
}

func TestNotInstalledError(t *testing.T) {
	err := &NotInstalledError{}
	if !strings.Contains(err.Error(), "gh cli not installed") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestNotAuthenticatedError(t *testing.T) {
	err := &NotAuthenticatedError{}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestParsePRJSON(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		wantNumber int
		wantTitle  string
		wantRepo   string
		wantState  string
		wantErr    bool
	}{
		{
			name: "full JSON",
			data: `{
				"number": 42,
				"title": "Add feature X",
				"state": "OPEN",
				"url": "https://github.com/owner/repo/pull/42",
				"headRepositoryOwner": {"login": "owner"},
				"headRepository": {"name": "repo"}
			}`,
			wantNumber: 42,
			wantTitle:  "Add feature X",
			wantRepo:   "owner/repo",
			wantState:  "OPEN",
		},
		{
			name: "missing owner/repo",
			data: `{
				"number": 7,
				"title": "Quick fix",
				"state": "MERGED",
				"url": "https://github.com/foo/bar/pull/7"
			}`,
			wantNumber: 7,
			wantTitle:  "Quick fix",
			wantRepo:   "",
			wantState:  "MERGED",
		},
		{
			name:    "invalid JSON",
			data:    "not json",
			wantErr: true,
		},
		{
			name:       "empty object",
			data:       "{}",
			wantNumber: 0,
			wantTitle:  "",
			wantRepo:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pr, err := parsePRJSON([]byte(tc.data))
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pr.Number != tc.wantNumber {
				t.Errorf("Number: got %d, want %d", pr.Number, tc.wantNumber)
			}
			if pr.Title != tc.wantTitle {
				t.Errorf("Title: got %q, want %q", pr.Title, tc.wantTitle)
			}
			if pr.Repo != tc.wantRepo {
				t.Errorf("Repo: got %q, want %q", pr.Repo, tc.wantRepo)
			}
			if tc.wantState != "" && pr.State != tc.wantState {
				t.Errorf("State: got %q, want %q", pr.State, tc.wantState)
			}
		})
	}
}
