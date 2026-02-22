package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/jsonl"
	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var exportQuiet bool

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export database to JSONL files",
	Long: `Export all insights, threads, and dependencies from the SQLite database
to JSONL files in the .beadcrumbs/ directory.

This is called automatically by git hooks to keep JSONL files in sync
with the database for version control.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		dir := filepath.Dir(dbPath)

		// Export insights
		insights, err := s.ListInsights("", types.InsightType(""), time.Time{})
		if err != nil {
			return fmt.Errorf("failed to list insights: %w", err)
		}
		insightsPath := filepath.Join(dir, "insights.jsonl")
		if err := jsonl.ExportInsights(insights, insightsPath); err != nil {
			return fmt.Errorf("failed to export insights: %w", err)
		}

		// Export threads
		threads, err := s.ListThreads(types.ThreadStatus(""))
		if err != nil {
			return fmt.Errorf("failed to list threads: %w", err)
		}
		threadsPath := filepath.Join(dir, "threads.jsonl")
		if err := jsonl.ExportThreads(threads, threadsPath); err != nil {
			return fmt.Errorf("failed to export threads: %w", err)
		}

		// Export dependencies
		deps, err := s.ListAllDependencies()
		if err != nil {
			return fmt.Errorf("failed to list dependencies: %w", err)
		}
		depsPath := filepath.Join(dir, "deps.jsonl")
		if err := jsonl.ExportDependencies(deps, depsPath); err != nil {
			return fmt.Errorf("failed to export dependencies: %w", err)
		}

		if !exportQuiet {
			fmt.Printf("Exported %d insights, %d threads, %d dependencies\n",
				len(insights), len(threads), len(deps))
		}

		return nil
	},
}

func init() {
	exportCmd.Flags().BoolVarP(&exportQuiet, "quiet", "q", false, "suppress output")
	rootCmd.AddCommand(exportCmd)
}
