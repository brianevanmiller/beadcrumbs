package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The JSON surface is a promise, so it is tested as one. Every invocation in
// the CLI contract runs against a real ledger, its envelope is canonicalised —
// ids, timestamps, hashes, sizes, and paths replaced by stable placeholders —
// and compared byte for byte with a checked-in golden file. A field that
// appears, disappears, or changes meaning fails the build rather than a
// caller's script.
//
// Regenerate with `go test ./cmd/bdc -run TestGoldenEnvelope -update` and read
// the diff: every line of it is a change to a published contract.

var update = flag.Bool("update", false, "rewrite the golden envelopes in testdata/golden")

// step is one invocation of the CLI contract. The table is ordered and
// stateful: each step runs against the ledger the steps before it built, which
// is what makes `promote record` reachable at all.
type step struct {
	name string   // subtest and golden file name
	args []string // "{var}" is replaced with a value bound by an earlier step

	exit int

	// dataKeys is the exact top-level shape of `data` from §3.3 of the plan.
	// nil means the step fails and data is null.
	dataKeys []string

	// errCode is the expected error.code for a failing step.
	errCode string

	// binds records values out of this step's data for later steps to use.
	binds map[string]string // path -> variable name

	// facts are paths whose values the human rendering must also carry. They
	// are what "human and JSON agree" is checked against in output_test.go.
	facts []string

	// readOnly marks a step that may safely run a second time against the same
	// ledger, which is what lets the human rendering be compared against the
	// very same records rather than an equivalent set.
	readOnly bool
}

const (
	insightContent = "Every bdc invocation opens one short-lived engine, performs one bounded " +
		"transaction, and closes it. Reads state the freshness of anything cached."
	revisedContent = "Every bdc invocation opens one short-lived engine and closes it before it " +
		"returns. Reads state the freshness of anything cached, and a handoff never refreshes."
)

// awsExampleKey is the access key id from AWS's own documentation. It matches
// the aws-access-key-id rule, and it is public by construction — a redaction
// test that needed a real secret would be a redaction test that leaked one.
const awsExampleKey = "AKIAIOSFODNN7EXAMPLE"

