package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

func (a *app) newCrumbCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "crumb",
		Short: "List, inspect, review, and prune Crumbs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return usageError(fmt.Errorf("crumb needs a subcommand; run `bdc crumb --help`"))
		},
	}
	cmd.AddCommand(
		a.newCrumbListCommand(),
		a.newCrumbShowCommand(),
		a.newCrumbReviewCommand(),
		a.newCrumbPruneCommand(),
	)
	return cmd
}

func (a *app) newCrumbListCommand() *cobra.Command {
	var (
		states  []string
		since   string
		session string
		limit   int
		offset  int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Crumbs, newest first",
		Args:  cobra.NoArgs,
	}
	cmd.Flags().StringArrayVar(&states, "state", nil, "candidate, accepted, or rejected (repeatable)")
	cmd.Flags().StringVar(&since, "since", "", "only Crumbs captured at or after this time (RFC3339, a date, or a duration like 24h)")
	cmd.Flags().StringVar(&session, "session", "", "only Crumbs captured in this session")
	cmd.Flags().IntVar(&limit, "limit", 0, "return at most this many Crumbs")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip this many Crumbs")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		parsedStates, err := parseReviewStates(states)
		if err != nil {
			return result{}, err
		}
		at, err := parseTimeFlag("--since", since)
		if err != nil {
			return result{}, err
		}
		page, err := led.Crumbs(cmd.Context(), ledger.CrumbQuery{
			States: parsedStates, Since: at, SessionID: session,
			Limit: limit, Offset: offset,
		})
		if err != nil {
			return result{}, err
		}
		return result{Data: page, Human: func(w io.Writer) { renderCrumbs(w, page) }}, nil
	})
	return cmd
}

func (a *app) newCrumbShowCommand() *cobra.Command {
	var events bool

	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one Crumb and everything it participates in",
		Long: "Show one Crumb with its review history, references, and the Harvests and Insight " +
			"revisions it feeds. A Crumb is never consumed: it stays available to further " +
			"Harvests and Insights, which is why both lists can be non-empty at once.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().BoolVar(&events, "events", false, "include the review history in the human rendering")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, args []string, led *ledger.Ledger) (result, error) {
		detail, err := led.Crumb(cmd.Context(), ledger.CrumbID(args[0]))
		if err != nil {
			return result{}, err
		}
		return result{Data: detail, Human: func(w io.Writer) { renderCrumbDetail(w, detail, events) }}, nil
	})
	return cmd
}

func (a *app) newCrumbReviewCommand() *cobra.Command {
	var state, rationale string

	cmd := &cobra.Command{
		Use:   "review <id>...",
		Short: "Accept or reject Crumbs",
		Long: "Record a review decision. Review appends a transition and moves the Crumb's " +
			"state; it never rewrites content, confidence, or capture provenance. Reviewing " +
			"several Crumbs under one rationale is one decision and one transaction.",
		Args: cobra.MinimumNArgs(1),
	}
	cmd.Flags().StringVar(&state, "state", "", "accepted or rejected (required)")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why (required; the decision is the record)")
	_ = cmd.MarkFlagRequired("state")
	_ = cmd.MarkFlagRequired("rationale")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, args []string, led *ledger.Ledger) (result, error) {
		ids := make([]ledger.CrumbID, 0, len(args))
		for _, raw := range args {
			id, err := ledger.ParseID(ledger.PrefixCrumb, raw)
			if err != nil {
				return result{}, err
			}
			ids = append(ids, ledger.CrumbID(id))
		}
		res, err := led.ReviewCrumb(cmd.Context(), ledger.ReviewCrumb{
			IDs: ids, ToState: ledger.ReviewState(state), Rationale: rationale,
		})
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)
		return result{Data: res, Human: func(w io.Writer) {
			for _, e := range res.Events {
				fmt.Fprintf(w, "%s %s -> %s\n", e.CrumbID, e.FromState, e.ToState)
			}
		}}, nil
	})
	return cmd
}

