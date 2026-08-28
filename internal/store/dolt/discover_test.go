package dolt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// TestDiscoverIdenticalFromWorktree is the whole reason discovery is structural:
// a linked worktree must reach the same ledger as the root with no bookkeeping,
// in both stealth and visible mode.
func TestDiscoverIdenticalFromWorktree(t *testing.T) {
	for _, visible := range []bool{false, true} {
		name := "stealth"
		if visible {
			name = "visible"
		}
		t.Run(name, func(t *testing.T) {
			repo := fixtureRepo(t)
			worktree := filepath.Join(filepath.Dir(repo), "linked")
			git(t, repo, "worktree", "add", "-q", worktree, "-b", "linked")

			base, err := Discover(context.Background(), repo)
			if !errors.Is(err, ledger.ErrNoLedger) {
				t.Fatalf("expected ErrNoLedger before init, got %v", err)
			}
			fakeLedger(t, base.Resolve(visible).Dir)

			fromRoot, err := Discover(context.Background(), repo)
			if err != nil {
				t.Fatalf("discover from root: %v", err)
			}
			fromWorktree, err := Discover(context.Background(), worktree)
			if err != nil {
				t.Fatalf("discover from worktree: %v", err)
			}

			if fromRoot.Dir != fromWorktree.Dir {
				t.Fatalf("worktree resolved a different ledger:\n  root:     %s\n  worktree: %s",
					fromRoot.Dir, fromWorktree.Dir)
			}
			if fromRoot.Stealth == visible {
				t.Fatalf("Stealth=%v for visible=%v; the two must be opposites", fromRoot.Stealth, visible)
			}
			if fromWorktree.MainRoot != repo {
				t.Fatalf("MainRoot from the worktree is %s, want the main root %s", fromWorktree.MainRoot, repo)
			}
			if fromWorktree.RepoRoot != worktree {
				t.Fatalf("RepoRoot from the worktree is %s, want %s", fromWorktree.RepoRoot, worktree)
			}
		})
	}
}

// TestStealthLeavesGitStatusClean asserts the property that makes stealth mode
// need no ignore file: the ledger is structurally invisible, from the root and
// from a linked worktree. The visible case is included because it makes the same
// promise through .git/info/exclude, which is the part a user could break.
func TestStealthLeavesGitStatusClean(t *testing.T) {
	for _, visible := range []bool{false, true} {
		name := "stealth"
		if visible {
			name = "visible"
		}
		t.Run(name, func(t *testing.T) {
			repo := fixtureRepo(t)
			worktree := filepath.Join(filepath.Dir(repo), "linked")
			git(t, repo, "worktree", "add", "-q", worktree, "-b", "linked")

			loc := initLedger(t, repo, visible)
			// A real ledger with real files, not a marker directory.
			store := openLedger(t, loc, Config{})
			seedRows(t, store, 2)
			if err := store.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			for _, dir := range []string{repo, worktree} {
				if status := git(t, dir, "status", "--porcelain"); status != "" {
					t.Fatalf("git status in %s is not clean:\n%s", dir, status)
				}
			}
		})
	}
}

// TestDiscoverIgnoresInheritedGitEnv is the hostile case: an inherited GIT_DIR
// makes `git rev-parse --git-common-dir` resolve the hijacked repository, so
// without stripping it the ledger would be written into an unrelated checkout.
func TestDiscoverIgnoresInheritedGitEnv(t *testing.T) {
	victim := fixtureRepo(t)
	hijacker := fixtureRepo(t)

	t.Setenv("GIT_DIR", filepath.Join(hijacker, ".git"))
	t.Setenv("GIT_WORK_TREE", hijacker)
	t.Setenv("GIT_COMMON_DIR", filepath.Join(hijacker, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(hijacker, ".git", "index"))
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(hijacker, ".git", "objects"))

	loc, err := Discover(context.Background(), victim)
	if !errors.Is(err, ledger.ErrNoLedger) {
		t.Fatalf("expected ErrNoLedger, got %v", err)
	}
	if !strings.HasPrefix(loc.Dir, victim) {
		t.Fatalf("inherited GIT_* redirected the ledger to %s, outside %s", loc.Dir, victim)
	}
	if loc.GitCommon != filepath.Join(victim, ".git") {
		t.Fatalf("GitCommon is %s, want %s", loc.GitCommon, filepath.Join(victim, ".git"))
	}
}

// TestDiscoverRejectsBareRepository: --git-common-dir succeeds in a bare repo, so
// without an explicit check `bdc init` would create a ledger no working tree can
// reach.
func TestDiscoverRejectsBareRepository(t *testing.T) {
	dir := t.TempDir()
	bare := filepath.Join(dir, "bare.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, bare, "init", "-q", "--bare", ".")

	_, err := Discover(context.Background(), bare)
	if !errors.Is(err, ledger.ErrNoLedger) {
		t.Fatalf("expected ErrNoLedger, got %v", err)
	}
	var le *ledger.Error
	if !errors.As(err, &le) || le.Code != "no_ledger_bare" {
		t.Fatalf("expected error code no_ledger_bare, got %v", err)
	}
}

// TestDiscoverInSubmoduleUsesModuleGitDir: a submodule is a different repository,
// so its own ledger is the correct boundary.
func TestDiscoverInSubmoduleUsesModuleGitDir(t *testing.T) {
	super := fixtureRepo(t)
	child := fixtureRepo(t)
	git(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", child, "sub")
	git(t, super, "commit", "-q", "-m", "add submodule")

	sub := filepath.Join(super, "sub")
	loc, err := Discover(context.Background(), sub)
	if !errors.Is(err, ledger.ErrNoLedger) {
		t.Fatalf("expected ErrNoLedger, got %v", err)
	}
	wantCommon := filepath.Join(super, ".git", "modules", "sub")
	if loc.GitCommon != wantCommon {
		t.Fatalf("submodule GitCommon is %s, want %s", loc.GitCommon, wantCommon)
	}
	if loc.Dir != filepath.Join(wantCommon, stealthLeaf) {
		t.Fatalf("submodule ledger is %s, want its own at %s", loc.Dir, filepath.Join(wantCommon, stealthLeaf))
	}
}

// TestDiscoverRejectsBothStealthAndVisible: two ledgers is an integrity error,
// not a preference — silently picking one would split the record in half.
func TestDiscoverRejectsBothStealthAndVisible(t *testing.T) {
	repo := fixtureRepo(t)
	base, err := Discover(context.Background(), repo)
	if !errors.Is(err, ledger.ErrNoLedger) {
		t.Fatalf("expected ErrNoLedger, got %v", err)
	}
	fakeLedger(t, base.StealthDir())
	fakeLedger(t, base.VisibleDir())

	_, err = Discover(context.Background(), repo)
	if !errors.Is(err, ledger.ErrIntegrity) {
		t.Fatalf("expected ErrIntegrity, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, base.StealthDir()) || !strings.Contains(msg, base.VisibleDir()) {
		t.Fatalf("error must name both ledgers, got: %s", msg)
	}
}

// Init refuses to create a second ledger in the other mode, which is what keeps
// the state TestDiscoverRejectsBothStealthAndVisible detects from being
// reachable through normal use.
func TestInitRefusesSecondLedgerInOtherMode(t *testing.T) {
	repo := fixtureRepo(t)
	loc := initLedger(t, repo, false)

	_, err := Init(context.Background(), loc.Resolve(true), InitOptions{Visible: true})
	if !errors.Is(err, ledger.ErrIntegrity) {
		t.Fatalf("expected ErrIntegrity when a stealth ledger exists, got %v", err)
	}
}
