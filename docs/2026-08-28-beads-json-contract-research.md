# Optional Beads Reference Integration Contract

**Date**: 2026-08-28

**Status**: Research complete; resolves [bdc-7ah.4](beads:bdc-7ah.4)

**Design**: [Beadcrumbs as a Repository-Local Reasoning Ledger](2026-08-27-reasoning-ledger-design.md)

**Measured against**: `bd version 1.2.2 (Homebrew)`, embedded-Dolt stealth workspace

---

## Question

What stable contract should the optional Beads integration use for canonical
issue references, on-demand enrichment, handoff context, and spawned-work
causality — using supported `bd --json` commands only, never Beads' Dolt tables?

## Decision

Nine commands, all `--json`, all non-interactive, all invoked with
`-C <repo-root> --readonly` for reads. Detection is a single command
(`bd where --json`). Version guarding is a single command
(`bd version --json`). Everything Beads knows beyond a stable ID stays *outside*
the core reference model and is re-fetched on demand with explicit freshness
metadata.

## The command surface

| Purpose | Command | Notes |
|---|---|---|
| Detect a workspace | `bd where --json` | the only detection primitive needed |
| Version guard | `bd version --json` | structured; `bd --version` is prose |
| Repo/backend context | `bd context --json` | for handoff context and worktree awareness |
| Resolve one reference | `bd show --json <id>` | returns an **array**, even for one ID |
| Enumerate | `bd list --json [filters]` | rich filter set; `--limit 0` for unlimited |
| Read discussion | `bd comments <id> --json` | append-only thread |
| Write a note back | `bd comment <id> --json --stdin` | `--stdin`/`--file` avoids arg-quoting hazards |
| Create spawned work | `bd create --json …` | returns the created issue incl. `id` |
| Record causality | `bd link <id1> <id2> --type discovered-from` | see relations below |

Beadcrumbs performs no other Beads command. `bd sql`, `bd dolt`, `bd export`,
and any direct `.beads/embeddeddolt` access are out of contract by design.

## Measured JSON shapes

### `bd where --json` (exit 0)

```json
{
  "database_path": "/…/beadcrumbs/.beads/embeddeddolt",
  "path": "/…/beadcrumbs/.beads",
  "prefix": "bdc",
  "schema_version": 1
}
```

### `bd context --json` (exit 0)

```json
{
  "backend": "dolt",
  "bd_version": "1.2.2",
  "beads_dir": "/…/beadcrumbs/.beads",
  "cwd_repo_root": "/…/beadcrumbs",
  "database": "bdc",
  "dolt_mode": "embedded",
  "is_redirected": false,
  "is_worktree": false,
  "project_id": "fdef2090-e97a-4ea3-911a-90ac71d75f75",
  "repo_root": "/…/beadcrumbs",
  "role": "maintainer",
  "schema_version": 1
}
```

`is_worktree`, `is_redirected`, `dolt_mode`, and `project_id` are the fields
worth surfacing in `bdc handoff` — they tell the next agent whether the tracker
it sees is the same one the previous agent used.

### `bd version --json` (exit 0)

```json
{ "branch": "v1.2.2", "build": "Homebrew", "schema_version": 1, "version": "1.2.2" }
```

### `bd show --json <id>` (exit 0)

An array of issue objects. Observed fields on a real ticket: `id`, `title`,
`description`, `status`, `priority` (int), `issue_type`, `assignee`, `owner`,
`created_at`, `created_by`, `updated_at`, `started_at`, `closed_at`,
`close_reason`, `labels[]`, `dependencies[]`, `parent`, `dependent_count`,
`dependency_count`, `comment_count`.

`dependencies[]` is **not uniform**: on `bd show` it contains *hydrated* related
issues (full title/description/status of the parent, plus `dependency_type`),
while on `bd list` it contains edge records (`issue_id`, `depends_on_id`,
`type`, `created_at`, `created_by`). An adapter must not share one struct
between the two commands.

### `bd list --json` (exit 0)

Array of issue objects — same core fields, no `description` hydration of
relations, plus `external_ref` when set. Defaults to `--limit 50` and excludes
closed issues; `--all` includes closed, `--limit 0` is unlimited. Filters used
by the adapter: `--id`, `--status` (comma-separated only — repeating `-s`
silently overwrites), `--label`/`--label-any`, `--parent`, `--type`,
`--skip-labels`.

### `bd comments <id> --json` (exit 0)

```json
[
  {
    "id": "01a048aa-745e-785c-bb89-4f2d96c719f5",
    "issue_id": "tst-bjt",
    "author": "brianmiller",
    "text": "promotion receipt test",
    "created_at": "2026-08-28T13:58:46Z"
  }
]
```

