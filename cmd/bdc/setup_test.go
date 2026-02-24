package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestAddHookCommand(t *testing.T) {
	hooks := make(map[string]interface{})
	added := addHookCommand(hooks, "SessionStart", "bdc prime")
	if !added {
		t.Error("addHookCommand returned false for new hook")
	}

	eventHooks, ok := hooks["SessionStart"].([]interface{})
	if !ok {
		t.Fatal("SessionStart is not []interface{}")
	}
	if len(eventHooks) != 1 {
		t.Fatalf("expected 1 hook entry, got %d", len(eventHooks))
	}

	hookMap := eventHooks[0].(map[string]interface{})
	commands := hookMap["hooks"].([]interface{})
	cmdMap := commands[0].(map[string]interface{})
	if cmdMap["command"] != "bdc prime" {
		t.Errorf("command = %v, want 'bdc prime'", cmdMap["command"])
	}
}

func TestAddHookCommand_Idempotent(t *testing.T) {
	hooks := make(map[string]interface{})
	addHookCommand(hooks, "SessionStart", "bdc prime")
	added := addHookCommand(hooks, "SessionStart", "bdc prime")

	if added {
		t.Error("addHookCommand returned true for duplicate hook")
	}

	eventHooks := hooks["SessionStart"].([]interface{})
	if len(eventHooks) != 1 {
		t.Errorf("expected 1 hook entry after double add, got %d", len(eventHooks))
	}
}

func TestAddHookCommand_PreservesExisting(t *testing.T) {
	hooks := make(map[string]interface{})
	// Add another hook first
	addHookCommand(hooks, "SessionStart", "bd prime")
	addHookCommand(hooks, "SessionStart", "bdc prime")

	eventHooks := hooks["SessionStart"].([]interface{})
	if len(eventHooks) != 2 {
		t.Errorf("expected 2 hook entries, got %d", len(eventHooks))
	}
}

func TestRemoveHookCommand(t *testing.T) {
	hooks := make(map[string]interface{})
	addHookCommand(hooks, "SessionStart", "bdc prime")
	addHookCommand(hooks, "SessionStart", "bd prime")

	removeHookCommand(hooks, "SessionStart", "bdc prime")

	eventHooks, ok := hooks["SessionStart"].([]interface{})
	if !ok {
		t.Fatal("SessionStart should still exist (bd prime remains)")
	}
	if len(eventHooks) != 1 {
		t.Errorf("expected 1 hook entry after remove, got %d", len(eventHooks))
	}

	// Verify the remaining hook is bd prime
	hookMap := eventHooks[0].(map[string]interface{})
	commands := hookMap["hooks"].([]interface{})
	cmdMap := commands[0].(map[string]interface{})
	if cmdMap["command"] != "bd prime" {
		t.Errorf("remaining command = %v, want 'bd prime'", cmdMap["command"])
	}
}

func TestRemoveHookCommand_DeletesEmptyEvent(t *testing.T) {
	hooks := make(map[string]interface{})
	addHookCommand(hooks, "SessionStart", "bdc prime")
	removeHookCommand(hooks, "SessionStart", "bdc prime")

	if _, exists := hooks["SessionStart"]; exists {
		t.Error("SessionStart key should be deleted when empty")
	}
}

func TestRemoveHookCommand_NoOp(t *testing.T) {
	hooks := make(map[string]interface{})
	// Should not panic
	removeHookCommand(hooks, "SessionStart", "bdc prime")
	if len(hooks) != 0 {
		t.Errorf("hooks should be empty, got %d keys", len(hooks))
	}
}

func TestHasBeadcrumbsHooks(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "settings-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	settings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "bdc prime",
						},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	if err := os.WriteFile(tmpFile.Name(), data, 0644); err != nil {
		t.Fatal(err)
	}

	if !hasBeadcrumbsHooks(tmpFile.Name()) {
		t.Error("hasBeadcrumbsHooks should return true")
	}
}

func TestHasBeadcrumbsHooks_NoFile(t *testing.T) {
	if hasBeadcrumbsHooks("/nonexistent/path/settings.json") {
		t.Error("hasBeadcrumbsHooks should return false for missing file")
	}
}

func TestHasBeadcrumbsHooks_NoHooks(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "settings-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	settings := map[string]interface{}{
		"allowedTools": []string{"Read", "Write"},
	}
	data, _ := json.Marshal(settings)
	if err := os.WriteFile(tmpFile.Name(), data, 0644); err != nil {
		t.Fatal(err)
	}

	if hasBeadcrumbsHooks(tmpFile.Name()) {
		t.Error("hasBeadcrumbsHooks should return false when no hooks section")
	}
}

func TestHasBeadcrumbsHooks_WrongCommand(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "settings-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	settings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "bd prime",
						},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	if err := os.WriteFile(tmpFile.Name(), data, 0644); err != nil {
		t.Fatal(err)
	}

	if hasBeadcrumbsHooks(tmpFile.Name()) {
		t.Error("hasBeadcrumbsHooks should return false for 'bd prime' (not 'bdc prime')")
	}
}
