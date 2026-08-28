# Destinations: conventions, not code

Beadcrumbs ships no destination adapters. A destination is an opaque
`kind:locator` plus the capabilities the *proposer* declares, and the write is
performed by whoever proposed it — you. Core never calls out.

## The descriptor

```
--destination <kind>:<locator>   # kind is a namespace token; locator is opaque to bdc
--workspace <id>                 # optional: which instance of that kind
--capability <name>              # repeatable
```

Capabilities, and what each one changes:

| Capability | Meaning | Effect |
|---|---|---|
| `requires-human-authority` | a human must decide before the write | raises the authority requirement |
| `supports-supersession` | the destination can express "replaced by" | lets `--supersedes` mean something |
| `supports-review-thread` | discussion can be attached | informational |
| `append-only` | records cannot be edited in place | informational |
| `stable-anchor` | the locator stays resolvable indefinitely | decides whether a receipt is `durable` |
| `content-addressable` | the destination reports a hash of what was written | lets `--external-hash` detect drift |

Declare only what is true. `stable-anchor` on a destination that does not have
one turns "an attempt happened" into a false claim of durability.

## Worked example: a docs file

```
bdc promote propose --insight ins_… --class adr \
  --destination docs:docs/adr/ --capability stable-anchor --capability append-only \
  --content-file - --json
```

Target the *directory*, not a filename you invented: the destination allocates
the number. Write the file, commit it, then record what actually landed:

```
git add docs/adr/0007-one-engine-per-command.md && git commit -m "docs: ADR 0007"
bdc promote record pp_… \
  --locator docs/adr/0007-one-engine-per-command.md \
  --anchor "$(git rev-parse HEAD)" --verified --json
```

The receipt's locator legitimately differs from the proposal's: `docs/adr/` is
where it was aimed, `0007-….md` is where it landed. The anchor is the commit
SHA, which is what makes the content recoverable forever.

`--verified` means you observed the record. Without it the receipt says you are
asserting the write, not that you saw it.

## Worked example: a Beads ticket

```
bdc promote propose --insight ins_… --class decision \
  --destination beads:bdc-7ah --workspace "$(pwd)" \
  --capability stable-anchor --capability append-only --content-file - --json

bd comment bdc-7ah --stdin --json <<'EOF'
…the rendered proposal content…
EOF

bdc promote record pp_… \
  --locator bdc-7ah/01a048aa-745e-785c-bb89-4f2d96c719f5 \
  --anchor "<the comment's created_at>" --verified --json
```

Beads comments are append-only with a stable id, so the anchor is strong. A
ticket's own anchor is `updated_at` from `bd show --json`.

When a promotion should also be traceable from the tracker, `bd`'s
`--external-ref` takes a single string: use `bdc:<insight-id>@<revision>` and put
anything richer in `--metadata`.

## A destination with no stable anchor

Chat messages are the standard example: editable, deletable, and subject to
retention. Do not declare `stable-anchor` for them. `bdc promote record` will
return `"durable": false`, which is the honest answer — the receipt proves an
attempt happened, not that a durable record exists. Say that to the user rather
than reporting the promotion as done.

## If the write fails

```
bdc promote fail pp_… --detail "the destination returned 503 twice" --json
```

The proposal stays retryable and the next outcome is attempt *n+1* against the
same proposal. A proposal abandoned at `proposed` is indistinguishable from one
nobody got to.
