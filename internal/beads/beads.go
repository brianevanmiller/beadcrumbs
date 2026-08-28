// Package beads is the optional Beads integration: nine supported `bd --json`
// commands behind a detection ladder, and nothing else.
//
// Two measured facts shape every line of it.
//
//   - `--json` does not guarantee JSON on failure. Against bd 1.2.2 only
//     `where`, `context`, and `version` emit structured errors; `list`, `show`,
//     `comments`, and `info` print plain text on stderr and exit 1. stdout and
//     stderr are therefore captured separately, only stdout is ever parsed, and
//     a nonzero exit becomes a bounded adapter error naming the command and the
//     exit code — never a pattern match on the message.
//   - Enrichment may not fail a core write. Detect never returns an error, and
//     every wrapper's failure is a ledger.ErrAdapter the caller renders as a
//     warning against one Reference. A missing `bd`, a repository with no
//     tracker, and unreadable output are all degradation, not failure.
//
// Every error this package returns is one of exactly two kinds:
// ledger.ErrInvalidInput for an argument rejected before `bd` runs, and
// ledger.ErrAdapter for everything `bd` answered. Nothing else escapes.
//
// Beads' own storage is out of contract — no `bd sql`, no `bd dolt`, no direct
// `.beads` access — and `bd doctor` is never invoked, because in embedded mode
// it exits 0 and prints prose rather than acting as a health check.
package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// MinVersion is the floor the JSON shapes in this package were measured
// against. Below it the adapter is disabled rather than hopeful.
const MinVersion = "1.2.2"

// refKind is the Reference kind this adapter serves. It is the only place the
// string appears; core never learns what a Beads locator looks like.
const refKind = "beads"

// Detection outcomes. Each rung of the ladder answers a different question, and
// each failure keeps its own reason so `bdc doctor` can tell "no bd" from "bd
// too old" from "this repository has no tracker".
const (
	ReasonOK           = "ok"
	ReasonNotInstalled = "not_installed"
	ReasonNoVersion    = "version_unreadable"
	ReasonBelowFloor   = "below_floor"
	ReasonNoWorkspace  = "no_workspace"
)

