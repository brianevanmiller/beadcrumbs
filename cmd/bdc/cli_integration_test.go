package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var testBinary string

func TestMain(m *testing.M) {
	// Build the binary once for all CLI integration tests
	tmpDir, err := os.MkdirTemp("", "bdc-build-*")
	if err != nil {
		panic("failed to create temp dir: " + err.Error())
	}
	defer os.RemoveAll(tmpDir)

	testBinary = filepath.Join(tmpDir, "bdc")
	cmd := exec.Command("go", "build", "-o", testBinary, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("build failed: " + string(out))
	}

	os.Exit(m.Run())
}

// bdcRun executes the bdc binary in the given directory with args.
func bdcRun(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(testBinary, args...)
	cmd.Dir = dir
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// setupTestEnv creates a temp dir with git init and bdc init --quiet.
func setupTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitCmd := exec.Command("git", "init")
	gitCmd.Dir = dir
	if out, err := gitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %s %v", out, err)
	}

	stdout, stderr, err := bdcRun(t, dir, "init", "--quiet")
	if err != nil {
		t.Fatalf("bdc init failed: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	return dir
}

// extractInsightID parses "Created insight: ins-xxxx" from stdout.
func extractInsightID(t *testing.T, stdout string) string {
	t.Helper()
	re := regexp.MustCompile(`ins-[a-f0-9]+`)
	match := re.FindString(stdout)
	if match == "" {
		t.Fatalf("could not extract insight ID from: %q", stdout)
	}
	return match
}

// extractThreadID parses "thr-xxxx" from stdout.
func extractThreadID(t *testing.T, stdout string) string {
	t.Helper()
	re := regexp.MustCompile(`thr-[a-f0-9]+`)
	match := re.FindString(stdout)
	if match == "" {
		t.Fatalf("could not extract thread ID from: %q", stdout)
	}
	return match
}

// ============================================================================
// Init & Export (PRs #1, #2, #10)
// ============================================================================

func TestCLI_InitCreatesDB(t *testing.T) {
	dir := t.TempDir()

	gitCmd := exec.Command("git", "init")
	gitCmd.Dir = dir
	gitCmd.Run()

	_, _, err := bdcRun(t, dir, "init", "--quiet")
	if err != nil {
		t.Fatalf("bdc init failed: %v", err)
	}

	// Verify files exist
	for _, name := range []string{"beadcrumbs.db", "insights.jsonl", "threads.jsonl", "deps.jsonl"} {
		path := filepath.Join(dir, ".beadcrumbs", name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected %s to exist after init", name)
		}
	}
}

func TestCLI_InitQuiet(t *testing.T) {
	dir := t.TempDir()

	gitCmd := exec.Command("git", "init")
	gitCmd.Dir = dir
	gitCmd.Run()

	stdout, _, err := bdcRun(t, dir, "init", "--quiet")
	if err != nil {
		t.Fatalf("bdc init --quiet failed: %v", err)
	}
	if stdout != "" {
		t.Errorf("expected empty stdout for --quiet, got: %q", stdout)
	}
}

func TestCLI_ExportCreatesJSONL(t *testing.T) {
	dir := setupTestEnv(t)

	// Create a thread and insight
	tOut, _, _ := bdcRun(t, dir, "thread", "new", "Export Test")
	thrID := extractThreadID(t, tOut)

	bdcRun(t, dir, "capture", "--thread", thrID, "--decision", "test export")

	// Export
	_, _, err := bdcRun(t, dir, "export")
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Verify JSONL files have content
	for _, name := range []string{"insights.jsonl", "threads.jsonl"} {
		data, err := os.ReadFile(filepath.Join(dir, ".beadcrumbs", name))
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("%s is empty after export", name)
		}
	}
}

func TestCLI_ExportQuiet(t *testing.T) {
	dir := setupTestEnv(t)

	stdout, _, err := bdcRun(t, dir, "export", "--quiet")
	if err != nil {
		t.Fatalf("export --quiet failed: %v", err)
	}
	if stdout != "" {
		t.Errorf("expected empty stdout for export --quiet, got: %q", stdout)
	}
}

// ============================================================================
// Origin Tracking (PR #9)
// ============================================================================

func TestCLI_CaptureWithOrigin(t *testing.T) {
	dir := setupTestEnv(t)

	// Create thread
	tOut, _, _ := bdcRun(t, dir, "thread", "new", "Origin Test")
	thrID := extractThreadID(t, tOut)

	// Capture with explicit origin
	cOut, _, err := bdcRun(t, dir, "capture", "--thread", thrID, "--origin", "claude:test123", "--decision", "test origin")
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}
	insID := extractInsightID(t, cOut)

	// Show should display origin
	sOut, _, _ := bdcRun(t, dir, "show", insID)
	if !strings.Contains(sOut, "claude:test123") {
		t.Errorf("show output missing origin 'claude:test123': %q", sOut)
	}
}

