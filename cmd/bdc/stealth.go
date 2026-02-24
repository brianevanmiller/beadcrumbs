package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var stealthStatus bool

var stealthCmd = &cobra.Command{
	Use:   "stealth",
	Short: "Convert to stealth mode (local-only, not tracked in git)",
	Long: `Convert an existing beadcrumbs installation to stealth mode.

This hides .beadcrumbs/ from git without deleting any data:
  - Adds .beadcrumbs/ to .git/info/exclude
  - Removes beadcrumbs entries from .gitignore
  - Removes beadcrumbs git hooks
  - Unstages .beadcrumbs/ JSONL files from git tracking

Your existing insights are preserved. Run 'bdc unstealth' to reverse.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if stealthStatus {
			return showStealthStatus()
		}
		return enableStealth()
	},
}

var unstealthCmd = &cobra.Command{
	Use:   "unstealth",
	Short: "Convert from stealth to normal mode (git-tracked)",
	Long: `Convert a stealth beadcrumbs installation to normal git-tracked mode.

This makes .beadcrumbs/ visible to git for team collaboration:
  - Removes .beadcrumbs/ from .git/info/exclude
  - Adds SQLite database to .gitignore (JSONL files get tracked)
  - Installs git hooks for auto-sync
  - Exports current insights to JSONL

After unstealthing, 'git add .beadcrumbs/' will stage the JSONL files.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return disableStealth()
	},
}

func showStealthStatus() error {
	s, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()
	val, err := s.GetConfig("stealth_mode")
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}
	if val == "true" {
		fmt.Println("stealth")
	} else {
		fmt.Println("normal")
	}
	return nil
}

func enableStealth() error {
	s, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()

	// Check if already stealth
	val, err := s.GetConfig("stealth_mode")
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}
	if val == "true" {
		fmt.Println("Already in stealth mode.")
		return nil
	}

	bcDir := filepath.Dir(dbPath)
	fmt.Println("Converting to stealth mode...")

	// Step 1: Update config first (before git operations that may spawn subprocesses)
	if err := s.SetConfig("stealth_mode", "true"); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}
	closeStore()

	// Step 2: Add .beadcrumbs/ to .git/info/exclude
	if err := setupStealthMode(); err != nil {
		fmt.Printf("Warning: %v\n", err)
	} else {
		fmt.Println("  Added .beadcrumbs/ to .git/info/exclude")
	}

	// Step 3: Remove beadcrumbs entries from .gitignore
	if removed, err := removeGitignoreEntries(); err != nil {
		fmt.Printf("  Warning: failed to clean .gitignore: %v\n", err)
	} else if removed {
		fmt.Println("  Removed beadcrumbs entries from .gitignore")
	}

	// Step 4: Remove beadcrumbs git hooks
	if removed, err := removeBeadcrumbsHooks(); err != nil {
		fmt.Printf("  Warning: failed to remove git hooks: %v\n", err)
	} else if removed {
		fmt.Println("  Removed beadcrumbs git hooks")
	}

	// Step 5: Unstage .beadcrumbs/ JSONL files from git
	if unstaged := unstageBeadcrumbsFiles(bcDir); unstaged {
		fmt.Println("  Unstaged .beadcrumbs/ files from git tracking")
	}

	fmt.Println("\nStealth mode enabled. .beadcrumbs/ is now invisible to git.")
	fmt.Println("Your data is preserved. Run 'bdc unstealth' to reverse.")
	return nil
}

func disableStealth() error {
	s, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()

	// Check if already normal
	val, err := s.GetConfig("stealth_mode")
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}
	if val != "true" {
		fmt.Println("Already in normal mode.")
		return nil
	}

	fmt.Println("Converting to normal (git-tracked) mode...")

	// Step 1: Update config first (before export subprocess that opens its own DB connection)
	if err := s.SetConfig("stealth_mode", "false"); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}
	closeStore()

	// Step 2: Remove .beadcrumbs/ from .git/info/exclude
	if removed, err := removeExcludeEntry(); err != nil {
		fmt.Printf("  Warning: failed to clean .git/info/exclude: %v\n", err)
	} else if removed {
		fmt.Println("  Removed .beadcrumbs/ from .git/info/exclude")
	}

	// Step 3: Add .gitignore entries (track JSONL, ignore SQLite)
	if err := addGitignoreEntries(); err != nil {
		fmt.Printf("  Warning: failed to update .gitignore: %v\n", err)
	} else {
		fmt.Println("  Updated .gitignore to track JSONL, ignore SQLite")
	}

	// Step 4: Install git hooks
	if err := installGitHooks(); err != nil {
		fmt.Printf("  Warning: failed to install git hooks: %v\n", err)
	} else {
		fmt.Println("  Installed git hooks (pre-commit, post-commit, post-merge, post-checkout)")
	}

	// Step 5: Export current insights to JSONL
	if exported := exportForUnstealth(); exported {
		fmt.Println("  Exported insights to JSONL files")
	}

	fmt.Println("\nNormal mode enabled. .beadcrumbs/ JSONL files are now visible to git.")
	fmt.Println("Next steps:")
	fmt.Println("  git add .beadcrumbs/")
	fmt.Println("  git commit -m \"Add beadcrumbs insight tracking\"")
	return nil
}

