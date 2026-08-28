package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// referenceData is the `{reference, link}` shape from the CLI contract.
type referenceData struct {
	Reference ledger.ReferenceView `json:"reference"`
	Link      ledger.ReferenceLink `json:"link"`
}

// referenceListData is `{references[]}`. Each entry carries fetched_at inside
// its freshness, because a cached label a reader cannot date is worse than no
// label at all.
type referenceListData struct {
	References []ledger.ReferenceView `json:"references"`
}

func (a *app) newReferenceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reference",
		Short: "Attach and list tracker-neutral references",
		Long: "A Reference is an opaque locator in an adapter namespace — a beads id, a file " +
			"path, a URL — attached to a Crumb, an Insight revision, a promotion proposal, or a " +
			"validation under one semantic relation. Beadcrumbs never parses a locator and holds " +
			"no tracker-specific field; with no enricher installed a Reference resolves to the " +
			"locator you stored, which is not an error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return usageError(fmt.Errorf("reference needs a subcommand; run `bdc reference --help`"))
		},
	}
	cmd.AddCommand(a.newReferenceAddCommand(), a.newReferenceListCommand())
	return cmd
}

func (a *app) newReferenceAddCommand() *cobra.Command {
	var kind, locator, workspace, relation, label string

	cmd := &cobra.Command{
		Use:   "add <target-id>",
		Short: "Attach a reference to a record",
		Long: "Attach a reference to the record the id names. The target kind is read from the " +
			"id prefix, so a Crumb, revision, proposal, or validation id is the only argument " +
			"needed. Attaching the same locator twice under the same relation is idempotent.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&kind, "kind", "", "adapter namespace, e.g. beads or docs (required)")
	cmd.Flags().StringVar(&locator, "locator", "", "the opaque locator; never parsed (required)")
	cmd.Flags().StringVar(&workspace, "workspace", "", "workspace the locator is scoped to")
	cmd.Flags().StringVar(&relation, "relation", string(ledger.RelationSubject),
		"source, evidence, subject, or spawned-work")
	cmd.Flags().StringVar(&label, "label", "", "display label to record alongside the locator")
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("locator")

	cmd.RunE = a.handle(func(cmd *cobra.Command, args []string) (result, error) {
		led, err := a.ledger(cmd.Context())
		if err != nil {
			return result{}, err
		}
		target, err := ledger.TargetRef(args[0])
		if err != nil {
			return result{}, err
		}
		res, err := led.AttachReference(cmd.Context(), ledger.AttachReference{
			Target: target,
			Ref: ledger.RefSpec{
				Kind: kind, Locator: locator, Workspace: workspace,
				Relation: ledger.Relation(relation),
			},
			Label: label,
		})
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)

		data := referenceData{Reference: res.Reference, Link: res.Link}
		return result{Data: data, Human: func(w io.Writer) {
			verb := "attached"
			if !res.Created {
				verb = "attached (reference already known)"
			}
			fmt.Fprintf(w, "%s %s %s:%s -> %s\n", verb, res.Link.Relation,
				res.Reference.Kind, res.Reference.Locator, target)
		}}, nil
	})
	return cmd
}

func (a *app) newReferenceListCommand() *cobra.Command {
	var (
		target    string
		kinds     []string
		relations []string
		refresh   bool
		limit     int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List references and their cache freshness",
		Long: "List the reference graph. Label and metadata are an observed cache and are never " +
			"authoritative, so every entry carries its freshness: never, cached with the time it " +
			"was fetched, or live when this command observed it.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&target, "target", "", "only references attached to this record id")
	cmd.Flags().StringArrayVar(&kinds, "kind", nil, "only references in this adapter namespace (repeatable)")
	cmd.Flags().StringArrayVar(&relations, "relation", nil,
		"source, evidence, subject, or spawned-work (repeatable)")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-observe the cache through an installed enricher")
	cmd.Flags().IntVar(&limit, "limit", 0, "return at most this many references")

	cmd.RunE = a.handle(func(cmd *cobra.Command, _ []string) (result, error) {
		led, err := a.ledger(cmd.Context())
		if err != nil {
			return result{}, err
		}
		q := ledger.ReferenceQuery{Kinds: kinds, Limit: limit}
		if target != "" {
			ref, err := ledger.TargetRef(target)
			if err != nil {
				return result{}, err
			}
			q.Target = &ref
		}
		for _, raw := range relations {
			q.Relations = append(q.Relations, ledger.Relation(raw))
		}

		if refresh && a.noEnrich {
			a.out.warn("enrichment_disabled", "--no-enrich was given, so --refresh observed nothing")
			refresh = false
		}
		views, err := led.References(cmd.Context(), q, ledger.ReferenceOptions{Refresh: refresh})
		if err != nil {
			return result{}, err
		}
		if refresh {
			a.warnFreshness(cmd.Context(), views)
		}

		data := referenceListData{References: views}
		return result{Data: data, Human: func(w io.Writer) { renderReferences(w, views) }}, nil
	})
	return cmd
}

// warnFreshness reports what a refresh could not do. An absent enricher is not
// a failure, so it is one warning per adapter kind rather than one per
// reference; an enricher that failed is reported per reference, because that is
// where the answer differs.
//
// Beads gets its own code because its absence has a diagnosable reason — no
// `bd`, a version below the floor, or no workspace here — and "install one" is
// a different instruction from "this kind has no adapter at all".
func (a *app) warnFreshness(ctx context.Context, views []ledger.ReferenceView) {
	reported := map[string]bool{}
	for _, v := range views {
		switch {
		case v.Freshness.Error != "":
			a.out.warn("enrich_failed", fmt.Sprintf("%s:%s: %s", v.Kind, v.Locator, v.Freshness.Error))
		case v.Freshness.Enricher != "" || reported[v.Kind]:
		case v.Kind == beadsKind:
			reported[v.Kind] = true
			a.out.warn("beads_unavailable", beadsUnavailable(a.beadsAvailability(ctx)))
		default:
			reported[v.Kind] = true
			a.out.warn("no_enricher", fmt.Sprintf(
				"no enricher is installed for %q; its references resolve to their locator", v.Kind))
		}
	}
}

// beadsKind is the Reference kind the Beads adapter serves. cmd/bdc needs the
// literal to decide which warning to emit; nothing else here knows what a Beads
// locator looks like.
const beadsKind = "beads"

func beadsUnavailable(av *beads.Availability) string {
	reason := "enrichment was disabled with --no-enrich"
	if av != nil {
		reason = "bd is unavailable here: " + av.Reason
	}
	return "beads references resolve to their locator; " + reason
}

func renderReferences(w io.Writer, views []ledger.ReferenceView) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tRELATION\tFRESHNESS\tFETCHED\tDISPLAY")
	for _, v := range views {
		fetched := "-"
		if !v.Freshness.FetchedAt.IsZero() {
			fetched = v.Freshness.FetchedAt.Format(time.RFC3339)
		}
		relation := string(v.Relation)
		if relation == "" {
			relation = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			v.ID, v.Kind, relation, v.Freshness.State, fetched, oneLine(v.Display, 60))
	}
	tw.Flush()
	fmt.Fprintf(w, "%d reference(s)\n", len(views))
}
