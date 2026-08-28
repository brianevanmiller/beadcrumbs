-- 001_init.sql — schema version 1.
--
-- The whole ledger, authored once. No later migration adds DDL in v1: a single
-- script is what lets parallel implementation slices share one schema without
-- contending, and a slice that believes it needs a change has found a defect
-- here rather than a reason for 002.
--
-- Every table declares COLLATE=utf8mb4_0900_bin so identity comparison is
-- byte-exact and independent of the server default. Under a case-insensitive
-- collation `docs/Foo.md` and `docs/foo.md` collide on uq_refs_identity, which
-- would silently make two distinct References one.
--
-- Table order is load-bearing: harvests before crumbs, insights before
-- insight_revisions, refs before ref_links and receipts, promotion_proposals
-- before promotions before receipts.
--
-- The script's last statement records the schema version in schema_meta, which
-- is what keeps the migration runner from having to know this table's shape.
-- The table is a singleton: 001 inserts the row, and every later script ends by
-- REPLACEing it. A second INSERT would collide with the primary key, so the
-- protocol is a replace, not an append — the migration history lives in Dolt's
-- own commit log, which is the one place it cannot drift from the data.

CREATE TABLE schema_meta (
  id          TINYINT     NOT NULL PRIMARY KEY DEFAULT 1,
  version     INT         NOT NULL,
  bdc_version VARCHAR(32) NOT NULL,
  applied_at  DATETIME(6) NOT NULL,
  CONSTRAINT ck_schema_singleton CHECK (id = 1)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE repo_config (
  k          VARCHAR(128) NOT NULL PRIMARY KEY,
  v          TEXT         NOT NULL,
  updated_at DATETIME(6)  NOT NULL
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE harvests (
  id                CHAR(40)     NOT NULL PRIMARY KEY,
  mode              ENUM('manual','automatic') NOT NULL,
  outcome           ENUM('completed','failed','aborted') NOT NULL,
  failure_code      VARCHAR(64)  NULL,
  crumbs_considered INT          NOT NULL DEFAULT 0,
  crumbs_selected   INT          NOT NULL DEFAULT 0,
  policy_version    VARCHAR(32)  NOT NULL,
  redaction_version VARCHAR(32)  NOT NULL,
  started_at        DATETIME(6)  NOT NULL,
  finished_at       DATETIME(6)  NOT NULL,
  actor_id          VARCHAR(255) NOT NULL,
  actor_kind        ENUM('human','agent') NOT NULL,
  actor_model       VARCHAR(128) NULL,
  session_id        VARCHAR(128) NULL,
  KEY ix_harvests_time (finished_at),
  CONSTRAINT ck_harvests_failure CHECK (outcome = 'completed' OR failure_code IS NOT NULL),
  CONSTRAINT ck_harvests_ok      CHECK (outcome <> 'completed' OR failure_code IS NULL),
  CONSTRAINT ck_harvests_counts  CHECK (crumbs_considered >= 0
      AND crumbs_selected >= 0 AND crumbs_selected <= crumbs_considered),
  CONSTRAINT ck_harvests_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE crumbs (
  id                CHAR(40)     NOT NULL PRIMARY KEY,
  content           TEXT         NOT NULL,
  content_hash      CHAR(64)     NOT NULL,
  review_state      ENUM('candidate','accepted','rejected') NOT NULL DEFAULT 'candidate',
  confidence        DECIMAL(4,3) NOT NULL,
  captured_at       DATETIME(6)  NOT NULL,
  harvest_id        CHAR(40)     NULL,
  policy_version    VARCHAR(32)  NULL,
  redaction_version VARCHAR(32)  NOT NULL,
  actor_id          VARCHAR(255) NOT NULL,
  actor_kind        ENUM('human','agent') NOT NULL,
  actor_model       VARCHAR(128) NULL,
  session_id        VARCHAR(128) NULL,
  KEY ix_crumbs_state_time (review_state, captured_at),
  KEY ix_crumbs_session (session_id, captured_at),
  UNIQUE KEY uq_crumbs_hash_session (content_hash, session_id),
  CONSTRAINT fk_crumbs_harvest FOREIGN KEY (harvest_id) REFERENCES harvests(id) ON DELETE RESTRICT,
  CONSTRAINT ck_crumbs_conf CHECK (confidence >= 0 AND confidence <= 1),
  CONSTRAINT ck_crumbs_size CHECK (CHAR_LENGTH(content) > 0 AND CHAR_LENGTH(content) <= 4096),
  CONSTRAINT ck_crumbs_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0))),
  CONSTRAINT ck_crumbs_harvest_policy CHECK (harvest_id IS NULL OR policy_version IS NOT NULL)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
-- uq_crumbs_hash_session dedupes repeated automatic capture within one session.
-- session_id is NULL for human captures and unique keys permit repeated NULLs,
-- so a human may deliberately capture the same text twice. That is intended.
-- ck_crumbs_size is the database-level statement that a Crumb is a fragment,
-- not a transcript.

CREATE TABLE crumb_review_events (
  id          CHAR(40)     NOT NULL PRIMARY KEY,
  crumb_id    CHAR(40)     NOT NULL,
  from_state  ENUM('candidate','accepted','rejected') NOT NULL,
  to_state    ENUM('candidate','accepted','rejected') NOT NULL,
  rationale   TEXT         NOT NULL,
  occurred_at DATETIME(6)  NOT NULL,
  actor_id    VARCHAR(255) NOT NULL,
  actor_kind  ENUM('human','agent') NOT NULL,
  actor_model VARCHAR(128) NULL,
  session_id  VARCHAR(128) NULL,
  KEY ix_cre_crumb (crumb_id, occurred_at),
  CONSTRAINT fk_cre_crumb FOREIGN KEY (crumb_id) REFERENCES crumbs(id) ON DELETE CASCADE,
  CONSTRAINT ck_cre_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE harvest_crumbs (
  harvest_id CHAR(40) NOT NULL,
  crumb_id   CHAR(40) NOT NULL,
  role       ENUM('considered','selected') NOT NULL,
  PRIMARY KEY (harvest_id, crumb_id),
  KEY ix_hc_crumb (crumb_id),
  CONSTRAINT fk_hc_harvest FOREIGN KEY (harvest_id) REFERENCES harvests(id) ON DELETE CASCADE,
  CONSTRAINT fk_hc_crumb   FOREIGN KEY (crumb_id)   REFERENCES crumbs(id)   ON DELETE CASCADE
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE insights (
  id            CHAR(40)     NOT NULL PRIMARY KEY,
  head_revision INT          NOT NULL DEFAULT 1,
  created_at    DATETIME(6)  NOT NULL,
  actor_id      VARCHAR(255) NOT NULL,
  actor_kind    ENUM('human','agent') NOT NULL,
  actor_model   VARCHAR(128) NULL,
  session_id    VARCHAR(128) NULL,
  KEY ix_insights_time (created_at),
  CONSTRAINT ck_insights_head CHECK (head_revision >= 1),
  CONSTRAINT ck_insights_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE insight_revisions (
  id                 CHAR(40)     NOT NULL PRIMARY KEY,
  insight_id         CHAR(40)     NOT NULL,
  revision           INT          NOT NULL,
  title              VARCHAR(512) NOT NULL,
  content            MEDIUMTEXT   NOT NULL,
  content_hash       CHAR(64)     NOT NULL,
  class              VARCHAR(64)  NOT NULL,
  confidence         DECIMAL(4,3) NOT NULL,
  rationale          TEXT         NULL,
  harvest_id         CHAR(40)     NULL,
  parent_revision_id CHAR(40)     NULL,
  created_at         DATETIME(6)  NOT NULL,
  actor_id           VARCHAR(255) NOT NULL,
  actor_kind         ENUM('human','agent') NOT NULL,
  actor_model        VARCHAR(128) NULL,
  session_id         VARCHAR(128) NULL,
  UNIQUE KEY uq_rev (insight_id, revision),
  UNIQUE KEY uq_rev_identity (insight_id, id),
  KEY ix_rev_class (class, created_at),
  CONSTRAINT fk_rev_insight FOREIGN KEY (insight_id) REFERENCES insights(id) ON DELETE RESTRICT,
  CONSTRAINT fk_rev_parent  FOREIGN KEY (insight_id, parent_revision_id)
                            REFERENCES insight_revisions(insight_id, id) ON DELETE RESTRICT,
  CONSTRAINT fk_rev_harvest FOREIGN KEY (harvest_id) REFERENCES harvests(id) ON DELETE RESTRICT,
  CONSTRAINT ck_rev_conf CHECK (confidence >= 0 AND confidence <= 1),
  CONSTRAINT ck_rev_number CHECK (revision >= 1),
  CONSTRAINT ck_rev_size CHECK (CHAR_LENGTH(content) > 0 AND CHAR_LENGTH(content) <= 262144),
  CONSTRAINT ck_rev_lineage CHECK (revision = 1
      OR (parent_revision_id IS NOT NULL AND rationale IS NOT NULL)),
  CONSTRAINT ck_rev_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
-- fk_rev_parent is composite so a revision cannot inherit from another Insight's
-- lineage. A NULL parent_revision_id skips the check (MATCH SIMPLE), which is
-- what revision 1 needs.

CREATE TABLE insight_crumbs (
  revision_id CHAR(40) NOT NULL,
  crumb_id    CHAR(40) NOT NULL,
  PRIMARY KEY (revision_id, crumb_id),
  KEY ix_ic_crumb (crumb_id),
  CONSTRAINT fk_ic_rev   FOREIGN KEY (revision_id) REFERENCES insight_revisions(id) ON DELETE RESTRICT,
  CONSTRAINT fk_ic_crumb FOREIGN KEY (crumb_id)    REFERENCES crumbs(id)            ON DELETE RESTRICT
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
-- RESTRICT is deliberate: the database refuses to prune a Crumb that supports an
-- Insight. harvest_crumbs CASCADEs because a harvest's "considered" list is
-- bookkeeping, not lineage.

CREATE TABLE refs (
  id         CHAR(40)      NOT NULL PRIMARY KEY,
  kind       VARCHAR(64)   NOT NULL,
  locator    VARCHAR(1024) NOT NULL,
  workspace  VARCHAR(255)  NOT NULL DEFAULT '',
  label      VARCHAR(512)  NULL,
  meta       JSON          NULL,
  fetched_at DATETIME(6)   NULL,
  created_at DATETIME(6)   NOT NULL,
  UNIQUE KEY uq_refs_identity (kind, locator, workspace),
  KEY ix_refs_kind (kind),
  CONSTRAINT ck_refs_locator CHECK (CHAR_LENGTH(kind) > 0 AND CHAR_LENGTH(locator) > 0)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
-- The domain concept is "Reference". The physical table is `refs` because
-- `references` is a reserved word: only the backticked form parses in Dolt, and
-- a Go raw string literal cannot contain a backtick.

CREATE TABLE ref_links (
  record_kind  ENUM('crumb','insight_revision','promotion_proposal','validation') NOT NULL,
  record_id    CHAR(40) NOT NULL,
  reference_id CHAR(40) NOT NULL,
  relation     ENUM('source','evidence','subject','spawned-work') NOT NULL,
  created_at   DATETIME(6) NOT NULL,
  PRIMARY KEY (record_kind, record_id, reference_id, relation),
  KEY ix_rl_ref (reference_id),
  KEY ix_rl_record (record_kind, record_id),
  CONSTRAINT fk_rl_ref FOREIGN KEY (reference_id) REFERENCES refs(id) ON DELETE RESTRICT
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
-- record_id is polymorphic and therefore has no FK. Its referential integrity is
-- a ledger invariant plus the `bdc doctor` orphan scan. ix_rl_record is what
-- makes both the prune-time cleanup and that scan a keyed lookup.

CREATE TABLE validations (
  id                 CHAR(40)     NOT NULL PRIMARY KEY,
  target_kind        ENUM('crumb','insight_revision','promotion_proposal') NOT NULL,
  target_id          CHAR(40)     NOT NULL,
  verdict            ENUM('unreviewed','supported','disputed','rejected','superseded') NOT NULL,
  rationale          TEXT         NOT NULL,
  superseded_by_kind ENUM('insight_revision','promotion_proposal') NULL,
  superseded_by_id   CHAR(40)     NULL,
  occurred_at        DATETIME(6)  NOT NULL,
  actor_id           VARCHAR(255) NOT NULL,
  actor_kind         ENUM('human','agent') NOT NULL,
  actor_model        VARCHAR(128) NULL,
  session_id         VARCHAR(128) NULL,
  KEY ix_val_target (target_kind, target_id, occurred_at),
  CONSTRAINT ck_val_supersede CHECK (verdict <> 'superseded'
      OR (superseded_by_kind IS NOT NULL AND superseded_by_id IS NOT NULL)),
  CONSTRAINT ck_val_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
-- Absence of any row means "unreviewed". Current state is the latest row by
-- occurred_at. Rows are never updated. They are deleted only with the pruned
-- Crumb they target.

CREATE TABLE authorities (
  id                  CHAR(40)      NOT NULL PRIMARY KEY,
  target_kind         ENUM('insight_revision','promotion_proposal') NOT NULL,
  target_id           CHAR(40)      NOT NULL,
  level               ENUM('advisory','default','mandatory') NOT NULL,
  scope               VARCHAR(255)  NOT NULL DEFAULT '',
  destination_kind    VARCHAR(64)   NULL,
  destination_locator VARCHAR(1024) NULL,
  rationale           TEXT          NOT NULL,
  occurred_at         DATETIME(6)   NOT NULL,
  actor_id            VARCHAR(255)  NOT NULL,
  actor_kind          ENUM('human','agent') NOT NULL,
  actor_model         VARCHAR(128)  NULL,
  session_id          VARCHAR(128)  NULL,
  KEY ix_aut_target (target_kind, target_id, occurred_at),
  CONSTRAINT ck_aut_mandatory_human CHECK (level <> 'mandatory' OR actor_kind = 'human'),
  CONSTRAINT ck_aut_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
-- ck_aut_mandatory_human is the database-level enforcement of "only a human
-- grants mandatory". The ledger rejects it first with a typed error; the
-- constraint is the assertion that stays live if that check is ever bypassed.

CREATE TABLE promotion_proposals (
  id                     CHAR(40)      NOT NULL PRIMARY KEY,
  insight_id             CHAR(40)      NOT NULL,
  revision_id            CHAR(40)      NOT NULL,
  class                  VARCHAR(64)   NOT NULL,
  dest_kind              VARCHAR(64)   NOT NULL,
  dest_locator           VARCHAR(1024) NOT NULL,
  dest_workspace         VARCHAR(255)  NOT NULL DEFAULT '',
  dest_capabilities      SET('requires-human-authority','supports-supersession',
                             'supports-review-thread','append-only','stable-anchor',
                             'content-addressable') NOT NULL DEFAULT '',
  content                MEDIUMTEXT    NOT NULL,
  content_hash           CHAR(64)      NOT NULL,
  confidence             DECIMAL(4,3)  NOT NULL,
  requested_authority    ENUM('advisory','default','mandatory') NOT NULL DEFAULT 'advisory',
  supersedes_proposal_id CHAR(40)      NULL,
  policy_version         VARCHAR(32)   NOT NULL,
  redaction_version      VARCHAR(32)   NOT NULL,
  created_at             DATETIME(6)   NOT NULL,
  actor_id               VARCHAR(255)  NOT NULL,
  actor_kind             ENUM('human','agent') NOT NULL,
  actor_model            VARCHAR(128)  NULL,
  session_id             VARCHAR(128)  NULL,
  UNIQUE KEY uq_pp_hash (content_hash),
  KEY ix_pp_insight (insight_id, created_at),
  KEY ix_pp_dest (dest_kind, dest_locator(255)),
  CONSTRAINT fk_pp_insight FOREIGN KEY (insight_id) REFERENCES insights(id) ON DELETE RESTRICT,
  CONSTRAINT fk_pp_rev     FOREIGN KEY (insight_id, revision_id)
                           REFERENCES insight_revisions(insight_id, id) ON DELETE RESTRICT,
  CONSTRAINT fk_pp_super   FOREIGN KEY (supersedes_proposal_id)
                           REFERENCES promotion_proposals(id) ON DELETE RESTRICT,
  CONSTRAINT ck_pp_conf CHECK (confidence >= 0 AND confidence <= 1),
  CONSTRAINT ck_pp_size CHECK (CHAR_LENGTH(content) > 0 AND CHAR_LENGTH(content) <= 262144),
  CONSTRAINT ck_pp_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
-- fk_pp_rev is composite so a proposal cannot pair Insight A with a revision of
-- Insight B. uq_pp_hash is what makes idempotency a database property rather
-- than a code convention; the hashed tuple is defined in internal/ledger.
-- There is deliberately no proposal-to-Crumb join table: a proposal names one
-- revision, and that revision's supporting Crumbs are `insight_crumbs`, which is
-- immutable once written. Proposal-level evidence is not derivable and attaches
-- as ref_links with record_kind='promotion_proposal'.

CREATE TABLE promotions (
  id          CHAR(40)     NOT NULL PRIMARY KEY,
  proposal_id CHAR(40)     NOT NULL,
  attempt     INT          NOT NULL,
  status      ENUM('proposed','applied','rejected','failed','superseded') NOT NULL,
  detail      TEXT         NULL,
  occurred_at DATETIME(6)  NOT NULL,
  actor_id    VARCHAR(255) NOT NULL,
  actor_kind  ENUM('human','agent') NOT NULL,
  actor_model VARCHAR(128) NULL,
  session_id  VARCHAR(128) NULL,
  UNIQUE KEY uq_prm_attempt (proposal_id, attempt),
  KEY ix_prm_status (status, occurred_at),
  CONSTRAINT fk_prm_pp FOREIGN KEY (proposal_id) REFERENCES promotion_proposals(id) ON DELETE RESTRICT,
  CONSTRAINT ck_prm_detail CHECK (status NOT IN ('rejected','failed') OR detail IS NOT NULL),
  CONSTRAINT ck_prm_attempt CHECK (attempt >= 1),
  CONSTRAINT ck_prm_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

CREATE TABLE receipts (
  id            CHAR(40)      NOT NULL PRIMARY KEY,
  promotion_id  CHAR(40)      NOT NULL,
  kind          VARCHAR(64)   NOT NULL,
  locator       VARCHAR(1024) NOT NULL,
  anchor        VARCHAR(512)  NULL,
  external_hash CHAR(64)      NULL,
  verified      TINYINT(1)    NOT NULL DEFAULT 0,
  reference_id  CHAR(40)      NULL,
  recorded_at   DATETIME(6)   NOT NULL,
  actor_id      VARCHAR(255)  NOT NULL,
  actor_kind    ENUM('human','agent') NOT NULL,
  actor_model   VARCHAR(128)  NULL,
  session_id    VARCHAR(128)  NULL,
  UNIQUE KEY uq_rcp_promotion (promotion_id),
  CONSTRAINT fk_rcp_prm FOREIGN KEY (promotion_id) REFERENCES promotions(id) ON DELETE RESTRICT,
  CONSTRAINT fk_rcp_ref FOREIGN KEY (reference_id) REFERENCES refs(id)       ON DELETE RESTRICT,
  CONSTRAINT ck_rcp_prov CHECK (CHAR_LENGTH(actor_id) > 0 AND (actor_kind = 'human'
      OR (CHAR_LENGTH(COALESCE(actor_model,'')) > 0 AND CHAR_LENGTH(COALESCE(session_id,'')) > 0)))
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;

-- Seeds. Automatic harvesting is opt-in and off by default; manual is the
-- default operating mode, not a special one.
INSERT INTO repo_config (k, v, updated_at) VALUES
  ('harvest.auto',                     '0',                UTC_TIMESTAMP(6)),
  ('authority.agent_may_set_default',  '0',                UTC_TIMESTAMP(6)),
  ('policy.version',                   '1',                UTC_TIMESTAMP(6)),
  ('redaction.version',                '1',                UTC_TIMESTAMP(6)),
  ('redact.patterns',                  '[]',               UTC_TIMESTAMP(6)),
  ('ledger.created_at',                UTC_TIMESTAMP(6),   UTC_TIMESTAMP(6));

-- Last statement, always: the migration records its version. A later script
-- ends with the REPLACE form of this statement. bdc_version names the release
-- the migration shipped in, not the build that ran it.
INSERT INTO schema_meta (id, version, bdc_version, applied_at)
VALUES (1, 1, '1.0.0', UTC_TIMESTAMP(6));
