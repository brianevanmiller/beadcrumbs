package linear

import (
	"strings"
	"testing"
)

// --- CLIError.Error() ---

func TestCLIError_Error(t *testing.T) {
	tests := []struct {
		name        string
		err         *CLIError
		wantContain string
	}{
		{
			name: "with Stderr set — message includes stderr text",
			err: &CLIError{
				Command:  "linear issue view ENG-1",
				ExitCode: 1,
				Stderr:   "authentication required",
			},
			wantContain: "authentication required",
		},
		{
			name: "without Stderr — message includes command",
			err: &CLIError{
				Command:  "linear issue view ENG-2",
				ExitCode: 2,
				Stderr:   "",
			},
			wantContain: "linear issue view ENG-2",
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

// --- NotInstalledError.Error() ---

func TestNotInstalledError_Error(t *testing.T) {
	tests := []struct {
		tool string
	}{
		{"schpet"},
		{"finesssee"},
		{"linearis"},
		{"any"},
	}

	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			err := &NotInstalledError{Tool: tc.tool}
			msg := err.Error()
			if !strings.Contains(msg, tc.tool) {
				t.Errorf("Error() = %q; want it to contain tool name %q", msg, tc.tool)
			}
		})
	}
}

// --- NotAuthenticatedError.Error() ---

func TestNotAuthenticatedError_Error(t *testing.T) {
	tests := []struct {
		tool string
	}{
		{"schpet"},
		{"finesssee"},
		{"linearis"},
	}

	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			err := &NotAuthenticatedError{Tool: tc.tool}
			msg := err.Error()
			if !strings.Contains(msg, tc.tool) {
				t.Errorf("Error() = %q; want it to contain tool name %q", msg, tc.tool)
			}
		})
	}
}

// --- Adapter constructors ---

func TestNewSchpetAdapter(t *testing.T) {
	binPath := "/usr/bin/linear"
	apiKey := "key123"
	a := NewSchpetAdapter(binPath, apiKey)

	if a.Name() != "schpet" {
		t.Errorf("Name(): got %q, want %q", a.Name(), "schpet")
	}
	if a.BinPath() != binPath {
		t.Errorf("BinPath(): got %q, want %q", a.BinPath(), binPath)
	}
}

func TestNewFinessseeAdapter(t *testing.T) {
	binPath := "/usr/bin/linear-cli"
	apiKey := "key123"
	a := NewFinessseeAdapter(binPath, apiKey)

	if a.Name() != "finesssee" {
		t.Errorf("Name(): got %q, want %q", a.Name(), "finesssee")
	}
	if a.BinPath() != binPath {
		t.Errorf("BinPath(): got %q, want %q", a.BinPath(), binPath)
	}
}

func TestNewLinearisAdapter(t *testing.T) {
	binPath := "/usr/bin/linearis"
	apiKey := "key123"
	a := NewLinearisAdapter(binPath, apiKey)

	if a.Name() != "linearis" {
		t.Errorf("Name(): got %q, want %q", a.Name(), "linearis")
	}
	if a.BinPath() != binPath {
		t.Errorf("BinPath(): got %q, want %q", a.BinPath(), binPath)
	}
}
