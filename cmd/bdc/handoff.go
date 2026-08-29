package main

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

func (a *app) newHandoffCommand() *cobra.Command {
	var (
		since  string
		budget int
	)

	cmd := &cobra.Command{
		Use:   "handoff",
		Short: "Report where the next session picks this up",
		Long: "Report the standing state of the ledger, the work waiting on someone, and how " +
			"fresh the reference cache is.\n\n" +
			"handoff never contacts a tracker. Every reference label it reports was observed at " +
			"the time shown, or never observed at all, and the report says which — a handoff " +
			"that made network calls would fail exactly when a session is ending.\n\n" +
			"--since narrows the new work being handed over, not the state: the totals are " +
			"always the whole ledger, because whoever picks this up needs them.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&since, "since", "", "count unreviewed Crumbs and open proposals from this time")
	budgetFlag(cmd, &budget)

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		at, err := parseTimeFlag("--since", since)
		if err != nil {
			return result{}, err
		}
		n, err := led.Narrative(cmd.Context(), ledger.NarrativeQuery{
			Mode: ledger.ModeHandoff, Since: at, Budget: budget,
		})
		if err != nil {
			return result{}, err
		}
		a.warnNotices(n.Notices)
		return result{Data: n, Human: func(w io.Writer) { renderHandoff(w, n) }}, nil
	})
	return cmd
}

func renderHandoff(w io.Writer, n ledger.Narrative) {
	fmt.Fprintf(w, "%s\n\nSTATE\n", n.Summary)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, state := range sortedKeys(n.State.Crumbs) {
		fmt.Fprintf(tw, "  crumbs %s\t%d\n", state, n.State.Crumbs[state])
	}
	fmt.Fprintf(tw, "  insights\t%d (%d mandatory, %d unusable)\n",
		n.State.Insights, n.State.Mandatory, n.State.Unusable)
	fmt.Fprintf(tw, "  revisions\t%d\n", n.State.Revisions)
	fmt.Fprintf(tw, "  harvests\t%d\n", n.State.Harvests)
	fmt.Fprintf(tw, "  references\t%d\n", n.State.References)
	fmt.Fprintf(tw, "  proposals\t%d\n", n.State.Proposals)
	for _, status := range sortedKeys(n.State.Promotions) {
		fmt.Fprintf(tw, "  promotions %s\t%d\n", status, n.State.Promotions[status])
	}
	fmt.Fprintf(tw, "  unreviewed crumbs\t%d\n", n.UnreviewedCrumbs)
	if n.State.LastActivityAt != nil {
		fmt.Fprintf(tw, "  last activity\t%s\n", n.State.LastActivityAt.Format(time.RFC3339))
	}
	tw.Flush()

	if len(n.OpenProposals) > 0 {
		fmt.Fprintln(w, "\nOPEN PROPOSALS")
		renderNarrativePromotions(w, n.OpenProposals)
	}

	fmt.Fprintf(w, "\nWORKSPACE\n  enrichment: %s\n", n.Workspace.Enrichment)
	for _, k := range n.Workspace.References {
		observed := "never observed"
		if k.Cached > 0 && k.OldestAt != nil {
			observed = fmt.Sprintf("%d cached, oldest %s", k.Cached, k.OldestAt.Format(time.RFC3339))
		}
		fmt.Fprintf(w, "  %s: %d reference(s), %s\n", k.Kind, k.Count, observed)
	}
}

// sortedKeys renders map-valued counts in a stable order. Go randomises map
// iteration, and a summary whose lines move between runs is a summary nobody
// can diff.
func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
