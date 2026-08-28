// Package e2e drives the real `bdc` binary against a real Git repository, using
// only the commands the documentation tells an agent to run. Everything else in
// the suite tests a package; this tests the product.
//
// The binary is built rather than assumed — see build_test.go for how, and for
// why the build has to supply its own ICU flags. There is no installer, no
// network, and no global installation anywhere in this file.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// session is one repository plus the environment every invocation runs under.
type session struct {
	t      *testing.T
	binary string
	dir    string
	env    []string
	vars   map[string]string

	// base is the provenance and enrichment prefix every invocation carries.
	// It is a field rather than a constant because two things vary and both
	// change what the ledger records: who is acting, and whether the optional
	// tracker is consulted.
	base []string
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
	return &session{
		t: t, binary: bdcBinary(t), dir: repo, env: env, vars: map[string]string{},
		// --no-enrich by default: with `bd` installed the enrichment fields
		// describe the machine rather than the product. TestCoreWorkflowWithBeads
		// is the one test that asks for the detected path.
		base: []string{"--actor", "e2e", "--actor-kind", "human", "--no-enrich"},
	}
}

// asAgent records every later invocation as an agent, which is the provenance
// the authority axis is built to gate and the one the installed skill sets.
func (s *session) asAgent() *session {
	s.base = []string{"--actor", "e2e-agent", "--actor-kind", "agent",
		"--model", "test-model", "--session", "test-session", "--no-enrich"}
	return s
}

// bdc runs one documented invocation and returns its decoded envelope. Anything
// but exit 0 fails the test: this file is the happy path an agent is told to
// follow, and a failure in it is a broken product, not a case to branch on.
func (s *session) bdc(args ...string) envelope {
	s.t.Helper()
	env, exit := s.try(args...)
	if exit != 0 || !env.OK {
		s.t.Fatalf("`bdc %s` failed with exit %d: %+v", strings.Join(args, " "), exit, env.Error)
	}
	return env
}

// try runs one invocation and returns its envelope and exit code without
// judging either, which is what a test of a refusal needs.
func (s *session) try(args ...string) (envelope, int) {
	s.t.Helper()
	full := append(append([]string{"-C", s.dir, "--json"}, s.base...), s.resolve(args)...)
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
	if env.BDC != "1" {
		s.t.Fatalf("envelope version is %q, want \"1\"", env.BDC)
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		return env, exitErr.ExitCode()
	default:
		s.t.Fatalf("running `bdc %s`: %v", strings.Join(full, " "), err)
	}
	return env, 0
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

// TestCoreWorkflowWithBeads is the other half of TestCoreWorkflowWithoutBeads,
// and the only test in the suite that proves the adapter is wired to anything:
// with a real `bd` and a real workspace, doctor has to report the tracker as
// present and a Beads reference has to come back enriched from it. Without this
// the absent-Beads gate passes vacuously, because the present-Beads path never
// runs.
func TestCoreWorkflowWithBeads(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH, so the detected-tracker path cannot be exercised")
	}
	s := newSession(t, nil)
	s.base = []string{"--actor", "e2e", "--actor-kind", "human"}

	// `bd init` is what makes the fixture a Beads workspace; detection's third
	// rung asks `bd where`, which fails without one.
	if out, err := bd(t, s.dir, "init"); err != nil {
		t.Skipf("`bd init` did not run in the fixture: %v\n%s", err, out)
	}
	created, err := bd(t, s.dir, "create", "A ticket the ledger will reference", "--json")
	if err != nil {
		t.Skipf("`bd create` did not run in the fixture: %v\n%s", err, created)
	}
	var issue struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &issue); err != nil || issue.ID == "" {
		t.Skipf("`bd create --json` did not report an id: %v\n%s", err, created)
	}

	s.bdc("init")
	doctor := s.bdc("doctor")
	if got := s.field(doctor, "beads.present"); got != "true" {
		t.Fatalf("doctor reports beads.present=%s with bd %s installed and a workspace present",
			got, s.field(doctor, "beads.version"))
	}
	if got := s.field(doctor, "beads.reason"); got != "ok" {
		t.Errorf("doctor reports beads.reason=%q, want ok", got)
	}

	crumb := s.bdc("capture", "The tracker reference has to resolve to a real ticket.",
		"--confidence", "0.6", "--ref", "beads:"+issue.ID+"@subject")
	s.bind("crumb", crumb, "crumb.id")

	refs := s.bdc("reference", "list", "--target", "{crumb}", "--refresh")
	if got := s.field(refs, "references.0.freshness.state"); got != "live" {
		t.Errorf("a refreshed Beads reference reports freshness %q, want live", got)
	}
	if got := s.field(refs, "references.0.freshness.enricher"); got != "beads" {
		t.Errorf("a refreshed Beads reference names enricher %q, want beads", got)
	}
	if got := s.field(refs, "references.0.display"); got == issue.ID {
		t.Error("the reference still displays its locator, so nothing was enriched")
	}
	for _, w := range refs.Warnings {
		t.Errorf("reference list --refresh warned %s: %s", w.Code, w.Message)
	}

	if got := s.field(s.bdc("handoff"), "workspace.enrichment"); got != "beads" {
		t.Errorf("handoff reports enrichment %q with the adapter wired, want beads", got)
	}
}

