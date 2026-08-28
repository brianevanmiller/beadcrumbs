package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// validateData is the `{validation, effective_verdict}` shape from the CLI
// contract. The effective verdict is read back from the target's history rather
// than assumed to be the verdict just written.
type validateData struct {
	Validation       ledger.Validation `json:"validation"`
	EffectiveVerdict ledger.Verdict    `json:"effective_verdict"`
}

func (a *app) newValidateCommand() *cobra.Command {
	var (
		verdict      string
		rationale    string
		evidence     []string
		supersededBy string
	)

	cmd := &cobra.Command{
		Use:   "validate <target-id>",
		Short: "Record a review verdict about a Crumb, revision, or proposal",
		Long: "Record an append-only verdict. Nothing is rewritten: an earlier verdict and its " +
			"rationale stay readable, and the current verdict is simply the latest one. A " +
			"record with no verdict at all is unreviewed.\n\n" +
			"A rationale is always required. disputed, rejected, and superseded should cite " +
			"evidence with --evidence when evidence exists; superseded additionally needs " +
			"--superseded-by naming the record that replaced this one.\n\n" +
			"This is not the Crumb review state. `bdc crumb review` moves a Crumb through its " +
			"own lifecycle; this is a judgement about a record's content, and the two are " +
			"stored separately even though both can spell \"rejected\".",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&verdict, "verdict", string(ledger.VerdictSupported),
		"unreviewed, supported, disputed, rejected, or superseded")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why this verdict (required)")
	cmd.Flags().StringArrayVar(&evidence, "evidence", nil,
		"cite a reference as kind:locator[@relation] (repeatable; relation defaults to evidence)")
	cmd.Flags().StringVar(&supersededBy, "superseded-by", "",
		"the Insight revision or proposal that supersedes this record")
	_ = cmd.MarkFlagRequired("rationale")

	cmd.RunE = a.handle(func(cmd *cobra.Command, args []string) (result, error) {
		led, err := a.ledger(cmd.Context())
		if err != nil {
			return result{}, err
		}
		target, err := ledger.TargetRef(args[0])
		if err != nil {
			return result{}, err
		}
		specs, err := parseRefSpecs(evidence, ledger.RelationEvidence)
		if err != nil {
			return result{}, err
		}
		intent := ledger.RecordValidation{
			Target: target, Verdict: ledger.Verdict(verdict),
			Rationale: rationale, Evidence: specs,
		}
		if supersededBy != "" {
			if intent.SupersededBy, err = ledger.TargetRef(supersededBy); err != nil {
				return result{}, err
			}
		}

		res, err := led.RecordValidation(cmd.Context(), intent)
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)
		a.warnNotices(res.Notices)

		data := validateData{Validation: res.Validation, EffectiveVerdict: res.EffectiveVerdict}
		return result{Data: data, Human: func(w io.Writer) {
			fmt.Fprintf(w, "%s %s: recorded %s by %s (%s)\n",
				data.Validation.Target.Kind, data.Validation.Target.ID, data.Validation.Verdict,
				data.Validation.ActorID, data.Validation.ActorKind)
			fmt.Fprintf(w, "  effective verdict: %s\n", data.EffectiveVerdict)
			if !data.Validation.SupersededBy.Zero() {
				fmt.Fprintf(w, "  superseded by %s\n", data.Validation.SupersededBy.ID)
			}
			fmt.Fprintf(w, "  %s\n", data.Validation.Rationale)
		}}, nil
	})
	return cmd
}

// warnNotices renders the ledger's non-fatal channel as envelope warnings. A
// Notice is a condition the caller has to see — an unmet evidence expectation,
// an idempotent hit that diverged, a receipt that cannot prove durability —
// that does not make the write wrong.
func (a *app) warnNotices(notices []ledger.Notice) {
	for _, n := range notices {
		a.out.warn(n.Code, n.Message)
	}
}

// parseRefSpecs reads the repeatable `kind:locator[@relation]` arguments the
// evidence flags take. The fallback relation differs per flag, so it is a
// parameter rather than a default baked in here.
func parseRefSpecs(raw []string, fallback ledger.Relation) ([]ledger.RefSpec, error) {
	specs := make([]ledger.RefSpec, 0, len(raw))
	for _, arg := range raw {
		spec, err := ledger.ParseRefSpec(arg, fallback)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}