func contractSteps() []step {
	return []step{
		{
			name: "crumb.list.no_ledger", args: []string{"crumb", "list"},
			exit: exitNoLedger, errCode: "no_ledger_uninitialized", readOnly: true,
		},
		{
			name: "version", args: []string{"version"}, readOnly: true,
			dataKeys: []string{"version", "schema_version", "dolt_driver", "go", "platform"},
			facts:    []string{"version", "platform"},
		},
		{
			name: "init", args: []string{"init"},
			dataKeys: []string{"path", "stealth", "schema_version", "created"},
			facts:    []string{"path", "schema_version"},
		},
		{
			// The registry ships with the three curated questions the skill and
			// the answer paths name. A seed that failed to parse would make
			// every enqueue a not-found: a silent feature outage.
			name: "prompts.list", args: []string{"prompts", "list"},
			dataKeys: []string{"prompts"}, readOnly: true,
			facts: []string{"prompts.0.prompt_key", "prompts.0.answer_kind"},
		},
		{
			// Nothing is pending and that is success, not an error. The skill
			// runs this after every prime.
			name: "ask.deliver.human.empty", args: []string{"ask", "deliver", "--respondent", "human"},
			dataKeys: []string{"questions"},
			facts:    []string{},
		},
		{
			name: "capture", args: []string{"capture",
				"The engine holds an exclusive directory lock for the life of a command.",
				"--confidence", "0.7", "--ref", "docs:docs/design.md@source"},
			dataKeys: []string{"crumb"},
			binds:    map[string]string{"crumb.id": "crumbA"},
			facts:    []string{"crumb.id", "crumb.review_state"},
		},
		{
			name: "capture.second", args: []string{"capture",
				"A cached tracker label was mistaken for live state, so every read states its freshness.",
				"--confidence", "0.6"},
			dataKeys: []string{"crumb"},
			binds:    map[string]string{"crumb.id": "crumbB"},
			facts:    []string{"crumb.id"},
		},
		{
			name: "capture.prunable", args: []string{"capture",
				"An aside nobody followed up on.", "--confidence", "0.2"},
			dataKeys: []string{"crumb"},
			binds:    map[string]string{"crumb.id": "crumbC"},
			facts:    []string{"crumb.id"},
		},
		{
			// The warning is the point: the caller has to see that what it
			// asked to store is not what was stored.
			name: "capture.redacted", args: []string{"capture",
				"The failing run logged " + awsExampleKey + " into the build output.",
				"--confidence", "0.4"},
			dataKeys: []string{"crumb"},
			facts:    []string{"crumb.id"},
		},
		{
			name: "crumb.list", args: []string{"crumb", "list", "--state", "candidate"},
			dataKeys: []string{"crumbs", "total"}, readOnly: true,
			facts: []string{"crumbs.0.id", "total"},
		},
		{
			name: "crumb.show", args: []string{"crumb", "show", "{crumbA}", "--events"},
			dataKeys: []string{"crumb", "review_events", "references", "harvests", "insights"},
			readOnly: true,
			facts:    []string{"crumb.id", "references.0.locator"},
		},
		{
			name: "crumb.review", args: []string{"crumb", "review", "{crumbA}", "{crumbB}",
				"--state", "accepted", "--rationale", "both hold up against the measured behaviour"},
			dataKeys: []string{"crumbs", "events"},
			facts:    []string{"events.0.crumb_id"},
		},
		{
			name: "harvest", args: []string{"harvest", "--crumb", "{crumbA}", "--crumb", "{crumbB}",
				"--class", "decision", "--confidence", "0.8",
				"--title", "One engine, one transaction, one close",
				"--content", insightContent},
			dataKeys: []string{"harvest", "insight", "revision", "crumbs_captured", "redaction"},
			binds:    map[string]string{"insight.id": "insight", "revision.id": "rev1"},
			facts:    []string{"harvest.id", "insight.id", "revision.title"},
		},
		{
			name: "insight.list", args: []string{"insight", "list"},
			dataKeys: []string{"insights", "total"}, readOnly: true,
			facts: []string{"insights.0.id", "insights.0.title", "total"},
		},
		{
			name: "insight.show", args: []string{"insight", "show", "{insight}", "--lineage"},
			dataKeys: []string{"insight", "revision", "revisions", "crumbs", "references",
				"validations", "authorities", "proposals", "lineage"},
			readOnly: true,
			facts:    []string{"insight.id", "revision.title"},
		},
		{
			name: "insight.revise", args: []string{"insight", "revise", "{insight}",
				"--content", revisedContent,
				"--rationale", "the close now happens before the command returns"},
			dataKeys: []string{"insight", "revision"},
			binds:    map[string]string{"revision.id": "rev2"},
			facts:    []string{"insight.id", "revision.rationale"},
		},
		{
			name: "validate", args: []string{"validate", "{rev2}",
				"--verdict", "supported", "--rationale", "reproduced against dolt 2.3.1"},
			dataKeys: []string{"validation", "effective_verdict"},
			facts:    []string{"validation.target.id", "validation.verdict", "effective_verdict"},
		},
		{
			name: "authority", args: []string{"authority", "{rev2}",
				"--level", "mandatory", "--rationale", "this is how the store is used everywhere"},
			dataKeys: []string{"authority", "effective_level"},
			facts:    []string{"authority.target.id", "authority.level", "effective_level"},
		},
		{
			name: "reference.add", args: []string{"reference", "add", "{rev2}",
				"--kind", "beads", "--locator", "bdc-7ah.20", "--relation", "spawned-work"},
			dataKeys: []string{"reference", "link"},
			facts:    []string{"reference.kind", "reference.locator", "link.relation"},
		},
		{
			name: "reference.list", args: []string{"reference", "list", "--target", "{rev2}"},
			dataKeys: []string{"references"}, readOnly: true,
			facts: []string{"references.0.id", "references.0.kind"},
		},
		{
			name: "promote.propose", args: []string{"promote", "propose", "--insight", "{insight}",
				"--class", "decision", "--destination", "docs:docs/decisions/0001-one-engine.md",
				"--capability", "stable-anchor", "--confidence", "0.8",
				"--content", "One engine, one transaction, one close."},
			dataKeys: []string{"proposal", "created", "content_hash", "authority_required"},
			binds:    map[string]string{"proposal.id": "proposalA"},
			facts:    []string{"proposal.id", "proposal.dest_locator"},
		},
		{
			// Not an error: the same content to the same destination is the
			// same proposal, and created=false is the answer.
			name: "promote.propose.idempotent", args: []string{"promote", "propose",
				"--insight", "{insight}", "--class", "decision",
				"--destination", "docs:docs/decisions/0001-one-engine.md",
				"--capability", "stable-anchor", "--confidence", "0.8",
				"--content", "One engine, one transaction, one close."},
			dataKeys: []string{"proposal", "created", "content_hash", "authority_required"},
			facts:    []string{"proposal.id"},
		},
		{
			// The proposal is recorded even though the write is refused, and
			// the envelope has to carry its id or a human cannot grant
			// authority and retry against it. Revision 1 is the one no human
			// has spoken about: the head carries a human grant from the
			// `authority` step above, which would satisfy the requirement.
			name: "promote.propose.authority_required", args: []string{"promote", "propose",
				"--insight", "{insight}", "--revision", "1", "--class", "policy",
				"--destination", "docs:docs/policy/ledger.md",
				"--content", "Agents may not grant mandatory authority.",
				"--actor-kind", "agent", "--model", "test-model", "--session", "test-session"},
			exit: exitDenied, errCode: "authority_required",
		},
		{
			name: "promote.fail", args: []string{"promote", "fail", "{proposalA}",
				"--detail", "the docs write did not land: the branch was protected"},
			dataKeys: []string{"promotion"},
			facts:    []string{"promotion.status", "promotion.detail"},
		},
		{
			name: "promote.record", args: []string{"promote", "record", "{proposalA}",
				"--locator", "docs/decisions/0001-one-engine.md",
				"--anchor", "0f1e2d3c4b5a69788796a5b4c3d2e1f0", "--verified"},
			dataKeys: []string{"promotion", "receipt", "durable"},
			facts:    []string{"promotion.attempt", "receipt.id", "receipt.locator"},
		},
		{
			name: "promote.propose.second", args: []string{"promote", "propose",
				"--insight", "{insight}", "--class", "learning",
				"--destination", "docs:docs/learnings/freshness.md",
				"--content", "Every read states the freshness of anything it cached."},
			dataKeys: []string{"proposal", "created", "content_hash", "authority_required"},
			binds:    map[string]string{"proposal.id": "proposalB"},
			facts:    []string{"proposal.id"},
		},
		{
			name: "promote.reject", args: []string{"promote", "reject", "{proposalB}",
				"--rationale", "the learning is already covered by the decision record"},
			dataKeys: []string{"promotion"},
			facts:    []string{"promotion.status"},
		},
		{
			name: "promote.list", args: []string{"promote", "list"},
			dataKeys: []string{"proposals"}, readOnly: true,
			facts: []string{"proposals.0.id", "proposals.0.status"},
		},
		{
			// The dead-letter service. The policy proposal above is blocked on
			// a human decision, which is exactly a question the ledger cannot
			// answer for itself, so delivering to a human materialises it.
			name: "ask.deliver.human.nudge", args: []string{"ask", "deliver", "--respondent", "human"},
			dataKeys: []string{"questions"},
			binds:    map[string]string{"questions.0.id": "askNudge"},
			facts:    []string{"questions.0.id", "questions.0.prompt_key"},
		},
		{
			// "Keep waiting" is an answer, not a skip: a human looked and
			// decided not yet, and that is a record worth keeping.
			name: "ask.answer.nudge.wait", args: []string{"ask", "answer", "{askNudge}", "--choice", "wait"},
			dataKeys: []string{"ask", "crumb", "validation", "authority"},
			facts:    []string{"ask.id", "ask.state", "crumb.id"},
		},
		{
			name: "ask.enqueue.calibration", args: []string{"ask", "enqueue",
				"--prompt", "calibration", "--target", "{rev2}"},
			dataKeys: []string{"ask"},
			binds:    map[string]string{"ask.id": "askCalibration"},
			facts:    []string{"ask.id", "ask.prompt_key"},
		},
		{
			// Calibration is the one prompt that writes a validation, with the
			// respondent's provenance rather than the process actor's.
			name: "ask.answer.calibration.right", args: []string{"ask", "answer",
				"{askCalibration}", "--choice", "right"},
			dataKeys: []string{"ask", "crumb", "validation", "authority"},
			facts:    []string{"ask.id", "validation.verdict"},
		},
		{
			name: "ask.enqueue.context_flush", args: []string{"ask", "enqueue",
				"--prompt", "context-flush",
				"--actor-kind", "agent", "--model", "test-model", "--session", "test-session"},
			dataKeys: []string{"ask"},
			binds:    map[string]string{"ask.id": "askFlush"},
			facts:    []string{"ask.id", "ask.respondent"},
		},
		{
			// An agent answer is a hypothesis: an agent Crumb at lower
			// confidence, no validation, no grant.
			name: "ask.answer.context_flush", args: []string{"ask", "answer", "{askFlush}",
				"--text", "the exclusive lock is held for the whole command, not only the write",
				"--actor-kind", "agent", "--model", "test-model", "--session", "test-session"},
			dataKeys: []string{"ask", "crumb", "validation", "authority"},
			facts:    []string{"ask.id", "crumb.actor_kind"},
		},
		{
			// Left open on purpose: `bdc context` reports the human queue in
			// open_questions, and the steps below are what proves it.
			name: "ask.enqueue.calibration.rev1", args: []string{"ask", "enqueue",
				"--prompt", "calibration", "--target", "{rev1}"},
			dataKeys: []string{"ask"},
			binds:    map[string]string{"ask.id": "askSkippable"},
			facts:    []string{"ask.id"},
		},
		{
			name: "context", args: []string{"context"},
			dataKeys: []string{"summary", "insights", "open_questions", "recent_crumbs", "promotions"},
			readOnly: true,
			facts:    []string{"summary", "insights.0.id", "insights.0.standing"},
		},
		{
			name: "context.insight", args: []string{"context", "--insight", "{insight}", "--limit", "5"},
			dataKeys: []string{"summary", "insights", "open_questions", "recent_crumbs", "promotions"},
			readOnly: true,
			facts:    []string{"insights.0.id"},
		},
		{
			name: "context.budget", args: []string{"context", "--budget", "200"},
			dataKeys: []string{"summary", "insights", "open_questions", "recent_crumbs", "promotions"},
			readOnly: true,
			facts:    []string{"summary"},
		},
		{
			name: "handoff", args: []string{"handoff"},
			dataKeys: []string{"summary", "state", "unreviewed_crumbs", "open_proposals", "workspace"},
			readOnly: true,
			facts:    []string{"summary", "unreviewed_crumbs", "workspace.enrichment"},
		},
		{
			name: "prime", args: []string{"prime"},
			dataKeys: []string{"summary", "working_defaults", "mandatory", "cautions"},
			readOnly: true,
			facts:    []string{"summary", "mandatory.0.id", "mandatory.0.title"},
		},
		{
			// Skipping writes no Crumb. Nobody said anything, and recording
			// that they did is the fastest way to make sampled data worthless.
			name: "ask.skip", args: []string{"ask", "skip", "{askSkippable}", "--reason", "mid-task"},
			dataKeys: []string{"ask"},
			facts:    []string{"ask.id", "ask.state"},
		},
		{
			// Empty again, and it stays empty: an answered nudge is not
			// re-minted, because a question put once and decided is not a
			// question to ask every session.
			name: "ask.deliver.empty_ok", args: []string{"ask", "deliver", "--respondent", "human"},
			dataKeys: []string{"questions"},
			facts:    []string{},
		},
		{
			name: "crumb.prune", args: []string{"crumb", "prune", "--id", "{crumbC}", "--yes"},
			dataKeys: []string{"pruned", "pruned_ids", "blocked"},
			facts:    []string{"pruned"},
		},
		{
			// A Crumb that feeds an Insight is reported per id rather than
			// aborting the whole prune.
			name: "crumb.prune.blocked", args: []string{"crumb", "prune", "--id", "{crumbA}", "--yes"},
			dataKeys: []string{"pruned", "pruned_ids", "blocked"},
			facts:    []string{"pruned"},
		},
		{
			name: "doctor", args: []string{"doctor", "--verbose"},
			dataKeys: []string{"checks", "schema_version", "journal_bytes", "ledger_path", "beads", "counts", "ok"},
			readOnly: true,
			facts:    []string{"ledger_path", "schema_version"},
		},
		{
			// Idempotent on a ledger this build initialised: from == to and
			// nothing applied. It is the repair path for the other case.
			name: "migrate", args: []string{"migrate"},
			dataKeys: []string{"from", "to", "applied"},
			facts:    []string{"from", "to"},
		},
		{
			// Hooks are optional and never part of the workflow, but their
			// envelopes are published output like any other.
			name: "hooks.install", args: []string{"hooks", "install"},
			dataKeys: []string{"hooks", "chained", "auto_harvest"},
			facts:    []string{"hooks.0.hook", "hooks.0.path", "hooks.0.action"},
		},
		{
			// harvest.auto is off, so the trigger reports and writes nothing.
			name: "hooks.run", args: []string{"hooks", "run", "pre-push"},
			dataKeys: []string{"hook", "action", "result"},
			facts:    []string{"hook", "action", "result"},
		},
		{
			name: "hooks.uninstall", args: []string{"hooks", "uninstall"},
			dataKeys: []string{"hooks", "chained", "auto_harvest"},
			facts:    []string{"hooks.0.hook", "hooks.0.action"},
		},
		{
			name: "gc", args: []string{"gc"},
			dataKeys: []string{"before_bytes", "after_bytes", "duration_ms"},
			facts:    []string{"before_bytes", "after_bytes"},
		},
		{
			name: "backup", args: []string{"backup", "{backup}"},
			dataKeys: []string{"destination", "bytes", "schema_version"},
			facts:    []string{"schema_version"},
		},
		{
			name: "restore", args: []string{"restore", "{backup}", "--force"},
			dataKeys: []string{"restored", "schema_version", "records"},
			facts:    []string{"restored", "schema_version", "records"},
		},
		{
			name: "insight.show.not_found",
			args: []string{"insight", "show", "ins_00000000-0000-7000-8000-000000000000"},
			exit: exitNotFound, errCode: "not_found", readOnly: true,
		},
		{
			name: "crumb.review.invalid_usage",
			args: []string{"crumb", "review", "{crumbA}", "--rationale", "no state given"},
			exit: exitUsage, errCode: "invalid_usage", readOnly: true,
		},
		{
			// An identity value is never rewritten, so a secret in a locator
			// aborts the write instead of being redacted into a different name.
			name: "reference.add.redaction_failed",
			args: []string{"reference", "add", "{rev2}", "--kind", "docs", "--locator", awsExampleKey},
			exit: exitRedaction, errCode: "redaction_failed", readOnly: true,
		},
	}
}

