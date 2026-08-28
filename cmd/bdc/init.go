package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
	"github.com/brianevanmiller/beadcrumbs/internal/store/dolt"
)

type initData struct {
	Path          string `json:"path"`
	Stealth       bool   `json:"stealth"`
	SchemaVersion int    `json:"schema_version"`
	Created       bool   `json:"created"`
}

func (a *app) newInitCommand() *cobra.Command {
	var visible, stealth, force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the repository-local reasoning ledger",
		Long: "Create the reasoning ledger for this repository.\n\n" +
			"Stealth is the default: the ledger lives inside the Git common directory, so it is " +
			"shared by every worktree and `git status` cannot see it. --visible places it at " +
			"<main-worktree>/.beadcrumbs and hides it through .git/info/exclude.\n\n" +
			"init is idempotent. --force replaces a target directory that exists but holds no " +
			"Dolt database; it never replaces a working ledger.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{ledgerAnnotation: string(ledgerAbsent)},
	}
	cmd.Flags().BoolVar(&stealth, "stealth", false, "place the ledger inside .git (default)")
	cmd.Flags().BoolVar(&visible, "visible", false, "place the ledger at <main-worktree>/.beadcrumbs")
	cmd.Flags().BoolVar(&force, "force", false, "replace a target directory that holds no Dolt database")
	cmd.MarkFlagsMutuallyExclusive("stealth", "visible")

	cmd.RunE = a.handle(func(cmd *cobra.Command, _ []string) (result, error) {
		ctx := cmd.Context()
		loc := a.loc.Resolve(visible)

		created, err := dolt.Init(ctx, loc, dolt.InitOptions{Visible: visible, Force: force})
		if err != nil {
			return result{}, err
		}

		store, err := dolt.Open(ctx, loc, dolt.Config{Command: "init"})
		if err != nil {
			return result{}, err
		}
		defer store.Close()
		schema, err := store.SchemaVersion(ctx)
		if err != nil {
			return result{}, err
		}
		if want := dolt.CurrentSchemaVersion(); schema != want {
			return result{}, ledger.Fail(ledger.ErrIntegrity, "integrity_schema_version",
				"ledger at %s reports schema version %d, expected %d", loc.Dir, schema, want)
		}
		a.out.schema = func() int { return schema }

		d := initData{Path: loc.Dir, Stealth: loc.Stealth, SchemaVersion: schema, Created: created}
		return result{Data: d, Human: func(w io.Writer) {
			verb := "already initialised"
			if created {
				verb = "initialised"
			}
			mode := "stealth"
			if !d.Stealth {
				mode = "visible"
			}
			fmt.Fprintf(w, "ledger %s (%s, schema %d)\n%s\n", verb, mode, d.SchemaVersion, d.Path)
		}}, nil
	})
	return cmd
}
