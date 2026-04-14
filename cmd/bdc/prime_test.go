package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestOutputPrimeContext(t *testing.T) {
	var buf bytes.Buffer
	outputPrimeContext(&buf)

	if buf.Len() == 0 {
		t.Error("outputPrimeContext produced no output")
	}
}

func TestOutputPrimeContext_ContainsKeyContent(t *testing.T) {
	var buf bytes.Buffer
	outputPrimeContext(&buf)

	output := buf.String()
	required := []string{
		"Beadcrumbs",
		"hypothesis",
		"discovery",
		"opus-4.6",
		"--thread",
		"--author",
		"bdc capture",
		"bdc thread",
	}

	for _, keyword := range required {
		if !strings.Contains(output, keyword) {
			t.Errorf("prime context missing required keyword %q", keyword)
		}
	}
}

func TestFindBeadcrumbsDir_Present(t *testing.T) {
	// Save and restore working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	if err := os.Mkdir(tmpDir+"/.beadcrumbs", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	got := findBeadcrumbsDir()
	// findBeadcrumbsDir returns absolute paths; on macOS /var -> /private/var
	if got == "" {
		t.Error("findBeadcrumbsDir() returned empty string, want non-empty path ending in .beadcrumbs")
	} else if !strings.HasSuffix(got, ".beadcrumbs") {
		t.Errorf("findBeadcrumbsDir() = %q, want path ending in .beadcrumbs", got)
	}
}

func TestFindBeadcrumbsDir_Absent(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	got := findBeadcrumbsDir()
	if got != "" {
		t.Errorf("findBeadcrumbsDir() = %q, want empty string", got)
	}
}
