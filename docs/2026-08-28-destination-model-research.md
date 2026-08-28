# Generic Durable-Knowledge Destination Model

**Date**: 2026-08-28

**Status**: Research complete; resolves [bdc-7ah.3](beads:bdc-7ah.3)

**Design**: [Beadcrumbs as a Repository-Local Reasoning Ledger](2026-08-27-reasoning-ledger-design.md)

---

## Question

What generic destination model covers Learnings, Memories, Decisions, ADRs,
Policies, lexicon terms, Business and Technical Ontologies, and mappings —
without assuming one Project OS layout — and what belongs in a neutral
Promotion Proposal and receipt?

## Decision

Split the model on two orthogonal axes and let *neither* leak into the other:

1. **Semantic class** — what the knowledge *is* and what force it carries.
   Owned by Beadcrumbs. A closed-for-v1, extensible vocabulary.
2. **Destination descriptor** — *where* a record lands. A `kind` + adapter-owned
   opaque `locator` + declared `capabilities`. Beadcrumbs never parses the
   locator.

v1 ships **no destination adapters**. Instead the promotion flow is
*propose → (an actor writes it) → record receipt*, matching the approved CLI
(`bdc promote propose|record|reject`). Because Beadcrumbs never performs the
external write in v1, docs files, Beads tickets, Linear issues, Slack messages,
and GitHub artifacts are all reachable *today* — the only thing Beadcrumbs needs
is a receipt shape strong enough to prove and re-find what was written.

## The destination descriptor

```
destination:
  kind:        string          # adapter namespace, e.g. "docs", "beads", "linear"
  locator:     string          # opaque to core; adapter-owned addressing
  workspace:   string?         # optional repo/workspace identity (git remote, project id)
  class:       semantic-class  # what force the record carries at this destination
  capabilities: set<flag>      # declared, not inferred (below)
```

`kind` + `locator` + `workspace` is the same identity triple the design already
uses for References (adapter kind, opaque locator, optional workspace). A
destination is a *writable* Reference; keeping the shapes identical means the
reference graph can link a Promotion to its target with no new machinery.

Capability flags, declared per destination (per `kind`, overridable per
descriptor):

| Flag | Meaning | Why core needs it |
|---|---|---|
| `requires-human-authority` | a human must approve before the write counts | gates agent promotion without hardcoding policy per destination |
| `supports-supersession` | destination can express "replaced by X" | decides whether supersession is recorded externally or only in the ledger |
| `supports-review-thread` | destination can hold append-only discussion | decides where validation events live |
| `append-only` | records cannot be edited in place | forbids the "update the existing record" promotion strategy |
| `stable-anchor` | locator remains resolvable indefinitely | decides receipt strength (below) |
| `content-addressable` | destination exposes a hash/revision of written content | enables drift detection without re-reading prose |

Everything else a destination cares about — file paths, heading levels, ticket
types, channel IDs, template sections — is inside the locator or the rendered
content, and is invisible to core.

## Semantic classes

Nine classes cover the stated vocabulary. They differ in force, review, and
supersession, which is exactly why they cannot collapse into one "note" type.

| Class | Force | Review before it counts | Supersession | Freshness expectation |
|---|---|---|---|---|
| `learning` | informational | none | new learning may cite the old | ages gracefully; no expiry |
| `memory` | operational default | none | overwrite-by-recency is acceptable | must be re-validated on use |
| `decision` | binding for a scope | actor with authority for that scope | explicit successor required | valid until superseded |
| `adr` | binding + archival | authority + recorded deciders | explicit `superseded by` status | permanent record; never deleted |
| `policy` | mandatory constraint | human authority (always) | explicit successor + effective date | must be current or it is dangerous |
| `term` | definitional | subject-matter authority | redefinition supersedes | stable; drift is a defect |
| `business-ontology` | definitional + structural | domain authority | versioned successor | stable; schema-like |
| `technical-ontology` | definitional + structural | technical authority | versioned successor | tracks the code; drifts fastest |
| `mapping` | derived correspondence | authority over *both* sides | successor when either side changes | invalidated by either side changing |

Two consequences for the core schema:

- `requires-human-authority` is a **destination** capability, but `policy` (and
  `adr` in most repos) demands it regardless of destination. Authority policy is
  therefore evaluated as `max(class requirement, destination requirement)` — the
  stricter of the two wins. This keeps the design's rule that mandatory
  authority is human-only, without a per-destination allowlist.
- `mapping` is the only class whose validity depends on *two* external things.
  It needs at least two `subject` References, not one. The reference graph
  already allows many typed links per record, so no new relation is required —
  but proposal validation must enforce the arity.

## The neutral Promotion Proposal

Fields, and why each is destination-independent:

| Field | Rationale |
|---|---|
| `insight_id` + `revision` | the exact knowledge being promoted; revisions are immutable so the proposal cannot drift under it |
| `class` | semantic force, chosen by the author, not the destination |
| `destination` | descriptor above |
| `content` | rendered proposal text/structure. Rendered *from the reviewed record*, never from raw input |
| `content_hash` | canonical hash over `(insight_id, revision, class, destination.kind, destination.locator, content)`; the idempotency key |
| `supporting_crumbs[]` | evidence lineage; many-to-many, not consumed |
| `evidence[]` | typed References supporting or challenging |
| `confidence` | author's numeric self-assessment, `0 ≤ c ≤ 1` |
| `provenance` | actor / model / session / policy + redaction version |
| `requested_authority` | what the author is asking this record to establish |
| `supersedes[]` | prior Promotion or Reference this replaces, when the class requires a successor link |