// bd runs one `bd` command in the fixture. Beads owns its own storage, so this
// is the only place the suite talks to it directly, and only to build the
// workspace the adapter is supposed to find.
func bd(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	// `bd init` rejects -C on a directory that is not yet a Beads project, so
	// the working directory is the only way to say where the workspace goes.
	cmd := exec.Command("bd", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestAgentPromotionRequiresHumanAuthority is the authority axis end to end,
// with the provenance that makes it fire. Every other e2e run acts as a human,
// for whom the gate is a no-op — so without this test the refusal, the recorded
// proposal, the human grant, and the retry are proven nowhere above the domain
// package.
func TestAgentPromotionRequiresHumanAuthority(t *testing.T) {
	human := newSession(t, nil)
	human.bdc("init")

	crumb := human.bdc("capture", "Policy records are the one class an agent may not settle alone.",
		"--confidence", "0.7")
	human.bind("crumb", crumb, "crumb.id")
	human.bdc("crumb", "review", "{crumb}", "--state", "accepted", "--rationale", "it is the invariant")
	harvest := human.bdc("harvest", "--crumb", "{crumb}", "--class", "policy",
		"--title", "Policy is a human decision",
		"--content", "An agent may propose a policy record; only a human may authorise one.")
	human.bind("insight", harvest, "insight.id")

	// Same repository, same ledger, agent provenance.
	agent := newSession(t, nil).asAgent()
	agent.dir = human.dir
	agent.vars = human.vars

	propose := []string{"promote", "propose", "--insight", "{insight}", "--class", "policy",
		"--destination", "docs:docs/policy/ledger.md",
		"--content", "Only a human may authorise a policy record."}

	refused, exit := agent.try(propose...)
	if exit != 3 {
		t.Fatalf("an agent proposing a policy record exited %d, want 3", exit)
	}
	if refused.Error == nil || refused.Error.Code != "authority_required" {
		t.Fatalf("the refusal is %+v, want error.code authority_required", refused.Error)
	}
	proposalID, _ := refused.Error.Details["proposal_id"].(string)
	if proposalID == "" {
		t.Fatal("the refusal does not carry the proposal id a human has to grant authority against")
	}

	// The human grant is the documented unblock, and it is made against the
	// proposal the refusal named.
	human.bdc("authority", proposalID, "--level", "mandatory",
		"--rationale", "reviewed the proposed policy record and approved it")

	granted, exit := agent.try(propose...)
	if exit != 0 || !granted.OK {
		t.Fatalf("after the human grant the same proposal exited %d: %+v", exit, granted.Error)
	}
	if got := agent.field(granted, "proposal.id"); got != proposalID {
		t.Errorf("the retry created proposal %s rather than reusing %s", got, proposalID)
	}
}

// TestHarnessShimRecordsUsableAgentProvenance runs the shipped session-hook shim
// the way a harness does — event on argv, payload on stdin. The shim declares an
// agent actor, and the ledger refuses an agent that does not also name a model
// and a session, so a shim that sets only the session makes every hooked
// invocation fail behind the `2>/dev/null` that keeps hooks quiet.
func TestHarnessShimRecordsUsableAgentProvenance(t *testing.T) {
	s := newSession(t, nil)
	s.bdc("init")

	binDir := filepath.Dir(bdcBinary(t))
	env := append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BDC_ACTOR_MODEL=", "BDC_ACTOR_KIND=", "BDC_SESSION=", "BDC_ACTOR=")

	var stdout, stderr bytes.Buffer
	shim := exec.Command("sh", repoPath(t, "hooks", "bdc-hook.sh"), "SessionStart")
	shim.Dir = s.dir
	shim.Env = env
	shim.Stdin = strings.NewReader(
		`{"session_id":"sess-e2e","cwd":"` + s.dir + `","hook_event_name":"SessionStart"}`)
	shim.Stdout = &stdout
	shim.Stderr = &stderr
	if err := shim.Run(); err != nil {
		t.Fatalf("the shim exited nonzero, which a hook may never do: %v\n%s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatalf("SessionStart injected nothing, so `bdc prime` failed inside the shim\nstderr: %s",
			stderr.String())
	}

	// The same environment the shim exports, on a write: an agent harness that
	// names a model and a session is recorded as an agent without having to
	// remember a flag, and never as whoever owns the login.
	agent := &session{t: t, binary: s.binary, dir: s.dir, vars: map[string]string{},
		env: append(env, "BDC_ACTOR_MODEL=test-model", "BDC_SESSION=sess-e2e")}
	crumb := agent.bdc("capture", "Provenance is recorded, not assumed.", "--confidence", "0.5")
	if got := agent.field(crumb, "crumb.actor_kind"); got != "agent" {
		t.Errorf("a run carrying a model and a session recorded actor_kind %q, want agent", got)
	}
	if got := agent.field(crumb, "crumb.actor_id"); got == os.Getenv("USER") {
		t.Errorf("an agent's write was attributed to the logged-in user %q", got)
	}
}

// TestDoctorReportsAMissingLedgerInsideTheEnvelope pins the skill's first
// instruction to what the binary does. `doctor` is ledgerOptional: it always
// exits 0 with `error: null`, and an agent branching on the exit code or on
// `error.code` — which the rest of the contract trains it to do — would decide
// the ledger exists and never offer `bdc init`.
func TestDoctorReportsAMissingLedgerInsideTheEnvelope(t *testing.T) {
	s := newSession(t, nil)

	env, exit := s.try("doctor")
	if exit != 0 || !env.OK || env.Error != nil {
		t.Fatalf("doctor on a repository with no ledger exited %d with error %+v; "+
			"the skill's instruction depends on it succeeding", exit, env.Error)
	}
	if got := s.field(env, "ok"); got != "false" {
		t.Errorf("doctor reports data.ok=%s with no ledger present", got)
	}

	const check = "ledger_present"
	var data struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decoding doctor data: %v", err)
	}
	found := false
	for _, c := range data.Checks {
		if c.Name == check {
			found = true
			if c.Status != "fail" {
				t.Errorf("%s reports status %q with no ledger present", check, c.Status)
			}
		}
	}
	if !found {
		t.Fatalf("doctor names no %q check, which is the signal the skill tells an agent to read", check)
	}

	// The instruction and the check name have to stay one fact.
	skill := readFile(t, repoPath(t, "skills", "beadcrumbs", "SKILL.md"))
	if !strings.Contains(skill, check) {
		t.Errorf("SKILL.md does not tell an agent to read the %q check", check)
	}
}