// removeExcludeEntry removes the .beadcrumbs/ pattern from .git/info/exclude.
func removeExcludeEntry() (bool, error) {
	gitDir, err := getGitDir()
	if err != nil {
		return false, nil // Not in git, nothing to remove
	}

	excludePath := filepath.Join(gitDir, "info", "exclude")
	content, err := os.ReadFile(excludePath)
	if err != nil {
		return false, nil // No exclude file
	}

	lines := strings.Split(string(content), "\n")
	var filtered []string
	removed := false
	skipComment := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip the stealth mode comment and the pattern line
		if trimmed == "# beadcrumbs stealth mode (added by bdc init --stealth)" {
			skipComment = true
			removed = true
			continue
		}
		if skipComment && trimmed == ".beadcrumbs/" {
			skipComment = false
			continue
		}
		skipComment = false
		filtered = append(filtered, line)
	}

	if !removed {
		// Try removing just the bare pattern (no comment)
		filtered = nil
		for _, line := range lines {
			if strings.TrimSpace(line) == ".beadcrumbs/" {
				removed = true
				continue
			}
			filtered = append(filtered, line)
		}
	}

	if !removed {
		return false, nil
	}

	// Clean up trailing blank lines
	result := strings.Join(filtered, "\n")
	result = strings.TrimRight(result, "\n") + "\n"

	if err := os.WriteFile(excludePath, []byte(result), 0644); err != nil {
		return false, fmt.Errorf("failed to write exclude file: %w", err)
	}
	return true, nil
}

// removeGitignoreEntries removes beadcrumbs entries from .gitignore.
func removeGitignoreEntries() (bool, error) {
	gitignorePath := ".gitignore"
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		return false, nil // No .gitignore
	}

	original := string(content)
	lines := strings.Split(original, "\n")
	var filtered []string
	removed := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# Beadcrumbs database (ephemeral, rebuilt from JSONL)" ||
			strings.HasPrefix(trimmed, ".beadcrumbs/beadcrumbs.db") {
			removed = true
			continue
		}
		filtered = append(filtered, line)
	}

	if !removed {
		return false, nil
	}

	result := strings.Join(filtered, "\n")
	// Clean up multiple consecutive blank lines left behind
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}

	// If .gitignore is effectively empty and not tracked by git, remove it
	if strings.TrimSpace(result) == "" {
		tracked := exec.Command("git", "ls-files", gitignorePath)
		if out, err := tracked.Output(); err == nil && len(strings.TrimSpace(string(out))) == 0 {
			os.Remove(gitignorePath)
			return true, nil
		}
	}

	if err := os.WriteFile(gitignorePath, []byte(result), 0644); err != nil {
		return false, fmt.Errorf("failed to write .gitignore: %w", err)
	}
	return true, nil
}

// removeBeadcrumbsHooks removes beadcrumbs-specific content from git hooks.
func removeBeadcrumbsHooks() (bool, error) {
	gitDir, err := getGitDir()
	if err != nil {
		return false, nil
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	hookNames := []string{"pre-commit", "post-commit", "post-merge", "post-checkout"}
	anyRemoved := false

	for _, hookName := range hookNames {
		hookPath := filepath.Join(hooksDir, hookName)
		content, err := os.ReadFile(hookPath)
		if err != nil {
			continue
		}

		if !strings.Contains(string(content), "beadcrumbs") {
			continue
		}

		// Remove beadcrumbs sections from the hook
		cleaned := removeBeadcrumbsSection(string(content))
		cleaned = strings.TrimSpace(cleaned)

		if cleaned == "" || cleaned == "#!/bin/sh" {
			// Hook was only beadcrumbs content, remove the file
			os.Remove(hookPath)
		} else {
			os.WriteFile(hookPath, []byte(cleaned+"\n"), 0755)
		}
		anyRemoved = true
	}

	return anyRemoved, nil
}

// removeBeadcrumbsSection removes beadcrumbs blocks from a hook script.
func removeBeadcrumbsSection(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var lines []string
	inBlock := false

	for scanner.Scan() {
		line := scanner.Text()

		// Start of a beadcrumbs block (comment line)
		if strings.Contains(line, "beadcrumbs:") && strings.HasPrefix(strings.TrimSpace(line), "#") {
			inBlock = true
			continue
		}

		// Inside a beadcrumbs block -- skip lines until we hit the closing fi or a blank line after fi
		if inBlock {
			trimmed := strings.TrimSpace(line)
			if trimmed == "fi" {
				inBlock = false
				continue
			}
			continue
		}

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// unstageBeadcrumbsFiles runs git rm --cached on tracked .beadcrumbs/ files.
func unstageBeadcrumbsFiles(bcDir string) bool {
	// Check if any .beadcrumbs/ files are tracked
	cmd := exec.Command("git", "ls-files", bcDir)
	output, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(output))) == 0 {
		return false
	}

	// Unstage without deleting local files
	untrack := exec.Command("git", "rm", "--cached", "-r", bcDir)
	if err := untrack.Run(); err != nil {
		return false
	}
	return true
}

// exportForUnstealth runs bdc export to ensure JSONL files are populated.
func exportForUnstealth() bool {
	cmd := exec.Command("bdc", "export", "--quiet")
	return cmd.Run() == nil
}

func init() {
	stealthCmd.Flags().BoolVar(&stealthStatus, "status", false, "Show current mode (stealth or normal)")
	rootCmd.AddCommand(stealthCmd)
	rootCmd.AddCommand(unstealthCmd)
}
