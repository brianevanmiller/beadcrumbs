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

		fmt.Printf("Initialized beadcrumbs repository at %s\n", dir)
		if !initStealth {
			fmt.Println("Tip: Run 'bdc prime' to install git hooks for auto-sync")
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

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().BoolVar(&initStealth, "stealth", false, "local-only installation (uses .git/info/exclude)")
}
