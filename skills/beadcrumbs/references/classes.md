# The nine semantic classes

A class is the *force* a record carries, chosen by the author and not by the
destination. It is validated: anything outside this list is rejected with exit 1.

| Class | Force | Review before it counts | Supersession | Freshness |
|---|---|---|---|---|
| `learning` | informational | none | a new learning may cite the old | ages gracefully; no expiry |
| `memory` | operational default | none | overwrite-by-recency is acceptable | re-validate on use |
| `decision` | binding for a scope | an actor with authority for that scope | explicit successor required | valid until superseded |
| `adr` | binding and archival | authority plus recorded deciders | explicit "superseded by" status | permanent; never deleted |
| `policy` | mandatory constraint | **always a human** | explicit successor plus effective date | must be current or it is dangerous |
| `term` | definitional | subject-matter authority | redefinition supersedes | stable; drift is a defect |
| `business-ontology` | definitional and structural | domain authority | versioned successor | stable, schema-like |
| `technical-ontology` | definitional and structural | technical authority | versioned successor | tracks the code; drifts fastest |
| `mapping` | derived correspondence | authority over *both* sides | successor when either side changes | invalidated by either side changing |

## Choosing one

Ask what breaks if the record is wrong.

- Nothing breaks; someone is just better informed → `learning`.
- A future session repeats work without it → `memory`.
- A choice was made and later work depends on it → `decision`; if it deserves a
  permanent, numbered, archival record with named deciders → `adr`.
- It constrains what anyone is allowed to do → `policy`.
- A word means something specific here → `term`.
- It defines the shape of the domain → `business-ontology`; of the system →
  `technical-ontology`.
- It says "this thing over here corresponds to that thing over there" →
  `mapping`.

When two fit, pick the weaker one. Promoting a `learning` later costs a second
proposal; retracting a `policy` costs an argument.

## Two rules the CLI enforces

**`policy` always requires a human.** The effective requirement is the strictest
of the class's, the destination's, and the requested authority level's, and
`policy` demands a human whatever the destination declares. A proposal that
needs it and does not have it is still recorded, and the envelope fails with
exit 3 and `error.details.proposal_id`. Retry against that id once a human has
run `bdc authority`. Do not restate the record under a weaker class to get past
it — that is the thing the rule exists to stop.

**`mapping` needs two subjects.** It is the one class whose validity depends on
two external things, so it requires at least two
`--evidence kind:locator@subject` references. Evidence attached under any other
relation supports the mapping; it is not one of the things being mapped.

```
bdc promote propose --insight ins_… --class mapping \
  --destination docs:docs/ontology/mappings.md \
  --evidence sf:Account@subject --evidence foundry:Customer@subject \
  --evidence docs:docs/ontology.md@evidence --content-file - --json
```
