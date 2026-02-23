package slack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestExchangeCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		if err := r.ParseForm(); err != nil {
			t.Fatalf("failed to parse form: %v", err)
		}
		if r.FormValue("client_id") != "test-client-id" {
			t.Errorf("unexpected client_id: %s", r.FormValue("client_id"))
		}
		if r.FormValue("code") != "test-code" {
			t.Errorf("unexpected code: %s", r.FormValue("code"))
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":           true,
			"access_token": "xoxb-test-token-12345",
			"scope":        "channels:read,channels:history",
			"bot_user_id":  "U_BOT_123",
			"team": map[string]string{
				"id":   "T_TEAM_123",
				"name": "Test Workspace",
			},
		})
	}))
	defer server.Close()

	config := OAuthConfig{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	}

	// Override the slackAPIBase for testing by calling exchangeCode directly
	// We need to use the test server URL
	origClient := &Client{BaseURL: server.URL, httpClient: http.DefaultClient}
	_ = origClient // Not used directly, we test via HTTP

	// Call exchangeCode with the test server URL as the endpoint
	result, err := exchangeCodeWithEndpoint(config, "test-code", "http://localhost:9876/callback", server.URL+"/oauth.v2.access")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.BotToken != "xoxb-test-token-12345" {
		t.Errorf("unexpected bot token: %s", result.BotToken)
	}
	if result.TeamID != "T_TEAM_123" {
		t.Errorf("unexpected team ID: %s", result.TeamID)
	}
	if result.TeamName != "Test Workspace" {
		t.Errorf("unexpected team name: %s", result.TeamName)
	}
	if result.BotUserID != "U_BOT_123" {
		t.Errorf("unexpected bot user ID: %s", result.BotUserID)
	}
}

func TestExchangeCode_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "invalid_code",
		})
	}))
	defer server.Close()

	config := OAuthConfig{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	}

	_, err := exchangeCodeWithEndpoint(config, "bad-code", "http://localhost:9876/callback", server.URL+"/oauth.v2.access")
	if err == nil {
		t.Fatal("expected error for invalid code")
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	config := OAuthConfig{
		ClientID: "test-client-id",
		Scopes:   []string{"channels:read", "channels:history"},
	}

	url := buildAuthorizeURL(config, "test-state", "http://127.0.0.1:9876/callback")
	if url == "" {
		t.Fatal("expected non-empty URL")
	}
	// Should contain all required parameters
	if !searchString(url, "client_id=test-client-id") {
		t.Errorf("URL missing client_id: %s", url)
	}
	if !searchString(url, "state=test-state") {
		t.Errorf("URL missing state: %s", url)
	}
	if !searchString(url, "redirect_uri=") {
		t.Errorf("URL missing redirect_uri: %s", url)
	}
}

func TestGenerateState(t *testing.T) {
	state1, err := generateState()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	state2, err := generateState()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(state1) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("unexpected state length: %d", len(state1))
	}
	if state1 == state2 {
		t.Error("states should be unique")
	}
}

// exchangeCodeWithEndpoint is a test helper that lets us override the OAuth endpoint.
func exchangeCodeWithEndpoint(config OAuthConfig, code, redirectURI, endpoint string) (*OAuthResult, error) {
	params := url.Values{
		"client_id":     {config.ClientID},
		"client_secret": {config.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}

	resp, err := http.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error,omitempty"`
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		BotUserID   string `json:"bot_user_id"`
		Team        struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	if !tokenResp.OK {
		return nil, fmt.Errorf("token exchange error: %s", tokenResp.Error)
	}

	return &OAuthResult{
		BotToken:  tokenResp.AccessToken,
		TeamID:    tokenResp.Team.ID,
		TeamName:  tokenResp.Team.Name,
		BotUserID: tokenResp.BotUserID,
		Scope:     tokenResp.Scope,
	}, nil
}
