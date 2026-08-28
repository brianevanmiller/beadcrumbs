package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/store/dolt"
)

// holdEnv turns this test binary into a process that opens a ledger and holds
// the directory lock. A busy ledger is not reachable in-process — a second Open
// panics by design — so the only honest way to test the hook's behaviour behind
// one is a second process.
const holdEnv = "BDC_TEST_HOLD_LEDGER"

func TestMain(m *testing.M) {
	if dir := os.Getenv(holdEnv); dir != "" {
		os.Exit(runHolder(dir))
	}
	os.Exit(m.Run())
}

func runHolder(dir string) int {
	store, err := dolt.Open(context.Background(), dolt.Location{Dir: dir, Stealth: true}, dolt.Config{
		// The holder races the parent's just-closed engine for the same
		// directory lock, so its budget matches the parent's readiness timeout
		// below. A short one turns a loaded machine into a failure whose subject
		// is the holder's patience rather than the hook's behaviour.
		MaxOpenWait: 30 * time.Second,
		MaxOpenHold: time.Hour, // holding it is the whole job
		Command:     "test-holder",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "holder:", err)
		return 1
	}
	defer store.Close()
	fmt.Println("READY")
	_, _ = io.Copy(io.Discard, os.Stdin) // released when the parent closes stdin
	return 0
}

// TestHookExitsZeroWhenLedgerBusy is the rule that makes hooks safe to install:
// a `pre-push` that fails a push because another `bdc` held the engine is worse
// than no hook at all. Exit 0 is necessary but not sufficient — a hook that
// swallowed the miss silently would lose a harvest with nobody the wiser — so
// the skipped action has to be named on stderr and in the envelope.
func TestHookExitsZeroWhenLedgerBusy(t *testing.T) {
	// Production waits a minute for the lock. This test asserts the outcome of
	// exhausting that budget, not its size.
	restore := hookMaxOpenWait
	hookMaxOpenWait = 200 * time.Millisecond
	t.Cleanup(func() { hookMaxOpenWait = restore })

	h := newHookFixture(t)
	h.run(t, "init")
	h.run(t, "capture", "A Crumb the hook would otherwise harvest.", "--confidence", "0.5")
	h.holdLedger(t)

	out := h.run(t, "hooks", "run", "pre-push")
	if out.exit != exitOK {
		t.Fatalf("a busy ledger failed the hook with exit %d; it must never fail a git operation\n%s",
			out.exit, out.stderr)
	}
	data := h.data(t, out)
	if data["action"] != hookActionSkipped {
		t.Errorf("action is %q, want %q", data["action"], hookActionSkipped)
	}
	if !strings.Contains(out.stderr, "hook_skipped") {
		t.Errorf("the skipped action was not named on stderr: %q", out.stderr)
	}
	if result, _ := data["result"].(string); !strings.Contains(result, "locked") &&
		!strings.Contains(result, "busy") {
		t.Errorf("the result does not say the ledger was busy: %q", result)
	}
}

// TestReportOnlyTriggerDeclinesWithoutWaitingOutTheHarvestBudget is the other
// half of the busy-ledger rule. The minute a hook waits for the lock is there so
// a harvest is not lost (plan §4.2); `stop` writes nothing under any
// configuration, so waiting it out buys a minute of dead time at every agent
// turn end and loses nothing by declining at once.
func TestReportOnlyTriggerDeclinesWithoutWaitingOutTheHarvestBudget(t *testing.T) {
	restoreMax, restoreReport := hookMaxOpenWait, hookReportWait
	hookMaxOpenWait, hookReportWait = 4*time.Second, 100*time.Millisecond
	t.Cleanup(func() { hookMaxOpenWait, hookReportWait = restoreMax, restoreReport })

	h := newHookFixture(t)
	h.run(t, "init")
	h.run(t, "capture", "A Crumb a harvesting trigger would wait for.", "--confidence", "0.5")
	h.holdLedger(t)

	start := time.Now()
	stop := h.run(t, "hooks", "run", "stop")
	reporting := time.Since(start)
	if stop.exit != exitOK {
		t.Fatalf("the stop trigger exited %d against a busy ledger\n%s", stop.exit, stop.stderr)
	}
	if reporting > time.Second {
		t.Errorf("a report-only trigger waited %s against a %s report budget", reporting, hookReportWait)
	}

	// The narrowing is per trigger: one that may harvest still spends the full
	// budget, because giving up early is what loses the harvest.
	start = time.Now()
	if push := h.run(t, "hooks", "run", "pre-push"); push.exit != exitOK {
		t.Fatalf("the pre-push trigger exited %d against a busy ledger\n%s", push.exit, push.stderr)
	}
	// Backoff stops once the next interval would exceed the budget, so the
	// assertion is that the two triggers wait on different scales, not that
	// either lands on an exact duration.
	if harvesting := time.Since(start); harvesting <= time.Second {
		t.Errorf("a harvesting trigger gave up after %s, on the report-only scale rather than its %s budget",
			harvesting, hookMaxOpenWait)
	}
}

