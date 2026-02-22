package linear

import (
	"encoding/json"
)

// FinessseeAdapter wraps the Rust linear-cli tool (binary: "linear-cli").
type FinessseeAdapter struct {
	binPath string
	apiKey  string
}

// NewFinessseeAdapter creates an adapter for the Rust linear-cli.
func NewFinessseeAdapter(binPath, apiKey string) *FinessseeAdapter {
	return &FinessseeAdapter{binPath: binPath, apiKey: apiKey}
}

func (a *FinessseeAdapter) Name() string    { return "finesssee" }
func (a *FinessseeAdapter) BinPath() string { return a.binPath }

func (a *FinessseeAdapter) ViewIssue(issueID string) (*LinearIssue, error) {
	out, err := runCmd(a.binPath, a.apiKey, "i", "get", issueID, "--output", "json")
	if err != nil {
		return nil, err
	}
	return parseFinessseeJSON(out, issueID)
}

func (a *FinessseeAdapter) AddComment(issueID, body string) error {
	_, err := runCmd(a.binPath, a.apiKey, "i", "comment", issueID, "-b", body)
	return err
}

func (a *FinessseeAdapter) CheckAuth() error {
	_, err := runCmd(a.binPath, a.apiKey, "auth", "status")
	if err != nil {
		return &NotAuthenticatedError{Tool: "finesssee"}
	}
	return nil
}

// parseFinessseeJSON parses the JSON output from `linear-cli i get --output json`.
func parseFinessseeJSON(data []byte, fallbackID string) (*LinearIssue, error) {
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
