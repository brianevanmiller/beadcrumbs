-- 003_ask.sql — schema version 3.
--
-- The sampling surface: a curated prompt registry and the asks minted from it.
-- Pure SQL, so applyMigration runs it through execScript and gains no third
-- `if m.version == N` branch.
--
-- Two tables and no third knowledge pipeline. An answer becomes a Crumb (and,
-- where the prompt names a revision or a proposal, a validation or a capped
-- authority grant); asks.crumb_id is the join back, which is why there is no
-- `samples` table and no via_session column on crumbs, validations, or
-- authorities. The transport is a property of the ask, not of every record the
-- answer produced.
--
-- Both tables carry the provenance quartet under exactly the names crumbs uses
-- (actor_id, actor_kind, actor_model, session_id, harness) so anything generic
-- over provenance — the redaction census, a future provenance scan, a reader —
-- reads them correctly. The ask-scope session is therefore enqueue_session_id:
-- session_id already means "who wrote this row" and must not mean two things on
-- one table.
--
-- Open-ask uniqueness has no partial unique index to express it, so it is a
-- ledger invariant enforced in EnqueueAsk inside the same Write. See ask.go.
--
-- The last statement REPLACEs schema_meta. A failed run leaves version 2, so
-- bdc migrate is re-runnable.

CREATE TABLE prompts (
  id                CHAR(40)     NOT NULL PRIMARY KEY,
  prompt_key        VARCHAR(64)  NOT NULL,
  version           INT          NOT NULL,
  respondent        ENUM('human','agent','both') NOT NULL,
  question_template TEXT         NOT NULL,
  answer_kind       ENUM('choice','scale','short-text') NOT NULL,
  options_json      TEXT         NULL,
  trigger_class     VARCHAR(32)  NOT NULL,
  origin            ENUM('curated','agent-proposed') NOT NULL,
  active            TINYINT      NOT NULL DEFAULT 1,
  created_at        DATETIME(6)  NOT NULL,
  actor_id          VARCHAR(255) NOT NULL,
  actor_kind        ENUM('human','agent') NOT NULL,
  actor_model       VARCHAR(128) NULL,
  session_id        VARCHAR(128) NULL,
  harness           VARCHAR(64)  NULL,
  UNIQUE KEY uq_prompts_key_version (prompt_key, version),
  KEY ix_prompts_key_active (prompt_key, active),
  CONSTRAINT ck_prompts_version CHECK (version >= 1),
  CONSTRAINT ck_prompts_active  CHECK (active IN (0, 1)),
  CONSTRAINT ck_prompts_key     CHECK (CHAR_LENGTH(prompt_key) > 0),
  CONSTRAINT ck_prompts_q       CHECK (CHAR_LENGTH(question_template) > 0
      AND CHAR_LENGTH(question_template) <= 4096),
  CONSTRAINT ck_prompts_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE asks (
  id                 CHAR(40)     NOT NULL PRIMARY KEY,
  prompt_id          CHAR(40)     NOT NULL,
  prompt_key         VARCHAR(64)  NOT NULL,
  prompt_version     INT          NOT NULL,
  respondent         ENUM('human','agent') NOT NULL,
  target_kind        ENUM('crumb','insight_revision','promotion_proposal') NULL,
  target_id          CHAR(40)     NULL,
  state              ENUM('pending','delivered','answered','skipped','expired') NOT NULL,
  question_snapshot  TEXT         NOT NULL,
  options_snapshot   TEXT         NULL,
  enqueue_session_id VARCHAR(128) NULL,
  via_session        VARCHAR(128) NULL,
  crumb_id           CHAR(40)     NULL,
  validation_id      CHAR(40)     NULL,
  authority_id       CHAR(40)     NULL,
  choice_id          VARCHAR(64)  NULL,
  answer_text        TEXT         NULL,
  skip_reason        VARCHAR(255) NULL,
  latency_ms         INT          NULL,
  created_at         DATETIME(6)  NOT NULL,
  delivered_at       DATETIME(6)  NULL,
  resolved_at        DATETIME(6)  NULL,
  expires_at         DATETIME(6)  NULL,
  actor_id           VARCHAR(255) NOT NULL,
  actor_kind         ENUM('human','agent') NOT NULL,
  actor_model        VARCHAR(128) NULL,
  session_id         VARCHAR(128) NULL,
  harness            VARCHAR(64)  NULL,
  KEY ix_asks_state_resp (state, respondent, created_at),
  KEY ix_asks_session (enqueue_session_id, state),
  KEY ix_asks_prompt_target (prompt_key, target_id, state),
  CONSTRAINT fk_asks_prompt FOREIGN KEY (prompt_id) REFERENCES prompts(id) ON DELETE RESTRICT,
  CONSTRAINT fk_asks_crumb  FOREIGN KEY (crumb_id)  REFERENCES crumbs(id)  ON DELETE RESTRICT,
  CONSTRAINT ck_asks_target CHECK ((target_kind IS NULL AND target_id IS NULL)
      OR (target_kind IS NOT NULL AND target_id IS NOT NULL)),
  CONSTRAINT ck_asks_q      CHECK (CHAR_LENGTH(question_snapshot) > 0
      AND CHAR_LENGTH(question_snapshot) <= 4096),
  CONSTRAINT ck_asks_latency CHECK (latency_ms IS NULL OR latency_ms >= 0),
  CONSTRAINT ck_asks_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

-- Seeded registry. actor_id `bdc` with actor_kind `human` is the same spirit as
-- the 001 seeds: the repository shipped these questions, no live person did.
-- The ids are literals so two clones that both migrate mint the same rows and a
-- Dolt merge is a no-op rather than a duplicate registry.
--
-- {target}, {confidence}, and {excerpt} are substituted at enqueue from the
-- target record; the snapshot stores the substituted string. calibration quotes
-- the Insight verbatim rather than paraphrasing it — a leading question is a
-- question whose answer is worth less than nothing.
INSERT INTO prompts
  (id, prompt_key, version, respondent, question_template, answer_kind, options_json,
   trigger_class, origin, active, created_at, actor_id, actor_kind)
VALUES
  ('pmt_01992a00-0000-7000-8000-000000000001', 'authority-nudge', 1, 'human',
   'Proposal {target} has waited for a human authority grant. Grant a working default, recommend rejection, or keep waiting?',
   'choice',
   '[{"id":"grant-default","label":"Grant a working default"},{"id":"reject","label":"Recommend rejection"},{"id":"wait","label":"Keep waiting"}]',
   'ledger-state', 'curated', 1, UTC_TIMESTAMP(6), 'bdc', 'human'),
  ('pmt_01992a00-0000-7000-8000-000000000002', 'calibration', 1, 'human',
   'I concluded ({confidence}): {excerpt} Right, partly right, or wrong?',
   'choice',
   '[{"id":"right","label":"Right"},{"id":"partly","label":"Partly right"},{"id":"wrong","label":"Wrong"}]',
   'manual', 'curated', 1, UTC_TIMESTAMP(6), 'bdc', 'human'),
  ('pmt_01992a00-0000-7000-8000-000000000003', 'context-flush', 1, 'agent',
   'What do you currently know that the ledger does not? One fragment. Reply skip if nothing.',
   'short-text', NULL,
   'event', 'curated', 1, UTC_TIMESTAMP(6), 'bdc', 'human');

-- ask.max_per_deliver caps one presentation batch, not a session or a day: the
-- schema records no "delivered by session" fact, so Go must not invent one. 0
-- means no cap beyond maxDeliverBatch.
INSERT INTO repo_config (k, v, updated_at) VALUES
  ('ask.max_per_deliver', '0',    UTC_TIMESTAMP(6)),
  ('ask.expire_after',    '168h', UTC_TIMESTAMP(6));

REPLACE INTO schema_meta (id, version, bdc_version, applied_at)
VALUES (1, 3, '1.1.0', UTC_TIMESTAMP(6));
