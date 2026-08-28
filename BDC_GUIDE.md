# bdc command reference

Every `bdc` command, its flags, and the `data` object it returns under `--json`.

**This file is a reference, not the contract.** The contract an agent follows is
[`skills/beadcrumbs/SKILL.md`](skills/beadcrumbs/SKILL.md) — what to capture, when to harvest,
and what to do when a promotion is blocked. Read that first; come here for a flag.

## Envelope

Every command emits one envelope on stdout under `--json`. Prose goes to stderr. The two never
mix, and there is no partial envelope: a write that fails mid-transaction rolls back and reports
one error.

```json
{
  "bdc": "1",
  "command": "reference.list",
  "ok": true,
  "data": {},
  "warnings": [{"code": "beads_unavailable",
                "message": "beads references resolve to their locator; bd is unavailable here: not_installed"}],
  "error": null,
  "meta": {"bdc_version": "1.0.0", "ledger_schema": 1, "generated_at": "2026-08-28T14:00:00.000000Z"}
}
```

On failure `ok` is `false`, `data` is `null`, and `error` is `{"code", "message", "details"}`.

| Exit | Meaning | `error.code` prefix |
|---|---|---|
| 0 | success | — |
| 1 | usage or validation error | `invalid_*` |
| 2 | not found | `not_found` |
| 3 | policy or authority denied | `policy_denied`, `authority_denied`, `authority_required` |
| 4 | ledger busy — lock backoff exhausted | `ledger_busy` |
| 5 | no ledger | `no_ledger` |
| 6 | storage or integrity error | `storage_*`, `integrity_*` |
| 7 | redaction abort — nothing persisted | `redaction_failed` |
| 8 | adapter error | `adapter_*` |

Exit 4 is retryable and exit 7 is not: a redaction abort means the text itself has to change.

## Global flags

| Flag | Effect |
|---|---|
| `--json` | emit the envelope on stdout |
| `--actor` | who is acting; recorded as provenance (default: the OS user) |
| `--actor-kind` | `human` or `agent`. Only a human may grant `mandatory` authority |
| `--model` | the acting agent's model identifier |
| `--session` | session identifier for grouping provenance |
| `-C`, `--directory` | run as if started in this directory |
| `--quiet` | suppress warnings on stderr |
| `--no-enrich` | skip optional tracker enrichment |

---

## Ledger

### `bdc init`
`--stealth` (default) `--visible` `--force`

Creates the ledger at `<git-common-dir>/beadcrumbs`, which every linked worktree resolves to and
`git status` never sees. `--visible` uses `<main-worktree>/.beadcrumbs` and adds it to
`.git/info/exclude`. `--force` replaces a target directory that holds no Dolt database.

→ `{path, stealth, schema_version, created}`

### `bdc doctor`
`--verbose`

→ `{checks[], schema_version, journal_bytes, ledger_path, beads, ok}` — each check is
`{name, status, detail}`, and `beads` is what the optional tracker detection found
(`null` under `--no-enrich`).

doctor reports health inside the envelope and always exits 0 with `error: null` — a ledger it
cannot open is what it exists for. Branch on `data`, never on the exit code: `ledger_present`
failing is "no ledger here", `ledger_lock` failing is another process holding the engine, and
`schema_version` failing is a mismatch `bdc migrate` repairs.

### `bdc migrate`

Applies the schema migrations this build ships and the ledger has not. Idempotent: a current
ledger reports `from == to` and applies nothing. This is the repair for a `schema_version`
mismatch — `bdc init` is not, because it returns early on an existing ledger. A ledger newer
than this build cannot be repaired downward; upgrade bdc.

→ `{from, to, applied[]}`

### `bdc version`

→ `{version, schema_version, dolt_driver, go, platform}`. Runs without opening the engine, so it
answers even when the ledger is busy or broken.

### `bdc backup <dest-url>` / `bdc restore <src-url>`
restore: `--force` (required when a ledger already exists)

