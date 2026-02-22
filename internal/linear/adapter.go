// Package linear provides adapters for various Linear CLI tools.
// It is the only place in the codebase that calls out to Linear CLI binaries.
package linear

// LinearIssue holds parsed issue data from any Linear CLI.
type LinearIssue struct {
	ID          string // "ENG-456"
	Title       string
	Status      string
	Description string
	URL         string
}

// Adapter defines the interface any Linear CLI must satisfy.
type Adapter interface {
	// Name returns the adapter identifier (e.g., "schpet", "finesssee", "linearis").
	Name() string

	// BinPath returns the resolved path to the CLI binary.
	BinPath() string

	// ViewIssue fetches issue details by ID (e.g., "ENG-456").
	ViewIssue(issueID string) (*LinearIssue, error)

	// AddComment posts a comment body to the given issue.
	AddComment(issueID, body string) error

	// CheckAuth verifies that the CLI is authenticated and can reach Linear.
	CheckAuth() error
}
