package linear

import (
	"encoding/json"
	"strings"
)

// SchpetAdapter wraps the @schpet/linear-cli tool (binary: "linear").
type SchpetAdapter struct {
	binPath string
	apiKey  string
}

// NewSchpetAdapter creates an adapter for @schpet/linear-cli.
func NewSchpetAdapter(binPath, apiKey string) *SchpetAdapter {
	return &SchpetAdapter{binPath: binPath, apiKey: apiKey}
}

func (a *SchpetAdapter) Name() string    { return "schpet" }
func (a *SchpetAdapter) BinPath() string { return a.binPath }

func (a *SchpetAdapter) ViewIssue(issueID string) (*LinearIssue, error) {
	out, err := runCmd(a.binPath, a.apiKey, "issue", "view", issueID, "--json", "--no-comments")
	if err != nil {
		return nil, err
	}
	return parseSchpetJSON(out, issueID)
}

func (a *SchpetAdapter) AddComment(issueID, body string) error {
	_, err := runCmd(a.binPath, a.apiKey, "issue", "comment", "add", issueID, "-b", body)
	return err
}

func (a *SchpetAdapter) CheckAuth() error {
	_, err := runCmd(a.binPath, a.apiKey, "auth", "whoami")
	if err != nil {
		return &NotAuthenticatedError{Tool: "schpet"}
	}
	return nil
}

// parseSchpetJSON parses the JSON output from `linear issue view --json`.
func parseSchpetJSON(data []byte, fallbackID string) (*LinearIssue, error) {
	// Try structured parse first
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Fall back to line-based parsing if JSON fails
		return parseSchpetText(data, fallbackID), nil
	}

	issue := &LinearIssue{ID: fallbackID}

	if v, ok := raw["identifier"].(string); ok {
		issue.ID = v
	}
	if v, ok := raw["title"].(string); ok {
		issue.Title = v
	}
	if v, ok := raw["description"].(string); ok {
		issue.Description = v
	}
	if v, ok := raw["url"].(string); ok {
		issue.URL = v
	}
	// State may be nested
	if state, ok := raw["state"].(map[string]interface{}); ok {
		if v, ok := state["name"].(string); ok {
			issue.Status = v
		}
	}

	return issue, nil
}

// parseSchpetText extracts issue info from non-JSON text output.
func parseSchpetText(data []byte, fallbackID string) *LinearIssue {
	issue := &LinearIssue{ID: fallbackID}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Title:") {
			issue.Title = strings.TrimSpace(strings.TrimPrefix(line, "Title:"))
		} else if strings.HasPrefix(line, "Status:") {
			issue.Status = strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
		}
	}
	return issue
}
