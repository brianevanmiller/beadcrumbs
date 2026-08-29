# v1.0.1 — Deterministic Reference ids (and harness provenance)

**Date:** 2026-08-29

**Status:** Executable. Patch on `main` at v1.0.0 (`5ce7eab`). Does not merge PR #19.

**Release:** v1.0.1. Stop at a merge-ready PR; do not merge or tag.

| Source | Use |
|---|---|
| This file | Slice plan: steps, seams, verification |
| [v1 implementation plan](2026-08-28-dolt-reasoning-ledger-v1-plan.md) §2 / §3.3 / §6 | Schema conventions, JSON contract, release-gate matrix — update in place |
| PR #19 (`docs/cloud-harness-research`) | Design input only. Do **not** add `cloud-harness-research.md` or `cloud-sync-design.md` to this branch. Fold the durable rules into code comments, §2/§6, CHANGELOG, and the PR body |

## Why

Two clones merge cleanly over a Dolt git-backed remote **except** `refs`: `UNIQUE KEY uq_refs_identity (kind, locator, workspace)` collides when each clone mints a different UUIDv7 `id` for the same identity. That is the blocker for `bdc sync` (v1.1.0, out of scope). Every other table uses disjoint UUIDv7 primary keys and merges.

`promotion_proposals.uq_pp_hash` has the same shape. **Out of scope** — this patch does not change proposal ids.

## Non-goals

- `bdc remote` / `clone` / `pull` / `push` / `sync`
- Deterministic `pp_` ids
- New CLI flags (no `--harness`)
- Committing PR #19 research docs, or any new long-form design doc beyond this slice plan and the §2/§6 edits
- Re-litigating the v1 product

## Decisions

### D1. Canonical identity string

```
kind || 0x1F || locator || 0x1F || workspace
```

- `0x1F` is the unit separator from the sync design. Reject `0x1F` in `kind`, `locator`, and `workspace` as `invalid_reference` — that is a validation error, not a rewrite. Two different tuples can otherwise concatenate to the same bytes and collide on the primary key while remaining distinct on `uq_refs_identity`. Redaction of locators stays reject-only.
- `workspace` is the stored value. The column is `NOT NULL DEFAULT ''`, so the empty workspace is the empty string, never SQL NULL. Go passes `""`, not a nil.
- No other canonicalization: no trim, no case-fold, no path clean. `utf8mb4_0900_bin` already makes identity byte-exact.

### D2. Id encoding (fits `CHAR(40)`, keeps parsers)

```
ref_ + uuid.UUID(sha256(canonical)[:16]).String()
```

That is `ref_` + 8-4-4-4-12 lowercase hex = 40 characters. It is a hash formatted like a UUID, **not** a UUIDv7. Chosen over the design's base32-26 so that:

- `ParseID` / `assertID` length checks (`prefix + 36`) stay true for `ref_`
- the golden id canonicaliser (`cmd/bdc/golden_test.go` `idPattern`) still matches

Go is the source of truth for the rewrite (`ReferenceIDFor`). A vector test may compare against `SHA2(CONCAT(kind, CHAR(31), locator, CHAR(31), workspace), 256)` formatted the same way, but a mismatch does not change the writer — the migration updates ids in Go so the two cannot drift.

### D3. `Created` is a write result, not an id comparison

`AttachReference` currently does `out.Created = id == minted`. After D2, the minted id **equals** the existing id on an idempotent hit, so that comparison would lie. Change `UpsertReference` to `(id, created, error)` — the same shape as `UpsertProposal`. JSON contract is unchanged: `created` is already `json:"-"` on `AttachResult`; human output still says `attached (reference already known)`.

### D4. Columns that store a Reference id

Grep of `001_init.sql`:

| Column | Kind |
|---|---|
| `refs.id` | PK |
| `ref_links.reference_id` | FK `fk_rl_ref` |
| `receipts.reference_id` | FK `fk_rcp_ref` |

