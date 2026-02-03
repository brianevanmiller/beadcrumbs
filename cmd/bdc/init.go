package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/brianevanmiller/beadcrumbs/internal/store"
	"github.com/spf13/cobra"
)

var initStealth bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new beadcrumbs repository",
	Long: `Creates the .beadcrumbs directory and initializes the database and JSONL files.

Use --stealth for local-only installation that won't appear in git status.
This is useful when you want to use beadcrumbs without committing files to the repo.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := filepath.Dir(dbPath)

		// Create .beadcrumbs directory
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		// Check if database already exists
		if _, err := os.Stat(dbPath); err == nil {
			return fmt.Errorf("database already exists at %s", dbPath)
		}

		// Initialize the database
		s, err := store.NewStore(dbPath)
		if err != nil {
			return fmt.Errorf("failed to initialize database: %w", err)
		}

		// Set up stealth mode if requested
		if initStealth {
			if err := setupStealthMode(); err != nil {
				s.Close()
				return fmt.Errorf("failed to set up stealth mode: %w", err)
			}
			// Store stealth mode in config
			if err := s.SetConfig("stealth_mode", "true"); err != nil {
				s.Close()
				return fmt.Errorf("failed to save stealth mode config: %w", err)
			}
			fmt.Println("Stealth mode enabled - .beadcrumbs will not appear in git status")
		}

		s.Close()

		// Create empty JSONL files
		jsonlFiles := []string{
			filepath.Join(dir, "insights.jsonl"),
			filepath.Join(dir, "threads.jsonl"),
			filepath.Join(dir, "deps.jsonl"),
		}

		for _, file := range jsonlFiles {
			f, err := os.Create(file)
			if err != nil {
				return fmt.Errorf("failed to create %s: %w", file, err)
			}
			f.Close()
		}

		// Install git hooks (non-stealth mode only)
		if !initStealth {
			if err := installGitHooks(); err != nil {
				fmt.Printf("Warning: failed to install git hooks: %v\n", err)
				fmt.Println("Git hooks are optional - beadcrumbs will work without them")
			}
		}

		fmt.Printf("Initialized beadcrumbs repository at %s\n", dir)
		if !initStealth {
			fmt.Println("Tip: Run 'bdc setup claude' to integrate with Claude Code")
		}
		return nil
	},
}

// setupStealthMode configures git to ignore .beadcrumbs locally.
// Uses .git/info/exclude instead of .gitignore to avoid committing the ignore pattern.
func setupStealthMode() error {
	// Check if we're in a git repo
	gitDir, err := getGitDir()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	excludePath := filepath.Join(gitDir, "info", "exclude")

	// Ensure info directory exists
	if err := os.MkdirAll(filepath.Join(gitDir, "info"), 0755); err != nil {
		return fmt.Errorf("failed to create .git/info directory: %w", err)
	}

	// Read existing content
	var existingContent string
	if content, err := os.ReadFile(excludePath); err == nil {
		existingContent = string(content)
	}

	// Check if pattern already exists
	beadcrumbsPattern := ".beadcrumbs/"
	if containsExactPattern(existingContent, beadcrumbsPattern) {
		return nil // Already configured
	}

	// Append our pattern
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open exclude file: %w", err)
	}
	defer f.Close()

	// Add newline if needed
	if len(existingContent) > 0 && !strings.HasSuffix(existingContent, "\n") {
		f.WriteString("\n")
	}

	// Add our pattern with comment
	f.WriteString("\n# beadcrumbs stealth mode (added by bdc init --stealth)\n")
	f.WriteString(beadcrumbsPattern + "\n")

	return nil
}

// getGitDir returns the path to the .git directory.
// Uses git rev-parse --git-common-dir to handle worktrees correctly.
func getGitDir() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// containsExactPattern checks if the exclude content already has the pattern.
func containsExactPattern(content, pattern string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == pattern {
			return true
		}
	}
	return false
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
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().BoolVar(&initStealth, "stealth", false, "local-only installation (uses .git/info/exclude)")
}
