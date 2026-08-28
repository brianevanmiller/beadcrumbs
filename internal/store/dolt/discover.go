// Package dolt owns everything Dolt: discovery, init, engine open/close,
// transactions, migrations, lock discipline, backup, restore, GC, and
// diagnostics. No package above this one names a Dolt symbol.
package dolt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

const (
	// DatabaseName is the single Dolt database inside a ledger directory. Its
	// physical location is <Location.Dir>/<DatabaseName>/.dolt.
	DatabaseName = "bdc"

	stealthLeaf = "beadcrumbs"  // inside <git-common-dir>, so Git cannot see it
	visibleLeaf = ".beadcrumbs" // inside the main worktree, hidden via info/exclude

	// excludeEntry is what --visible appends to .git/info/exclude. info/exclude
	// is shared across worktrees, which is why it is used instead of .gitignore.
	excludeEntry = "/.beadcrumbs/"
)

// Location is the resolved ledger placement for one invocation.
type Location struct {
	Dir       string // stealth: <GitCommon>/beadcrumbs · visible: <MainRoot>/.beadcrumbs
	GitCommon string // git rev-parse --path-format=absolute --git-common-dir
	MainRoot  string // first `worktree` line of `git worktree list --porcelain`
	RepoRoot  string // the checkout the command ran in; equals MainRoot outside a linked worktree
	Stealth   bool   // true when Dir is inside GitCommon (the v1 default)
}

// StealthDir and VisibleDir are the only two paths a ledger may occupy.
func (l Location) StealthDir() string { return filepath.Join(l.GitCommon, stealthLeaf) }
func (l Location) VisibleDir() string { return filepath.Join(l.MainRoot, visibleLeaf) }

// Resolve returns l pointed at the requested mode. Used by `bdc init`, which
// chooses the mode instead of discovering it.
func (l Location) Resolve(visible bool) Location {
	if visible {
		l.Dir, l.Stealth = l.VisibleDir(), false
	} else {
		l.Dir, l.Stealth = l.StealthDir(), true
	}
	return l
}

// dbPath is the Dolt database directory whose presence means "a ledger exists here".
func dbPath(dir string) string { return filepath.Join(dir, DatabaseName, ".dolt") }

func ledgerExists(dir string) bool {
	fi, err := os.Stat(dbPath(dir))
	return err == nil && fi.IsDir()
}

// Discover resolves the ledger for cwd from Git structure alone — there is no
// config file to lose, and the result is identical from the repository root and
// from every linked worktree.
//
// On ledger.ErrNoLedger the returned Location is still fully populated and is
// what `bdc init` initialises; every other error leaves it unusable.
func Discover(ctx context.Context, cwd string) (Location, error) {
	cwd, err := realPath(cwd)
	if err != nil {
		return Location{}, ledger.FailWith(ledger.ErrInvalidInput, "invalid_directory", err,
			"cannot resolve directory %q", cwd)
	}

	// Bare first: --git-common-dir succeeds in a bare repository but no working
	// tree can ever reach a ledger created there.
	bare, err := gitOutput(ctx, cwd, "rev-parse", "--is-bare-repository")
	if err != nil {
		return Location{}, ledger.FailWith(ledger.ErrNoLedger, "no_ledger_not_git", err,
			"%s is not inside a Git repository", cwd)
	}
	if bare == "true" {
		return Location{}, ledger.Fail(ledger.ErrNoLedger, "no_ledger_bare",
			"%s is a bare repository; Beadcrumbs needs a working tree", cwd)
	}

	common, err := gitPath(ctx, cwd, "--git-common-dir")
	if err != nil {
		return Location{}, err
	}
	repoRoot, err := gitPath(ctx, cwd, "--show-toplevel")
	if err != nil {
		return Location{}, err
	}
	mainRoot, err := mainWorktree(ctx, cwd)
	if err != nil {
		return Location{}, err
	}

	// Guards against an inherited GIT_DIR resolving a different repository than
	// the one the command is standing in. gitOutput already strips those vars;
	// this asserts the outcome rather than trusting it.
	if !withinDir(repoRoot, cwd) {
		return Location{}, ledger.Fail(ledger.ErrIntegrity, "integrity_repo_mismatch",
			"directory %s resolved to repository root %s, which does not contain it", cwd, repoRoot)
	}

	loc := Location{GitCommon: common, MainRoot: mainRoot, RepoRoot: repoRoot}
	stealth, visible := ledgerExists(loc.StealthDir()), ledgerExists(loc.VisibleDir())
	switch {
	case stealth && visible:
		return loc, ledger.Fail(ledger.ErrIntegrity, "integrity_two_ledgers",
			"two ledgers found: %s and %s; remove one", loc.StealthDir(), loc.VisibleDir())
	case stealth:
		return loc.Resolve(false), nil
	case visible:
		return loc.Resolve(true), nil
	default:
		return loc.Resolve(false), ledger.Fail(ledger.ErrNoLedger, "no_ledger_uninitialized",
			"no ledger in %s; run `bdc init`", repoRoot)
	}
}

