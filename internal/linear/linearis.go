package linear

import (
	"encoding/json"
)

// LinearisAdapter wraps the Linearis CLI tool (binary: "linearis").
type LinearisAdapter struct {
	binPath string
	apiKey  string
}

// NewLinearisAdapter creates an adapter for Linearis.
func NewLinearisAdapter(binPath, apiKey string) *LinearisAdapter {
	return &LinearisAdapter{binPath: binPath, apiKey: apiKey}
}

func (a *LinearisAdapter) Name() string    { return "linearis" }
func (a *LinearisAdapter) BinPath() string { return a.binPath }

func (a *LinearisAdapter) ViewIssue(issueID string) (*LinearIssue, error) {
	// Linearis outputs JSON by default
	out, err := runCmd(a.binPath, a.apiKey, "issues", "read", issueID)
	if err != nil {
		return nil, err
	}
	return parseLinearisJSON(out, issueID)
}

func (a *LinearisAdapter) AddComment(issueID, body string) error {
	_, err := runCmd(a.binPath, a.apiKey, "comments", "create", issueID, "--body", body)
	return err
}

func (a *LinearisAdapter) CheckAuth() error {
	// Linearis doesn't have a dedicated auth check; test with a minimal API call
	_, err := runCmd(a.binPath, a.apiKey, "issues", "list", "-l", "1")
	if err != nil {
		return &NotAuthenticatedError{Tool: "linearis"}
	}
	return nil
}

// parseLinearisJSON parses the JSON output from `linearis issues read`.
func parseLinearisJSON(data []byte, fallbackID string) (*LinearIssue, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return &LinearIssue{ID: fallbackID}, nil
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
	if state, ok := raw["state"].(map[string]interface{}); ok {
		if v, ok := state["name"].(string); ok {
			issue.Status = v
		}
	}

	return issue, nil
}
