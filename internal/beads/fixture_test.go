package beads_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// These tests run the real `bd` when it is installed and skip with a named
// reason when it is not, because the contract under test is that binary's
// observed behavior. The cases the real binary cannot produce on demand — a
// version below the floor, unknown fields, a plain-text failure on stdout — use
// a stub `bd` on PATH rather than an injected seam, so argv assembly, the
// stdout/stderr split, and the exit code are still exercised for real.

func ctx() context.Context { return context.Background() }

func requireBD(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not installed; the real-binary contract cannot be checked here")
	}
}

// gitRepo is a fresh Git repository with one commit and no Beads workspace.
func gitRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the fixture path: %v", err)
	}
	run(t, root, "git", "init", "-q", ".")
	run(t, root, "git", "config", "user.email", "fixture@example.test")
	run(t, root, "git", "config", "user.name", "fixture")
	run(t, root, "git", "commit", "-q", "--allow-empty", "-m", "root")
	return root
}

// beadsRepo returns one stealth Beads workspace shared by every test that needs
// a real one. `bd init` costs about two seconds and nothing here mutates the
// workspace in a way another test can observe, so paying it once keeps the
// package fast.
var beadsRepo = sync.OnceValues(func() (string, error) {
	root, err := os.MkdirTemp("", "beadcrumbs-beads-")
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	for _, args := range [][]string{
		{"git", "init", "-q", "."},
		{"git", "config", "user.email", "fixture@example.test"},
		{"git", "config", "user.name", "fixture"},
		{"git", "commit", "-q", "--allow-empty", "-m", "root"},
		{"bd", "init", "--prefix", "tst", "--stealth"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", &fixtureError{args: args, out: string(out), err: err}
		}
	}
	return root, nil
})

type fixtureError struct {
	args []string
	out  string
	err  error
}

func (e *fixtureError) Error() string {
	return strings.Join(e.args, " ") + ": " + e.err.Error() + "\n" + e.out
}

func workspace(t *testing.T) string {
	t.Helper()
	requireBD(t)
	root, err := beadsRepo()
	if err != nil {
		t.Fatalf("preparing the shared Beads workspace: %v", err)
	}
	return root
}

func TestMain(m *testing.M) {
	code := m.Run()
	if root, err := beadsRepo(); err == nil {
		_ = os.RemoveAll(root)
	}
	os.Exit(code)
}

// stubPreamble finds the subcommand among the standing flags and appends the
// whole argv to $BD_ARGV_LOG, which is how a test proves which flags a rung
// passed.
const stubPreamble = `#!/bin/sh
cmd=
for a in "$@"; do
  case "$a" in
    version|where|context|show|list|comments|comment|create|link) cmd="$a"; break;;
  esac
done
if [ -n "$BD_ARGV_LOG" ]; then echo "$*" >> "$BD_ARGV_LOG"; fi
case "$cmd" in
`

// stubBD puts a stub `bd` on PATH. cases is the body of a shell case statement
// over the subcommand; anything it does not answer exits 1 with plain text, the
// way the real binary fails.
func stubBD(t *testing.T, cases string) {
	t.Helper()
	dir := t.TempDir()
	script := stubPreamble + cases + `
*) echo "Error: no beads database found" >&2; exit 1;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the stub bd: %v", err)
	}
	t.Setenv("PATH", dir)
}

// argvLog turns on argv recording and returns a reader for the recorded lines.
func argvLog(t *testing.T) func() []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "argv")
	t.Setenv("BD_ARGV_LOG", path)
	return func() []string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the recorded argv: %v", err)
		}
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}
