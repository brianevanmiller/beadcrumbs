-- 002_deterministic_refs.sql — schema version 2.
--
-- Two additive changes, one rewrite. ApplyPending runs the ALTERs, then Go
-- rewrites refs.id (and the FKs that store it) from (kind, locator, workspace),
-- then this script re-adds the FKs and records the version. See rewriteReferenceIDs.
--
-- harness is nullable text, not an ENUM: adding a platform must not be a
-- migration. refs and ref_links carry no provenance and get no column.
--
-- The last statement REPLACEs schema_meta. A failed run leaves version 1, so
-- bdc migrate is re-runnable; information_schema guards in Go skip ALTERs that
-- already landed.

ALTER TABLE harvests ADD COLUMN harness VARCHAR(64) NULL;
ALTER TABLE crumbs ADD COLUMN harness VARCHAR(64) NULL;
ALTER TABLE crumb_review_events ADD COLUMN harness VARCHAR(64) NULL;
ALTER TABLE insights ADD COLUMN harness VARCHAR(64) NULL;
ALTER TABLE insight_revisions ADD COLUMN harness VARCHAR(64) NULL;
ALTER TABLE validations ADD COLUMN harness VARCHAR(64) NULL;
ALTER TABLE authorities ADD COLUMN harness VARCHAR(64) NULL;
ALTER TABLE promotion_proposals ADD COLUMN harness VARCHAR(64) NULL;
ALTER TABLE promotions ADD COLUMN harness VARCHAR(64) NULL;
ALTER TABLE receipts ADD COLUMN harness VARCHAR(64) NULL;

ALTER TABLE ref_links DROP FOREIGN KEY fk_rl_ref;
ALTER TABLE receipts DROP FOREIGN KEY fk_rcp_ref;

-- rewriteReferenceIDs runs here.

ALTER TABLE ref_links ADD CONSTRAINT fk_rl_ref FOREIGN KEY (reference_id) REFERENCES refs(id) ON DELETE RESTRICT;
ALTER TABLE receipts ADD CONSTRAINT fk_rcp_ref FOREIGN KEY (reference_id) REFERENCES refs(id) ON DELETE RESTRICT;

REPLACE INTO schema_meta (id, version, bdc_version, applied_at)
VALUES (1, 2, '1.0.1', UTC_TIMESTAMP(6));