func TestGoldenEnvelope(t *testing.T) {
	f := newFixture(t)
	for _, s := range contractSteps() {
		t.Run(s.name, func(t *testing.T) {
			out := f.run(t, s, true)
			f.bind(t, s, out.stdout)
			golden := filepath.Join("testdata", "golden", s.name+".json")
			canonical := f.canonical(out.stdout)
			if *update {
				writeGolden(t, golden, canonical)
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("no golden envelope for %s: %v (run with -update)", s.name, err)
			}
			if got := canonical; got != string(want) {
				t.Errorf("the JSON contract for %s changed:\n--- want\n%s\n--- got\n%s", s.name, want, got)
			}
			if out.exit != s.exit {
				t.Errorf("exit code %d, want %d", out.exit, s.exit)
			}
			if out.stderr != "" && !strings.HasPrefix(out.stderr, "bdc: ") {
				t.Errorf("stderr carried something that is not a bdc diagnostic: %q", out.stderr)
			}
		})
	}
}

// TestJSONOutputHasNoUndeclaredFields is the leak test. Two rules, both
// checked over every invocation in the contract: `data` has exactly the keys
// §3.3 declares for that command, and no key anywhere in the envelope is one
// this test has never heard of. A write path that starts emitting an internal
// column fails here rather than in a caller's log aggregator.
func TestJSONOutputHasNoUndeclaredFields(t *testing.T) {
	f := newFixture(t)
	declared := declaredFields()
	for _, s := range contractSteps() {
		t.Run(s.name, func(t *testing.T) {
			out := f.run(t, s, true)
			f.bind(t, s, out.stdout)

			var env map[string]any
			if err := json.Unmarshal([]byte(out.stdout), &env); err != nil {
				t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, out.stdout)
			}
			assertKeys(t, "envelope", env,
				[]string{"bdc", "command", "ok", "data", "warnings", "error", "meta"})

			switch data := env["data"].(type) {
			case nil:
				if s.dataKeys != nil {
					t.Fatalf("data is null but %v was declared", s.dataKeys)
				}
			case map[string]any:
				assertKeys(t, "data", data, s.dataKeys)
			default:
				t.Fatalf("data is %T; the contract declares an object or null", data)
			}

			for _, key := range keyPaths(env) {
				if !declared[key] {
					t.Errorf("undeclared field %q in %s; add it to declaredFields once it is "+
						"part of the contract", key, s.name)
				}
			}
		})
	}
}

