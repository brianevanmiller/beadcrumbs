package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
	"github.com/brianevanmiller/beadcrumbs/internal/store/dolt"
)

type doctorData struct {
	Checks        []ledger.Check      `json:"checks"`
	SchemaVersion int                 `json:"schema_version"`
	JournalBytes  int64               `json:"journal_bytes"`
	LedgerPath    string              `json:"ledger_path"`
	Beads         *beads.Availability `json:"beads"`
	OK            bool                `json:"ok"`
}

func (a *app) newDoctorCommand() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report ledger health and recovery state",
		Long: "Report ledger health. doctor is the one command that stays useful when the ledger " +
			"cannot be opened: a ledger locked by another process, an uninitialised repository, and " +
			"an interrupted restore are each reported as a named failing check rather than an error.",
		Args: cobra.NoArgs,
		// Optional: a ledger that will not open is exactly what doctor is for.
		Annotations: map[string]string{
			ledgerAnnotation: string(ledgerOptional),
			// Short: doctor should report "locked" quickly rather than block.
			waitAnnotation: "2s",
		},
	}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show every check, not only the ones needing attention")

	cmd.RunE = a.handle(func(cmd *cobra.Command, _ []string) (result, error) {
		d, err := a.diagnose(cmd)
		if err != nil {
			return result{}, err
		}
		return result{Data: d, Human: func(w io.Writer) {
			fmt.Fprintf(w, "ledger %s\n", d.LedgerPath)
			for _, c := range d.Checks {
				if !verbose && c.Status == dolt.StatusOK {
					continue
				}
				fmt.Fprintf(w, "  [%-4s] %-20s %s\n", c.Status, c.Name, c.Detail)
			}
			fmt.Fprintf(w, "  %s\n", describeBeads(d.Beads))
			state := "ok"
			if !d.OK {
				state = "needs attention"
			}
			fmt.Fprintf(w, "%s (schema %d, journal %d bytes)\n", state, d.SchemaVersion, d.JournalBytes)
		}}, nil
	})
	return cmd
}

// diagnose runs the fullest diagnosis the ledger's state allows. An open ledger
// gets the domain's own invariant checks — the polymorphic targets and the
// materialised head revision, which no storage check can see — on top of the
// storage report; a ledger that will not open gets the storage report alone,
// because there is nothing to ask the domain about.
func (a *app) diagnose(cmd *cobra.Command) (doctorData, error) {
	ctx := cmd.Context()
	d := doctorData{Beads: a.beadsAvailability(ctx)}

	if a.store == nil {
		report := dolt.DiagnoseUnopened(a.loc, a.openErr)
		d.Checks, d.SchemaVersion = report.Checks, report.SchemaVersion
		d.JournalBytes, d.LedgerPath, d.OK = report.JournalBytes, report.LedgerPath, report.OK
		return d, nil
	}

	led, err := a.ledger(ctx)
	if err != nil {
		return doctorData{}, err
	}
	report, err := led.Doctor(ctx)
	if err != nil {
		return doctorData{}, err
	}
	d.Checks, d.SchemaVersion = report.Checks, report.SchemaVersion
	d.JournalBytes, d.LedgerPath, d.OK = report.JournalBytes, report.LedgerPath, report.OK
	return d, nil
}

func describeBeads(av *beads.Availability) string {
	switch {
	case av == nil:
		return "beads: not checked (--no-enrich)"
	case av.Present:
		return fmt.Sprintf("beads: %s, workspace prefix %q", av.Version, av.Prefix)
	default:
		return "beads: unavailable (" + av.Reason + ")"
	}
}