func TestCLI_OriginSetShowClear(t *testing.T) {
	dir := setupTestEnv(t)

	// Set
	stdout, _, err := bdcRun(t, dir, "origin", "set", "claude:sess_test")
	if err != nil {
		t.Fatalf("origin set failed: %v", err)
	}
	if !strings.Contains(stdout, "claude:sess_test") {
		t.Errorf("set output missing origin: %q", stdout)
	}

	// Show
	stdout, _, _ = bdcRun(t, dir, "origin", "show")
	if !strings.Contains(stdout, "claude:sess_test") {
		t.Errorf("show output missing origin: %q", stdout)
	}

	// Clear
	_, _, err = bdcRun(t, dir, "origin", "clear")
	if err != nil {
		t.Fatalf("origin clear failed: %v", err)
	}

	// Show after clear
	stdout, _, _ = bdcRun(t, dir, "origin", "show")
	if !strings.Contains(stdout, "(none)") {
		t.Errorf("expected '(none)' after clear, got: %q", stdout)
	}
}

func TestCLI_OriginsList(t *testing.T) {
	dir := setupTestEnv(t)

	// Create thread
	tOut, _, _ := bdcRun(t, dir, "thread", "new", "Origins List Test")
	thrID := extractThreadID(t, tOut)

	// Create insights with different origins
	bdcRun(t, dir, "capture", "--thread", thrID, "--origin", "claude:sess_a", "--decision", "from claude")
	bdcRun(t, dir, "capture", "--thread", thrID, "--origin", "cursor:ws_b", "--hypothesis", "from cursor")

	// List origins
	stdout, _, err := bdcRun(t, dir, "origins")
	if err != nil {
		t.Fatalf("origins failed: %v", err)
	}

	if !strings.Contains(stdout, "claude:sess_a") {
		t.Errorf("origins output missing 'claude:sess_a': %q", stdout)
	}
	if !strings.Contains(stdout, "cursor:ws_b") {
		t.Errorf("origins output missing 'cursor:ws_b': %q", stdout)
	}
}

func TestCLI_AutoSourceTypeFromAuthor(t *testing.T) {
	dir := setupTestEnv(t)

	tOut, _, _ := bdcRun(t, dir, "thread", "new", "Author Source Type Test")
	thrID := extractThreadID(t, tOut)

	// Capture with cc: author and an explicit origin — source_type should infer "ai-session"
	cOut, _, _ := bdcRun(t, dir, "capture", "--thread", thrID, "--author", "cc:opus-4.6", "--origin", "claude:test-session", "--decision", "AI decision")
	insID := extractInsightID(t, cOut)

	// Show should display origin with ai-session type
	sOut, _, _ := bdcRun(t, dir, "show", insID)
	if !strings.Contains(sOut, "ai-session") {
		t.Errorf("expected 'ai-session' in show output for cc: author with origin, got: %q", sOut)
	}
}

// ============================================================================
// Thread + External Ref Operations (PR #7)
// ============================================================================

func TestCLI_CaptureWithBeadThread(t *testing.T) {
	dir := setupTestEnv(t)

	stdout, _, err := bdcRun(t, dir, "capture", "--thread", "bd-abc1", "--hypothesis", "testing bead thread")
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	if !strings.Contains(stdout, "Created thread") {
		t.Errorf("expected 'Created thread' in output, got: %q", stdout)
	}
}

func TestCLI_CaptureReuseBeadThread(t *testing.T) {
	dir := setupTestEnv(t)

	// First capture creates thread
	bdcRun(t, dir, "capture", "--thread", "bd-abc1", "--hypothesis", "first")

	// Second capture reuses thread
	stdout, _, err := bdcRun(t, dir, "capture", "--thread", "bd-abc1", "--decision", "second")
	if err != nil {
		t.Fatalf("second capture failed: %v", err)
	}

	if strings.Contains(stdout, "Created thread") {
		t.Errorf("second capture should NOT create new thread, got: %q", stdout)
	}
}

func TestCLI_ThreadLinkExternalRef(t *testing.T) {
	dir := setupTestEnv(t)

	// Create a thread
	tOut, _, _ := bdcRun(t, dir, "thread", "new", "Link Test")
	thrID := extractThreadID(t, tOut)

	// Link to bead
	_, _, err := bdcRun(t, dir, "thread", "link", thrID, "bd-xyz1")
	if err != nil {
		t.Fatalf("thread link failed: %v", err)
	}

	// Show thread should mention the linked ref (format: "xyz1 (bead)")
	sOut, _, _ := bdcRun(t, dir, "thread", "show", thrID)
	if !strings.Contains(sOut, "xyz1") {
		t.Errorf("thread show should mention linked ref, got: %q", sOut)
	}
}

