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

// defaultProposalConfidence mirrors capture's: confidence is an independent
// axis and a silent 1.0 would claim certainty the caller never expressed.
const defaultProposalConfidence = 0.5

// proposeData is the `{proposal, created, content_hash, authority_required}`
// shape from the CLI contract. created=false is an idempotent hit, which is the
// answer rather than an error.
type proposeData struct {
	Proposal          ledger.Proposal             `json:"proposal"`
	Created           bool                        `json:"created"`
	ContentHash       string                      `json:"content_hash"`
	AuthorityRequired ledger.AuthorityRequirement `json:"authority_required"`
}

// recordData is `{promotion, receipt}`. Durable states whether the destination
// declared it can anchor a record at all, because a receipt from a destination
// that cannot proves an attempt happened and nothing more.
type recordData struct {
	Promotion ledger.Promotion `json:"promotion"`
	Receipt   ledger.Receipt   `json:"receipt"`
	Durable   bool             `json:"durable"`
}

// promotionData is `{promotion}`, the shape both terminal outcomes that write
// no receipt return.
type promotionData struct {
	Promotion ledger.Promotion `json:"promotion"`
}

// promotionListData is `{proposals[]}`, each with its attempts and receipt.
type promotionListData struct {
	Proposals []ledger.PromotionView `json:"proposals"`
}

func (a *app) newPromoteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Propose, record, reject, and fail durable promotions",
		Long: "A Promotion Proposal is a request to turn one Insight revision into one durable " +
			"external record. Beadcrumbs never performs the external write: it proposes, an " +
			"actor writes, and the receipt is recorded here.\n\n" +
			"Proposals are idempotent by content hash and immutable once written. Each " +
			"destination is its own proposal and each try is its own attempt, so a failure at " +
			"one destination has no effect on any other.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return usageError(fmt.Errorf("promote needs a subcommand; run `bdc promote --help`"))
		},
	}
	cmd.AddCommand(
		a.newPromoteProposeCommand(),
		a.newPromoteRecordCommand(),
		a.newPromoteRejectCommand(),
		a.newPromoteFailCommand(),
		a.newPromoteListCommand(),
	)
	return cmd
}