// declaredFields is every field name the JSON surface may contain. It is a flat
// set of names rather than a per-command schema because the point is coverage:
// a new name has to be added here deliberately, and adding it is the moment to
// ask whether it belongs in output at all.
func declaredFields() map[string]bool {
	names := []string{
		// envelope
		"bdc", "command", "ok", "data", "warnings", "error", "meta",
		"code", "message", "details", "bdc_version", "ledger_schema", "generated_at",
		// provenance, shared by every record and event
		"actor_id", "actor_kind", "actor_model", "session_id", "harness",
		// version, init, doctor, maintenance
		"version", "schema_version", "dolt_driver", "go", "platform",
		"path", "stealth", "created", "checks", "journal_bytes", "ledger_path", "beads",
		"hooks", "chained", "auto_harvest", "hook", "action", "result",
		"present", "reason", "prefix", "project_id", "repo_root",
		"name", "status", "detail", "before_bytes", "after_bytes", "duration_ms",
		"counts", "crumbs_by_state", "promotions_by_status",
		"from", "to", "applied",
		"destination", "bytes", "restored", "records",
		// crumbs
		"crumb", "crumbs", "total", "id", "content", "content_hash", "review_state",
		"confidence", "captured_at", "harvest_id", "policy_version", "redaction_version",
		"review_events", "harvests", "insights", "events", "kind", "target", "summary",
		"rationale", "occurred_at", "role", "finished_at", "pruned", "pruned_ids", "blocked",
		"crumb_id", "reason", "revisions", "from_state", "to_state",
		// harvest and insights
		"harvest", "insight", "revision", "crumbs_captured", "redaction", "findings",
		"mode", "outcome", "failure_code", "crumbs_considered", "crumbs_selected",
		"started_at", "head_revision", "created_at", "insight_id", "title", "class",
		"parent_revision_id", "head_revision_id", "updated_at", "verdict", "authority",
		"lineage", "parent_id", "rule", "offset", "length", "replacement",
		// references
		"reference", "references", "link", "locator", "workspace", "label", "meta",
		"fetched_at", "relation", "display", "freshness", "state", "age_seconds",
		"enricher", "error", "record",
		// validation and authority
		"validation", "validations", "effective_verdict", "superseded_by",
		"authorities", "effective_level", "level", "scope",
		"destination_kind", "destination_locator",
		// promotion
		"proposal", "proposals", "promotion", "promotions", "receipt", "receipts",
		"attempts", "durable", "authority_required", "requested_authority",
		"supersedes_proposal_id", "dest_kind", "dest_locator", "dest_workspace",
		"capabilities", "revision_id", "attempt", "anchor", "external_hash", "verified",
		"promotion_id", "reference_id", "recorded_at", "authority_held",
		// narrative
		"open_questions", "recent_crumbs", "question", "subject", "standing", "excerpt",
		"unreviewed_crumbs", "open_proposals", "working_defaults", "mandatory", "cautions",
		"enrichment", "cached", "never", "count", "oldest_fetched_at", "newest_fetched_at",
		"last_activity_at", "unusable", "receipt_locator", "proposal_id",
		// Closed vocabularies used as map keys: `state.crumbs` is keyed by
		// review state and `state.promotions` by promotion status, so every
		// member of both enums is a field name a caller may see.
		"candidate", "accepted", "rejected", "proposed", "applied", "failed", "superseded",
		// sampling: the prompt registry and the asks minted from it
		"prompts", "prompt", "asks", "ask", "questions", "prompt_id", "prompt_key", "prompt_version",
		"question_template", "answer_kind", "options", "trigger_class", "origin", "active",
		"respondent", "question_snapshot", "enqueue_session_id", "via_session",
		"validation_id", "authority_id", "choice_id", "answer_text", "skip_reason",
		"latency_ms", "delivered_at", "resolved_at", "expires_at",
		// error.details payloads
		"field",
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return set
}

func assertKeys(t *testing.T, what string, obj map[string]any, want []string) {
	t.Helper()
	got := make([]string, 0, len(obj))
	for k := range obj {
		got = append(got, k)
	}
	sort.Strings(got)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if strings.Join(got, ",") != strings.Join(sorted, ",") {
		t.Errorf("%s keys are [%s]; the contract declares [%s]",
			what, strings.Join(got, ", "), strings.Join(sorted, ", "))
	}
}

// keyPaths returns every key name appearing anywhere in the tree. Names rather
// than paths: the same record shape appears under a dozen parents, and a set of
// names is the check that actually catches a new field.
func keyPaths(v any) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			out = append(out, k)
			out = append(out, keyPaths(child)...)
		}
	case []any:
		for _, child := range t {
			out = append(out, keyPaths(child)...)
		}
	}
	return out
}