func TestCLI_TraceBeadID(t *testing.T) {
	dir := setupTestEnv(t)

	tOut, _, _ := bdcRun(t, dir, "thread", "new", "Trace Test")
	thrID := extractThreadID(t, tOut)

	// Create insight
	cOut, _, _ := bdcRun(t, dir, "capture", "--thread", thrID, "--decision", "Trace target insight")
	insID := extractInsightID(t, cOut)

	// Link insight to bead
	_, _, err := bdcRun(t, dir, "link", insID, "--spawns=bd-trace1")
	if err != nil {
		t.Fatalf("link --spawns failed: %v", err)
	}

	// Trace the bead
	stdout, _, err := bdcRun(t, dir, "trace", "bd-trace1")
	if err != nil {
		t.Fatalf("trace failed: %v", err)
	}

	if !strings.Contains(stdout, "Trace target insight") {
		t.Errorf("trace output should contain the insight content, got: %q", stdout)
	}
}

// ============================================================================
// Import Operations (PR #6)
// ============================================================================

func TestCLI_ImportCSV_DryRun(t *testing.T) {
	dir := setupTestEnv(t)

	// Create CSV file
	csvContent := "content,type\nTest CSV insight,hypothesis\nAnother insight,decision\n"
	csvPath := filepath.Join(dir, "test.csv")
	if err := os.WriteFile(csvPath, []byte(csvContent), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := bdcRun(t, dir, "import", "--dry-run", csvPath)
	if err != nil {
		t.Fatalf("import --dry-run failed: %v", err)
	}

	if !strings.Contains(stdout, "Test CSV insight") && !strings.Contains(stdout, "hypothesis") {
		t.Errorf("dry-run output should show preview, got: %q", stdout)
	}
}

func TestCLI_ImportJSONL_DryRun(t *testing.T) {
	dir := setupTestEnv(t)

	// Create JSONL file
	jsonlContent := `{"content":"JSONL test insight","type":"hypothesis"}
{"content":"Another JSONL insight","type":"decision"}
`
	jsonlPath := filepath.Join(dir, "test.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(jsonlContent), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := bdcRun(t, dir, "import", "--dry-run", jsonlPath)
	if err != nil {
		t.Fatalf("import --dry-run JSONL failed: %v", err)
	}

	if !strings.Contains(stdout, "JSONL test insight") {
		t.Errorf("dry-run output should show preview, got: %q", stdout)
	}
}

func TestCLI_ImportNonexistentFile(t *testing.T) {
	dir := setupTestEnv(t)

	_, _, err := bdcRun(t, dir, "import", "nonexistent.csv")
	if err == nil {
		t.Error("expected error importing non-existent file")
	}
}

func TestCLI_ImportAutoJSONL(t *testing.T) {
	dir := setupTestEnv(t)

	// First, create some data via the CLI and export it
	tOut, _, _ := bdcRun(t, dir, "thread", "new", "Auto-import Source Thread")
	thrID := extractThreadID(t, tOut)
	bdcRun(t, dir, "capture", "--thread", thrID, "--decision", "auto import content")
	bdcRun(t, dir, "export", "--quiet")

	// Read the exported JSONL to verify it exists
	threadsData, err := os.ReadFile(filepath.Join(dir, ".beadcrumbs", "threads.jsonl"))
	if err != nil {
		t.Fatalf("failed to read exported threads.jsonl: %v", err)
	}
	if len(threadsData) == 0 {
		t.Fatal("exported threads.jsonl is empty")
	}

	// Create a fresh environment and copy the JSONL files
	dir2 := t.TempDir()
	gitCmd := exec.Command("git", "init")
	gitCmd.Dir = dir2
	gitCmd.Run()
	bdcRun(t, dir2, "init", "--quiet")

	// Copy threads.jsonl from first env
	if err := os.WriteFile(filepath.Join(dir2, ".beadcrumbs", "threads.jsonl"), threadsData, 0644); err != nil {
		t.Fatal(err)
	}

	// Run auto import
	_, _, err = bdcRun(t, dir2, "import", "--auto", "--quiet")
	if err != nil {
		t.Fatalf("import --auto failed: %v", err)
	}

	// Verify thread was imported (must specify --status since default list requires it)
	stdout, _, _ := bdcRun(t, dir2, "thread", "list", "--status=active")
	if !strings.Contains(stdout, "Auto-import Source Thread") {
		t.Errorf("auto import should have imported thread, thread list: %q", stdout)
	}
}

// ============================================================================
// Prime (PRs #1, #8)
// ============================================================================

func TestCLI_PrimeOutput(t *testing.T) {
	dir := setupTestEnv(t)

	stdout, _, _ := bdcRun(t, dir, "prime")
	if !strings.Contains(stdout, "Beadcrumbs") {
		t.Errorf("prime output should contain 'Beadcrumbs', got: %q", stdout)
	}
}

func TestCLI_PrimeSilentWhenAbsent(t *testing.T) {
	dir := t.TempDir() // No .beadcrumbs/

	stdout, _, _ := bdcRun(t, dir, "prime")
	if stdout != "" {
		t.Errorf("prime should produce no stdout when .beadcrumbs/ absent, got: %q", stdout)
	}
}

func TestCLI_PrimeCustomOverride(t *testing.T) {
	dir := setupTestEnv(t)

	// Create custom PRIME.md
	primePath := filepath.Join(dir, ".beadcrumbs", "PRIME.md")
	if err := os.WriteFile(primePath, []byte("CUSTOM PRIME CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _, _ := bdcRun(t, dir, "prime")
	if !strings.Contains(stdout, "CUSTOM PRIME CONTENT") {
		t.Errorf("prime should use PRIME.md override, got: %q", stdout)
	}
	if strings.Contains(stdout, "Beadcrumbs Insight Tracker Active") {
		t.Error("prime should NOT show default content when PRIME.md exists")
	}
}

func TestCLI_PrimeExportFlag(t *testing.T) {
	dir := setupTestEnv(t)

	// Create custom PRIME.md
	primePath := filepath.Join(dir, ".beadcrumbs", "PRIME.md")
	if err := os.WriteFile(primePath, []byte("CUSTOM"), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _, _ := bdcRun(t, dir, "prime", "--export")
	if strings.Contains(stdout, "CUSTOM") {
		t.Error("prime --export should ignore PRIME.md")
	}
	if !strings.Contains(stdout, "Beadcrumbs") {
		t.Errorf("prime --export should show default content, got: %q", stdout)
	}
}

// ============================================================================
// Timeline + Filtering (PR #9)
// ============================================================================

func TestCLI_TimelineOriginFilter(t *testing.T) {
	dir := setupTestEnv(t)

	tOut, _, _ := bdcRun(t, dir, "thread", "new", "Timeline Filter Test")
	thrID := extractThreadID(t, tOut)

	// Two insights from different origins
	bdcRun(t, dir, "capture", "--thread", thrID, "--origin", "claude:sess_x", "--decision", "from claude")
	bdcRun(t, dir, "capture", "--thread", thrID, "--origin", "cursor:ws_y", "--hypothesis", "from cursor")

	// Filter by claude origin
	stdout, _, err := bdcRun(t, dir, "timeline", thrID, "--origin", "claude:sess_x")
	if err != nil {
		t.Fatalf("timeline with origin filter failed: %v", err)
	}

	if !strings.Contains(stdout, "from claude") {
		t.Errorf("timeline should contain 'from claude', got: %q", stdout)
	}
	if strings.Contains(stdout, "from cursor") {
		t.Errorf("timeline should NOT contain 'from cursor' with claude filter, got: %q", stdout)
	}
}

// ============================================================================
// Slack Error Paths (PR #6)
// ============================================================================

func TestCLI_SlackFetchNoAuth(t *testing.T) {
	dir := setupTestEnv(t)

	_, stderr, err := bdcRun(t, dir, "slack", "fetch", "general")
	if err == nil {
		t.Error("expected error for slack fetch without auth")
	}
	combined := stderr
	if !strings.Contains(combined, "token") && !strings.Contains(combined, "auth") && !strings.Contains(combined, "configured") {
		t.Errorf("expected helpful auth error, got: %q", combined)
	}
}

func TestCLI_ImportCSV_CustomMapping(t *testing.T) {
	dir := setupTestEnv(t)

	// Create CSV with custom column names
	csvContent := "body,category,who\nMy insight text,hypothesis,brian\n"
	csvPath := filepath.Join(dir, "custom.csv")
	if err := os.WriteFile(csvPath, []byte(csvContent), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := bdcRun(t, dir, "import", "--dry-run", "--csv", "--map-content=body", "--map-type=category", "--map-author=who", csvPath)
	if err != nil {
		t.Fatalf("import with custom mapping failed: %v", err)
	}

	if !strings.Contains(stdout, "My insight text") {
		t.Errorf("import should show mapped content, got: %q", stdout)
	}
}

func TestCLI_ShowInsightOrigin(t *testing.T) {
	dir := setupTestEnv(t)

	tOut, _, _ := bdcRun(t, dir, "thread", "new", "Show Origin Test")
	thrID := extractThreadID(t, tOut)

	cOut, _, _ := bdcRun(t, dir, "capture", "--thread", thrID, "--origin", "notion:page-abc", "--feedback", "from notion")
	insID := extractInsightID(t, cOut)

	stdout, _, _ := bdcRun(t, dir, "show", insID)
	if !strings.Contains(stdout, "notion:page-abc") {
		t.Errorf("show should display origin, got: %q", stdout)
	}
}
