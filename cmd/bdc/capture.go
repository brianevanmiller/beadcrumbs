package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// captureData is the `{crumb}` shape from the CLI contract. The redaction
// findings travel in warnings[] rather than in data, because a Crumb's content
// is already the redacted text and a caller acting on data should not have to
// reason about what it used to be.
type captureData struct {
	Crumb ledger.Crumb `json:"crumb"`
}

// defaultCaptureConfidence is the author confidence recorded when none is
// given. It is declared rather than inferred: confidence is an independent axis
// from evidence and validation, and a silent 1.0 would claim certainty the
// caller never expressed.
const defaultCaptureConfidence = 0.5

func (a *app) newCaptureCommand() *cobra.Command {
	var (
		confidence float64
		refs       []string
		fromFile   string
	)

	cmd := &cobra.Command{
		Use:   "capture [text|-]",
		Short: "Record one reasoning fragment as a Crumb",
		Long: "Capture one atomic fragment: a correction, a discovery, a rejected approach, a " +
			"decision fragment. One fragment per Crumb.\n\n" +
			"Text comes from the argument, from stdin with `-`, or from --from-file. Content is " +
			"redacted before it is written; a secret the redactor cannot confidently replace " +
			"aborts the capture with nothing persisted.",
		Args: cobra.MaximumNArgs(1),
	}
	cmd.Flags().Float64Var(&confidence, "confidence", defaultCaptureConfidence,
		"author confidence in this fragment, 0 to 1")
	cmd.Flags().StringArrayVar(&refs, "ref", nil,
		"attach a reference as kind:locator[@relation] (repeatable; relation defaults to subject)")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "read the fragment from a file")

	cmd.RunE = a.handle(func(cmd *cobra.Command, args []string) (result, error) {
		led, err := a.ledger(cmd.Context())
		if err != nil {
			return result{}, err
		}
		content, err := readContent(cmd.InOrStdin(), args, fromFile)
		if err != nil {
			return result{}, err
		}
		specs := make([]ledger.RefSpec, 0, len(refs))
		for _, raw := range refs {
			spec, err := ledger.ParseRefSpec(raw, ledger.RelationSubject)
			if err != nil {
				return result{}, err
			}
			specs = append(specs, spec)
		}

		res, err := led.CaptureCrumb(cmd.Context(), ledger.CaptureCrumb{
			Content:    content,
			Confidence: confidence,
			References: specs,
		})
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)
		if res.Deduplicated {
			a.out.warn("crumb_deduplicated",
				"this session already holds a Crumb with the same content; returning "+string(res.Crumb.ID))
		}

		return result{Data: captureData{Crumb: res.Crumb}, Human: func(w io.Writer) {
			fmt.Fprintf(w, "%s captured (%s, confidence %.3f)\n",
				res.Crumb.ID, res.Crumb.ReviewState, res.Crumb.Confidence)
			for _, ref := range res.References {
				fmt.Fprintf(w, "  %s %s:%s\n", ref.Relation, ref.Kind, ref.Locator)
			}
		}}, nil
	})
	return cmd
}

// readContent resolves the three ways a fragment arrives. They are mutually
// exclusive on purpose: two sources of content is a caller that does not know
// what it is storing.
func readContent(stdin io.Reader, args []string, fromFile string) (string, error) {
	positional := ""
	if len(args) == 1 {
		positional = args[0]
	}
	switch {
	case positional != "" && fromFile != "":
		return "", ledger.Fail(ledger.ErrInvalidInput, "invalid_usage",
			"pass the text or --from-file, not both")
	case fromFile != "":
		b, err := os.ReadFile(fromFile)
		if err != nil {
			return "", ledger.FailWith(ledger.ErrInvalidInput, "invalid_content", err,
				"cannot read %s", fromFile)
		}
		return string(b), nil
	case positional == "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", ledger.FailWith(ledger.ErrInvalidInput, "invalid_content", err,
				"cannot read the fragment from stdin")
		}
		return string(b), nil
	case strings.TrimSpace(positional) == "":
		return "", ledger.Fail(ledger.ErrInvalidInput, "invalid_content",
			"a Crumb needs content: pass it as an argument, as `-` for stdin, or with --from-file")
	default:
		return positional, nil
	}
}
