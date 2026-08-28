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

// insightRevisionData is the `{insight, revision}` shape from the CLI contract.
type insightRevisionData struct {
	Insight  ledger.Insight         `json:"insight"`
	Revision ledger.InsightRevision `json:"revision"`
}

func (a *app) newInsightCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "insight",
		Short: "List, inspect, and revise Insights",
		Long: "An Insight is what a Harvest concluded from one or more Crumbs. Its revisions " +
			"are immutable and append-only: revising records the next revision with its parent " +
			"and a rationale, and every earlier revision stays readable exactly as it was " +
			"written. Insights are created by `bdc harvest`, never here.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return usageError(fmt.Errorf("insight needs a subcommand; run `bdc insight --help`"))
		},
	}
	cmd.AddCommand(
		a.newInsightListCommand(),
		a.newInsightShowCommand(),
		a.newInsightReviseCommand(),
	)
	return cmd
}

func (a *app) newInsightListCommand() *cobra.Command {
	var (
		classes    []string
		since      string
		verdicts   []string
		authority  []string
		limit      int
		offsetFlag int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Insights at their head revision",
		Args:  cobra.NoArgs,
	}
	cmd.Flags().StringArrayVar(&classes, "class", nil,
		"semantic class (repeatable): "+strings.Join(ledger.Classes(), ", "))
	cmd.Flags().StringVar(&since, "since", "", "only Insights whose head was written at or after this time")
	cmd.Flags().StringArrayVar(&verdicts, "verdict", nil,
		"latest verdict (repeatable): unreviewed, supported, disputed, rejected, superseded")
	cmd.Flags().StringArrayVar(&authority, "authority", nil,
		"latest authority (repeatable): advisory, default, mandatory")
	cmd.Flags().IntVar(&limit, "limit", 0, "return at most this many Insights")
	cmd.Flags().IntVar(&offsetFlag, "offset", 0, "skip this many Insights")

	cmd.RunE = a.handle(func(cmd *cobra.Command, _ []string) (result, error) {
		led, err := a.ledger(cmd.Context())
		if err != nil {
			return result{}, err
		}
		at, err := parseTimeFlag("--since", since)
		if err != nil {
			return result{}, err
		}
		parsedVerdicts, err := parseVerdicts(verdicts)
		if err != nil {
			return result{}, err
		}
		parsedAuthority, err := parseAuthorityLevels(authority)
		if err != nil {
			return result{}, err
		}
		page, err := led.Insights(cmd.Context(), ledger.InsightQuery{
			Classes: classes, Since: at,
			Verdicts: parsedVerdicts, AuthorityLevels: parsedAuthority,
			Limit: limit, Offset: offsetFlag,
		})
		if err != nil {
			return result{}, err
		}
		return result{Data: page, Human: func(w io.Writer) { renderInsights(w, page) }}, nil
	})
	return cmd
}

func (a *app) newInsightShowCommand() *cobra.Command {
	var (
		revision int
		lineage  bool
	)

	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one Insight with its revisions, evidence, and judgements",
		Long: "Show one Insight. Without --revision this reports the head; with it, the " +
			"revision named, including the supporting Crumbs that revision was written with. " +
			"--lineage adds the derivation chain: which revision came from which parent, out " +
			"of which Harvest, on which evidence.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().IntVar(&revision, "revision", 0, "show this revision instead of the head")
	cmd.Flags().BoolVar(&lineage, "lineage", false, "include the derivation chain")

	cmd.RunE = a.handle(func(cmd *cobra.Command, args []string) (result, error) {
		led, err := a.ledger(cmd.Context())
		if err != nil {
			return result{}, err
		}
		id, err := ledger.ParseID(ledger.PrefixInsight, args[0])
		if err != nil {
			return result{}, err
		}
		detail, err := led.Insight(cmd.Context(), ledger.InsightID(id),
			ledger.InsightOptions{Revision: revision, Lineage: lineage})
		if err != nil {
			return result{}, err
		}
		return result{Data: detail, Human: func(w io.Writer) { renderInsightDetail(w, detail) }}, nil
	})
	return cmd
}