`ref_links.record_kind` is `ENUM('crumb','insight_revision','promotion_proposal','validation')` — it cannot name a Reference. `validations.target_id` / `authorities.target_id` / `superseded_by_id` are the same. The migration rewrites the three columns above and nothing else.

### D5. Harness provenance is additive and nullable

`harness VARCHAR(64) NULL` on every actor-bearing table: `harvests`, `crumbs`, `crumb_review_events`, `insights`, `insight_revisions`, `validations`, `authorities`, `promotion_proposals`, `promotions`, `receipts`. Not on `refs` / `ref_links` (they carry no provenance today).

Closed vocabulary, stored as text not ENUM (adding a harness must not be a migration): `claude-code`, `codex`, `amp`, `conductor`, `delta`, `opencode`, `unknown`.

Detection in `cmd/bdc/root.go` beside `resolveActorKind`. `$BDC_HARNESS` wins if set (invalid value → `invalid_harness`, exit 1). Otherwise first match:

| Value | Marker |
|---|---|
| `amp` | `AMP_ORB` or `AMP_THREAD_ID` |
| `conductor` | `CONDUCTOR_SESSION_ID` or `CONDUCTOR_IS_LOCAL` |
| `delta` | cwd/git-common-dir contains `.delta/clones/` or `.delta/worktrees/` |
| `claude-code` | `CLAUDE_CODE` / `CLAUDECODE` |
| `codex` | `CODEX_HOME` or `CODEX_THREAD_ID` |
| `opencode` | `OPENCODE` / `OPENCODE_DIR` |

No match → SQL NULL, omitted from JSON (`json:"harness,omitempty"`). Detection **does not** fill `session_id` and **does not** promote `actor_kind`. No new flag.

### D6. Domain commands refuse an unmigrated ledger

`prepare` already opens a v1 ledger. After this patch, domain SELECTs and INSERTs name `harness` and would fail as a storage error. If `schema < CurrentSchemaVersion()`, refuse with `integrity_schema_version` naming `bdc migrate`, except:

- `migrate` — the repair
- `doctor` — already reports `schema_version` fail with that remediation
- `backup`, `restore`, `gc` — storage procedures, no domain column lists
- `init`, `version` — no existing-ledger domain read

### D7. Research docs stay out of the repo

PR #19 is docs-only and stays open. This PR's body quotes the canonicalization, the harness table, and the merge proof. `docs/2026-08-28-dolt-reasoning-ledger-v1-plan.md` §2 (id rule + provenance group) and §6 (new test rows) are the in-repo record.

---

## Acceptance → step → verification

| # | Criterion | Step | Proof |
|---|---|---|---|
| A1 | Same `(kind, locator, workspace)` → same `ref_` id on two machines | S1 | `TestReferenceIDDeterministic`; empty vs omitted workspace; case-variant locators stay distinct |
| A2 | Canonical form documented and shared with SQL | S1 | Comment on `ReferenceIDFor`; `TestReferenceIDMatchesSQLHash` |
| A3 | Locators remain reject-only | — | existing `TestSecretInLocatorAbortsWrite`; no new locator rewrite |
| A4 | `reference add` of an existing identity returns the same id, `created=false` | S1, S5 | `TestReferenceIdentityIsKindLocatorWorkspace` (Created flag); golden `reference.add.json` still `{reference, link}` |
| A5 | v1 ledger: doctor says run migrate; migrate rewrites ids + FKs, doctor clean | S2, S3 | `TestDoctorOnV1LedgerNamesMigrate`; `TestMigrateV1RewritesReferenceIDsAndLeavesDoctorClean` |
| A6 | Two clones, same Reference, `DOLT_PULL` → no `dolt_constraint_violations` on `refs` | S4 | `TestTwoClonesSameReferencePullWithoutConstraintViolation` |
| A7 | `harness` nullable, populated from env, JSON omitempty | S3, S6 | `TestHarnessDetection`; port round-trip; privacy census lists `*.harness` as not-free-text; goldens unchanged aside from version/schema |
| A8 | Version 1.0.1, schema 2, CHANGELOG, plan §2/§6 | S7 | goldens, `npm-package/package.json`, `cmd/bdc/root.go` |

