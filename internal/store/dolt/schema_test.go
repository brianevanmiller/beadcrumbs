package dolt

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Every test in this file drives raw SQL past the ledger and asserts that the
// *database* refuses it. An invariant enforced only in Go is an invariant that
// disappears the first time a write path forgets it, and Dolt keeps committed
// history forever — there is no cleaning up afterwards.

// schemaFixture is a fresh ledger with the current schema applied.
func schemaFixture(t *testing.T) *Store {
	t.Helper()
	return openLedger(t, initLedger(t, fixtureRepo(t), false), Config{})
}

func mustExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.DB().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", firstLine(query), err)
	}
}

// rejects asserts the statement fails and that the failure names the constraint
// we meant to test. Matching the name is what keeps a test honest: a typo in the
// fixture also produces an error, and without the name the test would pass on it.
func rejects(t *testing.T, s *Store, constraint, query string, args ...any) {
	t.Helper()
	_, err := s.DB().ExecContext(context.Background(), query, args...)
	if err == nil {
		t.Fatalf("the database accepted a row it must reject: %s", firstLine(query))
	}
	if !strings.Contains(err.Error(), constraint) {
		t.Fatalf("expected %s to be violated, got: %v", constraint, err)
	}
}

const humanProv = `'brian', 'human', NULL, NULL`

// The id prefixes the raw-SQL fixtures mint. They are spelled out rather than
// imported so a change to the ledger's constants shows up here as a failing
// constraint test rather than as a silently renamed fixture.
const (
	ledgerPrefixPrompt = "pmt_"
	ledgerPrefixAsk    = "ask_"
)

func seedCrumb(t *testing.T, s *Store, id, hash string) {
	t.Helper()
	mustExec(t, s, fmt.Sprintf(`INSERT INTO crumbs
		(id, content, content_hash, review_state, confidence, captured_at, redaction_version,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'a fragment', ?, 'candidate', 0.700, UTC_TIMESTAMP(6), '1', %s)`, humanProv), id, hash)
}

// seedInsight creates an Insight with revision 1 and returns the revision id.
func seedInsight(t *testing.T, s *Store, insightID, revisionID string) string {
	t.Helper()
	mustExec(t, s, fmt.Sprintf(`INSERT INTO insights
		(id, head_revision, created_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 1, UTC_TIMESTAMP(6), %s)`, humanProv), insightID)
	mustExec(t, s, fmt.Sprintf(`INSERT INTO insight_revisions
		(id, insight_id, revision, title, content, content_hash, class, confidence, created_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, 1, 'a title', 'a body', REPEAT('a', 64), 'learning', 0.800, UTC_TIMESTAMP(6), %s)`,
		humanProv), revisionID, insightID)
	return revisionID
}

func seedProposal(t *testing.T, s *Store, id, insightID, revisionID, hash string) {
	t.Helper()
	mustExec(t, s, fmt.Sprintf(`INSERT INTO promotion_proposals
		(id, insight_id, revision_id, class, dest_kind, dest_locator, content, content_hash,
		 confidence, policy_version, redaction_version, created_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, ?, 'adr', 'docs', 'docs/adr/', 'rendered', ?, 0.900, '1', '1', UTC_TIMESTAMP(6), %s)`,
		humanProv), id, insightID, revisionID, hash)
}

func id(prefix, suffix string) string {
	return prefix + "0199aa11-2233-7444-8555-" + strings.Repeat("0", 12-len(suffix)) + suffix
}

