package beads

import (
	"strings"
	"testing"
)

// TestIsBeadID verifies that IsBeadID correctly identifies bead IDs.
func TestIsBeadID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"bead-abc1", true},
		{"bead-xyz", true},
		{"bd-abc1", true},
		{"bd-xyz", true},
		{"ins-abc1", false},
		{"thr-abc1", false},
		{"", false},
		{"random", false},
		{"bead", false},
		{"bd", false},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := IsBeadID(tt.id)
			if got != tt.want {
				t.Errorf("IsBeadID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

// TestIsInsightID verifies that IsInsightID correctly identifies insight IDs.
// Implementation: len(id) > 4 && id[:4] == "ins-"
func TestIsInsightID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"ins-xxxx", true},
		{"ins-abc1", true},
		{"ins-a", true},    // len=5, just over the threshold
		{"ins-", false},    // len==4, not > 4
		{"ins", false},     // len==3, too short
		{"", false},
		{"thr-abc1", false},
		{"bead-abc1", false},
		{"random", false},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := IsInsightID(tt.id)
			if got != tt.want {
				t.Errorf("IsInsightID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

// TestIsThreadID verifies that IsThreadID correctly identifies thread IDs.
// Implementation: len(id) > 4 && id[:4] == "thr-"
func TestIsThreadID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"thr-xxxx", true},
		{"thr-abc1", true},
		{"thr-a", true},    // len=5, just over the threshold
		{"thr-", false},    // len==4, not > 4
		{"thr", false},     // len==3, too short
		{"", false},
		{"ins-abc1", false},
		{"bead-abc1", false},
		{"random", false},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := IsThreadID(tt.id)
			if got != tt.want {
				t.Errorf("IsThreadID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

// TestParseExternalRef verifies that ParseExternalRef correctly parses all
// supported external reference formats and returns errors for invalid inputs.
func TestParseExternalRef(t *testing.T) {
	tests := []struct {
		name      string
		ref       string
		wantSys   string
		wantID    string
		wantErr   bool
	}{
		// Linear
		{
			name:    "linear ref",
			ref:     "linear:ENG-456",
			wantSys: "linear",
			wantID:  "ENG-456",
		},
		// GitHub long prefix
		{
			name:    "github ref",
			ref:     "github:owner/repo#123",
			wantSys: "github",
			wantID:  "owner/repo#123",
		},
		// GitHub short prefix
		{
			name:    "gh short ref",
			ref:     "gh:owner/repo#42",
			wantSys: "github",
			wantID:  "owner/repo#42",
		},
		// Jira
		{
			name:    "jira ref",
			ref:     "jira:PROJ-789",
			wantSys: "jira",
			wantID:  "PROJ-789",
		},
		// Notion
		{
			name:    "notion ref",
			ref:     "notion:abcdef01-2345-6789-abcd-ef0123456789",
			wantSys: "notion",
			wantID:  "abcdef01-2345-6789-abcd-ef0123456789",
		},
		// Bead
		{
			name:    "bead ref",
			ref:     "bead:abc1",
			wantSys: "bead",
			wantID:  "abc1",
		},
		// Generic fallback
		{
			name:    "custom generic ref",
			ref:     "custom:something",
			wantSys: "custom",
			wantID:  "something",
		},
		// Invalid: empty string
		{
			name:    "empty string",
			ref:     "",
			wantErr: true,
		},
		// Invalid: no colon
		{
			name:    "no colon",
			ref:     "nocolon",
			wantErr: true,
		},
		// Invalid: empty system part
		{
			name:    "empty system part",
			ref:     ":empty",
			wantErr: true,
		},
		// Invalid: empty ID part
		{
			name:    "empty id part",
			ref:     "empty:",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseExternalRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseExternalRef(%q) = %+v, nil error; want error", tt.ref, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseExternalRef(%q) returned unexpected error: %v", tt.ref, err)
			}
			if got.System != tt.wantSys {
				t.Errorf("ParseExternalRef(%q).System = %q, want %q", tt.ref, got.System, tt.wantSys)
			}
			if got.ID != tt.wantID {
				t.Errorf("ParseExternalRef(%q).ID = %q, want %q", tt.ref, got.ID, tt.wantID)
			}
			if got.Raw != strings.TrimSpace(tt.ref) {
				t.Errorf("ParseExternalRef(%q).Raw = %q, want %q", tt.ref, got.Raw, strings.TrimSpace(tt.ref))
			}
		})
	}
}

// TestIsExternalRef verifies that IsExternalRef returns true for valid external
// references and false for invalid ones.
func TestIsExternalRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"linear:ENG-456", true},
		{"github:owner/repo#123", true},
		{"gh:owner/repo#42", true},
		{"jira:PROJ-789", true},
		{"notion:abcdef01-2345-6789-abcd-ef0123456789", true},
		{"bead:abc1", true},
		{"custom:something", true},
		{"", false},
		{"nocolon", false},
		{":empty", false},
		{"empty:", false},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := IsExternalRef(tt.ref)
			if got != tt.want {
				t.Errorf("IsExternalRef(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

// TestFormatExternalRef verifies that FormatExternalRef produces the correct
// display string for each system type.
func TestFormatExternalRef(t *testing.T) {
	tests := []struct {
		name string
		ref  *ExternalRef
		want string
	}{
		{
			name: "github",
			ref:  &ExternalRef{System: "github", ID: "owner/repo#123"},
			want: "GitHub: owner/repo#123",
		},
		{
			name: "linear",
			ref:  &ExternalRef{System: "linear", ID: "ENG-456"},
			want: "Linear: ENG-456",
		},
		{
			name: "jira",
			ref:  &ExternalRef{System: "jira", ID: "PROJ-789"},
			want: "Jira: PROJ-789",
		},
		{
			name: "notion",
			ref:  &ExternalRef{System: "notion", ID: "abcdef01-2345-6789-abcd-ef0123456789"},
			want: "Notion: abcdef01-2345-6789-abcd-ef0123456789",
		},
		{
			name: "bead",
			ref:  &ExternalRef{System: "bead", ID: "abc1"},
			want: "Bead: bd-abc1",
		},
		{
			name: "unknown/custom system",
			ref:  &ExternalRef{System: "custom", ID: "something"},
			want: "custom: something",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatExternalRef(tt.ref)
			if got != tt.want {
				t.Errorf("FormatExternalRef(%+v) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

// TestBeadIDToExternalRef verifies that BeadIDToExternalRef normalizes all
// bead ID formats to the canonical "bead:<id>" external ref format.
func TestBeadIDToExternalRef(t *testing.T) {
	tests := []struct {
		beadID string
		want   string
	}{
		{"bd-abc1", "bead:abc1"},
		{"bead-abc1", "bead:abc1"},
		{"abc1", "bead:abc1"},
		{"bd-xyz", "bead:xyz"},
		{"bead-xyz", "bead:xyz"},
		{"xyz", "bead:xyz"},
	}

	for _, tt := range tests {
		t.Run(tt.beadID, func(t *testing.T) {
			got := BeadIDToExternalRef(tt.beadID)
			if got != tt.want {
				t.Errorf("BeadIDToExternalRef(%q) = %q, want %q", tt.beadID, got, tt.want)
			}
		})
	}
}

// TestResolveRefType verifies that ResolveRefType categorizes references correctly.
func TestResolveRefType(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"thr-xxxx", "thread"},
		{"thr-abc1", "thread"},
		{"bd-xxxx", "bead"},
		{"bead-xxxx", "bead"},
		{"linear:ENG-1", "external"},
		{"github:owner/repo#1", "external"},
		{"custom:something", "external"},
		{"random", "unknown"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := ResolveRefType(tt.ref)
			if got != tt.want {
				t.Errorf("ResolveRefType(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

// TestInvalidRefError verifies that InvalidRefError.Error() includes the ref string.
func TestInvalidRefError(t *testing.T) {
	ref := "bad-ref-value"
	err := &InvalidRefError{Ref: ref}
	msg := err.Error()
	if !strings.Contains(msg, ref) {
		t.Errorf("InvalidRefError.Error() = %q, expected it to contain %q", msg, ref)
	}
}
