package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// authorityData is the `{authority, effective_level}` shape from the CLI
// contract. The effective level is read back from the target's history rather
// than assumed to be the level just granted.
type authorityData struct {
	Authority      ledger.Authority      `json:"authority"`
	EffectiveLevel ledger.AuthorityLevel `json:"effective_level"`
}

func (a *app) newAuthorityCommand() *cobra.Command {
	var (
		level       string
		scope       string
		destination string
		rationale   string
	)

	cmd := &cobra.Command{
		Use:   "authority <target-id>",
		Short: "Grant an Insight revision or proposal a level of authority",
		Long: "Record what a record's conclusion establishes. Grants are append-only: the " +
			"current level is the latest grant, and a record with none is advisory.\n\n" +
			"advisory is informational — cite it, do not act on it as settled. default is a " +
			"working default a later agent may rely on. mandatory must be followed, and only a " +
			"human may grant it: an agent that asks for it gets an error and is never silently " +
			"downgraded. An agent may grant default only where this repository's configuration " +
			"permits it.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&level, "level", string(ledger.AuthorityAdvisory), "advisory, default, or mandatory")
	cmd.Flags().StringVar(&scope, "scope", "", "what the grant covers; empty is repository-wide")
	cmd.Flags().StringVar(&destination, "destination", "", "narrow the grant to one destination, as kind:locator")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why this level (required)")
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
		intent := ledger.GrantAuthority{
			Target: target, Level: ledger.AuthorityLevel(level),
			Scope: scope, Rationale: rationale,
		}
		if destination != "" {
			dest, err := ledger.ParseDestination(destination)
			if err != nil {
				return result{}, err
			}
			intent.DestinationKind, intent.DestinationLocator = dest.Kind, dest.Locator
		}

		res, err := led.GrantAuthority(cmd.Context(), intent)
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)

		data := authorityData{Authority: res.Authority, EffectiveLevel: res.EffectiveLevel}
		return result{Data: data, Human: func(w io.Writer) {
			fmt.Fprintf(w, "%s %s: granted %s by %s (%s)\n",
				data.Authority.Target.Kind, data.Authority.Target.ID, data.Authority.Level,
				data.Authority.ActorID, data.Authority.ActorKind)
			fmt.Fprintf(w, "  effective level: %s\n", data.EffectiveLevel)
			if data.Authority.Scope != "" {
				fmt.Fprintf(w, "  scope: %s\n", data.Authority.Scope)
			}
			if data.Authority.DestinationKind != "" {
				fmt.Fprintf(w, "  destination: %s:%s\n",
					data.Authority.DestinationKind, data.Authority.DestinationLocator)
			}
			fmt.Fprintf(w, "  %s\n", data.Authority.Rationale)
		}}, nil
	})
	return cmd
}
