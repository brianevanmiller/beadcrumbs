package slack

import "fmt"

// ConfigStore is the minimal interface needed for config persistence.
// This avoids importing the full store package.
type ConfigStore interface {
	GetConfig(key string) (string, error)
	SetConfig(key, value string) error
}

// UserCache resolves Slack user IDs to display names, caching results
// in memory and in the config table for cross-session persistence.
type UserCache struct {
	client *Client
	cache  map[string]string
	store  ConfigStore
}

// NewUserCache creates a user cache backed by a Slack client and config store.
func NewUserCache(client *Client, store ConfigStore) *UserCache {
	return &UserCache{
		client: client,
		cache:  make(map[string]string),
		store:  store,
	}
}

// Resolve returns the display name for a Slack user ID.
// Resolution order: in-memory cache -> config table -> Slack API -> raw userID.
func (uc *UserCache) Resolve(userID string) string {
	if userID == "" {
		return ""
	}

	// 1. In-memory cache
	if name, ok := uc.cache[userID]; ok {
		return name
	}

	// 2. Config table
	if uc.store != nil {
		configKey := fmt.Sprintf("slack.user.%s", userID)
		if name, err := uc.store.GetConfig(configKey); err == nil && name != "" {
			uc.cache[userID] = name
			return name
		}
	}

	// 3. Slack API
	if uc.client != nil {
		user, err := uc.client.GetUser(userID)
		if err == nil && user != nil {
			name := user.RealName
			if name == "" {
				name = user.Name
			}
			if name != "" {
				uc.cache[userID] = name
				// Persist to config table
				if uc.store != nil {
					configKey := fmt.Sprintf("slack.user.%s", userID)
					_ = uc.store.SetConfig(configKey, name)
				}
				return name
			}
		}
	}

	// 4. Fallback to raw userID
	return userID
}
