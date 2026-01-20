package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var primeCmd = &cobra.Command{
	Use:   "prime",
	Short: "Set up git hooks and verify database",
	Long: `Prime sets up the beadcrumbs environment:
  - Installs git hooks (post-commit, post-merge, post-checkout)
  - Verifies and repairs the database if needed
  - Ensures all required tables exist

Run this after init or when moving to a new machine.
In stealth mode, hooks are skipped since changes aren't tracked in git.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return fmt.Errorf("failed to open store: %w", err)
		}
		defer closeStore()

		// Check if in stealth mode
		stealthMode, _ := s.GetConfig("stealth_mode")
		if stealthMode == "true" {
			fmt.Println("Stealth mode enabled - skipping git hooks installation")
		} else {
			// Install git hooks
			if err := installGitHooks(); err != nil {
				fmt.Printf("Warning: failed to install git hooks: %v\n", err)
				fmt.Println("Git hooks are optional - beadcrumbs will work without them")
			} else {
				fmt.Println("Git hooks installed successfully")
			}
		}

		// Verify database
		if err := s.Verify(); err != nil {
			return fmt.Errorf("database verification failed: %w", err)
		}
		fmt.Println("Database verified successfully")

		fmt.Println("\nbeadcrumbs is primed and ready!")
		return nil
	},
}

// installGitHooks installs git hooks for beadcrumbs.
func installGitHooks() error {
	gitDir, err := getGitDir()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return fmt.Errorf("failed to create hooks directory: %w", err)
	}

	// Define hooks to install
	hooks := map[string]string{
		"post-commit": `#!/bin/sh
# beadcrumbs: Export insights after commit
# This hook exports local insights to JSONL for version control
if command -v bdc >/dev/null 2>&1; then
    bdc export --quiet 2>/dev/null || true
fi
`,
		"post-merge": `#!/bin/sh
# beadcrumbs: Import insights after merge/pull
# This hook imports JSONL changes from other collaborators
if command -v bdc >/dev/null 2>&1; then
    bdc import --auto --quiet 2>/dev/null || true
fi
`,
		"post-checkout": `#!/bin/sh
# beadcrumbs: Import insights after checkout
# This hook imports JSONL changes when switching branches
if command -v bdc >/dev/null 2>&1; then
    bdc import --auto --quiet 2>/dev/null || true
fi
`,
	}

	for hookName, hookContent := range hooks {
		hookPath := filepath.Join(hooksDir, hookName)

		// Check if hook already exists
		if _, err := os.Stat(hookPath); err == nil {
			// Read existing hook
			existing, err := os.ReadFile(hookPath)
			if err != nil {
				return fmt.Errorf("failed to read existing %s hook: %w", hookName, err)
			}

			// Check if our hook is already there
			if strings.Contains(string(existing), "beadcrumbs") {
				continue // Already installed
			}

			// Append to existing hook
			hookContent = string(existing) + "\n" + hookContent
		}

		// Write hook
		if err := os.WriteFile(hookPath, []byte(hookContent), 0755); err != nil {
			return fmt.Errorf("failed to write %s hook: %w", hookName, err)
		}
	}

	return nil
}

// isGitRepo checks if the current directory is in a git repository.
func isGitRepo() bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func init() {
	rootCmd.AddCommand(primeCmd)
}
