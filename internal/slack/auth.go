package slack

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	defaultPort  = "9876"
	authTimeout  = 120 * time.Second
	slackAuthURL = "https://slack.com/oauth/v2/authorize"
)

// OAuthConfig holds the Slack app credentials needed for the OAuth flow.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// OAuthResult contains the tokens returned from a successful OAuth flow.
type OAuthResult struct {
	BotToken  string
	TeamID    string
	TeamName  string
	BotUserID string
	Scope     string
}

// RunOAuthFlow starts the OAuth v2 flow. It tries to start a local HTTP server
// on port 9876 for automatic callback handling. If that fails, it falls back to
// manual code entry where the user pastes the redirect URL.
func RunOAuthFlow(config OAuthConfig) (*OAuthResult, error) {
	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	// Try local server first
	listener, err := net.Listen("tcp", "127.0.0.1:"+defaultPort)
	if err != nil {
		// Fallback to manual mode
		return runManualFlow(config, state)
	}

	return runServerFlow(config, state, listener)
}

// runServerFlow handles the OAuth flow using a local HTTP server.
func runServerFlow(config OAuthConfig, state string, listener net.Listener) (*OAuthResult, error) {
	redirectURI := fmt.Sprintf("http://127.0.0.1:%s/callback", defaultPort)
	authorizeURL := buildAuthorizeURL(config, state, redirectURI)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch: possible CSRF attack")
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}

		if errParam := r.URL.Query().Get("error"); errParam != "" {
			errCh <- fmt.Errorf("slack OAuth error: %s", errParam)
			fmt.Fprintf(w, "<html><body><h2>Authorization failed: %s</h2><p>You can close this window.</p></body></html>", errParam)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no authorization code received")
			http.Error(w, "No code received", http.StatusBadRequest)
			return
		}

		fmt.Fprint(w, `<html><body><h2>Authorization successful!</h2><p>You can close this window and return to your terminal.</p></body></html>`)
		codeCh <- code
	})

	server := &http.Server{Handler: mux}

	// Start server in background
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Open browser
	fmt.Printf("Opening browser for Slack authorization...\n")
	fmt.Printf("If the browser doesn't open, visit this URL:\n  %s\n\n", authorizeURL)
	openBrowser(authorizeURL)

	// Wait for callback or timeout
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()

	var code string
	select {
	case code = <-codeCh:
		// Success
	case err := <-errCh:
		server.Shutdown(ctx)
		return nil, err
	case <-ctx.Done():
		server.Shutdown(ctx)
		return nil, fmt.Errorf("OAuth flow timed out after %s", authTimeout)
	}

	server.Shutdown(ctx)

	// Exchange code for token
	return exchangeCode(config, code, redirectURI)
}

// runManualFlow handles the OAuth flow by prompting the user to paste the redirect URL.
func runManualFlow(config OAuthConfig, state string) (*OAuthResult, error) {
	// Use a placeholder redirect_uri — the user will paste the full URL back
	redirectURI := "http://127.0.0.1:" + defaultPort + "/callback"
	authorizeURL := buildAuthorizeURL(config, state, redirectURI)

	fmt.Printf("Could not start local server on port %s.\n\n", defaultPort)
	fmt.Printf("Please:\n")
	fmt.Printf("1. Open this URL in your browser:\n   %s\n\n", authorizeURL)
	fmt.Printf("2. After authorizing, paste the redirect URL here: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return nil, fmt.Errorf("failed to read input")
	}
	input := strings.TrimSpace(scanner.Text())

	// Parse code from the pasted URL
	parsedURL, err := url.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	code := parsedURL.Query().Get("code")
	if code == "" {
		// Maybe they just pasted the code directly
		code = input
	}

	if parsedState := parsedURL.Query().Get("state"); parsedState != "" && parsedState != state {
		return nil, fmt.Errorf("state mismatch: possible CSRF attack")
	}

	return exchangeCode(config, code, redirectURI)
}

// exchangeCode exchanges an authorization code for access tokens.
func exchangeCode(config OAuthConfig, code, redirectURI string) (*OAuthResult, error) {
	endpoint := slackAPIBase + "/oauth.v2.access"

	params := url.Values{
		"client_id":     {config.ClientID},
		"client_secret": {config.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}

	req, err := http.NewRequest("POST", endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
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
		return nil, fmt.Errorf("failed to parse token response: %w", err)
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

// buildAuthorizeURL constructs the Slack OAuth authorize URL.
func buildAuthorizeURL(config OAuthConfig, state, redirectURI string) string {
	params := url.Values{
		"client_id":    {config.ClientID},
		"scope":        {strings.Join(config.Scopes, ",")},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	return slackAuthURL + "?" + params.Encode()
}

// generateState creates a random state string for CSRF protection.
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// openBrowser opens a URL in the default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	_ = cmd.Start()
}
