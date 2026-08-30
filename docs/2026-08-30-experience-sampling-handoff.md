# Handoff: implement experience-sampling tracer

You are implementing a feature in **beadcrumbs** (Go, Cobra, embedded Dolt). Work on branch `feat/experience-sampling` (or recreate it from `main` / tag `v1.0.1` if needed). Do not implement anything past the tracer.

## Read first, in this order

1. [docs/2026-08-30-experience-sampling-plan.md](2026-08-30-experience-sampling-plan.md) — the contract. Follow it. The locked decisions section is not optional colour.
2. [skills/beadcrumbs/SKILL.md](../skills/beadcrumbs/SKILL.md) — portable CLI+skill contract you will extend.
3. [cmd/bdc/capture.go](../cmd/bdc/capture.go), [cmd/bdc/promote.go](../cmd/bdc/promote.go), [internal/ledger/store.go](../internal/ledger/store.go), [internal/store/dolt/schema/001_init.sql](../internal/store/dolt/schema/001_init.sql), [internal/ledger/privacy_test.go](../internal/ledger/privacy_test.go), [cmd/bdc/golden_test.go](../cmd/bdc/golden_test.go).

Then implement Phases 1–4 of the plan, in the commit sequence the plan names. Run `make check` (CGO + ICU; see Makefile). Do not skip tests. Do not bump `const version` in `cmd/bdc/root.go`.

## What you are building

A skippable sampling surface:

```
bdc prompts list|show|add|disable
bdc ask enqueue|deliver|answer|skip
bdc ask            # deliver, print the first question
```

Three seeded prompts: `authority-nudge` (human, choice), `calibration` (human, choice), `context-flush` (agent, short-text).

Answers are **Crumbs**. `calibration` also appends a **validation**. `authority-nudge` `grant-default` may append a **`default` authority grant** only when the proposal is not `policy` and did not request `mandatory`. Relayed human answers (process actor is agent, respondent is human) store `via_session` on the **Ask row**, never as a new column on every provenance table. They never grant `mandatory`. They never call `promote reject`.

Empty `ask deliver` is success: `ok: true`, `questions: []`.

Hooks never ask. `bdc prime` JSON is unchanged. Schema 3 is pure SQL; do not add `if m.version == 3` in `applyMigration`.

## Hard rules

- Command bodies never issue SQL and never see a Dolt type. Domain operations live in `internal/ledger`. Storage implements the port in `internal/store/dolt`.
- Redact free text before Write. Classify every new text column in `privacy_test.go`.
- `ParseRepoConfig` must require the new keys (`ask.max_per_session`, `ask.expire_after`). Missing is integrity, not a default.
- Ids: `pmt_` prompts, `ask_` asks, CHAR(40), UUIDv7. Do not reuse `prm_`.
- JSON envelope is a promise. Add keys to `declaredFields()`. Schema 3 will rewrite `meta.ledger_schema` on all goldens from 2 to 3; run `go test ./cmd/bdc -run TestGoldenEnvelope -update` and **read the diff**.
- Exit codes stay 0–8. New errors are `invalid_ask`, `ask_already_open`, existing `not_found` / `invalid_provenance` / `invalid_content` / `invalid_usage`.
- Skill: plain-text question format; agents must **not** export `BDC_ACTOR_KIND=human` to record a relayed tap.
- Do not mention any organisation, customer, or field program in code, comments, commits, or docs.

## Do not build

`ask stats`, MRT/randomisation, adaptive timing, `prompts propose`, organic authoring, paired sampling, answer receipts in `prime`, hook enqueue, a `samples` table, `via_session` on crumbs/validations/authorities, restoring `bdc questions`, changing `prime`'s JSON keys.

If a change is not in Phases 1–4 of the plan, it is out of this PR.

## Done when

Every checkbox in the plan's **Acceptance criteria** is true, including `make check` and the named authority-cap tests.

## Suggested skills for the implementing agent

Call the Skill tool for:

- `tdd` — tests first for ledger operations and CLI goldens
- `codebase-design` — keep `internal/ledger` a deep module; do not leak SQL or Dolt into `cmd/bdc`