---

## Steps

### S1 — Mint deterministic Reference ids

- Add `ReferenceIDFor(kind, locator, workspace)` in [`internal/ledger/ids.go`](internal/ledger/ids.go). Delete `NewReferenceID()`.
- Call it from [`reference.go`](internal/ledger/reference.go), [`crumb.go`](internal/ledger/crumb.go) `insertCrumb`, [`promotion.go`](internal/ledger/promotion.go) (evidence + receipt), [`validation.go`](internal/ledger/validation.go).
- Change `Tx.UpsertReference` to `(ReferenceID, bool, error)` in [`store.go`](internal/ledger/store.go) and [`tx.go`](internal/store/dolt/tx.go). `created` is `sql.ErrNoRows` on the identity lookup, not `id == minted`.
- `assertID` stays `prefix+36` for `ref_`.

### S2 — `002_deterministic_refs.sql` + Go rewrite

Leave `001_init.sql` frozen (it is the v1 snapshot). Fresh `bdc init` applies 001 then 002 via `applyPending`.

Dolt DDL typically auto-commits, so wrapping 002 in `BEGIN` is not the safety net. Protocol:

1. SQL file [`internal/store/dolt/schema/002_deterministic_refs.sql`](internal/store/dolt/schema/002_deterministic_refs.sql) does the ALTERs (add `harness`, drop `fk_rl_ref` / `fk_rcp_ref`, re-add those FKs) and ends with `REPLACE INTO schema_meta … version=2, bdc_version='1.0.1'`.
2. `applyPending` gains a version hook: after 002's statements that drop the FKs and before they are re-added — simplest shape: run 002's ALTERs from SQL, then a Go `rewriteReferenceIDs(ctx, db)` that `SELECT id, kind, locator, workspace FROM refs`, computes `ReferenceIDFor`, `UPDATE`s `refs`, `ref_links`, `receipts` when `old != new`, then the SQL re-adds FKs and REPLACE. If splitting the SQL file is awkward, **do the whole of 002 in Go** (`migrateTo2`) and keep the `.sql` as the embedded comment-and-ALTER source executed statement-wise with `information_schema` guards:
   - skip `ADD COLUMN harness` when the column exists
   - skip `DROP FOREIGN KEY` when the constraint is absent
   - rewrite is a function of identity, so it is idempotent
   - skip `ADD CONSTRAINT fk_*` when the constraint exists
   - `REPLACE schema_meta` last

`schema_meta` last is what makes a failed run re-runnable: version stays 1 until the whole of 002 succeeded. The information_schema guards are what make the re-run not die on `duplicate column`.

Do not list `harness` on existing seed INSERTs — the column is nullable, so `humanProv` does not need a fifth NULL.

### S3 — v1 fixture, doctor, migrate

Do **not** shell out to a v1.0.0 binary. `001_init.sql` still *is* schema 1.

Helper: apply only 001, insert a `refs` row with a UUIDv7-shaped `ref_` id, a `ref_links` row, and a `receipts` row (need the usual crumb/proposal/promotion chain — reuse `schema_test.go` seed helpers). Then:

- `Diagnose` → `schema_version` fail, detail contains `` run `bdc migrate` ``
- `Migrate` → `from=1, to=2, applied=["002_deterministic_refs.sql"]`
- `refs.id` equals `ReferenceIDFor(...)`
- `ref_links.reference_id` and `receipts.reference_id` match
- `Doctor` / `Diagnose` clean, including `polymorphic_targets`

Gate writes (D6) in [`cmd/bdc/root.go`](cmd/bdc/root.go) `prepare`.

### S4 — Two-clone merge (the release gate)

