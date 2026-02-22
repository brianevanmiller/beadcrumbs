package slack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockConfigStore struct {
	data map[string]string
}

func newMockConfigStore() *mockConfigStore {
	return &mockConfigStore{data: make(map[string]string)}
}

func (m *mockConfigStore) GetConfig(key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return v, nil
}

func (m *mockConfigStore) SetConfig(key, value string) error {
	m.data[key] = value
	return nil
}

func TestUserCache_InMemoryHit(t *testing.T) {
	uc := &UserCache{
		cache: map[string]string{"U001": "Alice"},
	}
	name := uc.Resolve("U001")
	if name != "Alice" {
		t.Errorf("expected 'Alice', got: %s", name)
	}
}

func TestUserCache_ConfigStoreHit(t *testing.T) {
	store := newMockConfigStore()
	store.data["slack.user.U002"] = "Bob"

	uc := &UserCache{
		cache: make(map[string]string),
		store: store,
	}
	name := uc.Resolve("U002")
	if name != "Bob" {
		t.Errorf("expected 'Bob', got: %s", name)
	}
	// Should now be in memory cache
	if uc.cache["U002"] != "Bob" {
		t.Error("expected name to be cached in memory")
	}
}

func TestUserCache_APIFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"user": map[string]interface{}{
				"id":        "U003",
				"name":      "charlie",
				"real_name": "Charlie Brown",
			},
		})
	}))
	defer server.Close()

	client := NewClient("xoxb-test")
	client.BaseURL = server.URL

	store := newMockConfigStore()
	uc := NewUserCache(client, store)

	name := uc.Resolve("U003")
	if name != "Charlie Brown" {
		t.Errorf("expected 'Charlie Brown', got: %s", name)
	}
	// Should be persisted to config store
	if store.data["slack.user.U003"] != "Charlie Brown" {
		t.Error("expected name to be persisted to config store")
	}
}

func TestUserCache_FallbackToUserID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "user_not_found",
		})
	}))
	defer server.Close()

	client := NewClient("xoxb-test")
	client.BaseURL = server.URL

	uc := NewUserCache(client, nil)

	name := uc.Resolve("U999")
	if name != "U999" {
		t.Errorf("expected fallback to 'U999', got: %s", name)
	}
}

func TestUserCache_EmptyUserID(t *testing.T) {
	uc := &UserCache{cache: make(map[string]string)}
	name := uc.Resolve("")
	if name != "" {
		t.Errorf("expected empty string, got: %s", name)
	}
}

func TestUserCache_PrefersRealName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"user": map[string]interface{}{
				"id":        "U004",
				"name":      "davehandle",
				"real_name": "Dave Wilson",
			},
		})
	}))
	defer server.Close()

	client := NewClient("xoxb-test")
	client.BaseURL = server.URL

	uc := NewUserCache(client, nil)

	name := uc.Resolve("U004")
	if name != "Dave Wilson" {
		t.Errorf("expected 'Dave Wilson' (real_name), got: %s", name)
	}
}

func TestUserCache_FallsBackToName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"user": map[string]interface{}{
				"id":        "U005",
				"name":      "evehandle",
				"real_name": "",
			},
		})
	}))
	defer server.Close()

	client := NewClient("xoxb-test")
	client.BaseURL = server.URL

	uc := NewUserCache(client, nil)

	name := uc.Resolve("U005")
	if name != "evehandle" {
		t.Errorf("expected 'evehandle' (name fallback), got: %s", name)
	}
}