Comment IDs are UUIDv7 — monotonic and stable, so `<issue-id>/<comment-id>` is a
valid durable locator for a promotion receipt.

### `bd comment <id> --json` (exit 0)

Returns the single created comment (`author`, `created_at`, `id`, `issue_id`,
`schema_version`, `text`).

### `bd create --json` (exit 0)

```json
{
  "created_at": "2026-08-28T13:58:38.624323Z",
  "created_by": "brianmiller",
  "description": "body",
  "external_ref": "bdc:insight-123",
  "id": "tst-bjt",
  "issue_type": "decision",
  "labels": ["beadcrumbs"],
  "owner": "brian@baltoenergy.com",
  "priority": 1,
  "schema_version": 1,
  "status": "open",
  "title": "Test crumb promotion target",
  "updated_at": "2026-08-28T13:58:38.624323Z"
}
```

`--external-ref` is a **single string**, so a back-reference to Beadcrumbs must
be one opaque token (`bdc:<insight-id>@<revision>`); richer payloads go in
`--metadata` (JSON string or `@file.json`). `--type` accepts
`bug|feature|task|epic|chore|decision`, with `adr` aliased to `decision`.

### `bd close <id> --json --reason <text>` (exit 0)

Returns an array of the closed issues including `closed_at` and `close_reason`.

### Timestamp precision differs by command

`bd create --json` and `bd comment --json` emit microsecond precision
(`2026-08-28T13:58:38.624323Z`); `bd list`/`bd show`/`bd comments` emit
second precision (`2026-08-28T13:58:39Z`). Parse as RFC3339 with optional
fractional seconds and never compare timestamps across commands for equality.

## Detection

`bd where --json` is the whole detection story, and it works for stealth and
embedded workspaces because it reports the resolved `.beads` directory
regardless of Git visibility.

Verified: `bd init --prefix tst --stealth` in a fresh repo created
`.beads/embeddeddolt` and wrote `.git/info/exclude` — `git status --short` was
empty afterwards. **A stealth workspace is Git-invisible, so no Git-tracked file
can be used as a detection signal.** `bd where --json` sees it; `git ls-files`
does not.

Detection ladder:

1. `exec.LookPath("bd")` — absent → integration off, no error surfaced to the
   user beyond an informational note.
2. `bd version --json` — parse `version`; compare against a declared minimum.
3. `bd where --json` — exit 0 → workspace present; record `prefix` and
   `database_path`.

`bd doctor` is **not** usable for detection: `bd doctor --json` in an
embedded-mode workspace exits **0** and prints prose
(`Note: 'bd doctor' is not yet supported in embedded mode.`), not JSON.

## Degradation and error handling

Measured with no workspace on `PATH`-resolvable `bd` (cwd outside any repo, and
with `BEADS_DIR=/nonexistent/.beads`):

| Command | Exit | Output |
|---|---|---|
| `bd where --json` | 1 | JSON: `{"error":"no_beads_directory","hint":…,"message":…,"schema_version":1}` |
| `bd context --json` | 1 | JSON: `{"error":"cannot resolve repo context: no .beads directory found","schema_version":1}` |
| `bd list --json` | 1 | **plain text** `Error: no beads database found` + hint lines |
| `bd show --json <id>` | 1 | **plain text** `Error: no beads database found` + hint lines |
| `bd info --json` | 1 | **plain text** `Error: no beads database found` + hint lines |
| `bd comments <id> --json` | 1 | **plain text** `Error: no beads database found` + hint lines |

This asymmetry is the single most important adapter constraint: **`--json` does
not guarantee JSON on failure.** Only `where`, `context`, and `version` emit
structured errors when they fail. Every other command's failure must be treated
as opaque text and wrapped in a bounded adapter error — never parsed, never
pattern-matched on the message.

Missing-ID inside a real workspace is a distinct, well-behaved case:

```
$ bd show --json bdc-9999            # exit 1
Error fetching bdc-9999: no issue found matching "bdc-9999"        # stderr
{"error":"no issues found matching the provided IDs","schema_version":1}  # stdout
```

Prose goes to stderr, JSON to stdout. Confirmed: `bd show --json bdc-9999
2>/dev/null` yields clean JSON. **The adapter must capture stdout and stderr
separately and only ever parse stdout.**

Degradation rules:

1. `bd` absent → integration disabled; stored References resolve to their opaque
   locator and observed label cache only.
2. Workspace absent → same, plus a one-line note in `bdc doctor`.
3. Command fails with unparseable output → bounded adapter error naming the
   command and exit code; core operation still succeeds.
