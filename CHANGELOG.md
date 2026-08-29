# Changelog

## v1.0.1 — 2026-08-29

Schema 2. A v1.0.0 ledger is readable after `bdc migrate`; `bdc doctor` names that command
when the schema is behind.

- **Deterministic Reference ids.** `refs.id` is `ref_` plus the first 16 bytes of SHA-256 over
  `kind || 0x1F || locator || 0x1F || workspace`, formatted as a UUID. Two clones that name the
  same identity mint the same primary key, so a Dolt merge no longer dies on `uq_refs_identity`.
  Locators stay opaque and reject-only. Event ids remain UUIDv7.
- **`harness` provenance.** A nullable `VARCHAR(64)` on every actor-bearing table, populated from
  `$BDC_HARNESS` or an environment marker (`amp`, `conductor`, `delta`, `claude-code`, `codex`,
  `opencode`, `unknown`). Additive JSON (`omitempty`); detection never promotes `actor_kind`.
- **JSON contract.** Envelope `meta.bdc_version` is `1.0.1`, `meta.ledger_schema` is `2`.
  `bdc reference add` is still `{reference, link}`.

## v1.0.0 — 2026-08-28

**A clean break. There is no migration from 0.x, no dual write, and no compatibility shim.** A
0.x ledger is not readable by 1.0 and will not be. If you have prototype data you want, export it
by hand before upgrading; nothing in this release will do it for you.

### The product

Beadcrumbs is a repository-local **reasoning ledger**: Crumbs are captured fragments, a Harvest
synthesises selected Crumbs into a revisioned Insight without consuming them, and a Promotion
Proposal carries an Insight to a durable destination — with `bdc` never performing the external
write and a Receipt recording the anchor that proves it did land. Evidence, confidence,
validation verdict, and authority level stay four independent axes. Review, validation, and
authority are append-only; only a human can grant `mandatory`.

### Storage

- SQLite and JSONL are gone. Storage is **embedded Dolt** through `github.com/dolthub/driver`
  v1.88.1 — one short-lived engine per command, holding an exclusive directory lock for its life.
- The ledger lives at `<git-common-dir>/beadcrumbs`, so every linked worktree resolves to one
  ledger and `git status` never sees it. Stealth is structural rather than configured.
  `bdc init --visible` opts into `<main-worktree>/.beadcrumbs`.
- A normalized 16-table schema enforces its invariants in the database: confidence range,
  non-empty provenance, no orphaned polymorphic targets, no duplicate proposal hash, no
  agent-granted `mandatory` authority.
- `bdc crumb prune` is **retention, not erasure**. Dolt keeps committed history, so a pruned
  Crumb stays readable through `dolt_history_*`. Never capture a secret expecting to prune it.

### Requirements

- **Go 1.26.2** is a hard floor, imposed by `dolthub/driver`.
- **CGO and ICU4C are mandatory.** There is no ICU-free build tag; a missing header is a build
  break, not a silent fallback. macOS needs `brew install icu4c` plus `CGO_CPPFLAGS`/`CGO_LDFLAGS`;
  Linux needs `libicu-dev`.
- **Windows is not supported** — not proven, so not claimed. No Windows job in CI; the installers
  refuse to run there.
- Released binaries are ~135 MB, built with `-tags icu_static` so they carry no non-system
  dynamic libraries. Source builds link ICU dynamically and are not portable between machines.

### CLI

One versioned JSON envelope on every command, prose on stderr, and stable exit codes 0–8.

- **Added**: `capture`, `crumb list|show|review|prune`, `harvest`, `insight list|show|revise`,
  `validate`, `authority`, `reference add|list`, `promote propose|record|reject|fail|list`,
  `context`, `handoff`, `prime`, `backup`, `restore`, `gc`, `migrate`, and
  `hooks install|uninstall|run`. `init`, `doctor`, and `version` were rewritten, not edited.
- `bdc doctor` runs the domain's own invariant checks (`polymorphic_targets`, `head_revision`)
  alongside the storage ones, and reports the optional tracker under `beads` and the per-table
  record counts under `counts`. It is the one command that stays useful on a ledger that will
  not open, so it always exits 0 with the diagnosis in `data` — read `data.ok` and
  `data.checks[]`, not the exit code.
- `bdc migrate` applies the schema migrations a build ships and a ledger has not. It is the
  repair for a `schema_version` mismatch; `bdc init` is not.
- **Removed with no alias and no deprecation shim**: `thread`, `origin`, `origins`, `timeline`,
  `pivots`, `decisions`, `questions`, `feedback`, `trace`, `link`, `list`, `show`, `locate`,
  `spawn`, `import`, `export`, `linear`, `slack`, `github`, `setup`, `upgrade`, `stealth`,
  `unstealth`.
- The Linear, Slack, and GitHub adapters are deleted. v1 ships the destination-neutral
  propose/record shape and no concrete adapter.

### Agents

- A portable skill at `skills/beadcrumbs/`, installable with `npx skills add` into any harness.
  Explicit `bdc` commands and the JSON envelope are the whole cross-agent contract.
- Optional Beads enrichment through supported `bd --json` commands, behind a detection ladder.
  Beadcrumbs never reads Beads' database, and an absent or stale `bd` is a warning, never a
  blocked write. `--no-enrich` skips detection entirely.
- Provenance defaults to `agent` when a run carries both `--model`/`BDC_ACTOR_MODEL` and
  `--session`/`BDC_SESSION`, and an agent's writes are never attributed to `$USER`. `human` is
  the value every authority gate is satisfied by, so it is not the value you get by forgetting.
- Automatic harvesting is **off by default** and opted into per repository with
  `bdc hooks install --auto-harvest`. Redaction runs before any write and its failure aborts with
  nothing persisted.

---

*Releases below are the 0.x prototype, kept as history. Nothing in them is supported by 1.0.*

## v0.10.0 — 2026-04-14

- Architecture improvements inspired by beads 1.0: storage interface extraction, read-only mode for query commands, content hashing for insight dedup, `--json` output on all commands, and `bdc doctor` health check command
- Fix content_hash not being read back from DB in query paths
- Fix pre-existing macOS symlink test failure in TestFindBeadcrumbsDir_Present

## v0.9.0 — 2026-02-24

- Add version and upgrade commands
- GitHub PR comment integration with shared summary formatter