func TestSchemaRejectsInvalidConfidence(t *testing.T) {
	s := schemaFixture(t)
	rejects(t, s, "ck_crumbs_conf", fmt.Sprintf(`INSERT INTO crumbs
		(id, content, content_hash, review_state, confidence, captured_at, redaction_version,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'over', REPEAT('a', 64), 'candidate', 1.500, UTC_TIMESTAMP(6), '1', %s)`, humanProv),
		id("crb_", "1"))

	seedInsight(t, s, id("ins_", "2"), id("rev_", "3"))
	rejects(t, s, "ck_rev_conf", fmt.Sprintf(`INSERT INTO insight_revisions
		(id, insight_id, revision, title, content, content_hash, class, confidence, rationale,
		 parent_revision_id, created_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, 2, 't', 'b', REPEAT('a', 64), 'learning', -0.100, 'why', ?, UTC_TIMESTAMP(6), %s)`,
		humanProv), id("rev_", "4"), id("ins_", "2"), id("rev_", "3"))
}

func TestSchemaRequiresAgentProvenance(t *testing.T) {
	s := schemaFixture(t)
	revision := seedInsight(t, s, id("ins_", "1"), id("rev_", "2"))

	rejects(t, s, "ck_aut_prov", `INSERT INTO authorities
		(id, target_kind, target_id, level, rationale, occurred_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'insight_revision', ?, 'advisory', 'why', UTC_TIMESTAMP(6),
		        'claude', 'agent', NULL, NULL)`,
		id("aut_", "3"), revision)

	rejects(t, s, "ck_crumbs_prov", `INSERT INTO crumbs
		(id, content, content_hash, review_state, confidence, captured_at, redaction_version,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'a fragment', REPEAT('a', 64), 'candidate', 0.700, UTC_TIMESTAMP(6), '1',
		        'claude', 'agent', 'claude-opus-5', NULL)`,
		id("crb_", "4"))
}

// TestSchemaRejectsEmptyStringProvenance is separate from the NULL case because
// NOT NULL alone is not "provenance present": an agent row with empty-string
// model and session inserts cleanly under an IS NOT NULL form of the check.
func TestSchemaRejectsEmptyStringProvenance(t *testing.T) {
	s := schemaFixture(t)
	revision := seedInsight(t, s, id("ins_", "1"), id("rev_", "2"))

	rejects(t, s, "ck_aut_prov", `INSERT INTO authorities
		(id, target_kind, target_id, level, rationale, occurred_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'insight_revision', ?, 'advisory', 'why', UTC_TIMESTAMP(6),
		        'claude', 'agent', '', '')`,
		id("aut_", "3"), revision)

	rejects(t, s, "ck_aut_prov", `INSERT INTO authorities
		(id, target_kind, target_id, level, rationale, occurred_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'insight_revision', ?, 'advisory', 'why', UTC_TIMESTAMP(6),
		        '', 'human', NULL, NULL)`,
		id("aut_", "4"), revision)
}

func TestSchemaRejectsOrphanRefLink(t *testing.T) {
	s := schemaFixture(t)
	seedCrumb(t, s, id("crb_", "1"), strings.Repeat("a", 64))

	rejects(t, s, "fk_rl_ref", `INSERT INTO ref_links
		(record_kind, record_id, reference_id, relation, created_at)
		VALUES ('crumb', ?, ?, 'subject', UTC_TIMESTAMP(6))`,
		id("crb_", "1"), id("ref_", "9"))
}

func TestSchemaRejectsDuplicateProposalHash(t *testing.T) {
	s := schemaFixture(t)
	revision := seedInsight(t, s, id("ins_", "1"), id("rev_", "2"))
	hash := strings.Repeat("b", 64)
	seedProposal(t, s, id("pp_", "3"), id("ins_", "1"), revision, hash)

	// uq_pp_hash is what makes idempotency a database property rather than a
	// code convention, so the duplicate must fail even from raw SQL.
	rejects(t, s, "duplicate", fmt.Sprintf(`INSERT INTO promotion_proposals
		(id, insight_id, revision_id, class, dest_kind, dest_locator, content, content_hash,
		 confidence, policy_version, redaction_version, created_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, ?, 'adr', 'beads', 'bdc-1', 'other text', ?, 0.500, '1', '1', UTC_TIMESTAMP(6), %s)`,
		humanProv), id("pp_", "4"), id("ins_", "1"), revision, hash)
}

