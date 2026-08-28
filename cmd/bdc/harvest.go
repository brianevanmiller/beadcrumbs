package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// harvestData is the `{harvest, insight, revision, crumbs_captured[], redaction}`
// shape from the CLI contract. Insight and revision are null when the Harvest
// weighed Crumbs without concluding anything, and on a dry run.
type harvestData struct {
	Harvest        ledger.Harvest          `json:"harvest"`
	Insight        *ledger.Insight         `json:"insight"`
	Revision       *ledger.InsightRevision `json:"revision"`
	CrumbsCaptured []ledger.Crumb          `json:"crumbs_captured"`
	Redaction      redactionReport         `json:"redaction"`
}

// redactionReport travels in data, not only in warnings[], because a harvest is
// the one command whose whole job is to move text into durable storage: a
// caller has to be able to see what was rewritten on the way. A finding names
// the rule and the position and never the value.
type redactionReport struct {
	Version  string           `json:"version"`
	Findings []ledger.Finding `json:"findings"`
}

// defaultInsightConfidence is the author confidence a synthesis records when
// none is given. Declared rather than inferred: a silent 1.0 would claim a
// certainty the caller never expressed.
const defaultInsightConfidence = 0.5

func (a *app) newHarvestCommand() *cobra.Command {
	var (
		crumbs      []string
		since       string
		title       string
		content     string
		contentFile string
		class       string
		confidence  float64
		auto        bool
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "harvest",
		Short: "Weigh Crumbs and synthesise them into an Insight",
		Long: "Harvest is the durable-completion step: run it before compaction and before " +
			"opening or merging a pull request.\n\n" +
			"With --title, --content, and --class it synthesises the named Crumbs into " +
			"revision 1 of a new Insight. Without them it records what it weighed and stops, " +
			"which is the right answer when a session has fragments but no conclusion yet.\n\n" +
			"Crumbs are never consumed: a Crumb stays available to every later Harvest and " +
			"Insight, and being selected is a relationship, not a state.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringArrayVar(&crumbs, "crumb", nil, "weigh this Crumb (repeatable; selected when synthesising)")
	cmd.Flags().StringVar(&since, "since", "",
		"also weigh every candidate captured at or after this time (RFC3339, a date, or a duration like 24h)")
	cmd.Flags().StringVar(&title, "title", "", "the Insight's title")
	cmd.Flags().StringVar(&content, "content", "", "the synthesised content")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read the synthesised content from a file, or - for stdin")
	cmd.Flags().StringVar(&class, "class", "", "semantic class: "+strings.Join(ledger.Classes(), ", "))
	cmd.Flags().Float64Var(&confidence, "confidence", defaultInsightConfidence,
		"author confidence in the Insight, 0 to 1")
	cmd.Flags().BoolVar(&auto, "auto", false, "record this as an automatic harvest rather than a manual one")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be synthesised and write nothing but the aborted Harvest")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		body, err := readTextInput("--content", content, contentFile, cmd.InOrStdin())
		if err != nil {
			return result{}, err
		}
		at, err := parseTimeFlag("--since", since)
		if err != nil {
			return result{}, err
		}
		ids, err := parseCrumbIDs(crumbs)
		if err != nil {
			return result{}, err
		}

		mode := ledger.HarvestManual
		if auto {
			mode = ledger.HarvestAutomatic
		}
		res, err := led.CompleteHarvest(cmd.Context(), ledger.CompleteHarvest{
			Mode: mode, Crumbs: ids, Since: at,
			Title: title, Content: body, Class: class, Confidence: confidence,
			DryRun: dryRun,
		})
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)

		data := harvestData{
			Harvest:        res.Harvest,
			Insight:        res.Insight,
			Revision:       res.Revision,
			CrumbsCaptured: res.CrumbsCaptured,
			Redaction:      redactionReport{Version: res.Harvest.RedactionVersion, Findings: res.Findings},
		}
		if data.CrumbsCaptured == nil {
			data.CrumbsCaptured = []ledger.Crumb{}
		}
		if data.Redaction.Findings == nil {
			data.Redaction.Findings = []ledger.Finding{}
		}
		return result{Data: data, Human: func(w io.Writer) { renderHarvest(w, data) }}, nil
	})
	return cmd
}

func renderHarvest(w io.Writer, d harvestData) {
	fmt.Fprintf(w, "%s %s: %d considered, %d selected\n", d.Harvest.ID, d.Harvest.Outcome,
		d.Harvest.CrumbsConsidered, d.Harvest.CrumbsSelected)
	if d.Harvest.FailureCode != "" {
		fmt.Fprintf(w, "  %s: nothing was synthesised\n", d.Harvest.FailureCode)
	}
	for _, c := range d.CrumbsCaptured {
		fmt.Fprintf(w, "  captured %s %s\n", c.ID, oneLine(c.Content, 64))
	}
	if d.Revision == nil {
		return
	}
	fmt.Fprintf(w, "%s revision %d: %s (%s, confidence %.3f)\n",
		d.Revision.InsightID, d.Revision.Revision, d.Revision.Title,
		d.Revision.Class, d.Revision.Confidence)
}

// readTextInput resolves the `--x` / `--x-file` pair every content-bearing
// command takes. They are mutually exclusive on purpose: two sources of content
// is a caller that does not know what it is storing.
func readTextInput(flag, inline, file string, stdin io.Reader) (string, error) {
	switch {
	case inline != "" && file != "":
		return "", ledger.Fail(ledger.ErrInvalidInput, "invalid_usage",
			"pass %s or %s-file, not both", flag, flag)
	case file == "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", ledger.FailWith(ledger.ErrInvalidInput, "invalid_content", err,
				"cannot read %s-file from stdin", flag)
		}
		return string(b), nil
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", ledger.FailWith(ledger.ErrInvalidInput, "invalid_content", err,
				"cannot read %s", file)
		}
		return string(b), nil
	default:
		return inline, nil
	}
}

func parseCrumbIDs(raw []string) ([]ledger.CrumbID, error) {
	out := make([]ledger.CrumbID, 0, len(raw))
	for _, r := range raw {
		id, err := ledger.ParseID(ledger.PrefixCrumb, r)
		if err != nil {
			return nil, err
		}
		out = append(out, ledger.CrumbID(id))
	}
	return out, nil
}
