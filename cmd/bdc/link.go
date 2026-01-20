package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

var (
	linkBuildsOn    string
	linkSupersedes  string
	linkContradicts string
	linkSpawns      string
)

var linkCmd = &cobra.Command{
	Use:   "link <from-id>",
	Short: "Create a dependency between insights or beads",
	Long:  `Creates a relationship between insights using dependency types: builds-on, supersedes, contradicts, or spawns.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromID := args[0]

		// Count how many dependency flags are set
		count := 0
		var toID string
		var depType types.DependencyType

		if linkBuildsOn != "" {
			count++
			toID = linkBuildsOn
			depType = types.DepBuildsOn
		}
		if linkSupersedes != "" {
			count++
			toID = linkSupersedes
			depType = types.DepSupersedes
		}
		if linkContradicts != "" {
			count++
			toID = linkContradicts
			depType = types.DepContradicts
		}
		if linkSpawns != "" {
			count++
			toID = linkSpawns
			depType = types.DepSpawns
		}

		if count == 0 {
			return fmt.Errorf("no dependency type specified. Use --builds-on, --supersedes, --contradicts, or --spawns")
		}

		if count > 1 {
			return fmt.Errorf("only one dependency type can be specified at a time")
		}

		// Create the dependency
		dep := types.NewDependency(fromID, toID, depType)

		// Save to store
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		if err := s.AddDependency(dep); err != nil {
			return fmt.Errorf("failed to add dependency: %w", err)
		}

		fmt.Printf("Created dependency: %s -> %s [%s]\n", fromID, toID, depType)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(linkCmd)

	linkCmd.Flags().StringVar(&linkBuildsOn, "builds-on", "", "ID of insight this builds on")
	linkCmd.Flags().StringVar(&linkSupersedes, "supersedes", "", "ID of insight this supersedes")
	linkCmd.Flags().StringVar(&linkContradicts, "contradicts", "", "ID of insight this contradicts")
	linkCmd.Flags().StringVar(&linkSpawns, "spawns", "", "ID of bead this spawned")
}
