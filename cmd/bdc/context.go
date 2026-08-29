package main

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// budgetFlag is the one place `--budget` is declared, so context, handoff, and
// prime cannot drift on its default or its meaning.
func budgetFlag(cmd *cobra.Command, into *int) {
	cmd.Flags().IntVar(into, "budget", ledger.DefaultBudgetTokens,
		"bound the output to approximately this many tokens; 0 for the whole answer")
}

func (a *app) newContextCommand() *cobra.Command {
	var (
		since   string
		insight string
		limit   int
		budget  int
	)

	cmd := &cobra.Command{
		Use:   "context",
		Short: "Summarise what this repository has concluded and what is still open",
		Long: "Report the current state of the reasoning ledger: recent Insights and what may be " +
			"relied on, the questions still waiting on someone, recent Crumbs, and outstanding " +
			"promotions.\n\n" +
			"With --insight the report narrows to one Insight and the Crumbs its head revision " +
			"was written from, rather than whatever was captured most recently.\n\n" +
			"Output is bounded by --budget. When something is dropped a warning says what and " +
			"how many, so a short answer is never mistaken for a complete one.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&since, "since", "", "only consider records at or after this time")
	cmd.Flags().StringVar(&insight, "insight", "", "narrow the report to one Insight")
	cmd.Flags().IntVar(&limit, "limit", 0, "how many Insights and Crumbs to consider (default 10)")
	budgetFlag(cmd, &budget)

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		at, err := parseTimeFlag("--since", since)
		if err != nil {
			return result{}, err
		}
		q := ledger.NarrativeQuery{Mode: ledger.ModeContext, Since: at, Limit: limit, Budget: budget}
		if insight != "" {
			id, err := ledger.ParseID(ledger.PrefixInsight, insight)
			if err != nil {
				return result{}, err
			}
			q.InsightID = ledger.InsightID(id)
		}
		n, err := led.Narrative(cmd.Context(), q)
		if err != nil {
			return result{}, err
		}
		a.warnNotices(n.Notices)
		return result{Data: n, Human: func(w io.Writer) { renderContext(w, n) }}, nil
	})
	return cmd
}

func renderContext(w io.Writer, n ledger.Narrative) {
	fmt.Fprintf(w, "%s\n", n.Summary)
	if len(n.Insights) > 0 {
		fmt.Fprintln(w, "\nINSIGHTS")
		renderNarrativeInsights(w, n.Insights)
	}
	if len(n.OpenQuestions) > 0 {
		fmt.Fprintln(w, "\nOPEN QUESTIONS")
		for _, q := range n.OpenQuestions {
			fmt.Fprintf(w, "  %s: %s\n", q.Kind, q.Question)
			if q.Subject.ID != "" {
				fmt.Fprintf(w, "    %s\n", q.Subject)
			}
		}
	}
	if len(n.RecentCrumbs) > 0 {
		fmt.Fprintln(w, "\nRECENT CRUMBS")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, c := range n.RecentCrumbs {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", c.ID, c.State,
				c.CapturedAt.Format(time.RFC3339), c.Excerpt)
		}
		tw.Flush()
	}
	if len(n.Promotions) > 0 {
		fmt.Fprintln(w, "\nPROMOTIONS")
		renderNarrativePromotions(w, n.Promotions)
	}
}

func renderNarrativeInsights(w io.Writer, insights []ledger.NarrativeInsight) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, i := range insights {
		fmt.Fprintf(tw, "  %s\trev %d\t%s\t%s\t%s/%s\t%s\n", i.ID, i.Revision, i.Standing,
			i.Class, i.Verdict, i.Authority, oneLine(i.Title, 56))
	}
	tw.Flush()
}

func renderNarrativePromotions(w io.Writer, promotions []ledger.NarrativePromotion) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, p := range promotions {
		waiting := ""
		if p.AuthorityRequired == ledger.RequireHuman && !p.AuthorityHeld {
			waiting = "  (waiting on a human)"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%d attempt(s)\tdurable %t%s\n",
			p.ID, p.Status, p.Destination, p.Attempts, p.Durable, waiting)
	}
	tw.Flush()
}