Independent `bdc init`s do not share Dolt history. B must be a clone of a common ancestor, not a second init.

Sequential (process lock is process-global); never shell out to `dolt` (`.dolt/config.json` is `{}`):

1. Init A, add a `file://` remote, `CALL DOLT_PUSH` (common ancestor = schema 2, no refs).
2. Close A. `CALL DOLT_CLONE` (or copy the pushed remote) into B's directory.
3. Open A, insert Reference identity `docs` / `internal/parse.go` / `''` with a **fixed** `created_at`/`label` (cell conflicts on those columns are v1.1.0 merge policy; this release proves the unique-key fix), push, close.
4. Open B, insert the same identity with the same cells (different crumb ids are fine), `CALL DOLT_PULL`.
5. `dolt_constraint_violations` empty (or table missing). `refs` has one row for that identity. `dolt_conflicts` empty under the equal-cell fixture.

Use `Store.DB()` as `history_test.go` does. Close each store before opening the next.

### S5 — JSON contract

- `{reference, link}` unchanged. `created` stays off the envelope.
- `meta.bdc_version` → `1.0.1`, `meta.ledger_schema` → `2` on every golden.
- `version` data: `version=1.0.1`, `schema_version=2`.
- `init` / `doctor` / `migrate` / `backup` / `restore` schema fields → 2. `migrate.json` becomes `from: 2, to: 2, applied: []` (init already applied 002).
- Add `harness` to `declaredFields()` in [`golden_test.go`](cmd/bdc/golden_test.go). Omitempty keeps it off the goldens unless a step sets `$BDC_HARNESS`.
- Regenerate with `go test ./cmd/bdc -run TestGoldenEnvelope -update`.

### S6 — Wire harness through the storage port

- `Provenance.Harness` in [`types.go`](internal/ledger/types.go); `Validate` accepts empty or the closed set.
- `provArgs` / `provScan` / every INSERT and SELECT column list in [`tx.go`](internal/store/dolt/tx.go) and [`snapshot.go`](internal/store/dolt/snapshot.go).
- `cmd/bdc/root.go`: detect, assign onto `ledger.Provenance`.
- [`privacy_test.go`](internal/ledger/privacy_test.go) `notFreeText`: every `*.harness`.
- [`schema_test.go`](internal/store/dolt/schema_test.go) `humanProv` gains a fifth NULL.
- [`port_test.go`](internal/store/dolt/port_test.go) asserts harness round-trips when set and stays empty when not.

### S7 — Bump

- `cmd/bdc/root.go` `version = "1.0.1"`
- `npm-package/package.json` → `1.0.1`
- `CHANGELOG.md` v1.0.1 entry: deterministic `ref_` ids, schema 2 migration, additive `harness`
- Plan §2.2: Reference ids are hash-derived; other ids stay UUIDv7. Provenance group includes `harness`.
- Plan §6: rows for A1, A5, A6, A7.
- Envelope examples in `README.md` / `BDC_GUIDE.md` / `skills/beadcrumbs/references/workflow.md` that hard-code `1.0.0` / `ledger_schema: 1` — update to 1.0.1 / 2 so they match the goldens. No command changes.

---

## Risks

| Risk | Handling |
|---|---|
| Dolt `SHA2` / temp tables differ from MySQL | Vector test first; rewrite ids in Go if SQL disagrees |
| `created_at` cell conflict on a real two-machine merge | In scope for v1.1.0 sync policy, not this patch. Merge test equalizes that cell |
| Partial 002 | `schema_meta` last; rewrite is a function of identity; doctor still names migrate |
| Process-global `Open` lock | Merge test is strictly sequential |
| Golden id regex misses non-UUID `ref_` ids | D2 keeps UUID shape |

## Checks

- `make check` (CGO + ICU via Makefile). Never `go install` / never commit `.beads/` or `./bdc`.
- `go test -race ./...`
- Targeted while building: `go test ./internal/ledger ./internal/store/dolt ./cmd/bdc`
