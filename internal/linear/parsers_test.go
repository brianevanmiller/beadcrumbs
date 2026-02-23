package linear

import (
	"testing"
)

// --- parseSchpetJSON ---

func TestParseSchpetJSON(t *testing.T) {
	tests := []struct {
		name        string
		data        string
		fallbackID  string
		wantID      string
		wantTitle   string
		wantStatus  string
		wantDesc    string
		wantURL     string
		wantNonNil  bool
	}{
		{
			name: "full JSON with all fields including nested state",
			data: `{
				"identifier": "ENG-123",
				"title": "Fix the thing",
				"description": "Detailed description",
				"url": "https://linear.app/issue/ENG-123",
				"state": {"name": "In Progress"}
			}`,
			fallbackID: "FALLBACK-1",
			wantID:     "ENG-123",
			wantTitle:  "Fix the thing",
			wantStatus: "In Progress",
			wantDesc:   "Detailed description",
			wantURL:    "https://linear.app/issue/ENG-123",
			wantNonNil: true,
		},
		{
			name:        "partial JSON with only title",
			data:        `{"title": "Only Title"}`,
			fallbackID:  "FALLBACK-2",
			wantID:      "FALLBACK-2",
			wantTitle:   "Only Title",
			wantStatus:  "",
			wantDesc:    "",
			wantURL:     "",
			wantNonNil:  true,
		},
		{
			name:       "invalid JSON falls back to parseSchpetText",
			data:       "Title: My Text Issue\nStatus: Done",
			fallbackID: "FALLBACK-3",
			wantNonNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseSchpetJSON([]byte(tc.data), tc.fallbackID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantNonNil {
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result, got nil")
			}
			// For the invalid JSON case we only verify non-nil; the text parser
			// behavior is covered by TestParseSchpetText.
			if tc.wantID == "" {
				return
			}
			if result.ID != tc.wantID {
				t.Errorf("ID: got %q, want %q", result.ID, tc.wantID)
			}
			if result.Title != tc.wantTitle {
				t.Errorf("Title: got %q, want %q", result.Title, tc.wantTitle)
			}
			if result.Status != tc.wantStatus {
				t.Errorf("Status: got %q, want %q", result.Status, tc.wantStatus)
			}
			if result.Description != tc.wantDesc {
				t.Errorf("Description: got %q, want %q", result.Description, tc.wantDesc)
			}
			if result.URL != tc.wantURL {
				t.Errorf("URL: got %q, want %q", result.URL, tc.wantURL)
			}
		})
	}
}

// --- parseSchpetText ---

func TestParseSchpetText(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		fallbackID string
		wantID     string
		wantTitle  string
		wantStatus string
	}{
		{
			name:       "input with Title and Status lines",
			data:       "Title: My Issue\nStatus: In Progress",
			fallbackID: "ENG-999",
			wantID:     "ENG-999",
			wantTitle:  "My Issue",
			wantStatus: "In Progress",
		},
		{
			name:       "input with no matching lines",
			data:       "some random output\nwith no headers",
			fallbackID: "ENG-000",
			wantID:     "ENG-000",
			wantTitle:  "",
			wantStatus: "",
		},
		{
			name:       "empty input",
			data:       "",
			fallbackID: "ENG-001",
			wantID:     "ENG-001",
			wantTitle:  "",
			wantStatus: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseSchpetText([]byte(tc.data), tc.fallbackID)
			if result == nil {
				t.Fatal("expected non-nil result, got nil")
			}
			if result.ID != tc.wantID {
				t.Errorf("ID: got %q, want %q", result.ID, tc.wantID)
			}
			if result.Title != tc.wantTitle {
				t.Errorf("Title: got %q, want %q", result.Title, tc.wantTitle)
			}
			if result.Status != tc.wantStatus {
				t.Errorf("Status: got %q, want %q", result.Status, tc.wantStatus)
			}
		})
	}
}

// --- parseFinessseeJSON ---

