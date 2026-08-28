// Package e2e drives the real `bdc` binary against a real Git repository, using
// only the commands the documentation tells an agent to run. Everything else in
// the suite tests a package; this tests the product.
//
// The binary is built here rather than assumed: `go test` inherits the CGO and
// ICU flags the Makefile exports, so the child `go build` gets them too. There
// is no installer, no network, and no global installation anywhere in this file.
package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// envelope is the published CLI contract, decoded loosely: the fields this
// package asserts on and nothing more. A stricter type here would duplicate
// cmd/bdc's golden tests without adding a guarantee.
type envelope struct {
	BDC      string          `json:"bdc"`
	Command  string          `json:"command"`
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Warnings []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"warnings"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

var (
	buildOnce  sync.Once
	binaryPath string
	buildErr   error
)

func bdcBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "bdc-e2e-")
		if err != nil {
			buildErr = err
			return
		}
		binaryPath = filepath.Join(dir, "bdc")
		cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/bdc")
		cmd.Dir = "../.."
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = err
			t.Logf("go build: %s", out)
		}
	})
	if buildErr != nil {
		t.Fatalf("building bdc: %v", buildErr)
	}
	return binaryPath
}

// session is one repository plus the environment every invocation runs under.
type session struct {
	t      *testing.T
	binary string
	dir    string
	env    []string
	vars   map[string]string
}

func newSession(t *testing.T, env []string) *session {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("creating the fixture repository: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=bdc", "-c", "user.email=bdc@example.com",
			"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return &session{t: t, binary: bdcBinary(t), dir: repo, env: env, vars: map[string]string{}}
}

// bdc runs one documented invocation and returns its decoded envelope. Anything
// but exit 0 fails the test: this file is the happy path an agent is told to
// follow, and a failure in it is a broken product, not a case to branch on.
func (s *session) bdc(args ...string) envelope {
	s.t.Helper()
	full := append([]string{"-C", s.dir, "--json", "--actor", "e2e", "--actor-kind", "human"},
		s.resolve(args)...)
	cmd := exec.Command(s.binary, full...)
	if s.env != nil {
		cmd.Env = s.env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	var env envelope
	if decodeErr := json.Unmarshal(stdout.Bytes(), &env); decodeErr != nil {
		s.t.Fatalf("`bdc %s` did not emit one envelope on stdout: %v\nstdout: %s\nstderr: %s",
			strings.Join(full, " "), decodeErr, stdout.String(), stderr.String())
	}
	if err != nil || !env.OK {
		s.t.Fatalf("`bdc %s` failed: %v\n%s", strings.Join(full, " "), err, stdout.String())
	}
	if env.BDC != "1" {
		s.t.Fatalf("envelope version is %q, want \"1\"", env.BDC)
	}
	return env
}

func (s *session) resolve(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "{") && strings.HasSuffix(a, "}") {
			v, ok := s.vars[a[1:len(a)-1]]
			if !ok {
				s.t.Fatalf("no earlier command bound %s", a)
			}
			out = append(out, v)
			continue
		}
		out = append(out, a)
	}
	return out
}

// field reads one dotted path out of an envelope's data.
func (s *session) field(env envelope, path string) string {
	s.t.Helper()
	var tree any
	if err := json.Unmarshal(env.Data, &tree); err != nil {
		s.t.Fatalf("decoding data for %s: %v", env.Command, err)
	}
	cur := tree
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			child, ok := node[part]
			if !ok {
				s.t.Fatalf("%s has no %s", env.Command, path)
			}
			cur = child
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i >= len(node) {
				s.t.Fatalf("%s has no %s", env.Command, path)
			}
			cur = node[i]
		default:
			s.t.Fatalf("%s has no %s", env.Command, path)
		}
	}
	switch v := cur.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return strings.TrimSuffix(strings.TrimRight(jsonNumber(v), "0"), ".")
	default:
		s.t.Fatalf("%s.%s is %T, which has no scalar form", env.Command, path, cur)
		return ""
	}
}

