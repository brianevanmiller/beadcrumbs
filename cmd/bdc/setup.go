package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	setupProject bool
	setupCheck   bool
	setupRemove  bool
	setupStealth bool
)

var setupCmd = &cobra.Command{
	Use:   "setup <editor>",
	Short: "Set up integration with AI editors",
	Long: `Set up integration with AI editors and coding assistants.

Currently supported editors:
  claude    Claude Code (hooks in ~/.claude/settings.json)

Examples:
  bdc setup claude             # Install Claude Code integration (global)
  bdc setup claude --project   # Install for this project only
  bdc setup claude --stealth   # Use stealth mode (no git operations)
  bdc setup claude --check     # Verify installation status
  bdc setup claude --remove    # Uninstall integration`,
	Args: cobra.ExactArgs(1),
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	editor := strings.ToLower(args[0])
	if editor != "claude" {
		return fmt.Errorf("unsupported editor: %s (supported: claude)", editor)
	}

	if setupCheck {
		return checkClaudeHooks()
	}
	if setupRemove {
		return removeClaudeHooks(setupProject)
	}
	return installClaudeHooks(setupProject, setupStealth)
}

func setupProjectSettingsPath(base string) string {
	return filepath.Join(base, ".claude", "settings.local.json")
}

func setupGlobalSettingsPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}

func installClaudeHooks(project bool, stealth bool) error {
	var settingsPath string
	if project {
		workDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("working directory: %w", err)
		}
		settingsPath = setupProjectSettingsPath(workDir)
		fmt.Println("Installing Claude hooks for this project...")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home directory: %w", err)
		}
		settingsPath = setupGlobalSettingsPath(home)
		fmt.Println("Installing Claude hooks globally...")
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	// Read existing settings
	settings := make(map[string]interface{})
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse settings.json: %w", err)
		}
	}

	// Get or create hooks section
	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		hooks = make(map[string]interface{})
		settings["hooks"] = hooks
	}

	// Clean up any null values (defensive, matching bd's GH#955 fix)
	for key, val := range hooks {
		if val == nil {
			delete(hooks, key)
		}
	}

	command := "bdc prime"
	if stealth {
		command = "bdc prime --stealth"
	}

	if addHookCommand(hooks, "SessionStart", command) {
		fmt.Println("  Registered SessionStart hook")
	}
	if addHookCommand(hooks, "PreCompact", command) {
		fmt.Println("  Registered PreCompact hook")
	}

	// Write settings back
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	fmt.Println("\n  Claude Code integration installed")
	fmt.Printf("  Settings: %s\n", settingsPath)
	fmt.Println("\nRestart Claude Code for changes to take effect.")
	return nil
}

func checkClaudeHooks() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home directory: %w", err)
	}
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}

	globalSettings := setupGlobalSettingsPath(home)
	projectSettings := setupProjectSettingsPath(workDir)

	switch {
	case hasBeadcrumbsHooks(globalSettings):
		fmt.Printf("  Global hooks installed: %s\n", globalSettings)
		return nil
	case hasBeadcrumbsHooks(projectSettings):
		fmt.Printf("  Project hooks installed: %s\n", projectSettings)
		return nil
	default:
		fmt.Println("  No hooks installed")
		fmt.Println("  Run: bdc setup claude")
		return fmt.Errorf("claude hooks not installed")
	}
}

func removeClaudeHooks(project bool) error {
	var settingsPath string
	if project {
		workDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("working directory: %w", err)
		}
		settingsPath = setupProjectSettingsPath(workDir)
		fmt.Println("Removing Claude hooks from project...")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home directory: %w", err)
		}
		settingsPath = setupGlobalSettingsPath(home)
		fmt.Println("Removing Claude hooks globally...")
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		fmt.Println("No settings file found")
		return nil
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("parse settings.json: %w", err)
	}

	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		fmt.Println("No hooks found")
		return nil
	}

	removeHookCommand(hooks, "SessionStart", "bdc prime")
	removeHookCommand(hooks, "PreCompact", "bdc prime")
	removeHookCommand(hooks, "SessionStart", "bdc prime --stealth")
	removeHookCommand(hooks, "PreCompact", "bdc prime --stealth")

	data, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	fmt.Println("  Claude hooks removed")
	return nil
}

// addHookCommand adds a hook command to an event if not already present.
// Returns true if hook was added, false if already exists.
func addHookCommand(hooks map[string]interface{}, event, command string) bool {
	eventHooks, ok := hooks[event].([]interface{})
	if !ok {
		eventHooks = []interface{}{}
	}

	// Check if bdc hook already registered
	for _, hook := range eventHooks {
		hookMap, ok := hook.(map[string]interface{})
		if !ok {
			continue
		}
		commands, ok := hookMap["hooks"].([]interface{})
		if !ok {
			continue
		}
		for _, cmd := range commands {
			cmdMap, ok := cmd.(map[string]interface{})
			if !ok {
				continue
			}
			if cmdMap["command"] == command {
				fmt.Printf("  Hook already registered: %s\n", event)
				return false
			}
		}
	}

	// Add bdc hook to array
	newHook := map[string]interface{}{
		"matcher": "",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": command,
			},
		},
	}

	eventHooks = append(eventHooks, newHook)
	hooks[event] = eventHooks
	return true
}

// removeHookCommand removes a hook command from an event.
func removeHookCommand(hooks map[string]interface{}, event, command string) {
	eventHooks, ok := hooks[event].([]interface{})
	if !ok {
		return
	}

	filtered := make([]interface{}, 0, len(eventHooks))
	for _, hook := range eventHooks {
		hookMap, ok := hook.(map[string]interface{})
		if !ok {
			filtered = append(filtered, hook)
			continue
		}

		commands, ok := hookMap["hooks"].([]interface{})
		if !ok {
			filtered = append(filtered, hook)
			continue
		}

		keepHook := true
		for _, cmd := range commands {
			cmdMap, ok := cmd.(map[string]interface{})
			if !ok {
				continue
			}
			if cmdMap["command"] == command {
				keepHook = false
				fmt.Printf("  Removed %s hook\n", event)
				break
			}
		}

		if keepHook {
			filtered = append(filtered, hook)
		}
	}

	// Delete the key entirely if no hooks remain
	if len(filtered) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = filtered
	}
}

// hasBeadcrumbsHooks checks if a settings file has bdc prime hooks.
func hasBeadcrumbsHooks(settingsPath string) bool {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}

	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		return false
	}

	for _, event := range []string{"SessionStart", "PreCompact"} {
		eventHooks, ok := hooks[event].([]interface{})
		if !ok {
			continue
		}

		for _, hook := range eventHooks {
			hookMap, ok := hook.(map[string]interface{})
			if !ok {
				continue
			}
			commands, ok := hookMap["hooks"].([]interface{})
			if !ok {
				continue
			}
			for _, cmd := range commands {
				cmdMap, ok := cmd.(map[string]interface{})
				if !ok {
					continue
				}
				c := cmdMap["command"]
				if c == "bdc prime" || c == "bdc prime --stealth" {
					return true
				}
			}
		}
	}

	return false
}

func init() {
	rootCmd.AddCommand(setupCmd)
	setupCmd.Flags().BoolVar(&setupCheck, "check", false, "Check if integration is installed")
	setupCmd.Flags().BoolVar(&setupRemove, "remove", false, "Remove the integration")
	setupCmd.Flags().BoolVar(&setupProject, "project", false, "Install for this project only")
	setupCmd.Flags().BoolVar(&setupStealth, "stealth", false, "Use stealth mode (no git operations)")
}
