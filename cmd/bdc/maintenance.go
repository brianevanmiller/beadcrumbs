package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/store/dolt"
)

// Backup, restore, and GC legitimately run long, so they set a larger lock
// budget than the 15s an ordinary command allows.
const (
	maintenanceWait = "2m"
	maintenanceHold = "30m"
)

func (a *app) newBackupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup <dest-url>",
		Short: "Push the ledger, history included, to a destination",
		Long: "Push the ledger to a destination URL. A filesystem path is accepted and normalised " +
			"to file://. The backup carries committed history, not just the current working set.",
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			waitAnnotation: maintenanceWait,
			holdAnnotation: maintenanceHold,
		},
	}
	cmd.RunE = a.handle(func(cmd *cobra.Command, args []string) (result, error) {
		res, err := a.store.Backup(cmd.Context(), args[0])
		if err != nil {
			return result{}, err
		}
		return result{Data: res, Human: func(w io.Writer) {
			fmt.Fprintf(w, "backed up to %s (%d bytes, schema %d)\n", res.Destination, res.Bytes, res.SchemaVersion)
		}}, nil
	})
	return cmd
}

func (a *app) newRestoreCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "restore <src-url>",
		Short: "Replace the ledger with a copy unpacked from a backup",
		Long: "Replace the ledger with a copy unpacked from a backup. restore never runs against an " +
			"open engine: it stages the copy beside the live ledger, verifies it, then swaps it into " +
			"place. An interruption after the swap leaves both directories on disk, which `bdc doctor` " +
			"reports.",
		Args: cobra.ExactArgs(1),
		// Restore replaces the directory the engine would live in, so it must
		// not be opened first.
		Annotations: map[string]string{ledgerAnnotation: string(ledgerAbsent)},
	}
	cmd.Flags().BoolVar(&force, "force", false, "required when a ledger already exists")

	cmd.RunE = a.handle(func(cmd *cobra.Command, args []string) (result, error) {
		res, err := dolt.Restore(cmd.Context(), a.loc, args[0], dolt.RestoreOptions{Force: force})
		if err != nil {
			return result{}, err
		}
		a.out.schema = func() int { return res.SchemaVersion }
		return result{Data: res, Human: func(w io.Writer) {
			fmt.Fprintf(w, "restored %s (schema %d, %d records)\n", res.Restored, res.SchemaVersion, res.Records)
		}}, nil
	})
	return cmd
}

func (a *app) newGCCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Reclaim the Dolt chunk journal",
		Long: "Reclaim the Dolt chunk journal, which per-transaction commits grow quickly. GC does " +
			"not rewrite committed history: a pruned Crumb stays readable through Dolt history " +
			"afterwards.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			waitAnnotation: maintenanceWait,
			holdAnnotation: maintenanceHold,
		},
	}
	cmd.RunE = a.handle(func(cmd *cobra.Command, _ []string) (result, error) {
		res, err := a.store.GC(cmd.Context())
		if err != nil {
			return result{}, err
		}
		return result{Data: res, Human: func(w io.Writer) {
			fmt.Fprintf(w, "reclaimed %d bytes in %d ms (%d -> %d)\n",
				res.BeforeBytes-res.AfterBytes, res.DurationMS, res.BeforeBytes, res.AfterBytes)
		}}, nil
	})
	return cmd
}
