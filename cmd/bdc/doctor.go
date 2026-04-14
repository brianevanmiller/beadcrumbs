package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run health checks on the beadcrumbs installation",
	Long: `Runs a series of diagnostic checks to verify the beadcrumbs installation is
healthy. Checks SQLite integrity, JSONL ↔ DB consistency, git hook installation,
directory permissions, origin file, and database file.

Exit code 0 if all checks pass, 1 if any fail.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		allPassed := true

		// --- Check: Database file exists and is non-zero ---
		dbInfo, err := os.Stat(dbPath)
		if err != nil {
			fmt.Printf("  ✗ Database file not found at %s\n", dbPath)
			allPassed = false
		} else if dbInfo.Size() == 0 {
			fmt.Printf("  ✗ Database file is empty at %s\n", dbPath)
			allPassed = false
		} else {
			sizeKB := dbInfo.Size() / 1024
			fmt.Printf("  ✓ Database file OK (%d KB)\n", sizeKB)
		}

		// --- Open read-only store for DB checks ---
		s, storeErr := getReadOnlyStore()
		defer func() {
			if s != nil {
				s.Close()
				storeInstance = nil
			}
		}()

		// --- Check: SQLite integrity ---
		if storeErr != nil {
			fmt.Printf("  ✗ Failed to open database: %v\n", storeErr)
			allPassed = false
		} else {
			db := s.DB()
			var result string
			row := db.QueryRow("PRAGMA integrity_check")
			if err := row.Scan(&result); err != nil {
				fmt.Printf("  ✗ SQLite integrity check failed: %v\n", err)
				allPassed = false
			} else if result != "ok" {
				fmt.Printf("  ✗ SQLite integrity check failed: %s\n", result)
				allPassed = false
			} else {
				fmt.Println("  ✓ SQLite integrity check passed")
			}

			// --- Check: JSONL ↔ DB consistency ---
			beadcrumbsDir := filepath.Dir(dbPath)
			type tableCheck struct {
				jsonlFile string
				table     string
				label     string
			}
			checks := []tableCheck{
				{filepath.Join(beadcrumbsDir, "insights.jsonl"), "insights", "insights"},
				{filepath.Join(beadcrumbsDir, "threads.jsonl"), "threads", "threads"},
				{filepath.Join(beadcrumbsDir, "deps.jsonl"), "dependencies", "deps"},
			}

			consistencyPassed := true
			parts := make([]string, 0, len(checks))
			for _, c := range checks {
				jsonlCount := countJSONLLines(c.jsonlFile)
				var dbCount int
				row := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", c.table))
				if err := row.Scan(&dbCount); err != nil {
					fmt.Printf("  ✗ JSONL ↔ DB consistency: failed to query %s: %v\n", c.table, err)
					consistencyPassed = false
					allPassed = false
					break
				}
				parts = append(parts, fmt.Sprintf("%s (%d/%d)", c.label, jsonlCount, dbCount))
				if jsonlCount != dbCount {
					consistencyPassed = false
					allPassed = false
				}
			}
			if len(parts) == len(checks) {
				if consistencyPassed {
					fmt.Printf("  ✓ JSONL ↔ DB consistency: %s\n", strings.Join(parts, ", "))
				} else {
					fmt.Printf("  ✗ JSONL ↔ DB consistency mismatch: %s\n", strings.Join(parts, ", "))
				}
			}
		}

		// --- Check: Directory permissions ---
		beadcrumbsDir := filepath.Dir(dbPath)
		if info, err := os.Stat(beadcrumbsDir); err != nil {
			fmt.Printf("  ✗ Directory not found: %s\n", beadcrumbsDir)
			allPassed = false
		} else if !info.IsDir() {
			fmt.Printf("  ✗ %s is not a directory\n", beadcrumbsDir)
			allPassed = false
		} else {
			// Test writability by attempting to create a temp file
			testFile := filepath.Join(beadcrumbsDir, ".bdc_doctor_write_test")
			f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
			if err != nil {
				fmt.Printf("  ✗ Directory not writable: %s\n", beadcrumbsDir)
				allPassed = false
			} else {
				f.Close()
				os.Remove(testFile)
				fmt.Println("  ✓ Directory permissions OK")
			}
		}

		// --- Check: Origin file ---
		originPath := filepath.Join(filepath.Dir(dbPath), "origin")
		if _, err := os.Stat(originPath); os.IsNotExist(err) {
			// Origin file is optional — not a failure
			fmt.Println("  ✓ Origin file not set (optional)")
		} else if err != nil {
			fmt.Printf("  ✗ Origin file error: %v\n", err)
			allPassed = false
		} else {
			content, err := os.ReadFile(originPath)
			if err != nil {
				fmt.Printf("  ✗ Origin file unreadable: %v\n", err)
				allPassed = false
			} else {
				trimmed := strings.TrimSpace(string(content))
				if trimmed == "" {
					fmt.Println("  ✗ Origin file exists but is empty")
					allPassed = false
				} else {
					fmt.Printf("  ✓ Origin file valid (%s)\n", truncate(trimmed, 60))
				}
			}
		}

		// --- Check: Git hooks ---
		gitDir, err := getGitDir()
		if err != nil {
			fmt.Println("  ✗ Git hooks: not a git repository")
			allPassed = false
		} else {
			hooksDir := filepath.Join(gitDir, "hooks")
			hookChecks := []string{"pre-commit", "post-merge"}
			var hookFailures []string
			for _, hook := range hookChecks {
				hookPath := filepath.Join(hooksDir, hook)
				content, err := os.ReadFile(hookPath)
				if err != nil {
					hookFailures = append(hookFailures, hook+" not installed")
				} else if !strings.Contains(string(content), "beadcrumbs") {
					hookFailures = append(hookFailures, hook+" missing beadcrumbs reference")
				}
			}
			if len(hookFailures) > 0 {
				fmt.Printf("  ✗ Git hooks: %s\n", strings.Join(hookFailures, "; "))
				allPassed = false
			} else {
				fmt.Printf("  ✓ Git hooks: pre-commit and post-merge installed\n")
			}
		}

		if !allPassed {
			return fmt.Errorf("one or more checks failed")
		}
		return nil
	},
}

// countJSONLLines counts non-empty lines in a JSONL file.
// Each non-empty line represents one record.
func countJSONLLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			count++
		}
	}
	return count
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
