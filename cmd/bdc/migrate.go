package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func (a *app) newMigrateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply the schema migrations this build ships and the ledger has not",
		Long: "Bring a ledger written by an older bdc up to this build's schema. It is idempotent: " +
			"a ledger already at the current version reports from == to and applies nothing.\n\n" +
			"This is the repair `bdc doctor` names for a schema-version mismatch. `bdc init` is not: " +
			"it returns early on an existing ledger without applying anything.\n\n" +
			"A ledger newer than this build cannot be repaired downward — upgrade bdc instead.",
		Args: cobra.NoArgs,
		// A migration legitimately runs long, so it gets the same budget as the
		// other maintenance commands rather than the ordinary 15s.
		Annotations: map[string]string{
			waitAnnotation: maintenanceWait,
			holdAnnotation: maintenanceHold,
		},
	}
	cmd.RunE = a.handle(func(cmd *cobra.Command, _ []string) (result, error) {
		res, err := a.store.Migrate(cmd.Context())
		if err != nil {
			return result{}, err
		}
		if res.Applied == nil {
			// Every list in the JSON contract is a list; a caller iterating
			// `applied` should not have to branch on null.
			res.Applied = []string{}
		}
		a.out.schema = func() int { return res.To }
		return result{Data: res, Human: func(w io.Writer) {
			if len(res.Applied) == 0 {
				fmt.Fprintf(w, "schema %d, already current\n", res.To)
				return
			}
			fmt.Fprintf(w, "schema %d -> %d\n", res.From, res.To)
			for _, name := range res.Applied {
				fmt.Fprintf(w, "  applied %s\n", name)
			}
		}}, nil
	})
	return cmd
}
