package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var (
	// Version is the current version of bdc (overridden by ldflags at build time)
	Version = "0.9.0"
	// Build can be set via ldflags at compile time
	Build = "dev"
	// Commit is the git revision the binary was built from (optional ldflag)
	Commit = ""
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		commit := resolveCommitHash()

		if commit != "" {
			fmt.Printf("bdc version %s (%s: %s)\n", Version, Build, shortCommit(commit))
		} else {
			fmt.Printf("bdc version %s (%s)\n", Version, Build)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

func resolveCommitHash() string {
	if Commit != "" {
		return Commit
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}

	return ""
}

func shortCommit(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
