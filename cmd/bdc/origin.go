package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var originCmd = &cobra.Command{
	Use:   "origin",
	Short: "Manage the origin identifier for this beadcrumbs instance",
	Long: `Get or set the origin identifier for this beadcrumbs instance.

The origin is stored in .beadcrumbs/origin and identifies the source
context (e.g., a Claude session ID or Notion page reference) from which
insights were captured.

Examples:
  bdc origin set claude:sess_abc123
  bdc origin show
  bdc origin clear`,
}

var originSetCmd = &cobra.Command{
	Use:   "set <value>",
	Short: "Set the origin identifier",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		value := strings.TrimSpace(args[0])
		if value == "" {
			return fmt.Errorf("origin value cannot be empty")
		}

		dir := filepath.Dir(dbPath)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return fmt.Errorf("beadcrumbs not initialized: directory %s does not exist. Run 'bdc init' first", dir)
		}

		originPath := filepath.Join(dir, "origin")
		if err := os.WriteFile(originPath, []byte(value+"\n"), 0644); err != nil {
			return fmt.Errorf("failed to write origin file: %w", err)
		}

		fmt.Printf("Origin set to: %s\n", value)
		return nil
	},
}

var originShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current origin identifier",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := filepath.Dir(dbPath)
		originPath := filepath.Join(dir, "origin")

		content, err := os.ReadFile(originPath)
		if os.IsNotExist(err) {
			fmt.Println("(none)")
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read origin file: %w", err)
		}

		fmt.Println(strings.TrimSpace(string(content)))
		return nil
	},
}

var originClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear the origin identifier",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := filepath.Dir(dbPath)
		originPath := filepath.Join(dir, "origin")

		if err := os.Remove(originPath); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("Origin is already unset")
				return nil
			}
			return fmt.Errorf("failed to remove origin file: %w", err)
		}

		fmt.Println("Origin cleared")
		return nil
	},
}

// readOriginFile reads the origin from .beadcrumbs/origin, returning empty string if not found.
func readOriginFile() string {
	dir := filepath.Dir(dbPath)
	originPath := filepath.Join(dir, "origin")
	content, err := os.ReadFile(originPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

func init() {
	rootCmd.AddCommand(originCmd)
	originCmd.AddCommand(originSetCmd)
	originCmd.AddCommand(originShowCmd)
	originCmd.AddCommand(originClearCmd)
}