// fixture is one temporary Git repository with one ledger, plus the variables
// the steps bind out of each other's output.
type fixture struct {
	dir     string
	backup  string
	vars    map[string]string
	aliases *aliasTable
	roots   []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	// Harness detection reads the process environment. An inherited AMP_ORB or
	// CLAUDECODE would add `harness` to every envelope and make the goldens
	// describe the machine rather than the contract.
	for _, key := range []string{
		"BDC_HARNESS", "AMP_ORB", "AMP_THREAD_ID",
		"CONDUCTOR_SESSION_ID", "CONDUCTOR_IS_LOCAL",
		"CLAUDE_CODE", "CLAUDECODE", "CODEX_HOME", "CODEX_THREAD_ID",
		"OPENCODE", "OPENCODE_DIR",
	} {
		t.Setenv(key, "")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("creating the fixture repository: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=bdc", "-c", "user.email=bdc@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// The ledger reports a symlink-resolved path on macOS, where the temp
	// directory is /var -> /private/var; both spellings have to canonicalise to
	// the same placeholder, and the resolved one has to be replaced first. The
	// other order rewrites the /var suffix inside /private/var and leaves
	// "/private<tmp>" — a macOS spelling baked into a golden that Linux, where
	// the two are the same path, can never produce.
	roots := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		roots = append(roots, resolved)
	}
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) > len(roots[j]) })
	return &fixture{
		dir:     repo,
		backup:  filepath.Join(root, "backup"),
		vars:    map[string]string{},
		aliases: newAliasTable(),
		roots:   roots,
	}
}

