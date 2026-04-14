package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var originsCmd = &cobra.Command{
	Use:   "origins",
	Short: "List distinct origins with insight counts",
	Long: `Show all distinct origins (source refs) that have associated insights,
along with insight counts, linked threads, and last activity date.

Example:
  bdc origins`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getReadOnlyStore()
		if err != nil {
			return err
		}
		defer closeStore()

		origins, err := s.ListOrigins()
		if err != nil {
			return fmt.Errorf("failed to list origins: %w", err)
		}

		if len(origins) == 0 {
			if jsonOutput {
				fmt.Println("[]")
				return nil
			}
			fmt.Println("No origins found")
			return nil
		}

		if jsonOutput {
			out, err := json.MarshalIndent(origins, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal JSON: %w", err)
			}
			fmt.Println(string(out))
			return nil
		}

		for _, o := range origins {
			insightWord := "insights"
			if o.InsightCount == 1 {
				insightWord = "insight"
			}

			threadDisplay := o.ThreadIDs
			if threadDisplay == "" {
				threadDisplay = "(no thread)"
			}

			lastActivity := o.LastActivity.Format("2006-01-02")

			fmt.Printf("%-30s  %3d %-8s  %-30s  %s\n",
				o.SourceRef,
				o.InsightCount,
				insightWord,
				threadDisplay,
				lastActivity,
			)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(originsCmd)
}