// The other two runtime conditions a hook meets. Neither is an error the git
// operation should ever see.
func TestHookExitsZeroWithNoLedgerAndWithNothingOutstanding(t *testing.T) {
	h := newHookFixture(t)

	before := h.run(t, "hooks", "run", "post-merge")
	if before.exit != exitOK {
		t.Fatalf("an uninitialised repository failed the hook with exit %d", before.exit)
	}
	if got := h.data(t, before)["action"]; got != hookActionSkipped {
		t.Errorf("action with no ledger is %q, want %q", got, hookActionSkipped)
	}

	h.run(t, "init")
	empty := h.run(t, "hooks", "run", "post-merge")
	if empty.exit != exitOK {
		t.Fatalf("an empty ledger failed the hook with exit %d", empty.exit)
	}
	if got := h.data(t, empty)["result"]; got != "no unharvested Crumbs" {
		t.Errorf("result on an empty ledger is %q", got)
	}
}

// Automatic harvesting is opt-in per repository and off by default, so the
// default operating mode is manual: the trigger reports and writes nothing.
// Opting in is what makes it write, and opting out returns it to reporting
// without touching what was already persisted.
func TestAutomaticHarvestingIsOptInPerRepository(t *testing.T) {
	h := newHookFixture(t)
	h.run(t, "init")
	h.run(t, "capture", "Automatic harvesting is off until this repository says otherwise.",
		"--confidence", "0.6")

	byDefault := h.data(t, h.run(t, "hooks", "run", "pre-push"))
	if byDefault["action"] != hookActionRemind {
		t.Fatalf("a fresh repository harvested automatically: action %q", byDefault["action"])
	}
	if h.harvestCount(t) != 0 {
		t.Fatal("a repository that never opted in recorded a Harvest")
	}

	installed := h.data(t, h.run(t, "hooks", "install", "--auto-harvest"))
	if installed["auto_harvest"] != true {
		t.Fatalf("--auto-harvest reported auto_harvest=%v", installed["auto_harvest"])
	}
	optedIn := h.data(t, h.run(t, "hooks", "run", "pre-push"))
	if optedIn["action"] != hookActionHarvest {
		t.Fatalf("an opted-in repository did not harvest: action %q, result %q",
			optedIn["action"], optedIn["result"])
	}
	if h.harvestCount(t) != 1 {
		t.Fatalf("the automatic harvest recorded %d Harvests, want 1", h.harvestCount(t))
	}

	// Opting back out reverts the policy and nothing else: the Harvest and the
	// Crumb it weighed are still there.
	out := h.data(t, h.run(t, "hooks", "uninstall"))
	if out["auto_harvest"] != false {
		t.Fatalf("uninstall left auto_harvest=%v", out["auto_harvest"])
	}
	if h.harvestCount(t) != 1 {
		t.Error("opting out changed what was already persisted")
	}
	if got := h.data(t, h.run(t, "hooks", "run", "pre-push"))["action"]; got != hookActionRemind {
		t.Errorf("after opting out the trigger did %q, want %q", got, hookActionRemind)
	}
}

// `stop` is a turn ending, not a durable completion point. It reports even for
// a repository that opted in, because harvesting mid-conversation records a
// judgement nobody made.
func TestStopTriggerNeverWrites(t *testing.T) {
	h := newHookFixture(t)
	h.run(t, "init")
	h.run(t, "capture", "A turn ending is not a durable completion point.", "--confidence", "0.5")
	h.run(t, "hooks", "install", "--auto-harvest")

	if got := h.data(t, h.run(t, "hooks", "run", "stop"))["action"]; got != hookActionRemind {
		t.Fatalf("stop did %q, want %q", got, hookActionRemind)
	}
	if h.harvestCount(t) != 0 {
		t.Fatal("the stop trigger recorded a Harvest")
	}
}