// InitOptions configures Init. Force removes a target directory that exists but
// holds no valid Dolt database; it never overwrites a working ledger and never
// authorises a second ledger in the other mode.
type InitOptions struct {
	Visible bool
	Force   bool
}

// Init creates the ledger at loc.Dir and applies the embedded schema. It is
// idempotent: an existing ledger at loc.Dir is left alone and created is false.
func Init(ctx context.Context, loc Location, o InitOptions) (created bool, err error) {
	other := loc.StealthDir()
	if loc.Stealth {
		other = loc.VisibleDir()
	}
	if ledgerExists(other) {
		return false, ledger.Fail(ledger.ErrIntegrity, "integrity_two_ledgers",
			"a ledger already exists at %s; two ledgers in one repository is unsupported", other)
	}

	if ledgerExists(loc.Dir) {
		if !loc.Stealth {
			if err := addExclude(loc); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	if entries, err := os.ReadDir(loc.Dir); err == nil && len(entries) > 0 {
		if !o.Force {
			return false, ledger.Fail(ledger.ErrIntegrity, "integrity_dirty_ledger_dir",
				"%s exists but holds no Dolt database; pass --force to replace it", loc.Dir)
		}
		if err := os.RemoveAll(loc.Dir); err != nil {
			return false, storageErr(err, "cannot remove %s", loc.Dir)
		}
	}

	if err := os.MkdirAll(loc.Dir, 0o700); err != nil {
		return false, storageErr(err, "cannot create %s", loc.Dir)
	}
	if err := createDatabase(ctx, loc); err != nil {
		// A half-created directory is worse than none: the next Discover would
		// see a dir with no database and demand --force.
		_ = os.RemoveAll(loc.Dir)
		return false, err
	}
	if !loc.Stealth {
		if err := addExclude(loc); err != nil {
			return false, err
		}
	}
	return true, nil
}

// addExclude appends the visible ledger to .git/info/exclude, which is shared
// across worktrees, so `git status` stays clean everywhere.
func addExclude(loc Location) error {
	path := filepath.Join(loc.GitCommon, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return storageErr(err, "cannot create %s", filepath.Dir(path))
	}
	body, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return storageErr(err, "cannot read %s", path)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == excludeEntry {
			return nil
		}
	}
	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		body = append(body, '\n')
	}
	body = append(body, (excludeEntry + "\n")...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return storageErr(err, "cannot write %s", path)
	}
	return nil
}

// strippedGitEnv are removed — not blanked — from every child `git`. GIT_DIR=""
// makes git fail outright, while an inherited GIT_DIR silently resolves a
// different repository and would write the ledger into an unrelated checkout.
var strippedGitEnv = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_COMMON_DIR",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
}

func hermeticEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
next:
	for _, kv := range src {
		for _, name := range strippedGitEnv {
			if strings.HasPrefix(kv, name+"=") {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = hermeticEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func gitPath(ctx context.Context, dir, flag string) (string, error) {
	out, err := gitOutput(ctx, dir, "rev-parse", "--path-format=absolute", flag)
	if err != nil {
		return "", ledger.FailWith(ledger.ErrNoLedger, "no_ledger_not_git", err,
			"cannot resolve %s for %s", flag, dir)
	}
	return realPath(out)
}

// mainWorktree is the main worktree root, which every linked worktree reports
// identically. `rev-parse --show-toplevel` returns the *current* worktree and
// would let a linked worktree miss a visible ledger and create a second one.
func mainWorktree(ctx context.Context, dir string) (string, error) {
	out, err := gitOutput(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", ledger.FailWith(ledger.ErrNoLedger, "no_ledger_not_git", err,
			"cannot list worktrees for %s", dir)
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			return realPath(strings.TrimSpace(rest))
		}
	}
	return "", ledger.Fail(ledger.ErrIntegrity, "integrity_no_main_worktree",
		"git worktree list reported no main worktree for %s", dir)
}

// realPath resolves symlinks so path comparisons hold on macOS, where /tmp is a
// symlink to /private/tmp and git reports the resolved form.
func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

func withinDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func storageErr(cause error, format string, a ...any) error {
	return ledger.FailWith(ledger.ErrIntegrity, "storage_io_error", cause, format, a...)
}
