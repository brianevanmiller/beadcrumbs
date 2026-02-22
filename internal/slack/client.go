// Package slack provides a Slack Web API client for beadcrumbs integration.
// It uses Go stdlib only (net/http, encoding/json) with no external dependencies.
package slack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	slackAPIBase = "https://slack.com/api"
	maxRetries   = 3
)

// Client is a Slack Web API client using bot tokens.
type Client struct {
	token      string
	httpClient *http.Client
	// BaseURL can be overridden for testing.
	BaseURL string
}

// NewClient creates a new Slack API client with the given bot token.
func NewClient(token string) *Client {
	return &Client{
		token:   token,
		BaseURL: slackAPIBase,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// slackResponse is the base wrapper for all Slack API responses.
type slackResponse struct {
	OK               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	ResponseMetadata responseMetadata `json:"response_metadata,omitempty"`
}

type responseMetadata struct {
	NextCursor string `json:"next_cursor"`
}

// AuthTestResponse contains the result of an auth.test API call.
type AuthTestResponse struct {
	TeamID   string `json:"team_id"`
	TeamName string `json:"team"`
	UserID   string `json:"user_id"`
	User     string `json:"user"`
	URL      string `json:"url"`
}

// Channel represents a Slack conversation.
type Channel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsChannel bool   `json:"is_channel"`
	IsGroup   bool   `json:"is_group"`
	IsIM      bool   `json:"is_im"`
	IsMPIM    bool   `json:"is_mpim"`
}

// Message represents a single Slack message.
type Message struct {
	Type       string `json:"type"`
	User       string `json:"user"`
	Text       string `json:"text"`
	Timestamp  string `json:"ts"`
	ThreadTS   string `json:"thread_ts,omitempty"`
	ReplyCount int    `json:"reply_count,omitempty"`
}

// User represents a Slack user.
type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RealName string `json:"real_name"`
}

// doGet performs an authenticated GET request to the Slack API with retry on rate limit.
func (c *Client) doGet(method string, params url.Values) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/%s", c.BaseURL, method)
	if params != nil {
		endpoint = endpoint + "?" + params.Encode()
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request to %s failed: %w", method, err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response from %s: %w", method, err)
		}

		// Handle rate limiting
		if resp.StatusCode == http.StatusTooManyRequests {
			if attempt >= maxRetries {
				lastErr = fmt.Errorf("rate limited on %s after %d retries", method, maxRetries)
				break
			}
			retryAfter := 1
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if parsed, err := strconv.Atoi(ra); err == nil {
					retryAfter = parsed
				}
			}
			time.Sleep(time.Duration(retryAfter) * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, method)
		}

		// Check Slack-level error
		var base slackResponse
		if err := json.Unmarshal(body, &base); err != nil {
			return nil, fmt.Errorf("failed to parse response from %s: %w", method, err)
		}
		if !base.OK {
			return nil, fmt.Errorf("slack API error on %s: %s", method, base.Error)
		}

		return body, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("unexpected error in doGet for %s", method)
}

// doPost performs a POST request with form-encoded body (no auth header — used for OAuth).
func (c *Client) doPost(endpoint string, params url.Values) ([]byte, error) {
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return body, nil
}

// AuthTest verifies the bot token and returns workspace information.
func (c *Client) AuthTest() (*AuthTestResponse, error) {
	body, err := c.doGet("auth.test", nil)
	if err != nil {
		return nil, err
	}

	var result AuthTestResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse auth.test response: %w", err)
	}

	return &result, nil
}

// ListChannels returns all conversations visible to the bot.
func (c *Client) ListChannels() ([]Channel, error) {
	var allChannels []Channel
	cursor := ""

	for {
		params := url.Values{
			"types": {"public_channel,private_channel,im,mpim"},
			"limit": {"200"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		body, err := c.doGet("conversations.list", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			Channels         []Channel        `json:"channels"`
			ResponseMetadata responseMetadata `json:"response_metadata"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse conversations.list: %w", err)
		}

		allChannels = append(allChannels, resp.Channels...)

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return allChannels, nil
}

// FetchHistory retrieves messages from a channel within a time range.
// Both since and until are optional (zero time means no bound).
func (c *Client) FetchHistory(channelID string, since, until time.Time) ([]Message, error) {
	var allMessages []Message
	cursor := ""

	for {
		params := url.Values{
			"channel": {channelID},
			"limit":   {"200"},
		}
		if !since.IsZero() {
			params.Set("oldest", fmt.Sprintf("%d", since.Unix()))
		}
		if !until.IsZero() {
			params.Set("latest", fmt.Sprintf("%d", until.Unix()))
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		body, err := c.doGet("conversations.history", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			Messages         []Message        `json:"messages"`
			HasMore          bool             `json:"has_more"`
			ResponseMetadata responseMetadata `json:"response_metadata"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse conversations.history: %w", err)
		}

		allMessages = append(allMessages, resp.Messages...)

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" || !resp.HasMore {
			break
		}
	}

	return allMessages, nil
}

// FetchThreadReplies retrieves all replies in a message thread.
func (c *Client) FetchThreadReplies(channelID, threadTS string) ([]Message, error) {
	var allMessages []Message
	cursor := ""

	for {
		params := url.Values{
			"channel": {channelID},
			"ts":      {threadTS},
			"limit":   {"200"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		body, err := c.doGet("conversations.replies", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			Messages         []Message        `json:"messages"`
			HasMore          bool             `json:"has_more"`
			ResponseMetadata responseMetadata `json:"response_metadata"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse conversations.replies: %w", err)
		}

		allMessages = append(allMessages, resp.Messages...)

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" || !resp.HasMore {
			break
		}
	}

	return allMessages, nil
}

// GetUser retrieves user information by user ID.
func (c *Client) GetUser(userID string) (*User, error) {
	params := url.Values{
		"user": {userID},
	}

	body, err := c.doGet("users.info", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse users.info: %w", err)
	}

	return &resp.User, nil
}
