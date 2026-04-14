package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/brianevanmiller/beadcrumbs/internal/store"
)

var (
	dbPath        string
	jsonOutput    bool
	storeInstance store.Storage
)

var rootCmd = &cobra.Command{
	Use:   "bdc",
	Short: "beadcrumbs - Track how understanding evolves through dialogues",
	Long: `beadcrumbs is a Git-backed CLI tool for tracking the evolution of understanding.
It captures insights from dialogues and preserves the narrative journey of discovery.

Like breadcrumbs leaving a trail, beadcrumbs captures the small pieces of understanding
that lead to the bigger tasks (beads) in your workflow.`,
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", ".beadcrumbs/beadcrumbs.db", "path to the database")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Skip resolution for commands that manage their own DB path
		switch cmd.Name() {
		case "init", "locate", "version", "upgrade":
			return nil
		}
		resolveDBPath(cmd)
		return nil
	}
}

// getStore returns the store instance (read-write), initializing it if necessary.
func getStore() (store.Storage, error) {
	if storeInstance != nil {
		return storeInstance, nil
	}

	// Check if the database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("database not found at %s. Run 'bdc init' first", dbPath)
	}

	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Open the store (runs migrations)
	s, err := store.NewStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	storeInstance = s
	return s, nil
}

// getReadOnlyStore returns a read-only store instance, initializing it if necessary.
// Used by query-only commands (list, timeline, show, questions, decisions, etc.)
// to avoid acquiring write locks or triggering file watchers.
func getReadOnlyStore() (store.Storage, error) {
	if storeInstance != nil {
		return storeInstance, nil
	}

	// Check if the database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("database not found at %s. Run 'bdc init' first", dbPath)
	}

	// Open in read-only mode — no migrations, no JSONL import
	s, err := store.NewReadOnlyStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database (read-only): %w", err)
	}

	storeInstance = s
	return s, nil
}

// closeStore closes the store if it's open.
func closeStore() {
	if storeInstance != nil {
		storeInstance.Close()
		storeInstance = nil
	}
}

// resolveDBPath resolves the database path using multiple strategies.
func resolveDBPath(cmd *cobra.Command) {
	if cmd.Flags().Changed("db") {
		return
	}
	if envPath := os.Getenv("BDC_DB_PATH"); envPath != "" {
		dbPath = envPath
		return
	}
	if resolved := walkUpForDB(); resolved != "" {
		dbPath = resolved
		return
	}
	if resolved := resolveViaGitCommonDir(); resolved != "" {
		dbPath = resolved
		return
	}
}

// walkUpForDB walks up from CWD looking for .beadcrumbs/beadcrumbs.db.
func walkUpForDB() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, ".beadcrumbs", "beadcrumbs.db")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// resolveViaGitCommonDir finds the main repo root via git-common-dir
// and checks for .beadcrumbs/beadcrumbs.db there.
func resolveViaGitCommonDir() string {
	repoRoot := gitCommonDirRoot("")
	if repoRoot == "" {
		return ""
	}
	candidate := filepath.Join(repoRoot, ".beadcrumbs", "beadcrumbs.db")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// gitCommonDirRoot returns the main repo root directory by running
// `git rev-parse --git-common-dir` and taking its parent. If dir is
// non-empty, git runs with -C dir; otherwise it uses the current directory.
// Returns "" if not in a git repo or if the result is the local .git
// (meaning we're already in the main repo, not a worktree).
func gitCommonDirRoot(dir string) string {
	var cmd *exec.Cmd
	if dir != "" {
		cmd = exec.Command("git", "-C", dir, "rev-parse", "--git-common-dir")
	} else {
		cmd = exec.Command("git", "rev-parse", "--git-common-dir")
	}
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	gitCommonDir := strings.TrimSpace(string(output))
	if gitCommonDir == "" || gitCommonDir == ".git" {
		return ""
	}
	baseDir := dir
	if baseDir == "" {
		baseDir, err = os.Getwd()
		if err != nil {
			return ""
		}
	}
	if !filepath.IsAbs(gitCommonDir) {
		gitCommonDir = filepath.Join(baseDir, gitCommonDir)
	}
	return filepath.Dir(gitCommonDir)
}

// truncate shortens a string to maxLen characters with ellipsis.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