// `bd hooks install` may already own pre-push. Clobbering it would silently
// disable another tool, so the existing hook is preserved, chained, and its exit
// status is what the git operation sees.
func TestInstallPreservesAnExistingHookAndUninstallRestoresIt(t *testing.T) {
	h := newHookFixture(t)
	h.run(t, "init")

	hooksDir := filepath.Join(h.dir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("creating the hooks directory: %v", err)
	}
	existing := filepath.Join(hooksDir, "pre-push")
	const theirs = "#!/bin/sh\n# somebody else's hook\nexit 0\n"
	if err := os.WriteFile(existing, []byte(theirs), 0o755); err != nil {
		t.Fatalf("seeding an existing hook: %v", err)
	}

	installed := h.data(t, h.run(t, "hooks", "install"))
	if chained, _ := installed["chained"].([]any); len(chained) != 1 || chained[0] != "pre-push" {
		t.Fatalf("the pre-existing hook was not reported as chained: %v", installed["chained"])
	}
	if installed["auto_harvest"] != false {
		t.Error("installing the shims opted the repository into automatic harvesting")
	}
	saved, err := os.ReadFile(existing + priorSuffix)
	if err != nil || string(saved) != theirs {
		t.Fatalf("the pre-existing hook was not preserved verbatim: %v", err)
	}
	shim, err := os.ReadFile(existing)
	if err != nil || !strings.Contains(string(shim), hookMarker) {
		t.Fatalf("the shim was not written: %v", err)
	}
	if !strings.Contains(string(shim), "exit $status") {
		t.Error("the shim does not exit with the chained hook's status")
	}

	// Installing twice is not a second chaining, and does not bury the shim
	// under itself.
	again := h.data(t, h.run(t, "hooks", "install"))
	if chained, _ := again["chained"].([]any); len(chained) != 0 {
		t.Errorf("a second install chained something: %v", again["chained"])
	}

	h.run(t, "hooks", "uninstall")
	restored, err := os.ReadFile(existing)
	if err != nil || string(restored) != theirs {
		t.Fatalf("uninstall did not restore the hook it replaced: %v", err)
	}
	if _, err := os.Stat(existing + priorSuffix); !os.IsNotExist(err) {
		t.Error("uninstall left the saved copy behind")
	}
}

// A hook bdc did not write is never removed, whatever was asked for.
func TestUninstallLeavesAForeignHookAlone(t *testing.T) {
	h := newHookFixture(t)
	h.run(t, "init")

	hooksDir := filepath.Join(h.dir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("creating the hooks directory: %v", err)
	}
	path := filepath.Join(hooksDir, "post-merge")
	const theirs = "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(path, []byte(theirs), 0o755); err != nil {
		t.Fatalf("seeding a foreign hook: %v", err)
	}

	out := h.run(t, "hooks", "uninstall", "--force")
	if !strings.Contains(out.stderr, "hook_not_ours") {
		t.Errorf("a foreign hook was not reported: %q", out.stderr)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != theirs {
		t.Fatalf("--force removed a hook bdc did not write: %v", err)
	}
}

// A trigger name `hooks run` does not recognise is a configuration mistake in
// whoever called it, so it fails as a usage error rather than declining
// silently. The hooks envelope shapes themselves are pinned by the golden table.
func TestUnknownHookTriggerIsAUsageError(t *testing.T) {
	h := newHookFixture(t)
	h.run(t, "init")

	unknown := h.run(t, "hooks", "run", "not-a-trigger")
	if unknown.exit != exitUsage {
		t.Errorf("an unknown trigger exited %d, want %d", unknown.exit, exitUsage)
	}
}

// hookFixture is one temporary Git repository the commands run against.
type hookFixture struct{ dir string }

func newHookFixture(t *testing.T) *hookFixture {
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
	return &hookFixture{dir: repo}
}

func (h *hookFixture) run(t *testing.T, args ...string) invocation {
	t.Helper()
	full := append([]string{"-C", h.dir, "--json", "--actor", "tester", "--actor-kind", "human"}, args...)
	var stdout, stderr bytes.Buffer
	exit := run(&stdout, &stderr, full)
	return invocation{stdout: stdout.String(), stderr: stderr.String(), exit: exit}
}

func (h *hookFixture) data(t *testing.T, out invocation) map[string]any {
	t.Helper()
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &env); err != nil {
		t.Fatalf("not one envelope on stdout: %v\n%s\n%s", err, out.stdout, out.stderr)
	}
	if !env.OK {
		t.Fatalf("the command failed: %s\n%s", out.stdout, out.stderr)
	}
	return env.Data
}

// harvestCount is how many Harvests the ledger holds, read through `handoff`,
// which is the documented command that reports the ledger's record counts.
func (h *hookFixture) harvestCount(t *testing.T) int {
	t.Helper()
	state, _ := h.data(t, h.run(t, "handoff"))["state"].(map[string]any)
	count, ok := state["harvests"].(float64)
	if !ok {
		t.Fatalf("handoff reported no harvest count: %v", state)
	}
	return int(count)
}

// holdLedger starts a second process holding the ledger's lock, released at
// test cleanup.
func (h *hookFixture) holdLedger(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	dir := filepath.Join(h.dir, ".git", "beadcrumbs")
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}

	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), holdEnv+"="+dir)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("holder stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("holder stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the holder: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- strings.TrimSpace(line)
	}()
	select {
	case line := <-ready:
		if line != "READY" {
			t.Fatalf("the holder did not take the lock, said %q", line)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the holder never reported READY")
	}
}