Backup carries history, not just the head. Restore stages into a temporary directory and swaps
atomically; an interrupted restore leaves the original intact and `doctor` reports the leftovers.

→ `{destination, bytes, schema_version}` / `{restored, schema_version, records}`

### `bdc gc`

Reclaims the Dolt chunk journal. → `{before_bytes, after_bytes, duration_ms}`

---

## Crumbs

### `bdc capture <text|->`
`--confidence` (0–1, default 0.5) `--ref kind:locator[@relation]` (repeatable) `--from-file`

One fragment per Crumb: a correction, a discovery, a rejected approach, external feedback.
Redaction runs before the write. A finding it cannot resolve aborts with exit 7 and nothing
persisted. Transcript-shaped and oversize input is refused rather than truncated.

→ `{crumb}`

### `bdc crumb list`
`--state candidate|accepted|rejected` (repeatable) `--since` `--session` `--limit` `--offset`

`--since` takes RFC3339, a date, or a duration like `24h`. → `{crumbs[], total}`

### `bdc crumb show <id>`
`--events`

→ `{crumb, review_events[], references[], harvests[], insights[]}`. `harvests` and `insights` can
both be non-empty at once — a Crumb is never consumed by being harvested.

### `bdc crumb review <id>...`
`--state accepted|rejected` (required) `--rationale` (required)

Append-only. Reviewing again adds an event; it never rewrites one. → `{crumbs[], events[]}`

### `bdc crumb prune`
`--id` (repeatable) `--before` `--state candidate` `--yes` (required)

