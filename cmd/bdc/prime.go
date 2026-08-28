package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

func (a *app) newPrimeCommand() *cobra.Command {
	var budget int

	cmd := &cobra.Command{
		Use:   "prime",
		Short: "Report what a session may rely on before it starts",
		Long: "Report the ledger's standing conclusions, sorted by what a reader may do with " +
			"them:\n\n" +
			"  mandatory        must be followed\n" +
			"  working default  settled unless a new review says otherwise\n" +
			"  advisory         citable, not settled — not listed here\n" +
			"  cautions         disputed, rejected, or superseded: unusable without a new review\n\n" +
			"A working default is an Insight whose latest verdict is unreviewed or supported " +
			"and whose latest authority is default or mandatory. Mandatory Insights appear in " +
			"both lists, and --budget never drops them: an agent that cannot see a rule will " +
			"break it.",
		Args: cobra.NoArgs,
	}
	budgetFlag(cmd, &budget)

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		n, err := led.Narrative(cmd.Context(), ledger.NarrativeQuery{
			Mode: ledger.ModePrime, Budget: budget,
		})
		if err != nil {
			return result{}, err
		}
		a.warnNotices(n.Notices)
		return result{Data: n, Human: func(w io.Writer) { renderPrime(w, n) }}, nil
	})
	return cmd
}

func renderPrime(w io.Writer, n ledger.Narrative) {
	fmt.Fprintf(w, "%s\n", n.Summary)
	if len(n.Mandatory) > 0 {
		fmt.Fprintln(w, "\nMANDATORY — must be followed")
		renderRules(w, n.Mandatory)
	}
	if len(n.WorkingDefaults) > len(n.Mandatory) {
		fmt.Fprintln(w, "\nWORKING DEFAULTS — settled unless a new review says otherwise")
		for _, i := range n.WorkingDefaults {
			if i.Standing == ledger.StandingMandatory {
				continue // already listed above; repeating it here is noise
			}
			renderRules(w, []ledger.NarrativeInsight{i})
		}
	}
	if len(n.Cautions) > 0 {
		fmt.Fprintln(w, "\nCAUTIONS — unusable without a new review")
		for _, c := range n.Cautions {
			fmt.Fprintf(w, "  %s  %s (%s)\n    %s\n", c.ID, oneLine(c.Title, 56), c.Class, c.Detail)
		}
	}
}

func renderRules(w io.Writer, insights []ledger.NarrativeInsight) {
	for _, i := range insights {
		fmt.Fprintf(w, "  %s  %s (%s, confidence %.2f, %s)\n",
			i.ID, oneLine(i.Title, 56), i.Class, i.Confidence, i.Verdict)
		if i.Excerpt != "" {
			fmt.Fprintf(w, "    %s\n", i.Excerpt)
		}
	}
}