func (a *app) newCrumbPruneCommand() *cobra.Command {
	var (
		ids    []string
		before string
		state  string
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete candidate Crumbs from the ledger head",
		Long: "Prune is the only delete in Beadcrumbs and is allowed for candidate Crumbs only. " +
			"It is a retention operation, not an erase: Dolt retains committed history, so a " +
			"pruned Crumb is still reachable through the ledger's history. A Crumb that " +
			"supports an Insight revision is reported in blocked[] and never deleted.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringArrayVar(&ids, "id", nil, "prune this Crumb (repeatable)")
	cmd.Flags().StringVar(&before, "before", "", "prune candidates captured before this time (RFC3339, a date, or a duration like 720h)")
	cmd.Flags().StringVar(&state, "state", "", "candidate; prune refuses any other state")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the deletion (required)")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		parsed := make([]ledger.CrumbID, 0, len(ids))
		for _, raw := range ids {
			id, err := ledger.ParseID(ledger.PrefixCrumb, raw)
			if err != nil {
				return result{}, err
			}
			parsed = append(parsed, ledger.CrumbID(id))
		}
		at, err := parseTimeFlag("--before", before)
		if err != nil {
			return result{}, err
		}
		res, err := led.PruneCrumbs(cmd.Context(), ledger.PruneCrumbs{
			IDs: parsed, Before: at, State: ledger.ReviewState(state), Confirmed: yes,
		})
		if err != nil {
			return result{}, err
		}
		for _, b := range res.Blocked {
			a.out.warn("prune_blocked", fmt.Sprintf("%s: %s", b.CrumbID, b.Reason))
		}
		return result{Data: res, Human: func(w io.Writer) {
			fmt.Fprintf(w, "pruned %d Crumb(s) from the ledger head; %d blocked\n",
				res.Pruned, len(res.Blocked))
			fmt.Fprintln(w, "committed history retains them: prune is retention, not erasure")
		}}, nil
	})
	return cmd
}

func parseReviewStates(raw []string) ([]ledger.ReviewState, error) {
	known := []ledger.ReviewState{ledger.StateCandidate, ledger.StateAccepted, ledger.StateRejected}
	out := make([]ledger.ReviewState, 0, len(raw))
	for _, r := range raw {
		state := ledger.ReviewState(r)
		found := false
		for _, k := range known {
			if state == k {
				found = true
			}
		}
		if !found {
			return nil, ledger.Fail(ledger.ErrInvalidInput, "invalid_review_state",
				"%q is not a review state; expected candidate, accepted, or rejected", r)
		}
		out = append(out, state)
	}
	return out, nil
}

// parseTimeFlag accepts an absolute time, a plain date, or a duration back from
// now. Agents reach for `--since 24h` and humans reach for a date; refusing
// either would make the flag a lookup rather than a filter.
func parseTimeFlag(flag, raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			d = -d
		}
		return time.Now().UTC().Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, ledger.Fail(ledger.ErrInvalidInput, "invalid_time",
		"%s %q is not a time: use RFC3339, YYYY-MM-DD, or a duration like 24h", flag, raw)
}

func renderCrumbs(w io.Writer, page ledger.CrumbPage) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tCONF\tCAPTURED\tCONTENT")
	for _, c := range page.Crumbs {
		fmt.Fprintf(tw, "%s\t%s\t%.2f\t%s\t%s\n", c.ID, c.ReviewState, c.Confidence,
			c.CapturedAt.Format(time.RFC3339), oneLine(c.Content, 72))
	}
	tw.Flush()
	fmt.Fprintf(w, "%d of %d\n", len(page.Crumbs), page.Total)
}

func renderCrumbDetail(w io.Writer, d ledger.CrumbDetail, events bool) {
	fmt.Fprintf(w, "%s  %s  confidence %.3f  captured %s\n", d.Crumb.ID, d.Crumb.ReviewState,
		d.Crumb.Confidence, d.Crumb.CapturedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "by %s (%s)  redaction %s\n\n%s\n",
		d.Crumb.ActorID, d.Crumb.ActorKind, d.Crumb.RedactionVersion, d.Crumb.Content)
	for _, r := range d.References {
		fmt.Fprintf(w, "\nreference %s %s:%s", r.Relation, r.Kind, r.Locator)
	}
	for _, h := range d.Harvests {
		fmt.Fprintf(w, "\nharvest %s (%s)", h.HarvestID, h.Role)
	}
	for _, i := range d.Insights {
		fmt.Fprintf(w, "\ninsight %s revision %d: %s", i.InsightID, i.Revision, i.Title)
	}
	if events {
		for _, e := range d.ReviewEvents {
			fmt.Fprintf(w, "\n%s %s -> %s: %s", e.OccurredAt.Format(time.RFC3339), e.Kind, e.Summary, e.Rationale)
		}
	}
	fmt.Fprintln(w)
}

// oneLine flattens content for a table cell. The ledger stores what it was
// given; only the human rendering truncates, and the JSON never does.
func oneLine(s string, width int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= width {
		return s
	}
	return s[:width-1] + "…"
}
