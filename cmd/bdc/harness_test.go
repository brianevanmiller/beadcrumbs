package main

import (
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

func TestDetectHarness(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name     string
		declared string
		env      map[string]string
		path     string
		want     string
	}{
		{name: "override", declared: "delta", env: map[string]string{"AMP_ORB": "1"}, want: "delta"},
		{name: "amp orb", env: map[string]string{"AMP_ORB": "1"}, want: string(ledger.HarnessAmp)},
		{name: "amp thread", env: map[string]string{"AMP_THREAD_ID": "T-1"}, want: string(ledger.HarnessAmp)},
		{name: "conductor", env: map[string]string{"CONDUCTOR_IS_LOCAL": "1"}, want: string(ledger.HarnessConductor)},
		{name: "delta path", path: "/Users/x/.delta/worktrees/abc/repo", want: string(ledger.HarnessDelta)},
		{name: "claude", env: map[string]string{"CLAUDECODE": "1"}, want: string(ledger.HarnessClaudeCode)},
		{name: "codex", env: map[string]string{"CODEX_HOME": "/tmp"}, want: string(ledger.HarnessCodex)},
		{name: "opencode", env: map[string]string{"OPENCODE": "1"}, want: string(ledger.HarnessOpenCode)},
		{name: "none", want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectHarness(c.declared, env(c.env), c.path)
			if got != c.want {
				t.Fatalf("detectHarness = %q, want %q", got, c.want)
			}
		})
	}
}

func TestValidateHarnessRejectsUnknown(t *testing.T) {
	if err := ledger.ValidateHarness("not-a-harness"); err == nil {
		t.Fatal("an unknown harness was accepted")
	}
}
