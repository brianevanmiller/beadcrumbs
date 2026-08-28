package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The skill is the product's portable surface: it is the only artifact a user
// installs, and everything it tells an agent to do has to work in a repository
// that has nothing else. So this file installs it the way a user does — the
// real installer, into a clean fixture — and then drives the exact command
// sequence the installed text documents.
//
// The installer needs npx and the npm registry, so this test skips with a named
// reason when it cannot run. TestFullWorkflowInFixtureRepo covers the same
// sequence with no installer and no network, which is why that gate exists
// separately rather than being folded into this one.

// installerVersion is pinned rather than floating on latest: an unpinned
// installer makes this a live dependency test that can fail on somebody else's
// release.
const installerVersion = "skills@1.5.23"

// TestSkillInstallAndFullWorkflow: `npx skills add`, then capture → review →
// harvest → insight → propose → record → context/handoff, driven only by
// commands the installed SKILL.md documents.
func TestSkillInstallAndFullWorkflow(t *testing.T) {
	skillSource := repoPath(t, "skills", "beadcrumbs")
	requireInstaller(t)

	s := newSession(t, nil)
	installSkill(t, s.dir, skillSource)

	// The canonical install location, which is also every non-Claude harness's
	// project skills directory. Nothing was installed globally: the assertion
	// is that the skill is inside the fixture.
	installed := filepath.Join(s.dir, ".agents", "skills", "beadcrumbs")
	body := readFile(t, filepath.Join(installed, "SKILL.md"))
	for _, ref := range []string{"workflow.md", "classes.md", "destinations.md"} {
		if _, err := os.Stat(filepath.Join(installed, "references", ref)); err != nil {
			t.Fatalf("the installer did not carry references/%s: %v", ref, err)
		}
	}

	// Every command the run below uses has to be one the installed text names.
	// A workflow that works but is not documented is not the contract.
	documented := body + readFile(t, filepath.Join(installed, "references", "workflow.md"))
	for _, cmd := range []string{
		"bdc version", "bdc doctor", "bdc init", "bdc capture", "bdc crumb list",
		"bdc crumb review", "bdc harvest", "bdc insight show", "bdc validate",
		"bdc authority", "bdc promote propose", "bdc promote record",
		"bdc context", "bdc handoff", "bdc prime",
	} {
		if !strings.Contains(documented, cmd) {
			t.Errorf("the installed skill never mentions `%s`, which the workflow needs", cmd)
		}
	}

	// From here it is the skill's own sequence, in its own order.
	s.bdc("version")
	s.bdc("init")
	if ok := s.field(s.bdc("doctor"), "ok"); ok != "true" {
		t.Fatal("doctor reports the freshly initialised ledger as unhealthy")
	}

	first := s.bdc("capture",
		"An installed skill is only worth what its commands actually do.",
		"--confidence", "0.7", "--ref", "docs:docs/guides/hooks.md@source")
	s.bind("crumbA", first, "crumb.id")
	second := s.bdc("capture",
		"The portable contract is the commands and the JSON, not any harness hook.",
		"--confidence", "0.6")
	s.bind("crumbB", second, "crumb.id")

	candidates := s.bdc("crumb", "list", "--state", "candidate")
	if got := s.field(candidates, "total"); got != "2" {
		t.Fatalf("crumb list reports %s candidates, want 2", got)
	}

	s.bdc("crumb", "review", "{crumbA}", "{crumbB}",
		"--state", "accepted", "--rationale", "both are load-bearing for the skill")

	harvest := s.bdc("harvest", "--crumb", "{crumbA}", "--crumb", "{crumbB}",
		"--class", "decision", "--confidence", "0.8",
		"--title", "The skill's contract is explicit bdc commands",
		"--content", "Every harness integration shells out to the same documented commands; "+
			"nothing in the workflow depends on a hook.")
	insight := s.bind("insight", harvest, "insight.id")
	s.bind("revision", harvest, "revision.id")

	if got := s.field(s.bdc("insight", "show", "{insight}", "--lineage"), "insight.id"); got != insight {
		t.Fatalf("insight show returned %s, want %s", got, insight)
	}

	s.bdc("validate", "{revision}", "--verdict", "supported",
		"--rationale", "the installed skill ran end to end")
	s.bdc("authority", "{revision}", "--level", "default",
		"--rationale", "this is how every harness integrates")

	proposal := s.bdc("promote", "propose", "--insight", "{insight}",
		"--class", "decision", "--destination", "docs:docs/adr/",
		"--capability", "stable-anchor",
		"--content", "The skill's contract is explicit bdc commands plus stable JSON.")
	s.bind("proposal", proposal, "proposal.id")

	receipt := s.bdc("promote", "record", "{proposal}",
		"--locator", "docs/adr/0008-skill-contract.md",
		"--anchor", "1a2b3c4d5e6f70819293a4b5c6d7e8f9", "--verified")
	if s.field(receipt, "durable") != "true" {
		t.Error("a destination declaring stable-anchor produced a receipt that is not durable")
	}

	if prime := s.bdc("prime"); !strings.Contains(string(prime.Data), insight) {
		t.Errorf("prime does not list the working default the workflow established:\n%s", prime.Data)
	}
	if context := s.bdc("context"); !strings.Contains(string(context.Data), insight) {
		t.Errorf("context does not mention the Insight:\n%s", context.Data)
	}
	if got := s.field(s.bdc("handoff"), "state.insights"); got != "1" {
		t.Errorf("handoff reports %s Insights, want 1", got)
	}

	// The skill's own statement about automatic harvesting: off until this
	// repository says otherwise.
	if !strings.Contains(body, "Off by default") {
		t.Error("the skill does not tell an agent that automatic harvesting is opt-in")
	}
}

