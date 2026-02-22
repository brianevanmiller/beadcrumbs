package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	"github.com/brianevanmiller/beadcrumbs/internal/types"
	"github.com/spf13/cobra"
)

var traceCmd = &cobra.Command{
	Use:   "trace <bead-id>",
	Short: "Trace the insight journey that led to a bead",
	Long: `Trace backwards through the insight dependency graph to show how
understanding evolved to spawn the given bead.

Example:
  bdc trace bead-7f2a

Output shows the chain of insights with their relationships:
  ins-aaa "Initial hypothesis" [hypothesis]
    └── builds-on
  ins-bbb "Discovery" [discovery]
    └── supersedes
  ins-ccc "Pivot moment" [pivot]
    └── spawns → bead-7f2a`,
	Args: cobra.ExactArgs(1),
	RunE: runTrace,
}

func init() {
	rootCmd.AddCommand(traceCmd)
}

func runTrace(cmd *cobra.Command, args []string) error {
	beadID := args[0]

	// Validate it looks like a bead ID
	if !beads.IsBeadID(beadID) {
		return fmt.Errorf("invalid bead ID format: %s (expected bead-xxx or bd-xxx)", beadID)
	}

	s, err := getStore()
	if err != nil {
		return err
	}
	defer closeStore()

	// Find insights that spawn this bead
	// We need to search all dependencies for ones that point to this bead
	allInsights, err := s.ListInsights("", "", time.Time{})
	if err != nil {
		return fmt.Errorf("failed to list insights: %w", err)
	}

	// Build a map of insight ID -> insight for quick lookup
	insightMap := make(map[string]*types.Insight)
	for _, ins := range allInsights {
		insightMap[ins.ID] = ins
	}

	// Find the insight(s) that spawn this bead
	var spawningInsights []string
	for _, ins := range allInsights {
		deps, err := s.GetDependencies(ins.ID)
		if err != nil {
			continue
		}
		for _, dep := range deps {
			if dep.To == beadID && dep.Type == types.DepSpawns {
				spawningInsights = append(spawningInsights, ins.ID)
			}
		}
	}

	// Also find insights linked via thread-to-bead mappings
	externalRef := beads.BeadIDToExternalRef(beadID)
	mapping, _ := s.GetExternalRefMappingByRef(externalRef)

	var threadInsights []*types.Insight
	if mapping != nil {
		threadInsights, _ = s.ListInsights(mapping.ThreadID, "", time.Time{})
	}

	if len(spawningInsights) == 0 && len(threadInsights) == 0 {
		fmt.Printf("No insights found that spawn %s\n", beadID)
		if !beads.BeadsPresent() {
			fmt.Println("\nNote: No .beads/ directory found. The bead may exist in another project.")
		}
		return nil
	}

	fmt.Printf("Trace for %s:\n\n", beadID)

	// Build chains once to avoid redundant traceChain calls
	chains := make(map[string][]chainItem)
	for _, spawnID := range spawningInsights {
		chains[spawnID] = traceChain(spawnID, insightMap, s)
	}

	// Show dependency chain (spawns relationships)
	if len(spawningInsights) > 0 {
		fmt.Println("Dependency chain:")
		for _, spawnID := range spawningInsights {
			chain := chains[spawnID]

			// Print the chain
			for i, item := range chain {
				ins := insightMap[item.insightID]
				if ins == nil {
					continue
				}

				symbol := getInsightSymbol(ins.Type)
				typeStr := string(ins.Type)
				if ins.Type == types.InsightPivot || ins.Type == types.InsightDecision {
					typeStr = strings.ToUpper(typeStr)
				}

				fmt.Printf("%s \"%s\" [%s]\n", symbol, truncateForTrace(ins.Content, 50), typeStr)

				if i < len(chain)-1 {
					fmt.Printf("  └── %s\n", chain[i+1].relationFromPrev)
				} else {
					fmt.Printf("  └── spawns → %s\n", beadID)
				}
			}
			fmt.Println()
		}
	}

	// Show thread-linked insights (via external_ref_mappings)
	if len(threadInsights) > 0 {
		// Collect IDs already shown in dependency chains to avoid duplication
		shownIDs := make(map[string]bool)
		for _, spawnID := range spawningInsights {
			for _, item := range chains[spawnID] {
				shownIDs[item.insightID] = true
			}
		}

		var newInsights []*types.Insight
		for _, ins := range threadInsights {
			if !shownIDs[ins.ID] {
				newInsights = append(newInsights, ins)
			}
		}

		if len(newInsights) > 0 {
			fmt.Printf("Thread insights (thread %s):\n", mapping.ThreadID)
			for _, ins := range newInsights {
				symbol := getInsightSymbol(ins.Type)
				typeStr := string(ins.Type)
				if ins.Type == types.InsightPivot || ins.Type == types.InsightDecision {
					typeStr = strings.ToUpper(typeStr)
				}
				fmt.Printf("  %s %s \"%s\" [%s]\n", symbol, ins.ID, truncateForTrace(ins.Content, 50), typeStr)
			}
			fmt.Println()
		}
	}

	return nil
}

type chainItem struct {
	insightID        string
	relationFromPrev string
}

// traceChain walks backwards from an insight through builds-on/supersedes relationships.
func traceChain(startID string, insightMap map[string]*types.Insight, s interface {
	GetDependents(toID string) ([]*types.Dependency, error)
}) []chainItem {
	var chain []chainItem
	visited := make(map[string]bool)
	current := startID

	for current != "" && !visited[current] {
		visited[current] = true

		// Find what this insight builds-on or supersedes
		deps, err := s.GetDependents(current)
		if err != nil {
			break
		}

		var prevID string
		var relation string
		for _, dep := range deps {
			if dep.Type == types.DepBuildsOn || dep.Type == types.DepSupersedes {
				prevID = dep.From
				relation = string(dep.Type)
				break
			}
		}

		chain = append([]chainItem{{insightID: current, relationFromPrev: relation}}, chain...)
		current = prevID
	}

	return chain
}

func truncateForTrace(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