func TestSchemaRejectsAgentMandatoryAuthority(t *testing.T) {
	s := schemaFixture(t)
	revision := seedInsight(t, s, id("ins_", "1"), id("rev_", "2"))

	rejects(t, s, "ck_aut_mandatory_human", `INSERT INTO authorities
		(id, target_kind, target_id, level, rationale, occurred_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'insight_revision', ?, 'mandatory', 'because I said so', UTC_TIMESTAMP(6),
		        'claude', 'agent', 'claude-opus-5', 'sess-1')`,
		id("aut_", "3"), revision)

	// The same grant from a human is accepted: the constraint is about who, not
	// about the level.
	mustExec(t, s, fmt.Sprintf(`INSERT INTO authorities
		(id, target_kind, target_id, level, rationale, occurred_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'insight_revision', ?, 'mandatory', 'reviewed', UTC_TIMESTAMP(6), %s)`, humanProv),
		id("aut_", "4"), revision)
}

// TestSchemaRejectsNonPositiveOrdinals: a -5 revision passes every foreign key
// and unique key in the schema, so the ordinal checks are the only thing
// standing between it and the ledger.
func TestSchemaRejectsNonPositiveOrdinals(t *testing.T) {
	s := schemaFixture(t)
	revision := seedInsight(t, s, id("ins_", "1"), id("rev_", "2"))

	rejects(t, s, "ck_rev_number", fmt.Sprintf(`INSERT INTO insight_revisions
		(id, insight_id, revision, title, content, content_hash, class, confidence, rationale,
		 parent_revision_id, created_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, -5, 't', 'b', REPEAT('a', 64), 'learning', 0.500, 'why', ?, UTC_TIMESTAMP(6), %s)`,
		humanProv), id("rev_", "3"), id("ins_", "1"), revision)

	rejects(t, s, "ck_insights_head", fmt.Sprintf(`INSERT INTO insights
		(id, head_revision, created_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 0, UTC_TIMESTAMP(6), %s)`, humanProv), id("ins_", "4"))

	seedProposal(t, s, id("pp_", "5"), id("ins_", "1"), revision, strings.Repeat("c", 64))
	rejects(t, s, "ck_prm_attempt", fmt.Sprintf(`INSERT INTO promotions
		(id, proposal_id, attempt, status, occurred_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, 0, 'proposed', UTC_TIMESTAMP(6), %s)`, humanProv), id("prm_", "6"), id("pp_", "5"))
}

func TestSchemaRejectsCrossInsightParent(t *testing.T) {
	s := schemaFixture(t)
	revisionA := seedInsight(t, s, id("ins_", "1"), id("rev_", "2"))
	seedInsight(t, s, id("ins_", "3"), id("rev_", "4"))

	rejects(t, s, "fk_rev_parent", fmt.Sprintf(`INSERT INTO insight_revisions
		(id, insight_id, revision, title, content, content_hash, class, confidence, rationale,
		 parent_revision_id, created_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, 2, 't', 'b', REPEAT('a', 64), 'learning', 0.500, 'why', ?, UTC_TIMESTAMP(6), %s)`,
		humanProv), id("rev_", "5"), id("ins_", "3"), revisionA)
}

func TestSchemaRejectsCrossInsightProposal(t *testing.T) {
	s := schemaFixture(t)
	revisionA := seedInsight(t, s, id("ins_", "1"), id("rev_", "2"))
	seedInsight(t, s, id("ins_", "3"), id("rev_", "4"))

	rejects(t, s, "fk_pp_rev", fmt.Sprintf(`INSERT INTO promotion_proposals
		(id, insight_id, revision_id, class, dest_kind, dest_locator, content, content_hash,
		 confidence, policy_version, redaction_version, created_at,
		 actor_id, actor_kind, actor_model, session_id)
		VALUES (?, ?, ?, 'adr', 'docs', 'docs/adr/', 'rendered', REPEAT('d', 64),
		        0.900, '1', '1', UTC_TIMESTAMP(6), %s)`, humanProv),
		id("pp_", "5"), id("ins_", "3"), revisionA)
}

