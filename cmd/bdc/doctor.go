package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/store/dolt"
)

type doctorData struct {
	Checks        []dolt.Check `json:"checks"`
	SchemaVersion int          `json:"schema_version"`
	JournalBytes  int64        `json:"journal_bytes"`
	LedgerPath    string       `json:"ledger_path"`
	Beads         any          `json:"beads"`
	OK            bool         `json:"ok"`
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
		var report dolt.StoreReport
		if a.store == nil {
			report = dolt.DiagnoseUnopened(a.loc, a.openErr)
		} else {
			r, err := a.store.Diagnose(cmd.Context())
			if err != nil {
				return result{}, err
			}
			report = r
		}

		d := doctorData{
			Checks:        report.Checks,
			SchemaVersion: report.SchemaVersion,
			JournalBytes:  report.JournalBytes,
			LedgerPath:    report.LedgerPath,
			// Populated by the optional Beads adapter; absent until it is detected.
			Beads: nil,
			OK:    report.OK,
		}
		return result{Data: d, Human: func(w io.Writer) {
			fmt.Fprintf(w, "ledger %s\n", d.LedgerPath)
			for _, c := range d.Checks {
				if !verbose && c.Status == dolt.StatusOK {
					continue
				}
				fmt.Fprintf(w, "  [%-4s] %-20s %s\n", c.Status, c.Name, c.Detail)
			}
			state := "ok"
			if !d.OK {
				state = "needs attention"
			}
			fmt.Fprintf(w, "%s (schema %d, journal %d bytes)\n", state, d.SchemaVersion, d.JournalBytes)
		}}, nil
	})
	return cmd
}
