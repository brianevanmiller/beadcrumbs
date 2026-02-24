package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var locateCmd = &cobra.Command{
	Use:   "locate",
	Short: "Find beadcrumbs databases reachable from this directory",
	Long: `Search for .beadcrumbs/beadcrumbs.db files using multiple strategies:
walk up from CWD, check git worktree common-dir, and scan child directories.

Useful when bdc can't find a database automatically (e.g., running from a
workspace parent directory that isn't itself a git repo or worktree).`,
	Run: func(cmd *cobra.Command, args []string) {
		found := locateDatabases()
		if len(found) == 0 {
			fmt.Println("No beadcrumbs databases found.")
			fmt.Println()
			fmt.Println("To create one: cd <your-repo> && bdc init")
			return
		}

		fmt.Printf("Found %d beadcrumbs database(s):\n\n", len(found))
		for i, loc := range found {
			fmt.Printf("  %d. %s\n", i+1, loc.path)
			fmt.Printf("     Found via: %s\n", loc.source)
		}

		fmt.Println()
		fmt.Println("To use a database from this directory, set BDC_DB_PATH:")
		fmt.Printf("  export BDC_DB_PATH=\"%s\"\n", found[0].path)

		if _, err := exec.LookPath("direnv"); err == nil {
			fmt.Println()
			fmt.Println("Or add to .envrc for automatic activation:")
			fmt.Printf("  echo 'export BDC_DB_PATH=\"%s\"' >> .envrc && direnv allow\n", found[0].path)
		}
	},
}

type locateResult struct {
	path   string
	source string
}

func locateDatabases() []locateResult {
	seen := make(map[string]bool)
	var results []locateResult

	add := func(path, source string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			return
		}
		seen[abs] = true
		results = append(results, locateResult{path: abs, source: source})
	}

	// Strategy 1: Walk up from CWD
	if dir, err := os.Getwd(); err == nil {
		d := dir
		for {
			candidate := filepath.Join(d, ".beadcrumbs", "beadcrumbs.db")
			if _, err := os.Stat(candidate); err == nil {
				add(candidate, "walk-up from CWD")
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}

	// Strategy 2: git-common-dir from CWD
	if out, err := exec.Command("git", "rev-parse", "--git-common-dir").Output(); err == nil {
		gitCommonDir := strings.TrimSpace(string(out))
		if gitCommonDir != "" && gitCommonDir != "." {
			if !filepath.IsAbs(gitCommonDir) {
				if cwd, err := os.Getwd(); err == nil {
					gitCommonDir = filepath.Join(cwd, gitCommonDir)
				}
			}
			repoRoot := filepath.Dir(gitCommonDir)
			candidate := filepath.Join(repoRoot, ".beadcrumbs", "beadcrumbs.db")
			if _, err := os.Stat(candidate); err == nil {
				add(candidate, "git-common-dir")
			}
		}
	}

	// Strategy 3: Scan child directories for git repos/worktrees
	if cwd, err := os.Getwd(); err == nil {
		entries, err := os.ReadDir(cwd)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				childDir := filepath.Join(cwd, entry.Name())

				// Check for .beadcrumbs/ directly in child
				candidate := filepath.Join(childDir, ".beadcrumbs", "beadcrumbs.db")
				if _, err := os.Stat(candidate); err == nil {
					add(candidate, fmt.Sprintf("child directory: %s", entry.Name()))
					continue
				}

				// Check child's git-common-dir (child may be a worktree)
				cmd := exec.Command("git", "-C", childDir, "rev-parse", "--git-common-dir")
				if out, err := cmd.Output(); err == nil {
					gitCommonDir := strings.TrimSpace(string(out))
					if gitCommonDir != "" && gitCommonDir != "." {
						if !filepath.IsAbs(gitCommonDir) {
							gitCommonDir = filepath.Join(childDir, gitCommonDir)
						}
						repoRoot := filepath.Dir(gitCommonDir)
						candidate := filepath.Join(repoRoot, ".beadcrumbs", "beadcrumbs.db")
						if _, err := os.Stat(candidate); err == nil {
							add(candidate, fmt.Sprintf("git-common-dir via %s", entry.Name()))
						}
					}
				}
			}
		}
	}

	return results
}

func init() {
	rootCmd.AddCommand(locateCmd)
}
