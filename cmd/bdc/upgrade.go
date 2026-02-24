package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade bdc to the latest version",
	Long: `Detects how bdc was installed and runs the appropriate upgrade command.

Supported installation methods:
  - npm:  npm update -g @beadcrumbs/bdc
  - go:   go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@latest

If the installation method cannot be detected, manual upgrade instructions are shown.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		method := detectInstallMethod()

		switch method {
		case "npm":
			fmt.Println("Detected installation method: npm")
			fmt.Println("Running: npm update -g @beadcrumbs/bdc")
			fmt.Println()
			return runUpgrade("npm", "update", "-g", "@beadcrumbs/bdc")

		case "go":
			fmt.Println("Detected installation method: go install")
			fmt.Println("Running: go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@latest")
			fmt.Println()
			return runUpgrade("go", "install", "github.com/brianevanmiller/beadcrumbs/cmd/bdc@latest")

		default:
			fmt.Println("Could not detect installation method.")
			fmt.Println()
			fmt.Println("To upgrade, use one of:")
			fmt.Println("  npm update -g @beadcrumbs/bdc")
			fmt.Println("  go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@latest")
			return nil
		}
	},
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
}

// detectInstallMethod determines how bdc was installed by examining the executable path.
func detectInstallMethod() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// Check npm: path contains node_modules or matches npm global prefix
	if strings.Contains(exe, "node_modules") {
		return "npm"
	}
	if prefix, err := exec.Command("npm", "prefix", "-g").Output(); err == nil {
		if strings.HasPrefix(exe, strings.TrimSpace(string(prefix))) {
			return "npm"
		}
	}

	// Check go install: in GOBIN or GOPATH/bin
	if gobin := os.Getenv("GOBIN"); gobin != "" && strings.HasPrefix(exe, gobin) {
		return "go"
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		if strings.HasPrefix(exe, filepath.Join(gopath, "bin")) {
			return "go"
		}
	}
	if out, err := exec.Command("go", "env", "GOPATH").Output(); err == nil {
		if strings.HasPrefix(exe, filepath.Join(strings.TrimSpace(string(out)), "bin")) {
			return "go"
		}
	}

	return "unknown"
}

// runUpgrade executes the upgrade command with inherited stdio.
func runUpgrade(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}
