package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/brianevanmiller/beadcrumbs/internal/linear"
	"github.com/brianevanmiller/beadcrumbs/internal/store"
	"github.com/spf13/cobra"
)

var initStealth bool
var initQuiet bool

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
			if !initQuiet {
				fmt.Println("Stealth mode enabled - .beadcrumbs will not appear in git status")
			}
		}

		s.Close()

		// Create empty JSONL files (skip if they already exist, e.g., from git)
		jsonlFiles := []string{
			filepath.Join(dir, "insights.jsonl"),
			filepath.Join(dir, "threads.jsonl"),
			filepath.Join(dir, "deps.jsonl"),
		}

		for _, file := range jsonlFiles {
			if _, err := os.Stat(file); err == nil {
				continue // Don't overwrite existing JSONL files
			}
			f, err := os.Create(file)
			if err != nil {
				return fmt.Errorf("failed to create %s: %w", file, err)
			}
			f.Close()
		}

		// Add .gitignore entries (non-stealth mode only)
		if !initStealth {
			if err := addGitignoreEntries(); err != nil {
				if !initQuiet {
					fmt.Printf("Warning: failed to update .gitignore: %v\n", err)
				}
			} else if !initQuiet {
				fmt.Println("Updated .gitignore to track JSONL, ignore SQLite")
			}
		}

		// Install git hooks (non-stealth mode only)
		if !initStealth {
			if err := installGitHooks(); err != nil {
				if !initQuiet {
					fmt.Printf("Warning: failed to install git hooks: %v\n", err)
					fmt.Println("Git hooks are optional - beadcrumbs will work without them")
				}
			}
		}

		if !initQuiet {
			// Tip about pre-commit framework
			if _, err := os.Stat(".pre-commit-config.yaml"); err == nil {
				fmt.Println("Tip: You're using the pre-commit framework. See docs/guides/pre-commit-config.yaml for an alternative hook config.")
			}

			fmt.Printf("Initialized beadcrumbs repository at %s\n", dir)
			if !initStealth {
				fmt.Println("Tip: Run 'bdc setup claude' to integrate with Claude Code")
			}

			// Non-blocking Linear CLI detection
			detectLinearOnInit()
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
		"pre-commit": `#!/bin/sh
# beadcrumbs: Export and stage JSONL before commit
# Ensures insights are always in sync with the commit
if command -v bdc >/dev/null 2>&1; then
    bdc export --quiet 2>/dev/null || true
    for f in .beadcrumbs/insights.jsonl .beadcrumbs/threads.jsonl .beadcrumbs/deps.jsonl; do
        if [ -f "$f" ]; then
            if ! git diff --quiet -- "$f" 2>/dev/null || ! git diff --cached --quiet -- "$f" 2>/dev/null; then
                git add "$f" 2>/dev/null || true
            fi
        fi
    done
fi
`,
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
# beadcrumbs: Auto-bootstrap and import insights after checkout
# Creates .beadcrumbs in new worktrees and imports JSONL changes
if command -v bdc >/dev/null 2>&1; then
    if [ ! -d .beadcrumbs ]; then
        bdc init --quiet 2>/dev/null || true
    fi
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

// addGitignoreEntries adds .gitignore entries to track JSONL but ignore SQLite.
func addGitignoreEntries() error {
	gitignorePath := ".gitignore"

	var existingContent string
	if content, err := os.ReadFile(gitignorePath); err == nil {
		existingContent = string(content)
	}

	// Skip if beadcrumbs DB is already ignored or entire dir is ignored
	if strings.Contains(existingContent, "beadcrumbs.db") ||
		containsExactPattern(existingContent, ".beadcrumbs/") ||
		containsExactPattern(existingContent, ".beadcrumbs") {
		return nil
	}

	entries := `
# Beadcrumbs database (ephemeral, rebuilt from JSONL)
.beadcrumbs/beadcrumbs.db
.beadcrumbs/beadcrumbs.db-journal
.beadcrumbs/beadcrumbs.db-wal
.beadcrumbs/beadcrumbs.db-shm
# Beadcrumbs origin file (session-local, not for version control)
.beadcrumbs/origin
`

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open .gitignore: %w", err)
	}
	defer f.Close()

	// Add newline separator if file has content and doesn't end with newline
	if len(existingContent) > 0 && !strings.HasSuffix(existingContent, "\n") {
		f.WriteString("\n")
	}

	if _, err := f.WriteString(entries); err != nil {
		return fmt.Errorf("failed to write .gitignore entries: %w", err)
	}

	return nil
}

// isGitRepo checks if the current directory is in a git repository.
func isGitRepo() bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

// detectLinearOnInit checks for installed Linear CLI tools and reports findings.
// This is informational only — bdc init succeeds regardless.
func detectLinearOnInit() {
	fmt.Println()
	fmt.Println("Checking for Linear CLI...")
	adapters := linear.DetectAll("")
	if len(adapters) == 0 {
		fmt.Println("  No Linear CLI detected. To enable Linear integration, install one:")
		fmt.Println("    brew install schpet/tap/linear        (recommended)")
		fmt.Println("    cargo install linear-cli              (Rust alternative)")
		fmt.Println("    npm install -g czottmann/linearis     (Node alternative)")
		fmt.Println("  Then run: bdc linear setup")
		return
	}

	for _, a := range adapters {
		authStatus := "not authenticated"
		if err := a.CheckAuth(); err == nil {
			authStatus = "authenticated"
		}
		fmt.Printf("  Found: %s (%s) — %s\n", a.Name(), a.BinPath(), authStatus)
	}
	fmt.Println("  Run 'bdc linear setup' to configure.")
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().BoolVar(&initStealth, "stealth", false, "local-only installation (uses .git/info/exclude)")
	initCmd.Flags().BoolVar(&initQuiet, "quiet", false, "suppress output (for hooks and automation)")
}