Retention, not erasure — see [README](README.md#retention-not-erasure). Refuses any state but
`candidate`, and refuses a Crumb that feeds a Harvest. → `{pruned, pruned_ids[], blocked[]}`

---

## Harvest and Insights

### `bdc harvest`
`--crumb` (repeatable) `--since` `--title` `--content`/`--content-file` `--class` `--confidence`
`--auto` `--dry-run`

Weighs Crumbs and synthesises them into a revisioned Insight. With no `--title`/`--content`/
`--class` it records what it weighed and stops — the right answer when there are fragments but no
conclusion yet. `--auto` marks the Harvest automatic; that mode is opt-in per repository.

→ `{harvest, insight, revision, crumbs_captured[], redaction:{version, findings}}`

Classes: `learning`, `memory`, `decision`, `adr`, `policy`, `term`, `business-ontology`,
`technical-ontology`, `mapping`.

### `bdc insight list`
`--class` `--since` `--verdict` `--authority` `--limit` `--offset` (all repeatable filters)

→ `{insights[], total}`

### `bdc insight show <id>`
`--revision` `--lineage`

→ `{insight, revision, revisions[], crumbs[], references[], validations[], authorities[], proposals[], lineage[]}`

### `bdc insight revise <id>`
`--content`/`--content-file` `--rationale` (required) `--title` `--class` `--confidence` `--crumb`

A revision preserves the prior revision, its derivation, and its evidence. Unset fields carry
forward. → `{insight, revision}`

---

## Verdict and authority

These are two axes, not one. A `supported` verdict says the reasoning holds; an authority level
says how far a record binds. Both are append-only histories with an effective value.

### `bdc validate <target-id>`
`--verdict unreviewed|supported|disputed|rejected|superseded` `--rationale` (required)
`--evidence kind:locator` `--superseded-by`

→ `{validation, effective_verdict}`

### `bdc authority <target-id>`
`--level advisory|default|mandatory` `--scope` `--destination kind:locator` `--rationale` (required)

`mandatory` requires `--actor-kind human`; an agent attempting it is denied with exit 3, and that
refusal is a live runtime assertion, not a convention. → `{authority, effective_level}`

---

## References

Locators are opaque. Beadcrumbs never parses one, and there are no tracker-specific columns.

### `bdc reference add <target-id>`
`--kind` (required) `--locator` (required) `--workspace` `--relation source|evidence|subject|spawned-work`
`--label`

→ `{reference, link}`

### `bdc reference list`
`--target` `--kind` `--relation` `--limit` `--refresh`

`--refresh` re-observes the cache through an installed enricher. The cache is never
authoritative. → `{references[]}`, each with a `freshness`

---

## Promotion

### `bdc promote propose`
`--insight` (required) `--class` (required) `--destination kind:locator` (required) `--revision`
`--workspace` `--capability` (repeatable) `--evidence` (repeatable) `--content`/`--content-file`
`--authority` `--supersedes` `--confidence`

Never performs an external write and never calls an adapter. Idempotent by canonical content
hash — re-proposing identical content to the same destination returns the existing proposal with
`created:false`, enforced by a unique index rather than by convention.

Blocked by authority: the proposal is still recorded, but the response is `ok:false`, exit 3,
`error.code:"authority_required"`, `error.details:{proposal_id, content_hash, created, authority_required}`.
Grant with `bdc authority` and retry against that `proposal_id`. Do not work around it.

Capabilities are declared, never inferred: `requires-human-authority`, `supports-supersession`,
`supports-review-thread`, `append-only`, `stable-anchor`, `content-addressable`.

→ `{proposal, created, content_hash, authority_required}`

### `bdc promote record <proposal-id>`
`--locator` (required) `--anchor` `--external-hash` `--verified`

The receipt for a write that landed. `--verified` means the recorder observed the written record
rather than asserting it. → `{promotion, receipt, durable}`

### `bdc promote reject <proposal-id>` — `--rationale` (required)
A human decided not to write it. → `{promotion}`

### `bdc promote fail <proposal-id>` — `--detail` (required)
The write was attempted and did not land. The proposal stays retryable; the next `record` or
`fail` is attempt *n+1*. → `{promotion}`

`record`, `reject`, and `fail` are the three terminal outcomes of one attempt. A proposal left at
`proposed` forever is the failure mode the last two exist to prevent.

### `bdc promote list`
`--insight` `--status` `--destination-kind` `--limit`

→ `{proposals[]}`, each with `attempts[]` and `receipt`

---

## Narrative

All three bound their output with `--budget`, in approximate tokens; the default is **4000** and
`--budget 0` means the whole answer. `prime` never drops a mandatory Insight to fit a budget — it
emits a `budget_exceeded` warning instead, so a thin-looking `prime` is a warning to read.

| Command | Flags | `data` |
|---|---|---|
| `bdc prime` | `--budget` | `{summary, working_defaults[], mandatory[], cautions[]}` |
| `bdc context` | `--since` `--insight` `--limit` `--budget` | `{summary, insights[], open_questions[], recent_crumbs[], promotions[]}` |
| `bdc handoff` | `--since` `--budget` | `{summary, state, unreviewed_crumbs, open_proposals[], workspace}` |

---

## Hooks (optional)

Never part of the contract. See [docs/guides/hooks.md](docs/guides/hooks.md).

### `bdc hooks install` / `uninstall`
`--force` `--auto-harvest` (install only — the per-repository opt-in to automatic harvesting)

Writes chained `pre-push` and `post-merge` shims, preserving any pre-existing hook and its exit
status. → `{hooks[], chained[], auto_harvest}`

### `bdc hooks run <hook>`

A hook that cannot get the lock waits longer than a normal command and then **exits 0** with one
line on stderr naming what it skipped. A hook must never fail a `git push`, and a hook that
silently swallows the miss is how a harvest gets lost. → `{hook, action, result}`

---

## Beads

If `bd` is on PATH with a workspace, `beads:` references are enriched with title and status
through supported `bd --json` commands. Beadcrumbs never reads Beads' database. Absence,
staleness, and a missing workspace are distinguishable states, all of them warnings — never an
error, and never a blocked write. `--no-enrich` skips the adapter entirely.