// TestSkillFrontmatterUsesOnlySpecFields is the portability gate, and it needs
// no installer: Claude Code accepts its own extra frontmatter keys, and every
// other harness rejects the file outright when it finds one.
func TestSkillFrontmatterUsesOnlySpecFields(t *testing.T) {
	body := readFile(t, repoPath(t, "skills", "beadcrumbs", "SKILL.md"))
	if !strings.HasPrefix(body, "---\n") {
		t.Fatal("SKILL.md does not open with YAML frontmatter")
	}
	end := strings.Index(body[4:], "\n---\n")
	if end < 0 {
		t.Fatal("SKILL.md frontmatter is not terminated")
	}

	spec := map[string]bool{
		"name": true, "description": true, "license": true,
		"compatibility": true, "metadata": true, "allowed-tools": true,
	}
	seen := map[string]string{}
	for _, line := range strings.Split(body[4:4+end], "\n") {
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // a continuation of the previous value
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("frontmatter line is not a key: %q", line)
		}
		if !spec[key] {
			t.Errorf("frontmatter key %q is not one of the six Agent Skills spec fields; "+
				"harnesses other than Claude Code fail validation on it", key)
		}
		seen[key] = strings.TrimSpace(value)
	}

	// name must equal the directory name, or the spec's own loader rejects it.
	if seen["name"] != "beadcrumbs" {
		t.Errorf("name is %q, but the skill directory is beadcrumbs", seen["name"])
	}
	if seen["description"] == "" {
		t.Error("description is required and says when to use the skill")
	}
	if !strings.Contains(seen["compatibility"], "bdc") {
		t.Errorf("compatibility does not state the bdc prerequisite: %q", seen["compatibility"])
	}
}

func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

// requireInstaller skips rather than fails when the installer cannot run. The
// distinction matters: a missing npx says nothing about whether the skill is
// correct, and failing on it would make the suite depend on a network.
//
// BDC_REQUIRE_INSTALLER=1 turns every skip here into a failure, which is how a
// release run pins the one gate that drives the installed text — otherwise a
// network-restricted runner loses it silently.
func requireInstaller(t *testing.T) {
	t.Helper()
	give := func(format string, args ...any) {
		t.Helper()
		if os.Getenv("BDC_REQUIRE_INSTALLER") == "1" {
			t.Fatalf(format, args...)
		}
		t.Skipf(format, args...)
	}
	if _, err := exec.LookPath("npx"); err != nil {
		give("npx is not installed, so the skills installer cannot be exercised: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npx", "--yes", installerVersion, "--version")
	if out, err := cmd.CombinedOutput(); err != nil {
		give("the %s installer could not be fetched: %v\n%s", installerVersion, err, out)
	}
}

// installSkill runs the real installer against the local skill directory. It is
// deliberately project-scoped: nothing is installed globally, which is also what
// the release gate requires of a verification run.
func installSkill(t *testing.T, project, source string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// -y is not optional: without it the installer may prompt and a CI job
	// hangs to its timeout.
	cmd := exec.CommandContext(ctx, "npx", "--yes", installerVersion, "add", source, "-y")
	cmd.Dir = project
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`npx %s add %s -y` failed: %v\n%s", installerVersion, source, err, out)
	}
}
