package dolt

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestSIGKILLMidTransactionLeavesNoPartialWrite is the release gate for
// "process interruption cannot leave a partially applied domain operation". A
// harvest writes a Harvest, its Crumbs, an Insight, and a revision in one
// transaction; a machine that dies halfway through must leave none of it, not
// some of it.
//
// SIGKILL is the honest interruption to test: it is the one signal no deferred
// Close can run for, so what survives is whatever the engine already made
// durable and nothing the process would have tidied up.
func TestSIGKILLMidTransactionLeavesNoPartialWrite(t *testing.T) {
	loc := initLedger(t, fixtureRepo(t), false)

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), killEnv+"="+loc.Dir)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("child stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the writer: %v", err)
	}

	// The child reports once its rows are written and before it commits, which
	// is the only window where a partial write could become visible.
	wrote := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		wrote <- strings.TrimSpace(line)
	}()
	select {
	case line := <-wrote:
		if line != "WROTE" {
			t.Fatalf("the writer said %q, not WROTE", line)
		}
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the writer never reached its uncommitted rows")
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing the writer: %v", err)
	}
	_ = cmd.Wait()

	// The ledger must still open — a killed writer holds no lock — and hold
	// none of the killed transaction's rows.
	store := openLedger(t, loc, Config{Command: "check-killed", MaxOpenWait: 30 * time.Second})
	if n := countRows(t, store.DB(), "SELECT COUNT(*) FROM crumbs"); n != 0 {
		t.Fatalf("%d row(s) from a transaction that never committed", n)
	}
	report, err := store.Diagnose(t.Context())
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if !report.OK {
		t.Fatalf("the ledger is not usable after a killed writer: %+v", report.Checks)
	}
}