func jsonNumber(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (s *session) bind(name string, env envelope, path string) string {
	s.t.Helper()
	v := s.field(env, path)
	s.vars[name] = v
	return v
}

// TestFullWorkflowInFixtureRepo walks the documented path end to end:
// capture -> review -> harvest -> insight -> propose -> record -> context ->
// handoff, using only commands from the CLI contract.
func TestFullWorkflowInFixtureRepo(t *testing.T) {
	s := newSession(t, nil)

	s.bdc("init")

	first := s.bdc("capture",
		"The engine holds an exclusive directory lock for the life of a command.",
		"--confidence", "0.7", "--ref", "docs:docs/design.md@source")
	s.bind("crumbA", first, "crumb.id")
	second := s.bdc("capture",
		"A cached tracker label is not live state, so every read states its freshness.",
		"--confidence", "0.6")
	s.bind("crumbB", second, "crumb.id")

	s.bdc("crumb", "review", "{crumbA}", "{crumbB}",
		"--state", "accepted", "--rationale", "both hold up against the measured behaviour")

	harvest := s.bdc("harvest", "--crumb", "{crumbA}", "--crumb", "{crumbB}",
		"--class", "decision", "--confidence", "0.8",
		"--title", "One engine, one transaction, one close",
		"--content", "Every bdc invocation opens one short-lived engine, performs one bounded "+
			"transaction, and closes it before returning.")
	insight := s.bind("insight", harvest, "insight.id")
	s.bind("revision", harvest, "revision.id")

	shown := s.bdc("insight", "show", "{insight}", "--lineage")
	if got := s.field(shown, "insight.id"); got != insight {
		t.Fatalf("insight show returned %s, want %s", got, insight)
	}

	// An Insight nobody has judged is advisory. Granting a working default is
	// what makes it something a later session may rely on, and prime is where
	// that shows up.
	s.bdc("validate", "{revision}", "--verdict", "supported",
		"--rationale", "reproduced against the running engine")
	s.bdc("authority", "{revision}", "--level", "default",
		"--rationale", "this is how the store is used everywhere")

	proposal := s.bdc("promote", "propose", "--insight", "{insight}",
		"--class", "decision", "--destination", "docs:docs/decisions/0001-one-engine.md",
		"--capability", "stable-anchor",
		"--content", "One engine, one transaction, one close.")
	s.bind("proposal", proposal, "proposal.id")
	if created := s.field(proposal, "created"); created != "true" {
		t.Fatalf("the first proposal reported created=%s", created)
	}

	// Proposing the same content to the same destination again is the same
	// proposal. This is the property an agent retrying a step depends on.
	again := s.bdc("promote", "propose", "--insight", "{insight}",
		"--class", "decision", "--destination", "docs:docs/decisions/0001-one-engine.md",
		"--capability", "stable-anchor",
		"--content", "One engine, one transaction, one close.")
	if s.field(again, "created") != "false" {
		t.Error("re-proposing identical content created a second proposal")
	}
	if s.field(again, "proposal.id") != s.vars["proposal"] {
		t.Error("the idempotent hit returned a different proposal")
	}

	receipt := s.bdc("promote", "record", "{proposal}",
		"--locator", "docs/decisions/0001-one-engine.md",
		"--anchor", "0f1e2d3c4b5a69788796a5b4c3d2e1f0", "--verified")
	if s.field(receipt, "durable") != "true" {
		t.Error("a destination declaring stable-anchor produced a receipt that is not durable")
	}

	context := s.bdc("context")
	if !strings.Contains(string(context.Data), insight) {
		t.Errorf("context does not mention the Insight it was built from:\n%s", context.Data)
	}
	if got := s.field(context, "insights.0.standing"); got != "working-default" {
		t.Errorf("an Insight with a human default reads as %q in context", got)
	}

	handoff := s.bdc("handoff")
	if got := s.field(handoff, "state.insights"); got != "1" {
		t.Errorf("handoff reports %s Insights, want 1", got)
	}
	if got := s.field(handoff, "workspace.enrichment"); got != "none" {
		t.Errorf("handoff reports enrichment %q with no adapter installed", got)
	}

	prime := s.bdc("prime")
	if !strings.Contains(string(prime.Data), insight) {
		t.Errorf("prime does not list the working default:\n%s", prime.Data)
	}

	if ok := s.field(s.bdc("doctor"), "ok"); ok != "true" {
		t.Error("doctor reports the ledger as unhealthy after the documented workflow")
	}
}

// TestCoreWorkflowWithoutBeads: the tracker integration is optional, so the
// core workflow has to complete with no `bd` anywhere on PATH. A missing
// adapter may degrade a label; it may never fail a write or corrupt the ledger.
func TestCoreWorkflowWithoutBeads(t *testing.T) {
	empty := t.TempDir()
	// git is still needed — discovery shells out to it — so PATH keeps only the
	// directory git lives in, and that directory is checked for a stray `bd`.
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	gitDir := filepath.Dir(gitPath)
	if _, err := os.Stat(filepath.Join(gitDir, "bd")); err == nil {
		t.Skipf("bd shares a directory with git (%s), so it cannot be removed from PATH", gitDir)
	}
	env := append(os.Environ(), "PATH="+empty+string(os.PathListSeparator)+gitDir)

	s := newSession(t, env)
	s.bdc("init")

	crumb := s.bdc("capture", "Beads is optional; nothing in the core path may need it.",
		"--confidence", "0.5", "--ref", "beads:bdc-7ah.20@subject")
	s.bind("crumb", crumb, "crumb.id")

	s.bdc("crumb", "review", "{crumb}", "--state", "accepted", "--rationale", "it is the invariant")
	harvest := s.bdc("harvest", "--crumb", "{crumb}", "--class", "learning",
		"--title", "Optional integrations stay optional",
		"--content", "A missing tracker degrades a label and never fails a core write.")
	s.bind("insight", harvest, "insight.id")

	// The reference resolves to its locator: never enriched is a state, not a
	// failure, and it is reported rather than hidden.
	refs := s.bdc("reference", "list", "--target", "{crumb}")
	if got := s.field(refs, "references.0.freshness.state"); got != "never" {
		t.Errorf("a reference with no enricher reports freshness %q, want never", got)
	}

	for _, env := range []envelope{
		s.bdc("context"), s.bdc("handoff"), s.bdc("prime"), s.bdc("doctor"),
	} {
		for _, w := range env.Warnings {
			if strings.HasPrefix(w.Code, "adapter_") {
				t.Errorf("%s reported an adapter warning with no adapter installed: %s",
					env.Command, w.Message)
			}
		}
	}

	if ok := s.field(s.bdc("doctor"), "ok"); ok != "true" {
		t.Error("doctor reports the ledger as unhealthy with no bd on PATH")
	}
}
