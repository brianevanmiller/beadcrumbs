package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// realpath resolves symlinks (e.g., macOS /var -> /private/var) so paths
// from t.TempDir() match paths from os.Getwd() used by the production code.
func realpath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("failed to resolve symlinks for %s: %v", path, err)
	}
	return resolved
}

func TestWalkUpForDB(t *testing.T) {
	root := realpath(t, t.TempDir())
	bcDir := filepath.Join(root, ".beadcrumbs")
	if err := os.MkdirAll(bcDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbFile := filepath.Join(bcDir, "beadcrumbs.db")
	if err := os.WriteFile(dbFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	child := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	t.Run("finds db from nested child", func(t *testing.T) {
		os.Chdir(child)
		result := walkUpForDB()
		if result != dbFile {
			t.Errorf("expected %s, got %s", dbFile, result)
		}
	})

	t.Run("finds db from root itself", func(t *testing.T) {
		os.Chdir(root)
		result := walkUpForDB()
		if result != dbFile {
			t.Errorf("expected %s, got %s", dbFile, result)
		}
	})

	t.Run("returns empty when no db exists", func(t *testing.T) {
		emptyDir := realpath(t, t.TempDir())
		os.Chdir(emptyDir)
		result := walkUpForDB()
		if result != "" {
			t.Errorf("expected empty string, got %s", result)
		}
	})

	t.Run("closest db wins", func(t *testing.T) {
		innerBcDir := filepath.Join(root, "a", ".beadcrumbs")
		if err := os.MkdirAll(innerBcDir, 0755); err != nil {
			t.Fatal(err)
		}
		innerDB := filepath.Join(innerBcDir, "beadcrumbs.db")
		if err := os.WriteFile(innerDB, []byte("inner"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(innerBcDir)

		os.Chdir(child) // root/a/b/c — should find root/a/.beadcrumbs first
		result := walkUpForDB()
		if result != innerDB {
			t.Errorf("expected closest db %s, got %s", innerDB, result)
		}
	})
}

func TestGitCommonDirRoot(t *testing.T) {
	t.Run("returns empty for non-git directory", func(t *testing.T) {
		dir := t.TempDir()
		result := gitCommonDirRoot(dir)
		if result != "" {
			t.Errorf("expected empty string for non-git dir, got %s", result)
		}
	})

	t.Run("returns empty for main repo", func(t *testing.T) {
		// git rev-parse --git-common-dir returns ".git" in a normal repo,
		// which gitCommonDirRoot correctly skips (not a worktree).
		dir := t.TempDir()
		if err := runGit(dir, "init"); err != nil {
			t.Skip("git not available")
		}
		result := gitCommonDirRoot(dir)
		if result != "" {
			t.Errorf("expected empty for main repo, got %s", result)
		}
	})

	t.Run("returns repo root from worktree", func(t *testing.T) {
		mainRepo := realpath(t, t.TempDir())
		if err := runGit(mainRepo, "init"); err != nil {
			t.Skip("git not available")
		}
		// Need at least one commit to create a worktree
		dummyFile := filepath.Join(mainRepo, "dummy.txt")
		if err := os.WriteFile(dummyFile, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := runGit(mainRepo, "add", "."); err != nil {
			t.Fatal(err)
		}
		if err := runGit(mainRepo, "commit", "-m", "init"); err != nil {
			t.Fatal(err)
		}

		wtDir := realpath(t, t.TempDir())
		if err := runGit(mainRepo, "worktree", "add", wtDir, "-b", "test-branch"); err != nil {
			t.Fatalf("failed to create worktree: %v", err)
		}
		defer runGit(mainRepo, "worktree", "remove", wtDir)

		result := gitCommonDirRoot(wtDir)
		if result != mainRepo {
			t.Errorf("expected repo root %s, got %s", mainRepo, result)
		}
	})
}

func TestLocateDatabases(t *testing.T) {
	workspace := realpath(t, t.TempDir())

	childA := filepath.Join(workspace, "child-a")
	bcDirA := filepath.Join(childA, ".beadcrumbs")
	if err := os.MkdirAll(bcDirA, 0755); err != nil {
		t.Fatal(err)
	}
	dbA := filepath.Join(bcDirA, "beadcrumbs.db")
	if err := os.WriteFile(dbA, []byte("test-a"), 0644); err != nil {
		t.Fatal(err)
	}

	childB := filepath.Join(workspace, "child-b")
	if err := os.MkdirAll(childB, 0755); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	t.Run("finds db in child directory", func(t *testing.T) {
		os.Chdir(workspace)
		results := locateDatabases()
		found := false
		for _, r := range results {
			if r.path == dbA {
				found = true
				if r.source != "child directory: child-a" {
					t.Errorf("expected source 'child directory: child-a', got %s", r.source)
				}
			}
		}
		if !found {
			t.Errorf("expected to find %s in results: %v", dbA, results)
		}
	})

	t.Run("deduplicates results", func(t *testing.T) {
		os.Chdir(childA)
		results := locateDatabases()
		count := 0
		for _, r := range results {
			if r.path == dbA {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected db to appear exactly once, appeared %d times", count)
		}
	})

	t.Run("returns empty for empty directory", func(t *testing.T) {
		emptyDir := realpath(t, t.TempDir())
		os.Chdir(emptyDir)
		results := locateDatabases()
		if len(results) != 0 {
			t.Errorf("expected no results, got %v", results)
		}
	})

	t.Run("skips hidden directories", func(t *testing.T) {
		hiddenDir := filepath.Join(workspace, ".hidden")
		hiddenBC := filepath.Join(hiddenDir, ".beadcrumbs")
		if err := os.MkdirAll(hiddenBC, 0755); err != nil {
			t.Fatal(err)
		}
		hiddenDB := filepath.Join(hiddenBC, "beadcrumbs.db")
		if err := os.WriteFile(hiddenDB, []byte("hidden"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(hiddenDir)

		os.Chdir(workspace)
		results := locateDatabases()
		for _, r := range results {
			if r.path == hiddenDB {
				t.Errorf("should not find db in hidden directory: %s", r.path)
			}
		}
	})
}

func TestResolveDBPath(t *testing.T) {
	root := realpath(t, t.TempDir())
	bcDir := filepath.Join(root, ".beadcrumbs")
	if err := os.MkdirAll(bcDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbFile := filepath.Join(bcDir, "beadcrumbs.db")
	if err := os.WriteFile(dbFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	t.Run("env var takes precedence over walk-up", func(t *testing.T) {
		os.Chdir(root)
		envDB := "/tmp/env-override.db"
		t.Setenv("BDC_DB_PATH", envDB)

		dbPath = ".beadcrumbs/beadcrumbs.db"
		resolveDBPath(rootCmd)
		if dbPath != envDB {
			t.Errorf("expected env var path %s, got %s", envDB, dbPath)
		}
	})

	t.Run("walk-up resolves when no env var", func(t *testing.T) {
		os.Chdir(root)
		t.Setenv("BDC_DB_PATH", "")

		dbPath = ".beadcrumbs/beadcrumbs.db"
		resolveDBPath(rootCmd)
		if dbPath != dbFile {
			t.Errorf("expected walk-up path %s, got %s", dbFile, dbPath)
		}
	})

	t.Run("default preserved when nothing found", func(t *testing.T) {
		emptyDir := realpath(t, t.TempDir())
		os.Chdir(emptyDir)
		t.Setenv("BDC_DB_PATH", "")

		dbPath = ".beadcrumbs/beadcrumbs.db"
		resolveDBPath(rootCmd)
		if dbPath != ".beadcrumbs/beadcrumbs.db" {
			t.Errorf("expected default path preserved, got %s", dbPath)
		}
	})
}

// runGit runs a git command in the given directory.
func runGit(dir string, args ...string) error {
	allArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", allArgs...)
	return cmd.Run()
}