type invocation struct {
	stdout string
	stderr string
	exit   int
}

func (f *fixture) run(t *testing.T, s step, jsonMode bool) invocation {
	t.Helper()
	// Every provenance and location input is explicit: an inherited BDC_ACTOR
	// or a different working directory would make the golden envelopes depend
	// on whose machine ran them. --no-enrich is the same rule applied to the
	// optional tracker: with `bd` installed, doctor's `beads` and handoff's
	// `workspace.enrichment` describe the machine rather than the contract.
	// TestCoreWorkflowWithBeads is where the detected path is asserted.
	args := []string{"-C", f.dir, "--actor", "tester", "--actor-kind", "human", "--no-enrich"}
	if jsonMode {
		args = append(args, "--json")
	}
	args = append(args, f.resolve(t, s.args)...)

	var stdout, stderr bytes.Buffer
	exit := run(&stdout, &stderr, args)
	return invocation{stdout: stdout.String(), stderr: stderr.String(), exit: exit}
}

func (f *fixture) resolve(t *testing.T, args []string) []string {
	t.Helper()
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "{backup}" {
			out = append(out, f.backup)
			continue
		}
		if strings.HasPrefix(a, "{") && strings.HasSuffix(a, "}") {
			name := a[1 : len(a)-1]
			v, ok := f.vars[name]
			if !ok {
				t.Fatalf("step needs %q, which no earlier step bound", name)
			}
			out = append(out, v)
			continue
		}
		out = append(out, a)
	}
	return out
}

