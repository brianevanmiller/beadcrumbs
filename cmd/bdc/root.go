package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/brianevanmiller/beadcrumbs/internal/store"
)

var (
	dbPath string
	storeInstance *store.Store
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
}

// getStore returns the store instance, initializing it if necessary.
func getStore() (*store.Store, error) {
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

	// Open the store
	s, err := store.NewStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
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

// truncate shortens a string to maxLen characters with ellipsis.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
