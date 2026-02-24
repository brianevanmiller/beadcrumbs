package main

import (
	"os"
	"strings"
	"testing"
)

func TestContainsExactPattern(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		pattern  string
		expected bool
	}{
		{"exact match", "foo\nbar\nbaz", "bar", true},
		{"first line", "bar\nfoo", "bar", true},
		{"last line", "foo\nbar", "bar", true},
		{"partial no match", "foobar\nbaz", "bar", false},
		{"empty pattern", "foo\nbar", "", false},
		{"empty content", "", "bar", false},
		{"both empty", "", "", false},
		{"with whitespace", "  bar  \nfoo", "bar", true},
		{"trailing slash", ".beadcrumbs/\nfoo", ".beadcrumbs/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsExactPattern(tt.content, tt.pattern)
			if got != tt.expected {
				t.Errorf("containsExactPattern(%q, %q) = %v, want %v", tt.content, tt.pattern, got, tt.expected)
			}
		})
	}
}

func TestAddGitignoreEntries(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	if err := addGitignoreEntries(); err != nil {
		t.Fatalf("addGitignoreEntries() error: %v", err)
	}

	content, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatalf("failed to read .gitignore: %v", err)
	}

	s := string(content)
	if !strings.Contains(s, "beadcrumbs.db") {
		t.Error(".gitignore missing beadcrumbs.db entry")
	}
	if !strings.Contains(s, "beadcrumbs.db-journal") {
		t.Error(".gitignore missing beadcrumbs.db-journal entry")
	}
	if !strings.Contains(s, "beadcrumbs.db-wal") {
		t.Error(".gitignore missing beadcrumbs.db-wal entry")
	}
	if !strings.Contains(s, ".beadcrumbs/origin") {
		t.Error(".gitignore missing .beadcrumbs/origin entry")
	}
}

func TestAddGitignoreEntries_Idempotent(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Run twice
	if err := addGitignoreEntries(); err != nil {
		t.Fatal(err)
	}
	if err := addGitignoreEntries(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatal(err)
	}

	// Count occurrences of beadcrumbs.db — should appear only in the pattern lines
	count := strings.Count(string(content), ".beadcrumbs/beadcrumbs.db\n")
	if count > 1 {
		t.Errorf("found %d occurrences of beadcrumbs.db entry, expected 1", count)
	}
}

func TestAddGitignoreEntries_SkipsExisting(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Pre-create .gitignore with beadcrumbs.db already present
	existing := "node_modules/\nbeadcrumbs.db\n"
	if err := os.WriteFile(".gitignore", []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	if err := addGitignoreEntries(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatal(err)
	}

	// Should not have added new entries since beadcrumbs.db is already present
	if string(content) != existing {
		t.Errorf("expected .gitignore unchanged, got:\n%s", string(content))
	}
}