// Availability is what detection learned. Version and Prefix are display
// enrichment and never gate the adapter: a `bd where` that omits `prefix` still
// describes a usable workspace.
//
// ProjectID is workspace identity — the one field the adapter trusts to decide
// whether two directories name the same tracker — and it is empty until
// Workspace has been called, because it costs an extra `bd context` that the
// enrichment path does not otherwise need.
type Availability struct {
	Present   bool   `json:"present"`
	Reason    string `json:"reason"`
	Version   string `json:"version,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	RepoRoot  string `json:"repo_root,omitempty"`
}

// Adapter is one detected `bd` binary bound to one repository root. It is safe
// for concurrent use; the only mutable state is the memoised workspace context.
type Adapter struct {
	bin      string
	repoRoot string

	mu    sync.Mutex
	av    Availability
	ws    *WorkspaceContext
	wsErr error
}

// Detect runs the three-rung ladder and never returns an error: every failure
// is an Availability a caller reports and continues past. A nil *Adapter is
// returned whenever Present is false.
//
// Rung 2 is the one command invoked without `-C`. Measured: `bd -C <dir>
// version --json` exits 1 with plain text whenever <dir> has no Beads
// workspace, which would collapse "not installed", "too old", and "no tracker
// here" into one indistinguishable failure.
func Detect(ctx context.Context, repoRoot string) (*Adapter, Availability) {
	av := Availability{Reason: ReasonNotInstalled, RepoRoot: repoRoot}

	bin, err := exec.LookPath("bd")
	if err != nil {
		return nil, av
	}
	a := &Adapter{bin: bin, repoRoot: repoRoot}

	var v struct {
		Version string `json:"version"`
	}
	out, err := a.run(ctx, bare, nil, "version")
	if err != nil || json.Unmarshal(out, &v) != nil || v.Version == "" {
		// A failure here means the binary is broken, not that the repository
		// lacks a workspace — rung 3 has not been asked yet.
		av.Reason = ReasonNoVersion
		return nil, av
	}
	av.Version = v.Version
	if !atLeast(v.Version, MinVersion) {
		av.Reason = ReasonBelowFloor
		return nil, av
	}

	var w struct {
		Prefix string `json:"prefix"`
	}
	out, err = a.run(ctx, read, nil, "where")
	if err != nil || json.Unmarshal(out, &w) != nil {
		// `where` is the workspace question, so an unreadable answer to it is
		// not a usable workspace either way.
		av.Reason = ReasonNoWorkspace
		return nil, av
	}
	av.Prefix, av.Present, av.Reason = w.Prefix, true, ReasonOK
	a.av = av
	return a, av
}

// Availability reports what the adapter knows now, which differs from Detect's
// return only in that ProjectID is filled once Workspace has run.
func (a *Adapter) Availability() Availability {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.av
}

// WorkspaceContext is `bd context --json`.
//
// IsWorktree and IsRedirected describe *the directory `bd` was invoked from*,
// not the tracker: a linked git worktree and its main checkout resolve to the
// same workspace while reporting different flags. They are surfaced verbatim
// for handoff and are never used to decide which tracker is which. ProjectID is
// the only identity this adapter trusts.
type WorkspaceContext struct {
	Backend       string `json:"backend"`
	BDVersion     string `json:"bd_version"`
	BeadsDir      string `json:"beads_dir"`
	CWDRepoRoot   string `json:"cwd_repo_root"`
	Database      string `json:"database"`
	DoltMode      string `json:"dolt_mode"`
	IsRedirected  bool   `json:"is_redirected"`
	IsWorktree    bool   `json:"is_worktree"`
	ProjectID     string `json:"project_id"`
	RepoRoot      string `json:"repo_root"`
	Role          string `json:"role"`
	SchemaVersion int    `json:"schema_version"`
}

// Workspace is `bd context --json`, memoised for the life of the process —
// including its failure. One `bdc` run is one short process, so the cache
// cannot go stale, and re-asking a broken `bd context` once per Reference would
// multiply one failure into a visible delay.
func (a *Adapter) Workspace(ctx context.Context) (WorkspaceContext, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ws == nil && a.wsErr == nil {
		var w WorkspaceContext
		out, err := a.run(ctx, read, nil, "context")
		if err == nil {
			err = decode("context", out, &w)
		}
		if err != nil {
			a.wsErr = err
		} else {
			a.ws, a.av.ProjectID = &w, w.ProjectID
		}
	}
	if a.wsErr != nil {
		return WorkspaceContext{}, a.wsErr
	}
	return *a.ws, nil
}

// scope selects the standing flags.
//
// `--readonly` makes a read provably incapable of mutating the tracker and
// `--sandbox` keeps a write from triggering Dolt auto-push. `--ignore-schema-skew`
// is never passed: forward drift must surface as an adapter error rather than
// be papered over. bare is `version` alone — see Detect.
type scope int

const (
	bare scope = iota
	read
	write
)

// run executes one `bd` subcommand and returns its stdout.
//
// stderr is captured into its own buffer and dropped. That is deliberate rather
// than lazy: `bd` prints advisory prose there even under `--quiet`, failure
// messages there are plain text on most commands, and issue text can appear in
// both — so it may never be parsed and may never be quoted into an error that
// the ledger's privacy scan would then have to defend.
func (a *Adapter) run(ctx context.Context, sc scope, stdin []byte, args ...string) ([]byte, error) {
	full := make([]string, 0, len(args)+5)
	switch sc {
	case bare:
		full = append(full, "--json", "--quiet")
	case read:
		full = append(full, "-C", a.repoRoot, "--json", "--quiet", "--readonly")
	case write:
		full = append(full, "-C", a.repoRoot, "--json", "--quiet", "--sandbox")
	}
	full = append(full, args...)

	cmd := exec.CommandContext(ctx, a.bin, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		return nil, failed(args[0], exitCode(err))
	}
	return stdout.Bytes(), nil
}

// failed is the bounded adapter error. It names the command and the exit code
// and carries nothing `bd` printed, because on most commands what `bd` printed
// is unstructured text this package has promised never to interpret.
func failed(command string, code int) *ledger.Error {
	return ledger.Fail(ledger.ErrAdapter, "beads_command_failed",
		"bd %s exited with status %d, so this enrichment was skipped", command, code).
		WithDetails(map[string]any{"command": "bd " + command, "exit_code": code})
}

// exitCode is -1 when `bd` never ran or was killed, which is still a bounded
// answer: the caller reports "bd <cmd> exited with status -1" and continues.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// decode parses a successful command's stdout. The output is never quoted into
// the error for the same reason stderr is dropped.
func decode(command string, out []byte, v any) error {
	if err := json.Unmarshal(out, v); err != nil {
		return ledger.Fail(ledger.ErrAdapter, "beads_unreadable_output",
			"bd %s exited 0 but did not print the JSON this adapter expects", command).
			WithDetails(map[string]any{"command": "bd " + command})
	}
	return nil
}

// missing is the failure signal for a field this adapter needs and `bd` did not
// send. Unknown fields are the opposite case and are ignored as additive.
func missing(command, field string) *ledger.Error {
	return ledger.Fail(ledger.ErrAdapter, "beads_unreadable_output",
		"bd %s returned a record with no %s, which this adapter cannot use", command, field).
		WithDetails(map[string]any{"command": "bd " + command, "field": field})
}

func invalid(format string, a ...any) *ledger.Error {
	return ledger.Fail(ledger.ErrInvalidInput, "invalid_input", format, a...)
}

// atLeast compares dotted numeric versions. Anything after the third component
// — `-dev`, `+build`, a fourth number — is ignored, because the floor is about
// the JSON contract and prerelease ordering would only add ways to be wrong.
func atLeast(have, floor string) bool {
	h, f := parseVersion(have), parseVersion(floor)
	for i := range h {
		if h[i] != f[i] {
			return h[i] > f[i]
		}
	}
	return true
}

func parseVersion(s string) [3]int {
	var out [3]int
	fields := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".", 4)
	for i := 0; i < len(out) && i < len(fields); i++ {
		digits := fields[i]
		if cut := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }); cut >= 0 {
			digits = digits[:cut]
		}
		out[i], _ = strconv.Atoi(digits)
	}
	return out
}
