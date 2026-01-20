package main

import (
	"fmt"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var spawnCmd = &cobra.Command{
	Use:   "spawn <insight-id>",
	Short: "Create a task from an insight",
	Long: `Create a task (bead) from an insight, establishing a "spawns" dependency.

If beads is installed and a .beads/ directory exists, this will suggest
the bd create command to run. Otherwise, it creates a placeholder dependency.

Example:
  bdc spawn ins-7f2a --title="Upgrade JWT library"

This will:
  1. Create a spawns dependency from ins-7f2a to the new bead
  2. If beads is present, suggest: bd create --title="Upgrade JWT library"`,
	Args: cobra.ExactArgs(1),
	RunE: runSpawn,
}

var spawnTitle string

func init() {
	spawnCmd.Flags().StringVar(&spawnTitle, "title", "", "title for the new task (required)")
	spawnCmd.MarkFlagRequired("title")
	rootCmd.AddCommand(spawnCmd)
}

func runSpawn(cmd *cobra.Command, args []string) error {
	insightID := args[0]

	// Validate it looks like an insight ID
	if !beads.IsInsightID(insightID) {
		return fmt.Errorf("invalid insight ID format: %s (expected ins-xxx)", insightID)
	}

	s, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()

	// Verify insight exists
	insight, err := s.GetInsight(insightID)
	if err != nil {
		return fmt.Errorf("insight not found: %w", err)
	}

	// Check if beads is present
	beadsAvailable := beads.BeadsPresent()

	if beadsAvailable {
		// Beads is available - give instructions
		fmt.Printf("Insight: %s\n", insight.ID)
		fmt.Printf("Content: %s\n\n", truncateForSpawn(insight.Content, 60))

		fmt.Println("To create a bead from this insight, run:")
		fmt.Printf("  bd create --title=%q\n\n", spawnTitle)

		fmt.Println("Then link the insight to the bead:")
		fmt.Printf("  bdc link %s --spawns=<bead-id>\n\n", insightID)

		fmt.Println("Alternatively, create a placeholder spawns dependency now? (y/n)")
		fmt.Println("Note: You'll need to update the dependency with the real bead ID later.")

	} else {
		// No beads - create placeholder dependency
		fmt.Printf("Insight: %s\n", insight.ID)
		fmt.Printf("Content: %s\n\n", truncateForSpawn(insight.Content, 60))

		fmt.Println("No .beads/ directory found.")
		fmt.Println("Creating placeholder spawns dependency...")

		// Generate a placeholder bead ID
		placeholderID := "bead-" + types.GenerateID("")[4:] // Use same ID generation

		dep := types.NewDependency(insightID, placeholderID, types.DepSpawns)
		if err := s.AddDependency(dep); err != nil {
			return fmt.Errorf("failed to create dependency: %w", err)
		}

		fmt.Printf("\nCreated dependency: %s -> %s [spawns]\n", insightID, placeholderID)
		fmt.Printf("\nPlaceholder bead ID: %s\n", placeholderID)
		fmt.Println("When you create the actual bead, update this dependency with the real ID.")
	}

	return nil
}

func truncateForSpawn(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
