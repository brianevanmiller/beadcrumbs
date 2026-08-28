# The full sequence, with worked JSON

Every envelope has the same shape. `data` and `error` are never both populated,
JSON goes to stdout, prose goes to stderr, and warnings survive a failure.

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

## 1. Check the ledger

```
bdc version --json      # {version, schema_version, dolt_driver, go, platform}
bdc doctor --json       # {checks[], schema_version, journal_bytes, ledger_path, beads, ok}
bdc init --json         # only after asking. {path, stealth, schema_version, created}
```

`bdc init` is stealth by default: the ledger lives at `<git-common-dir>/beadcrumbs`
and never appears in `git status`. `--visible` puts it at `<repo>/.beadcrumbs`.

## 2. Capture

```
bdc capture "The engine holds an exclusive directory lock for the life of a command." \
  --confidence 0.7 --ref docs:docs/design.md@source --json
```

```json
{
  "data": {
    "crumb": {
      "id": "crb_01k3q…",
      "content": "The engine holds an exclusive directory lock for the life of a command.",
      "content_hash": "…",
      "review_state": "candidate",
      "confidence": 0.7,
      "captured_at": "2026-08-28T14:00:00.000000Z",
      "redaction_version": "1",
      "actor_id": "you",
      "actor_kind": "agent"
    }
  }
}
```

`--ref kind:locator[@relation]`, repeatable. Relations: `source`, `evidence`,
`subject`, `spawned-work`. `--from-file <path>` and `-` for stdin read the text
instead of taking it as an argument.

Two identical captures in one session resolve to one Crumb. Confidence is
written once and never rewritten — a later judgement is a validation, not an
edit.

If redaction rewrote the text you get a `redacted` warning naming the rule, the
offset, and the length, never the secret. If it could not resolve a finding, the
command exits 7 and nothing was persisted.

## 3. Review

```
bdc crumb list --state candidate --json           # {crumbs[], total}
bdc crumb show <id> --events --json               # {crumb, review_events[], references[], harvests[], insights[]}
bdc crumb review <id> <id> --state accepted --rationale "both hold up" --json
```

`--rationale` is required. Review appends; it never rewrites.

`bdc crumb prune --state candidate --before 720h --yes` is retention, not
erasure: Dolt keeps committed history, so a pruned Crumb stays readable through
that history. Say so rather than implying deletion.

## 4. Harvest

```
bdc harvest --crumb crb_… --crumb crb_… \
  --title "One engine, one transaction, one close" \
  --class decision --confidence 0.8 --content-file - --json
```

```json
{
  "data": {
    "harvest":  {"id": "hrv_…", "mode": "manual", "outcome": "completed",
                 "crumbs_considered": 2, "crumbs_selected": 2},
    "insight":  {"id": "ins_…", "head_revision": 1},
    "revision": {"id": "rev_…", "revision": 1, "class": "decision", "confidence": 0.8},
    "crumbs_captured": [],
    "redaction": {"version": "1", "findings": []}
  }
}
```

`--title`, `--content`, and `--class` travel together: all three, or none. With
none it records what it weighed and stops. `--since 24h` sweeps every
outstanding candidate. `--dry-run` reports what would be synthesised and records
an `aborted` harvest.

## 5. Read and revise an Insight

```
bdc insight list --class decision --verdict supported --json   # {insights[], total}
bdc insight show <id> --lineage --json
bdc insight revise <id> --content-file - --rationale "…" --json
```

`bdc insight show --lineage` returns the Insight, its head revision, every
revision, the Crumbs behind it, its references, validations, authorities, and
proposals. A revision is immutable: `revise` appends a new one and preserves the
derivation and the prior evidence rather than editing anything.

## 6. Judge

```
bdc validate <revision-id> --verdict supported --rationale "reproduced" --json
bdc authority <revision-id> --level default --rationale "how the store is used" --json
```

Four independent axes: confidence (the author's number), evidence (references),
validation (`unreviewed`, `supported`, `disputed`, `rejected`, `superseded`), and authority
(`advisory`, `default`, `mandatory`). None derives from another. Only a human
may grant `mandatory`, and an agent's request for it is refused, never silently
downgraded.

## 7. Promote

```
bdc promote propose --insight ins_… --class decision \
  --destination docs:docs/adr/ --capability stable-anchor \
  --evidence beads:bdc-7ah@subject --content-file - --json
```

```json
{"data": {"proposal": {"id": "pp_…"}, "created": true, "content_hash": "…", "authority_required": "none"}}
```

Proposing identical content to the same destination again returns
`"created": false` and the same proposal id. That is the property a retry
depends on.

When authority is required and not held, the proposal is still recorded and the
envelope fails:

```json
{
  "ok": false, "data": null,
  "error": {
    "code": "authority_required",
    "message": "this proposal requires human authority: class \"policy\", destination docs:docs/policy/ledger.md.",
    "details": {"proposal_id": "pp_…", "content_hash": "…", "created": true, "authority_required": "human"}
  }
}
```

Then write the record yourself and close the attempt — exactly one of:

```
bdc promote record <pp_…> --locator docs/adr/0007-one-engine.md --anchor <sha> --verified --json
bdc promote reject <pp_…> --rationale "the team decided against it" --json
bdc promote fail   <pp_…> --detail "the API returned 503 twice" --json
```

`record` returns `{promotion, receipt, durable}`. `durable` is false when the
destination declared no `stable-anchor`: the receipt then proves an attempt
happened, not that a durable record exists. `fail` leaves the proposal
retryable; the next outcome is attempt *n+1* against the same proposal.

## 8. Read back

```
bdc prime   --json   # {summary, working_defaults[], mandatory[], cautions[]}
bdc context --json   # {summary, insights[], open_questions[], recent_crumbs[], promotions[]}
bdc handoff --json   # {summary, state, unreviewed_crumbs, open_proposals[], workspace}
```

All three take `--budget` in approximate tokens, default 4000, `0` for
everything. Truncation is reported as a `budget_truncated` warning naming what
was dropped; `prime` never drops a mandatory Insight and warns
`budget_exceeded` instead.

## References to a tracker

```
bdc reference add <target-id> --kind beads --locator bdc-7ah --relation subject --json
bdc reference list --target <id> --refresh --json
```

A reference is an opaque `kind:locator` plus a relation. Cached tracker metadata
is never authoritative: every read states its freshness (`live`, `cached`,
`never`). If `bd` is missing the reference still resolves to its locator and the
enrichment is reported as `never`, which is a state and not an error.

`--refresh` warns rather than fails when it cannot observe: `beads_unavailable`
once for the kind when `bd` is missing, too old, or has no workspace here (the
reason is in the message), `no_enricher` once for a kind no adapter serves, and
`enrich_failed` per reference the adapter answered for and could not read.

`bdc doctor --json` reports what detection found under `beads`:
`{present, reason, version, prefix, project_id, repo_root}`, where `reason` is
`ok`, `not_installed`, `version_unreadable`, `below_floor`, or `no_workspace`.
The global `--no-enrich` skips detection entirely and reports `beads: null`.