func (a *app) newPromoteProposeCommand() *cobra.Command {
	var (
		insight      string
		revision     int
		class        string
		destination  string
		workspace    string
		capabilities []string
		evidence     []string
		content      string
		contentFile  string
		authority    string
		supersedes   string
		confidence   float64
	)

	cmd := &cobra.Command{
		Use:   "propose",
		Short: "Record a proposal to promote an Insight revision to a destination",
		Long: "Record a proposal. Nothing is written externally and no adapter is called.\n\n" +
			"Re-proposing identical content to the same destination returns the existing " +
			"proposal with created=false — the content hash is a unique index, so idempotency " +
			"is a property of the database rather than a convention. Confidence and evidence " +
			"are outside the hash: changing either names the same proposal, and the difference " +
			"is reported rather than applied, because proposals are immutable.\n\n" +
			"Capabilities are declared, never inferred. requires-human-authority and the " +
			"`policy` class each mean a human must decide; a blocked proposal is still " +
			"recorded, with exit 3, so a human can grant authority and retry against it.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&insight, "insight", "", "the Insight to promote (required)")
	cmd.Flags().IntVar(&revision, "revision", 0, "promote this revision instead of the head")
	cmd.Flags().StringVar(&class, "class", "", "semantic class: "+strings.Join(ledger.Classes(), ", ")+" (required)")
	cmd.Flags().StringVar(&destination, "destination", "", "where it would land, as kind:locator (required)")
	cmd.Flags().StringVar(&workspace, "workspace", "", "workspace the locator is scoped to")
	cmd.Flags().StringArrayVar(&capabilities, "capability", nil,
		"a capability the destination declares (repeatable): requires-human-authority, "+
			"supports-supersession, supports-review-thread, append-only, stable-anchor, content-addressable")
	cmd.Flags().StringArrayVar(&evidence, "evidence", nil,
		"cite a reference as kind:locator[@relation] (repeatable; relation defaults to evidence)")
	cmd.Flags().StringVar(&content, "content", "", "the content that would be written")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read the content from a file, or - for stdin")
	cmd.Flags().StringVar(&authority, "authority", string(ledger.AuthorityAdvisory),
		"the authority the promoted record would carry: advisory, default, or mandatory")
	cmd.Flags().StringVar(&supersedes, "supersedes", "", "the proposal this one replaces")
	cmd.Flags().Float64Var(&confidence, "confidence", defaultProposalConfidence,
		"author confidence in this proposal, 0 to 1")
	_ = cmd.MarkFlagRequired("insight")
	_ = cmd.MarkFlagRequired("class")
	_ = cmd.MarkFlagRequired("destination")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		id, err := ledger.ParseID(ledger.PrefixInsight, insight)
		if err != nil {
			return result{}, err
		}
		dest, err := ledger.ParseDestination(destination)
		if err != nil {
			return result{}, err
		}
		dest.Workspace = workspace
		for _, raw := range capabilities {
			dest.Capabilities = append(dest.Capabilities, ledger.Capability(raw))
		}
		specs, err := parseRefSpecs(evidence, ledger.RelationEvidence)
		if err != nil {
			return result{}, err
		}
		body, err := readTextInput("--content", content, contentFile, cmd.InOrStdin())
		if err != nil {
			return result{}, err
		}
		intent := ledger.ProposePromotion{
			InsightID: ledger.InsightID(id), Revision: revision, Class: class,
			Destination: dest, Content: body, Evidence: specs,
			RequestedAuthority: ledger.AuthorityLevel(authority), Confidence: confidence,
		}
		if supersedes != "" {
			superseded, err := ledger.ParseID(ledger.PrefixProposal, supersedes)
			if err != nil {
				return result{}, err
			}
			intent.Supersedes = ledger.ProposalID(superseded)
		}

		// An authority block still recorded the proposal, so the warnings are
		// emitted before the error is returned: they describe a write that
		// happened.
		res, err := led.ProposePromotion(cmd.Context(), intent)
		a.warnRedaction(res.Findings)
		a.warnNotices(res.Notices)
		if err != nil {
			return result{}, err
		}

		data := proposeData{
			Proposal: res.Proposal, Created: res.Created,
			ContentHash: res.ContentHash, AuthorityRequired: res.AuthorityRequired,
		}
		return result{Data: data, Human: func(w io.Writer) {
			verb := "proposed"
			if !res.Created {
				verb = "already proposed"
			}
			fmt.Fprintf(w, "%s %s -> %s:%s (%s, confidence %.3f)\n", verb, data.Proposal.ID,
				data.Proposal.DestKind, data.Proposal.DestLocator,
				data.Proposal.Class, data.Proposal.Confidence)
			fmt.Fprintf(w, "  hash %s  authority required: %s\n",
				data.ContentHash, data.AuthorityRequired)
		}}, nil
	})
	return cmd
}

func (a *app) newPromoteRecordCommand() *cobra.Command {
	var (
		locator      string
		anchor       string
		externalHash string
		verified     bool
	)

	cmd := &cobra.Command{
		Use:   "record <proposal-id>",
		Short: "Record the receipt of a promotion that was written",
		Long: "Record that the external write landed. The locator is the one that was actually " +
			"written, which may differ from the proposed one — ADR numbering is decided by the " +
			"repository, not by the proposal.\n\n" +
			"An anchor is only as strong as the destination's declared capabilities: without " +
			"stable-anchor the receipt proves an attempt happened, not that a durable record " +
			"exists, and the output says so. Pass --verified only when the record was read " +
			"back rather than asserted.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&locator, "locator", "", "the locator that was written (required)")
	cmd.Flags().StringVar(&anchor, "anchor", "", "proof of the written version: commit SHA, comment id, timestamp")
	cmd.Flags().StringVar(&externalHash, "external-hash", "", "the destination's own content hash, when it has one")
	cmd.Flags().BoolVar(&verified, "verified", false, "the recorder observed the written record rather than asserting it")
	_ = cmd.MarkFlagRequired("locator")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, args []string, led *ledger.Ledger) (result, error) {
		id, err := ledger.ParseID(ledger.PrefixProposal, args[0])
		if err != nil {
			return result{}, err
		}
		res, err := led.RecordPromotion(cmd.Context(), ledger.RecordPromotion{
			ProposalID: ledger.ProposalID(id), Locator: locator,
			Anchor: anchor, ExternalHash: externalHash, Verified: verified,
		})
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)
		a.warnNotices(res.Notices)

		data := recordData{Promotion: res.Promotion, Receipt: res.Receipt, Durable: res.Durable}
		return result{Data: data, Human: func(w io.Writer) {
			fmt.Fprintf(w, "attempt %d applied: %s %s\n", data.Promotion.Attempt,
				data.Receipt.Kind, data.Receipt.Locator)
			proof := "no durable anchor"
			if data.Durable {
				proof = "anchored at " + data.Receipt.Anchor
			}
			fmt.Fprintf(w, "  receipt %s  %s\n", data.Receipt.ID, proof)
		}}, nil
	})
	return cmd
}

