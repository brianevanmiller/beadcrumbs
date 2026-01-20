// Package beads provides integration with the beads issue tracker.
package beads

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ExternalRef represents a parsed external reference.
type ExternalRef struct {
	System string // e.g., "linear", "github", "jira"
	ID     string // e.g., "TASK-123", "owner/repo#42"
	Raw    string // Original input
}

// External reference patterns
var (
	// linear:TASK-123 or linear:ENG-456
	linearPattern = regexp.MustCompile(`^linear:([A-Z]+-\d+)$`)

	// github:owner/repo#123 or gh:owner/repo#123
	githubPattern = regexp.MustCompile(`^(?:github|gh):([^#]+)#(\d+)$`)

	// jira:PROJECT-123
	jiraPattern = regexp.MustCompile(`^jira:([A-Z]+-\d+)$`)

	// notion:page-id
	notionPattern = regexp.MustCompile(`^notion:([a-f0-9-]+)$`)
)

// BeadsPresent checks if a .beads/ directory exists in the current directory
// or any parent directory.
func BeadsPresent() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}

	for {
		beadsPath := filepath.Join(dir, ".beads")
		if info, err := os.Stat(beadsPath); err == nil && info.IsDir() {
			return true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root
			break
		}
		dir = parent
	}

	return false
}

// GetBeadsDir returns the path to the .beads/ directory, or empty string if not found.
func GetBeadsDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		beadsPath := filepath.Join(dir, ".beads")
		if info, err := os.Stat(beadsPath); err == nil && info.IsDir() {
			return beadsPath
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return ""
}

// IsBeadID checks if an ID looks like a bead ID (starts with "bead-" or "bd-").
func IsBeadID(id string) bool {
	return len(id) > 5 && (id[:5] == "bead-" || id[:3] == "bd-")
}

// IsInsightID checks if an ID looks like an insight ID (starts with "ins-").
func IsInsightID(id string) bool {
	return len(id) > 4 && id[:4] == "ins-"
}

// IsThreadID checks if an ID looks like a thread ID (starts with "thr-").
func IsThreadID(id string) bool {
	return len(id) > 4 && id[:4] == "thr-"
}

// IsExternalRef checks if a string looks like an external reference.
// External refs have the format "system:identifier" (e.g., "linear:TASK-123").
func IsExternalRef(ref string) bool {
	if !strings.Contains(ref, ":") {
		return false
	}
	_, err := ParseExternalRef(ref)
	return err == nil
}

// ParseExternalRef parses an external reference string.
// Supported formats:
//   - linear:TASK-123
//   - github:owner/repo#123 (or gh:owner/repo#123)
//   - jira:PROJECT-123
//   - notion:page-id
//   - system:anything (generic fallback)
func ParseExternalRef(ref string) (*ExternalRef, error) {
	ref = strings.TrimSpace(ref)

	// Try known patterns first
	if matches := linearPattern.FindStringSubmatch(ref); matches != nil {
		return &ExternalRef{
			System: "linear",
			ID:     matches[1],
			Raw:    ref,
		}, nil
	}

	if matches := githubPattern.FindStringSubmatch(ref); matches != nil {
		return &ExternalRef{
			System: "github",
			ID:     matches[1] + "#" + matches[2],
			Raw:    ref,
		}, nil
	}

	if matches := jiraPattern.FindStringSubmatch(ref); matches != nil {
		return &ExternalRef{
			System: "jira",
			ID:     matches[1],
			Raw:    ref,
		}, nil
	}

	if matches := notionPattern.FindStringSubmatch(ref); matches != nil {
		return &ExternalRef{
			System: "notion",
			ID:     matches[1],
			Raw:    ref,
		}, nil
	}

	// Generic fallback: system:anything
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return &ExternalRef{
			System: parts[0],
			ID:     parts[1],
			Raw:    ref,
		}, nil
	}

	return nil, &InvalidRefError{Ref: ref}
}

// InvalidRefError indicates an invalid external reference format.
type InvalidRefError struct {
	Ref string
}

func (e *InvalidRefError) Error() string {
	return "invalid external reference format: " + e.Ref
}

// FormatExternalRef formats an external reference for display.
func FormatExternalRef(ref *ExternalRef) string {
	switch ref.System {
	case "github":
		return "GitHub: " + ref.ID
	case "linear":
		return "Linear: " + ref.ID
	case "jira":
		return "Jira: " + ref.ID
	case "notion":
		return "Notion: " + ref.ID
	default:
		return ref.System + ": " + ref.ID
	}
}

// ResolveRef determines the type of reference and returns categorization.
// Returns: "thread", "bead", "external", or "unknown"
func ResolveRefType(ref string) string {
	if IsThreadID(ref) {
		return "thread"
	}
	if IsBeadID(ref) {
		return "bead"
	}
	if IsExternalRef(ref) {
		return "external"
	}
	return "unknown"
}