4. Enrichment never blocks a core write. A Crumb, Harvest, Insight, or Promotion
   is never rejected because Beads was unavailable.

## Version guarding

Guard on `version` from `bd version --json`, not on `bd --version` prose. The
JSON payloads carry `schema_version: 1` on the envelope-style outputs
(`where`, `context`, `version`, `create`, `comment`) but **not** on the array
outputs (`list`, `show`, `comments`) — so `schema_version` cannot be the only
guard.

Policy:

- Declare a minimum supported `bd` (1.2.2 as measured).
- Below the minimum → integration disabled with a named reason.
- Above it → proceed, and treat unknown JSON fields as additive (ignore, don't
  fail). Missing *expected* fields are the failure signal.
- Re-check on a version change rather than per command.

Flags to always pass, and why:

| Flag | Reason |
|---|---|
| `-C <repo-root>` | avoids depending on the child process's cwd |
| `--json` | machine output |
| `--readonly` | reads must be provably incapable of mutating the tracker |
| `--sandbox` | on writes, prevents Dolt auto-push side effects |
| `--quiet` | suppresses non-essential prose (does not make errors JSON) |

Do **not** pass `--ignore-schema-skew`: forward schema drift should surface as a
bounded adapter error, not be papered over.

## What the core reference model stores

Per the design, a Reference is `kind` + opaque `locator` + optional workspace +
optional observed label/metadata cache. For Beads:

- `kind`: `beads`
- `locator`: the issue ID (`bdc-7ah.4`) — stable, prefix-scoped
- `workspace`: `project_id` from `bd context --json` (a UUID, stable across
  clones of the same tracker) and/or the repo root
- observed cache: `title`, `status`, `updated_at`, plus `fetched_at`

Everything else Beads knows stays outside core and is fetched on demand:
`priority`, `assignee`, `labels`, `dependencies`, `comment_count`,
`close_reason`, `started_at`, molecule/gate/swarm metadata, `role`. None of it
is competing task truth, and none of it is trusted without `fetched_at`.

## Spawned-work causality

`bd link <id1> <id2> --type <t>` supports
`blocks | tracks | related | parent-child | discovered-from`.

`discovered-from` is the correct edge for Beadcrumbs-spawned work: an Insight
produced a ticket. `bd create --deps 'discovered-from:bdc-7ah.4'` records it at
creation time in one call. Beadcrumbs' own `spawned-work` relation points at the
resulting Reference; the Beads-side `discovered-from` edge is the reciprocal
recorded in the tracker. Both are optional and neither is authoritative for the
other.

`bd dep add` also accepts `external:<project>:<capability>` locators, which is
Beads' own answer to cross-tool references. Beadcrumbs does not use it in v1 —
its opaque-locator References already cover the case, and adopting Beads'
external-ref syntax would make Beads' config (`external_projects`) a dependency.

## Consequences

- The adapter is ~9 command wrappers plus a detection ladder. It is a thin
  seam over a stable CLI, not an abstraction over trackers in general.
- Two distinct issue structs are needed (`show` vs `list` dependency
  hydration), or one struct with the relation field typed as raw JSON.
- Enrichment results must carry `fetched_at` into `bdc context`/`handoff` output
  so a reader can tell live state from cached state.
- Beads is a legitimate *promotion destination* (`kind: beads`) via
  `bd create --json` / `bd comment --json`, using the returned `id` (or
  `<issue-id>/<comment-id>`) as the receipt locator. See
  [Destination model research](2026-08-28-destination-model-research.md).

## Open risks

| Risk | Mitigation |
|---|---|
| `--json` failures are plain text on most commands | never parse failure output; bounded adapter errors keyed on exit code |
| `dependencies[]` shape differs between `show` and `list` | separate types; covered by adapter tests against real `bd` output |
| Timestamp precision varies by command | parse RFC3339 with optional fractional seconds; no cross-command equality |
| `bd` upgrades change field names | version floor + additive-field tolerance + tests that run real `bd` when present, skip when absent |
| `bd doctor` looks like a health check but isn't (embedded mode) | detection uses `bd where --json` only |

## Related Documentation

| Document | Description |
|---|---|
| **[Reasoning ledger design](2026-08-27-reasoning-ledger-design.md)** | Approved v1 architecture and adapter boundary |
| [Reasoning ledger Wayfinder](reasoning-ledger-wayfinder.md) | Portable decision dependency path |
| [Destination model research](2026-08-28-destination-model-research.md) | Beads as a promotion destination |
| [Portable skill install research](2026-08-28-portable-skill-install-research.md) | How agents invoke the enriched commands |