func (a *app) newPromoteRejectCommand() *cobra.Command {
	var rationale string

	cmd := &cobra.Command{
		Use:   "reject <proposal-id>",
		Short: "Record a decision not to write a proposal",
		Long: "Record that someone decided not to write this. That is different from a write " +
			"that failed: use `bdc promote fail` for an attempt that did not land. Both leave " +
			"the proposal retryable — the next record or fail is the following attempt.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&rationale, "rationale", "", "why this was not written (required)")
	_ = cmd.MarkFlagRequired("rationale")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, args []string, led *ledger.Ledger) (result, error) {
		id, err := ledger.ParseID(ledger.PrefixProposal, args[0])
		if err != nil {
			return result{}, err
		}
		res, err := led.RejectPromotion(cmd.Context(), ledger.RejectPromotion{
			ProposalID: ledger.ProposalID(id), Rationale: rationale,
		})
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)
		return outcomeResult(res.Promotion), nil
	})
	return cmd
}

func (a *app) newPromoteFailCommand() *cobra.Command {
	var detail string

	cmd := &cobra.Command{
		Use:   "fail <proposal-id>",
		Short: "Record a promotion attempt that did not land",
		Long: "Record that the external write was attempted and failed. The proposal stays " +
			"retryable and the next record or fail is the following attempt, so a destination " +
			"outage does not strand a proposal at proposed forever.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&detail, "detail", "", "what went wrong (required)")
	_ = cmd.MarkFlagRequired("detail")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, args []string, led *ledger.Ledger) (result, error) {
		id, err := ledger.ParseID(ledger.PrefixProposal, args[0])
		if err != nil {
			return result{}, err
		}
		res, err := led.FailPromotion(cmd.Context(), ledger.FailPromotion{
			ProposalID: ledger.ProposalID(id), Detail: detail,
		})
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)
		return outcomeResult(res.Promotion), nil
	})
	return cmd
}

func (a *app) newPromoteListCommand() *cobra.Command {
	var (
		insight  string
		statuses []string
		destKind []string
		limit    int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List proposals with their attempts and receipts",
		Args:  cobra.NoArgs,
	}
	cmd.Flags().StringVar(&insight, "insight", "", "only proposals for this Insight")
	cmd.Flags().StringArrayVar(&statuses, "status", nil,
		"latest attempt status (repeatable): proposed, applied, rejected, failed, superseded")
	cmd.Flags().StringArrayVar(&destKind, "destination-kind", nil, "only this adapter namespace (repeatable)")
	cmd.Flags().IntVar(&limit, "limit", 0, "return at most this many proposals")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		q := ledger.PromotionQuery{DestKinds: destKind, Limit: limit}
		if insight != "" {
			id, err := ledger.ParseID(ledger.PrefixInsight, insight)
			if err != nil {
				return result{}, err
			}
			q.InsightID = ledger.InsightID(id)
		}
		for _, raw := range statuses {
			q.Statuses = append(q.Statuses, ledger.PromotionStatus(raw))
		}

		views, err := led.Promotions(cmd.Context(), q)
		if err != nil {
			return result{}, err
		}
		data := promotionListData{Proposals: views}
		return result{Data: data, Human: func(w io.Writer) { renderPromotions(w, views) }}, nil
	})
	return cmd
}

func outcomeResult(p ledger.Promotion) result {
	data := promotionData{Promotion: p}
	return result{Data: data, Human: func(w io.Writer) {
		fmt.Fprintf(w, "attempt %d %s: %s\n", data.Promotion.Attempt, data.Promotion.Status, data.Promotion.Detail)
	}}
}

func renderPromotions(w io.Writer, views []ledger.PromotionView) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCLASS\tSTATUS\tATTEMPTS\tAUTHORITY\tDURABLE\tCREATED\tDESTINATION")
	for _, v := range views {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%t\t%s\t%s:%s\n", v.ID, v.Class, v.Status,
			len(v.Attempts), v.AuthorityRequired, v.Durable,
			v.CreatedAt.Format(time.RFC3339), v.DestKind, oneLine(v.DestLocator, 40))
	}
	tw.Flush()
	fmt.Fprintf(w, "%d proposal(s)\n", len(views))
}
