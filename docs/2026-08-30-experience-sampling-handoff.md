# Handoff: implement experience-sampling tracer

You are implementing a feature in **beadcrumbs** (Go, Cobra, embedded Dolt). Check out branch `feat/experience-sampling` at commit `b462f2e` or a descendant — the plan document you must follow lives only on this branch. Do not recreate the branch from `main` or tag `v1.0.1`; if the branch is missing, stop and say so. Do not implement anything past the tracer.

Where this handoff and the plan disagree, the plan wins. Where the plan and the code's existing invariants disagree, stop and flag it — do not improvise.

## Read first, in this order

0. Run the plan's **Prerequisites check** verbatim before writing anything: HEAD descends from `5d22374`; no schema 3 exists anywhere (stop and reconcile if it does); no other branch adds a `003_*.sql`; `make check` is green on HEAD before your first change.
1. [docs/2026-08-30-experience-sampling-plan.md](2026-08-30-experience-sampling-plan.md) — the contract. Follow it. The locked decisions section is not optional colour.
2. [skills/beadcrumbs/SKILL.md](../skills/beadcrumbs/SKILL.md) — portable CLI+skill contract you will extend.
3. [cmd/bdc/capture.go](../cmd/bdc/capture.go), [cmd/bdc/promote.go](../cmd/bdc/promote.go), [internal/ledger/store.go](../internal/ledger/store.go), [internal/ledger/harvest.go](../internal/ledger/harvest.go) (the tx-level write-composition pattern `AnswerAsk` copies), [internal/store/dolt/schema/001_init.sql](../internal/store/dolt/schema/001_init.sql), [internal/ledger/privacy_test.go](../internal/ledger/privacy_test.go), [cmd/bdc/golden_test.go](../cmd/bdc/golden_test.go).

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

Empty `ask deliver` is success: `ok: true`, `data.questions: []`.

Hooks never ask. `bdc prime` JSON is unchanged. Schema 3 is pure SQL; do not add `if m.version == 3` in `applyMigration`.

## Hard rules

- Command bodies never issue SQL and never see a Dolt type. Domain operations live in `internal/ledger`. Storage implements the port in `internal/store/dolt`.
- `AnswerAsk` is **one** `store.Write`. Never call `CaptureCrumb` / `RecordValidation` / `GrantAuthority` from inside it — they stamp `l.actor` and open their own transactions. Follow the plan's **Write composition** section: prepare/redact with explicit respondent provenance before the transaction, compose tx-level inserts inside it. The Go caps in `AnswerAsk` are the only gate on relayed grants — the DB check cannot fire on a row stamped human.
- Redact free text before Write. Classify every new text column in `privacy_test.go`.
- `ParseRepoConfig` must require the new keys (`ask.max_per_deliver`, `ask.expire_after`). Missing is integrity, not a default.
- Ids: `pmt_` prompts, `ask_` asks, CHAR(40), UUIDv7. Do not reuse `prm_`.
- JSON envelope is a promise. Add keys to `declaredFields()`. Schema 3 will rewrite `meta.ledger_schema` on all goldens from 2 to 3; run `go test ./cmd/bdc -run TestGoldenEnvelope -update` and **read the diff**.
- Exit codes stay 0–8. New errors are `invalid_ask`, `ask_already_open`, existing `not_found` / `invalid_provenance` / `invalid_content` / `invalid_usage`.
- Skill: plain-text question format; agents must **not** export `BDC_ACTOR_KIND=human` to record a relayed tap.
- Do not mention any organisation, customer, or field program in code, comments, commits, or docs.

## Do not build

`ask stats`, MRT/randomisation, adaptive timing, `prompts propose`, organic authoring, paired sampling, answer receipts in `prime`, hook enqueue, a `samples` table, `via_session` on crumbs/validations/authorities, restoring `bdc questions`, changing `prime`'s JSON keys, a public `withActor`.

If a change is not in Phases 1–4 of the plan, it is out of this PR.

## Parallelism

The spine is a dependency chain — schema (commit 1) → ledger ops (commits 2–3) → CLI + goldens (commits 4–5) — and cannot be parallelised across workers. What can run concurrently:

- **Track A (spine)**: Phase 1 schema/ids/config/census, then Phase 2 ledger ops and tests, then Phase 3 CLI.
- **Track B (docs)**: the Phase 4 text — SKILL.md sampling section, BDC_GUIDE.md sampling section, workflow.md worked JSON, hooks.md sentence — can be drafted in parallel from the plan's fixed command shapes any time after work starts, but lands as commit 6, after the CLI exists and the worked JSON can be copied from real output.
- **Within Track A, after commit 1 lands**: the prompt registry (`prompt.go` + `cmd/bdc/prompts.go`) and the ask operations (`ask.go`) can split between two workers — ask ops consume the registry's read path, so agree the `Prompts(PromptQuery)` signature first.
- **Never parallel**: the golden regeneration is one serial `-update` run by one worker at the end (schema 2→3 rewrites every golden); and no second schema-3 migration may exist on any branch.

If you are one agent, ignore the tracks and follow the commit sequence.

## Done when

Every checkbox in the plan's **Acceptance criteria** is true, including `make check` and the named authority-cap tests.

## Suggested skills for the implementing agent

Check the available-skills listing first; invoke only names that appear there.

- `codebase-design` — keep `internal/ledger` a deep module; do not leak SQL or Dolt into `cmd/bdc`
- A TDD-style skill if one is listed — otherwise simply write the plan's named ledger tests and CLI goldens before the implementation they pin.