Deliberately **not** in the proposal: file paths, headings, template names,
ticket types, labels, channel IDs, assignees, ADR numbers. All of those are
either inside `destination.locator` or produced by whoever performs the write.

## The receipt

A receipt is the attributable link to a successful external result. To be worth
anything it must let a later reader (a) find the record again and (b) tell
whether it still says what was promoted.

```
receipt:
  promotion_id:   id            # which attempt succeeded
  kind:           string        # from the destination
  locator:        string        # locator of the created record (may differ from the target locator)
  anchor:         string?       # version/revision proof at write time
  external_hash:  string?       # destination-reported content hash, when content-addressable
  recorded_at:    timestamp
  recorded_by:    actor         # who recorded the receipt (may differ from who wrote the record)
  verified:       bool          # whether the recorder observed the record, or is asserting it
```

`anchor` is the field that makes this work across wildly different destinations,
and its strength is exactly the `stable-anchor` capability:

| Destination | `locator` example | `anchor` | Strength |
|---|---|---|---|
| docs file | `docs/2026-08-28-foo-research.md#decision` | git commit SHA | strong; content recoverable forever |
| ADR file | `docs/adr/0007-use-dolt.md` | git commit SHA | strong |
| Beads ticket | `tst-bjt` | `updated_at` from `bd show --json` | strong; ID is stable |
| Beads comment | `tst-bjt/01a048aa-745e-785c-bb89-4f2d96c719f5` | comment `created_at` | strong; comments are append-only |
| GitHub PR/issue comment | API node id or URL | comment id + `updated_at` | strong |
| Linear issue | issue identifier | `updatedAt` | strong |
| Slack message | `channel/ts` | `ts` | **weak** — editable, deletable, retention-limited |

Slack is the reason `verified` and `stable-anchor` exist rather than being
assumed. A destination without a stable anchor gets a receipt that proves *an
attempt happened*, not that a durable record exists — and the ledger should say
so rather than pretend otherwise.

## Prior art checked

**MADR** ([adr.github.io/madr](https://adr.github.io/madr/)) — front matter is
`status`, `date`, `decision-makers`, `consulted`, `informed`; files are
`NNNN-title-with-dashes.md`; supersession is expressed *in the status string*
(`superseded by ADR-0123`). Two lessons: (1) the audience axes (`consulted`,
`informed`) are destination-template concerns, not ledger concerns — they belong
in rendered content, not the proposal; (2) `NNNN` sequence numbers are allocated
*by the destination*, which is precisely why the receipt's `locator` can differ
from the proposal's target locator. A proposal can target `docs/adr/` and get
back `docs/adr/0007-….md`.

**Beads** (`bd 1.2.2`, measured) — a usable ticket destination with no adapter:
`bd create --json` returns the created issue including `id`, and accepts
`--external-ref` (a *single* string), `--metadata` (arbitrary JSON), `--labels`,
`--type decision` (aliased from `adr`), and `--parent`. Relations available for
causality: `blocks|tracks|related|parent-child|discovered-from`. Because
`external_ref` is one string, a back-reference to Beadcrumbs must be a single
opaque token (`bdc:<insight-id>@<revision>`) with anything richer in
`--metadata`. Full command detail in
[Beads JSON contract research](2026-08-28-beads-json-contract-research.md).

**doc-patterns skill** — dated docs are
`docs/YYYY-MM-DD-{feature}-{type}.md`, flat, with cross-reference style fixed by
location (table in `docs/*.md`, blockquote in `ontology/**`, footer links in CLI
docs). This is a per-repository convention, which confirms the docs destination
locator must be repo-supplied, not Beadcrumbs-supplied. Beadcrumbs must not
invent filenames.

## Consequences

- v1 needs no adapter code: `bdc promote propose` emits a proposal (with hash),
  an actor writes the record, `bdc promote record` stores the receipt,
  `bdc promote reject` stores the rejection. The extension seam is the *shape* of
  the descriptor and receipt, not an interface with one implementation.
- The `docs` and `beads` kinds should ship as **documented conventions with
  worked examples**, not as code. That keeps the one-adapter-is-a-hypothetical-
  seam rule intact.
- Idempotency is per `(insight revision, destination kind, destination locator,
  canonical content)`. Re-proposing identical content to the same place is a
  no-op; changing either the content or the destination is a new logical
  proposal.
- Freshness is not a status field. It is `class` (expectation) + `anchor`
  (evidence) + on-demand re-read. Nothing in the ledger caches destination state
  as truth.

## Open risks

| Risk | Mitigation |
|---|---|
| Nine classes is either too many or too few | classes are data, not code paths; adding one must not require a schema migration — store as a validated string with a seed vocabulary |
| `capabilities` becomes a config burden | defaults per `kind`, overridable per descriptor; only `requires-human-authority` and `stable-anchor` are load-bearing in v1 |
| Weak-anchor destinations (Slack) look as durable as docs | `verified` + `stable-anchor` surfaced in `bdc promote` output and in `context` |
| Receipt locator ≠ proposal locator confuses readers | make it explicit in the CLI output and docs; ADR numbering is the canonical example |

## Related Documentation

| Document | Description |
|---|---|
| **[Reasoning ledger design](2026-08-27-reasoning-ledger-design.md)** | Approved v1 architecture, Promotion Proposal fields |
| [Reasoning ledger Wayfinder](reasoning-ledger-wayfinder.md) | Portable decision dependency path |
| [Beads JSON contract research](2026-08-28-beads-json-contract-research.md) | Measured `bd --json` surface for the ticket destination |
| [Portable skill install research](2026-08-28-portable-skill-install-research.md) | How the promote step is invoked across agents |