// bind records the ids later steps need. It runs after every step, including in
// the tests that do not check goldens, so the table stays in one place.
func (f *fixture) bind(t *testing.T, s step, stdout string) {
	t.Helper()
	if len(s.binds) == 0 {
		return
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("cannot bind from %s: %v", s.name, err)
	}
	for path, name := range s.binds {
		v, ok := lookup(env["data"], path)
		if !ok {
			t.Fatalf("%s does not carry %q, which later steps need", s.name, path)
		}
		f.vars[name] = fmt.Sprint(v)
	}
}

// lookup resolves a dotted path with numeric segments for array indices:
// "crumbs.0.id".
func lookup(v any, path string) (any, bool) {
	cur := v
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			child, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = child
		case []any:
			var i int
			if _, err := fmt.Sscanf(part, "%d", &i); err != nil || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// The volatile shapes. Everything a second run of the same steps would produce
// differently, and nothing else: an over-eager canonicaliser would hide exactly
// the changes these goldens exist to catch.
var (
	idPattern = regexp.MustCompile(
		`\b(crb|cre|hrv|ins|rev|ref|val|aut|pp|prm|rcp|pmt|ask)_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	timePattern   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
	hashPattern   = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	sizePattern   = regexp.MustCompile(`("(?:journal_bytes|total_bytes|bytes|before_bytes|after_bytes|duration_ms|age_seconds|latency_ms)": )-?\d+(?:\.\d+)?`)
	buildPattern  = regexp.MustCompile(`("(?:go|platform|dolt_driver)": ")[^"]*"`)
	numberPattern = regexp.MustCompile(`\b\d+ bytes\b`)
)

// canonical replaces every volatile value with a stable placeholder. Ids keep
// their identity: the same id is the same alias everywhere, so a golden still
// proves that a receipt names the proposal it was recorded against.
func (f *fixture) canonical(text string) string {
	for _, root := range f.roots {
		text = strings.ReplaceAll(text, root, "<tmp>")
	}
	text = idPattern.ReplaceAllStringFunc(text, f.aliases.alias)
	text = timePattern.ReplaceAllString(text, "<time>")
	text = hashPattern.ReplaceAllString(text, "<sha256>")
	text = sizePattern.ReplaceAllString(text, "${1}<size>")
	text = buildPattern.ReplaceAllString(text, `${1}<build>"`)
	text = numberPattern.ReplaceAllString(text, "<size> bytes")
	return text
}

// aliasTable numbers ids per kind in first-encounter order, which is stable as
// long as the step table is.
type aliasTable struct {
	seen  map[string]string
	count map[string]int
}

func newAliasTable() *aliasTable {
	return &aliasTable{seen: map[string]string{}, count: map[string]int{}}
}

func (a *aliasTable) alias(id string) string {
	if known, ok := a.seen[id]; ok {
		return known
	}
	kind := id[:strings.Index(id, "_")]
	a.count[kind]++
	name := fmt.Sprintf("<%s_%d>", kind, a.count[kind])
	a.seen[id] = name
	return name
}

func writeGolden(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
