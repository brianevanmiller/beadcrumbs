package main

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/store/dolt"
)

const driverModulePath = "github.com/dolthub/driver"

type versionData struct {
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
	DoltDriver    string `json:"dolt_driver"`
	Go            string `json:"go"`
	Platform      string `json:"platform"`
}

func (a *app) newVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "version",
		Short:       "Print version, schema, and build information",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{ledgerAnnotation: string(ledgerDetached)},
	}
	cmd.RunE = a.handle(func(cmd *cobra.Command, _ []string) (result, error) {
		d := versionData{
			Version:       version,
			SchemaVersion: dolt.CurrentSchemaVersion(),
			DoltDriver:    driverVersion(),
			Go:            runtime.Version(),
			Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		}
		return result{Data: d, Human: func(w io.Writer) {
			fmt.Fprintf(w, "bdc %s\n", d.Version)
			fmt.Fprintf(w, "ledger schema %d\n", d.SchemaVersion)
			fmt.Fprintf(w, "dolt driver   %s\n", d.DoltDriver)
			fmt.Fprintf(w, "go            %s\n", d.Go)
			fmt.Fprintf(w, "platform      %s\n", d.Platform)
		}}, nil
	})
	return cmd
}

// driverVersion reads the embedded driver version rather than restating it, so
// it cannot drift from go.mod.
func driverVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == driverModulePath {
			return dep.Version
		}
	}
	return "unknown"
}