func TestParseFinessseeJSON(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		fallbackID string
		wantID     string
		wantTitle  string
		wantStatus string
		wantDesc   string
		wantURL    string
	}{
		{
			name: "full JSON with all fields",
			data: `{
				"identifier": "ENG-200",
				"title": "Finesssee Issue",
				"description": "A finesssee description",
				"url": "https://linear.app/issue/ENG-200",
				"state": {"name": "Todo"}
			}`,
			fallbackID: "FALL-200",
			wantID:     "ENG-200",
			wantTitle:  "Finesssee Issue",
			wantStatus: "Todo",
			wantDesc:   "A finesssee description",
			wantURL:    "https://linear.app/issue/ENG-200",
		},
		{
			name:       "invalid JSON returns fallback issue",
			data:       "not json at all",
			fallbackID: "FALL-BAD",
			wantID:     "FALL-BAD",
			wantTitle:  "",
			wantStatus: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseFinessseeJSON([]byte(tc.data), tc.fallbackID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result, got nil")
			}
			if result.ID != tc.wantID {
				t.Errorf("ID: got %q, want %q", result.ID, tc.wantID)
			}
			if result.Title != tc.wantTitle {
				t.Errorf("Title: got %q, want %q", result.Title, tc.wantTitle)
			}
			if result.Status != tc.wantStatus {
				t.Errorf("Status: got %q, want %q", result.Status, tc.wantStatus)
			}
			if result.Description != tc.wantDesc {
				t.Errorf("Description: got %q, want %q", result.Description, tc.wantDesc)
			}
			if result.URL != tc.wantURL {
				t.Errorf("URL: got %q, want %q", result.URL, tc.wantURL)
			}
		})
	}
}

// --- parseLinearisJSON ---

func TestParseLinearisJSON(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		fallbackID string
		wantID     string
		wantTitle  string
		wantStatus string
		wantDesc   string
		wantURL    string
	}{
		{
			name: "full JSON with all fields",
			data: `{
				"identifier": "ENG-300",
				"title": "Linearis Issue",
				"description": "A linearis description",
				"url": "https://linear.app/issue/ENG-300",
				"state": {"name": "In Review"}
			}`,
			fallbackID: "FALL-300",
			wantID:     "ENG-300",
			wantTitle:  "Linearis Issue",
			wantStatus: "In Review",
			wantDesc:   "A linearis description",
			wantURL:    "https://linear.app/issue/ENG-300",
		},
		{
			name:       "invalid JSON returns fallback issue",
			data:       "{bad json}",
			fallbackID: "FALL-BAD",
			wantID:     "FALL-BAD",
			wantTitle:  "",
			wantStatus: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseLinearisJSON([]byte(tc.data), tc.fallbackID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result, got nil")
			}
			if result.ID != tc.wantID {
				t.Errorf("ID: got %q, want %q", result.ID, tc.wantID)
			}
			if result.Title != tc.wantTitle {
				t.Errorf("Title: got %q, want %q", result.Title, tc.wantTitle)
			}
			if result.Status != tc.wantStatus {
				t.Errorf("Status: got %q, want %q", result.Status, tc.wantStatus)
			}
			if result.Description != tc.wantDesc {
				t.Errorf("Description: got %q, want %q", result.Description, tc.wantDesc)
			}
			if result.URL != tc.wantURL {
				t.Errorf("URL: got %q, want %q", result.URL, tc.wantURL)
			}
		})
	}
}

// --- adapterForTool ---

func TestAdapterForTool(t *testing.T) {
	tests := []struct {
		toolName string
		wantName string
	}{
		{"schpet", "schpet"},
		{"finesssee", "finesssee"},
		{"linearis", "linearis"},
		{"unknown", "schpet"}, // falls back to SchpetAdapter
	}

	for _, tc := range tests {
		t.Run(tc.toolName, func(t *testing.T) {
			adapter, err := adapterForTool(tc.toolName, "/usr/bin/dummy", "key")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if adapter == nil {
				t.Fatal("expected non-nil adapter, got nil")
			}
			if adapter.Name() != tc.wantName {
				t.Errorf("Name(): got %q, want %q", adapter.Name(), tc.wantName)
			}
		})
	}
}
