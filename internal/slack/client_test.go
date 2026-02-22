package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(handler http.HandlerFunc) (*httptest.Server, *Client) {
	server := httptest.NewServer(handler)
	client := NewClient("xoxb-test-token")
	client.BaseURL = server.URL
	return server, client
}

func TestAuthTest(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth.test" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-test-token" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"team_id": "T123",
			"team":    "Test Workspace",
			"user_id": "U123",
			"user":    "testbot",
			"url":     "https://test.slack.com/",
		})
	})
	defer server.Close()

	resp, err := client.AuthTest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TeamID != "T123" {
		t.Errorf("unexpected team ID: %s", resp.TeamID)
	}
	if resp.TeamName != "Test Workspace" {
		t.Errorf("unexpected team name: %s", resp.TeamName)
	}
}

func TestListChannels(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"channels": []map[string]interface{}{
				{"id": "C001", "name": "general", "is_channel": true},
				{"id": "C002", "name": "engineering", "is_channel": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	defer server.Close()

	channels, err := client.ListChannels()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(channels))
	}
	if channels[0].Name != "general" {
		t.Errorf("unexpected channel name: %s", channels[0].Name)
	}
}

func TestFetchHistory(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		channel := r.URL.Query().Get("channel")
		if channel != "C001" {
			t.Errorf("unexpected channel: %s", channel)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"messages": []map[string]interface{}{
				{"type": "message", "user": "U001", "text": "Hello world", "ts": "1705312200.000000"},
				{"type": "message", "user": "U002", "text": "Hi there", "ts": "1705312300.000000"},
			},
			"has_more":          false,
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	defer server.Close()

	messages, err := client.FetchHistory("C001", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Text != "Hello world" {
		t.Errorf("unexpected message text: %s", messages[0].Text)
	}
}

func TestFetchHistory_WithTimeBounds(t *testing.T) {
	var receivedOldest, receivedLatest string
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		receivedOldest = r.URL.Query().Get("oldest")
		receivedLatest = r.URL.Query().Get("latest")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":                true,
			"messages":          []map[string]interface{}{},
			"has_more":          false,
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	defer server.Close()

	since := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	until := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)
	_, err := client.FetchHistory("C001", since, until)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedOldest == "" {
		t.Error("expected oldest parameter to be set")
	}
	if receivedLatest == "" {
		t.Error("expected latest parameter to be set")
	}
}

func TestGetUser(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user")
		if userID != "U001" {
			t.Errorf("unexpected user ID: %s", userID)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"user": map[string]interface{}{
				"id":        "U001",
				"name":      "alice",
				"real_name": "Alice Smith",
			},
		})
	})
	defer server.Close()

	user, err := client.GetUser("U001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.RealName != "Alice Smith" {
		t.Errorf("unexpected real name: %s", user.RealName)
	}
}

func TestSlackAPIError(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "channel_not_found",
		})
	})
	defer server.Close()

	_, err := client.FetchHistory("C999", time.Time{}, time.Time{})
	if err == nil {
		t.Fatal("expected error for failed Slack API call")
	}
	if !contains(err.Error(), "channel_not_found") {
		t.Errorf("error should contain 'channel_not_found': %s", err.Error())
	}
}

func TestRateLimitRetry(t *testing.T) {
	calls := 0
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"team_id": "T123",
			"team":    "Test",
			"user_id": "U123",
			"user":    "bot",
		})
	})
	defer server.Close()

	_, err := client.AuthTest()
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (1 retry), got %d", calls)
	}
}

func TestFetchThreadReplies(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		threadTS := r.URL.Query().Get("ts")
		if threadTS != "1705312200.000000" {
			t.Errorf("unexpected thread_ts: %s", threadTS)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"messages": []map[string]interface{}{
				{"type": "message", "user": "U001", "text": "Original message", "ts": "1705312200.000000"},
				{"type": "message", "user": "U002", "text": "Reply 1", "ts": "1705312300.000000"},
			},
			"has_more":          false,
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	defer server.Close()

	messages, err := client.FetchThreadReplies("C001", "1705312200.000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