// TestSchemaTreatsCaseVariantLocatorsAsDistinct: under a case-insensitive
// collation these two collide on uq_refs_identity and the second is silently the
// first. Declaring utf8mb4_0900_bin is what makes "the locator is opaque to
// core" true at the storage layer.
func TestSchemaTreatsCaseVariantLocatorsAsDistinct(t *testing.T) {
	s := schemaFixture(t)
	for i, locator := range []string{"docs/Foo.md", "docs/foo.md"} {
		mustExec(t, s, `INSERT INTO refs (id, kind, locator, workspace, created_at)
			VALUES (?, 'docs', ?, '', UTC_TIMESTAMP(6))`, id("ref_", fmt.Sprint(i+1)), locator)
	}
	if n := countRows(t, s.DB(), `SELECT COUNT(*) FROM refs`); n != 2 {
		t.Fatalf("expected two distinct References, got %d", n)
	}
}

// TestSchemaVersionIsRecordedByReplacingTheSingleton is the migration protocol
// a 002 script has to follow. schema_meta carries CHECK (id = 1), so a second
// INSERT cannot record a version: the last statement of every later migration
// is a REPLACE, and SchemaVersion reads it back. Written as SQL because the
// runner takes its scripts from the embedded filesystem, so a fixture migration
// would prove nothing about the ones that ship.
func TestSchemaVersionIsRecordedByReplacingTheSingleton(t *testing.T) {
	ctx := context.Background()
	s := openLedger(t, initLedger(t, fixtureRepo(t), false), Config{Command: t.Name()})

	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO schema_meta (id, version, bdc_version, applied_at) VALUES (2, 2, '1.1.0', UTC_TIMESTAMP(6))`,
	); err == nil {
		t.Fatal("schema_meta accepted a second row; the singleton constraint is not enforced")
	}
	if _, err := s.DB().ExecContext(ctx,
		`REPLACE INTO schema_meta (id, version, bdc_version, applied_at) VALUES (1, 2, '1.1.0', UTC_TIMESTAMP(6))`,
	); err != nil {
		t.Fatalf("a migration cannot record its version: %v", err)
	}
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("reading the schema version: %v", err)
	}
	if version != 2 {
		t.Fatalf("schema version = %d, want the version the replacement recorded", version)
	}
}

// The sampling tables carry the same kind of database-level guarantees the rest
// of the schema does. Each of these is reachable from a write path, and each is
// checked in Go as well; the point of the pair is that the Go half can be
// forgotten and the row still cannot exist.

const seededFlushPrompt = "pmt_01992a00-0000-7000-8000-000000000003"

func askInsert(columns, values string) string {
	return `INSERT INTO asks
		(id, prompt_id, prompt_key, prompt_version, respondent, state, question_snapshot,
		 created_at, expires_at, actor_id, actor_kind` + columns + `)
		VALUES (?, '` + seededFlushPrompt + `', 'context-flush', 1, 'human', 'pending', 'a question',
		 UTC_TIMESTAMP(6), UTC_TIMESTAMP(6), 'brian', 'human'` + values + `)`
}

func TestSchemaRejectsMalformedPrompts(t *testing.T) {
	s := schemaFixture(t)
	insert := func(columns, values string) string {
		return `INSERT INTO prompts
			(id, prompt_key, version, respondent, question_template, answer_kind,
			 trigger_class, origin, active, created_at, actor_id, actor_kind` + columns + `)
			VALUES (?, 'census', 1, 'human', 'a question?', 'short-text',
			 'manual', 'curated', 1, UTC_TIMESTAMP(6), 'brian', 'human'` + values + `)`
	}
	mustExec(t, s, insert("", ""), id(ledgerPrefixPrompt, "1"))

	rejects(t, s, "ck_prompts_key", `INSERT INTO prompts
		(id, prompt_key, version, respondent, question_template, answer_kind,
		 trigger_class, origin, active, created_at, actor_id, actor_kind)
		VALUES (?, '', 1, 'human', 'a question?', 'short-text', 'manual', 'curated', 1,
		 UTC_TIMESTAMP(6), 'brian', 'human')`, id(ledgerPrefixPrompt, "2"))

	rejects(t, s, "ck_prompts_version", `INSERT INTO prompts
		(id, prompt_key, version, respondent, question_template, answer_kind,
		 trigger_class, origin, active, created_at, actor_id, actor_kind)
		VALUES (?, 'census', 0, 'human', 'a question?', 'short-text', 'manual', 'curated', 1,
		 UTC_TIMESTAMP(6), 'brian', 'human')`, id(ledgerPrefixPrompt, "3"))

	rejects(t, s, "ck_prompts_active", `INSERT INTO prompts
		(id, prompt_key, version, respondent, question_template, answer_kind,
		 trigger_class, origin, active, created_at, actor_id, actor_kind)
		VALUES (?, 'census', 2, 'human', 'a question?', 'short-text', 'manual', 'curated', 2,
		 UTC_TIMESTAMP(6), 'brian', 'human')`, id(ledgerPrefixPrompt, "4"))

	rejects(t, s, "ck_prompts_q", `INSERT INTO prompts
		(id, prompt_key, version, respondent, question_template, answer_kind,
		 trigger_class, origin, active, created_at, actor_id, actor_kind)
		VALUES (?, 'census', 3, 'human', '', 'short-text', 'manual', 'curated', 1,
		 UTC_TIMESTAMP(6), 'brian', 'human')`, id(ledgerPrefixPrompt, "5"))

	rejects(t, s, "ck_prompts_prov", `INSERT INTO prompts
		(id, prompt_key, version, respondent, question_template, answer_kind,
		 trigger_class, origin, active, created_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, 'census', 4, 'agent', 'a question?', 'short-text', 'manual', 'curated', 1,
		 UTC_TIMESTAMP(6), 'claude', 'agent', NULL, NULL)`, id(ledgerPrefixPrompt, "6"))
}

func TestSchemaRejectsMalformedAsks(t *testing.T) {
	s := schemaFixture(t)
	mustExec(t, s, askInsert("", ""), id(ledgerPrefixAsk, "1"))

	// A target is a kind and an id together. Half of one names nothing, and the
	// column pair is polymorphic so no foreign key can say so.
	rejects(t, s, "ck_asks_target", askInsert(", target_kind", ", 'crumb'"), id(ledgerPrefixAsk, "2"))
	rejects(t, s, "ck_asks_target", askInsert(", target_id", ", 'crb_x'"), id(ledgerPrefixAsk, "3"))

	rejects(t, s, "ck_asks_latency", askInsert(", latency_ms", ", -1"), id(ledgerPrefixAsk, "4"))

	rejects(t, s, "ck_asks_q", `INSERT INTO asks
		(id, prompt_id, prompt_key, prompt_version, respondent, state, question_snapshot,
		 created_at, actor_id, actor_kind)
		VALUES (?, '`+seededFlushPrompt+`', 'context-flush', 1, 'human', 'pending', '',
		 UTC_TIMESTAMP(6), 'brian', 'human')`, id(ledgerPrefixAsk, "5"))

	rejects(t, s, "ck_asks_prov", `INSERT INTO asks
		(id, prompt_id, prompt_key, prompt_version, respondent, state, question_snapshot,
		 created_at, actor_id, actor_kind, actor_model, session_id)
		VALUES (?, '`+seededFlushPrompt+`', 'context-flush', 1, 'agent', 'pending', 'a question',
		 UTC_TIMESTAMP(6), 'claude', 'agent', NULL, NULL)`, id(ledgerPrefixAsk, "6"))

	// The prompt an ask was minted from cannot be deleted out from under it.
	rejects(t, s, "fk_asks_prompt", `DELETE FROM prompts WHERE id = '`+seededFlushPrompt+`'`)
}