func (a *app) newInsightReviseCommand() *cobra.Command {
	var (
		content     string
		contentFile string
		rationale   string
		title       string
		class       string
		confidence  float64
		crumbs      []string
	)

	cmd := &cobra.Command{
		Use:   "revise <id>",
		Short: "Append the next revision of an Insight",
		Long: "Append a revision. The earlier revision is never rewritten, so a rationale is " +
			"required: the ledger keeps both, and the reason one replaced the other is the " +
			"part a later reader needs.\n\n" +
			"Title, class, and confidence carry forward when unset. Evidence accumulates: the " +
			"new revision inherits its parent's supporting Crumbs and --crumb adds more. A " +
			"revision cannot drop evidence.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&content, "content", "", "the revised content")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read the revised content from a file, or - for stdin")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why this revision replaces the last one (required)")
	cmd.Flags().StringVar(&title, "title", "", "a new title; carried forward when unset")
	cmd.Flags().StringVar(&class, "class", "", "a new semantic class; carried forward when unset")
	cmd.Flags().Float64Var(&confidence, "confidence", 0, "a new author confidence; carried forward when unset")
	cmd.Flags().StringArrayVar(&crumbs, "crumb", nil, "add this Crumb as supporting evidence (repeatable)")
	_ = cmd.MarkFlagRequired("rationale")

	cmd.RunE = a.handle(func(cmd *cobra.Command, args []string) (result, error) {
		led, err := a.ledger(cmd.Context())
		if err != nil {
			return result{}, err
		}
		id, err := ledger.ParseID(ledger.PrefixInsight, args[0])
		if err != nil {
			return result{}, err
		}
		body, err := readTextInput("--content", content, contentFile, cmd.InOrStdin())
		if err != nil {
			return result{}, err
		}
		ids, err := parseCrumbIDs(crumbs)
		if err != nil {
			return result{}, err
		}
		intent := ledger.ReviseInsight{
			InsightID: ledger.InsightID(id), Title: title, Content: body,
			Class: class, Rationale: rationale, Crumbs: ids,
		}
		// An unset --confidence carries forward; zero is a value a caller may
		// legitimately mean, so the flag's presence is what decides.
		if cmd.Flags().Changed("confidence") {
			intent.Confidence = &confidence
		}

		res, err := led.ReviseInsight(cmd.Context(), intent)
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)

		data := insightRevisionData{Insight: res.Insight, Revision: res.Revision}
		return result{Data: data, Human: func(w io.Writer) {
			fmt.Fprintf(w, "%s revision %d: %s (%s, confidence %.3f)\n",
				data.Revision.InsightID, data.Revision.Revision, data.Revision.Title,
				data.Revision.Class, data.Revision.Confidence)
			fmt.Fprintf(w, "  from revision %s: %s\n", data.Revision.ParentRevisionID, data.Revision.Rationale)
		}}, nil
	})
	return cmd
}

func parseVerdicts(raw []string) ([]ledger.Verdict, error) {
	known := []ledger.Verdict{
		ledger.VerdictUnreviewed, ledger.VerdictSupported, ledger.VerdictDisputed,
		ledger.VerdictRejected, ledger.VerdictSuperseded,
	}
	out := make([]ledger.Verdict, 0, len(raw))
	for _, r := range raw {
		v := ledger.Verdict(r)
		if !contains(known, v) {
			return nil, ledger.Fail(ledger.ErrInvalidInput, "invalid_verdict",
				"%q is not a verdict; expected unreviewed, supported, disputed, rejected, or superseded", r)
		}
		out = append(out, v)
	}
	return out, nil
}

func parseAuthorityLevels(raw []string) ([]ledger.AuthorityLevel, error) {
	known := []ledger.AuthorityLevel{
		ledger.AuthorityAdvisory, ledger.AuthorityDefault, ledger.AuthorityMandatory,
	}
	out := make([]ledger.AuthorityLevel, 0, len(raw))
	for _, r := range raw {
		level := ledger.AuthorityLevel(r)
		if !contains(known, level) {
			return nil, ledger.Fail(ledger.ErrInvalidInput, "invalid_authority",
				"%q is not an authority level; expected advisory, default, or mandatory", r)
		}
		out = append(out, level)
	}
	return out, nil
}

func contains[T comparable](values []T, want T) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func renderInsights(w io.Writer, page ledger.InsightPage) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tREV\tCLASS\tCONF\tVERDICT\tAUTHORITY\tUPDATED\tTITLE")
	for _, i := range page.Insights {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%.2f\t%s\t%s\t%s\t%s\n", i.ID, i.HeadRevision, i.Class,
			i.Confidence, i.Verdict, i.Authority, i.UpdatedAt.Format(time.RFC3339), oneLine(i.Title, 56))
	}
	tw.Flush()
	fmt.Fprintf(w, "%d of %d\n", len(page.Insights), page.Total)
}

func renderInsightDetail(w io.Writer, d ledger.InsightDetail) {
	r := d.Revision
	fmt.Fprintf(w, "%s revision %d of %d  %s  confidence %.3f\n",
		d.Insight.ID, r.Revision, d.Insight.HeadRevision, r.Class, r.Confidence)
	fmt.Fprintf(w, "by %s (%s)  %s\n\n%s\n\n%s\n",
		r.ActorID, r.ActorKind, r.CreatedAt.Format(time.RFC3339), r.Title, r.Content)
	if r.Rationale != "" {
		fmt.Fprintf(w, "\nrevised because: %s\n", r.Rationale)
	}
	for _, c := range d.Crumbs {
		fmt.Fprintf(w, "\ncrumb %s (%s) %s", c.ID, c.ReviewState, oneLine(c.Content, 64))
	}
	for _, ref := range d.References {
		fmt.Fprintf(w, "\nreference %s %s:%s", ref.Relation, ref.Kind, ref.Locator)
	}
	for _, v := range d.Validations {
		fmt.Fprintf(w, "\nvalidation %s: %s", v.Summary, v.Rationale)
	}
	for _, auth := range d.Authorities {
		fmt.Fprintf(w, "\nauthority %s: %s", auth.Summary, auth.Rationale)
	}
	for _, p := range d.Proposals {
		fmt.Fprintf(w, "\nproposal %s -> %s:%s", p.ID, p.DestKind, p.DestLocator)
	}
	for _, step := range d.Lineage {
		fmt.Fprintf(w, "\nlineage revision %d (%d crumbs)", step.Revision, len(step.Crumbs))
		if step.ParentID != "" {
			fmt.Fprintf(w, " from %s", step.ParentID)
		}
	}
	fmt.Fprintln(w)
}
