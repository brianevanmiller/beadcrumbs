# Cloud Sync and Harness Provenance — Design

**Date:** 2026-08-29

**Status:** Proposed; depends on v1.0.0 shipping first

**Release target:** v1.0.1 (migration) → v1.1.0 (sync)

| Document | Description |
|---|---|
| **[Cloud harness research](2026-08-29-cloud-harness-research.md)** | Amp, Conductor, Delta evidence and the matrix this design answers |
| **[Binary size and cloud-agent operating models](2026-08-29-binary-size-and-cloud-agents-research.md)** | Dolt remotes proven end to end; the merge blocker |
| [Reasoning ledger design](2026-08-27-reasoning-ledger-design.md) | v1 architecture |
| [Stealth mode guide](guides/stealth-mode.md) | Where the ledger lives |
| [Hooks guide](guides/hooks.md) | Trigger surface this extends |

## Objective

Let one repository's reasoning ledger survive and stay coherent across ephemeral, VM-sandboxed
cloud agents — Amp Orbs, Conductor Cloud workspaces, Delta threads — without leaving the Git
repository the code already lives in, and without a service to operate.

The mechanism is Dolt's own: **a Git-backed Dolt remote**, storing ledger data under
`refs/dolt/data` in the same repository as the source, defaulted from `origin`. It is first-party,
already linked into the binary, and proven end to end in the prior research.

## Outcomes

1. `bdc clone` bootstraps a ledger into a fresh sandbox that has none.
2. `bdc sync` (pull, then push) makes a sandbox's crumbs durable, idempotently.
3. Merges of two independently-written ledgers are additive and produce no constraint violations.
4. Crumbs carry the harness they came from, so parallel agents across platforms remain
   individually recoverable.
5. Zero configuration in the common case: the remote is derived from `git remote get-url origin`.

## Non-goals

- A hosted service, a daemon, or a `dolt sql-server`. The
  [operating model decision](2026-08-28-dolt-operating-model-research.md) stands.
- Committing the Dolt store to Git. It cannot merge; this is settled.
- JSONL export as a sync channel. Beads demoted it and was right to.
- Real-time or continuous sync. Sync happens at durable completion points, not per turn.
- Cross-repository or cross-project aggregation.

---

## 1. Deterministic reference ids (v1.0.1, blocking)

**The problem.** `refs` carries `UNIQUE KEY uq_refs_identity (kind, locator, workspace)`. Two
machines that both reference `internal/parse.go` mint two different `ref_…` UUIDv7 primary keys for
one logical identity. Dolt merges the rows — they are disjoint — and then the unique constraint
fails:

```
CONSTRAINT VIOLATION (content): Merge created constraint violation in refs
```

`uq_pp_hash` on `promotion_proposals` has the same shape.

**The fix.** For tables whose identity is a natural key, derive the id from that key instead of
minting it:

```
ref_<base32(sha256(kind | 0x1f | locator | 0x1f | workspace))[:26]>
```

Both machines produce the same primary key, the merge sees one row written twice with equal values,
and it is an idempotent no-op. `promotion_proposals` derives from `content_hash`, which it already
computes.

`crumbs`, `insights`, `harvests`, and the event tables keep UUIDv7 — they are genuinely distinct
events, and `uq_crumbs_hash_session` is already partitioned by session, so parallel agents do not
collide.

**Why this is a migration and not a flag.** Existing ledgers hold minted `ref_…` ids referenced by
foreign keys in `ref_links`. The migration rewrites those ids and their referents in one
transaction. It must land **before** any sync exists, because a ledger that has already synced with
mixed id schemes cannot be repaired without choosing a winner.

**Interface:** none. `bdc migrate` already exists; this is schema version *n+1*.

## 2. Harness provenance (same migration)

Add `harness VARCHAR(64) NULL` alongside `actor_model` on every provenance-bearing table, and
resolve it in `cmd/bdc/root.go` beside `resolveActorKind`.

Detection is an ordered list of environment markers, first match wins — one place, not five shell
scripts:

| Harness | Marker | Session id from |
|---|---|---|
| `amp-orb` | `AMP_ORB=1` | `AMP_THREAD_ID` |
| `amp` | `AMP_THREAD_ID` | `AMP_THREAD_ID` |
| `conductor-cloud` | `CONDUCTOR_IS_LOCAL=0` | `CONDUCTOR_SESSION_ID` |
| `conductor` | `CONDUCTOR_IS_LOCAL=1` | `CONDUCTOR_SESSION_ID` |
| `delta` | git-common-dir matches `.delta/clones/<id>/` | that `<id>` |
| `claude-code` | `CLAUDE_*` / hook stdin | hook stdin `session_id` |
| `codex` | hook stdin | hook stdin `session_id` |

`$BDC_HARNESS` overrides all of it. Detection **never** promotes `actor_kind` to `agent` on its own
— that still requires a model and a session, for the reason `resolveActorKind` already documents.

Two markers are unverified and must be probed before they are relied on: `AMP_THREAD_ID` in the
agent's own shell (documented only for declared services) and `CONDUCTOR_SESSION_ID` reaching a
hook process. Where detection fails, `harness` is NULL — an honest gap, consistent with how
`actor_model` already degrades.

## 3. The command surface (v1.1.0)

Five commands. The vocabulary is Beadcrumbs', not Dolt's — `bd dolt push` leaks the storage engine
into the CLI and we should not copy it.

```
bdc remote add|list|remove <name> <url>
bdc clone [<url>]        # bootstrap a ledger where none exists
bdc pull  [--remote <n>]
bdc push  [--remote <n>]
bdc sync  [--remote <n>] # pull, then push
```

**Default resolution**, in order: `--remote` → `$BDC_REMOTE` → `repo_config` → derived from
`git remote get-url origin`. Derivation rewrites an `https://`/`ssh://` Git URL to its `git+` form,
so the zero-config case is genuinely zero-config.

**`bdc clone` is the only command that must run before `no_ledger_uninitialized`.** It is the
cloud-sandbox entry point: `CALL DOLT_CLONE` into a staging directory, then the aside-and-swap
`internal/store/dolt/restore.go` already implements. Everything else follows the existing shape —
one short-lived engine, one bounded operation, close — with its own lock budget, exactly like
`backup`, `restore`, and `gc`.

Implementation is in-process through the already-registered `dolt_remote`, `dolt_push`,
`dolt_pull`, `dolt_fetch`, `dolt_clone` procedures. **Never shell out to a `dolt` binary**: `bdc`
sets its commit identity through the connector, and `.dolt/config.json` is `{}`, so the CLI would
fail with `fatal: empty ident name not allowed`.

## 4. Conflict and idempotency semantics

- **Sync is idempotent.** `bdc sync` twice with no intervening writes is a no-op both times.
- **A merge is expected to be additive.** After §1, two ledgers that captured different crumbs
  merge with no conflicts and no constraint violations.
- **Conflicts are a first-class exit code, not a warning.** `DOLT_PULL` returns a conflict count;
  constraint violations arrive separately via `dolt_constraint_violations`. Surface both, exit
  non-zero, and leave the ledger in the un-merged state for inspection. Never auto-resolve — a
  silent resolution of a reasoning ledger discards reasoning.
- **`bdc doctor` learns to read `dolt_conflicts` and `dolt_constraint_violations`**, so a wedged
  ledger reports itself.
- **A push that races loses safely.** Dolt rejects a non-fast-forward push; `bdc sync` pulls and
  retries once, then reports.
- **Hooks never fail the operation that triggered them.** The rule in
  [hooks.md](guides/hooks.md) extends unchanged to sync: a failed push writes one line to stderr
  and exits 0.

## 5. Install recipes

Each is five lines and the same five lines. `bdc init` is idempotent, `bdc clone` is the
fresh-sandbox path, so `clone || init` is the whole bootstrap.

**Amp** — `.agents/setup` (committed, executable, repo root; lands in the ≤72 h snapshot):

```sh
#!/usr/bin/env bash
set -euo pipefail
curl -fsSL https://raw.githubusercontent.com/brianevanmiller/beadcrumbs/v1.1.0/scripts/install.sh | bash
bdc clone || bdc init
```

`.agents/resume` (every wake-up, 10 s budget — a pull only):

```sh
#!/usr/bin/env bash
bdc pull || true
```

Durable completion is the git `pre-push` shim `bdc hooks install` already writes, plus the plugin
`agent.end` event if you want a per-turn reminder.

**Conductor Cloud** — organization settings → **Install software** (once per build; Node 24 and npm
are preinstalled, so the npm package is cheapest):

```sh
npm install -g @beadcrumbs/bdc
echo 'export PATH="$(npm prefix -g)/bin:$PATH"' >> "$HOME/.profile"
```

Repository **Setup script** (per workspace):

```sh
bdc clone || bdc init
```

Note cloud workspaces ignore `.conductor/settings.toml`, so the local half is configured separately
there. **Verify the git auth shim first** — see §7.

**Delta** — `.agents/prepare` (committed, executable, repo root):

```sh
#!/usr/bin/env bash
set -euo pipefail
command -v bdc >/dev/null || curl -fsSL https://raw.githubusercontent.com/brianevanmiller/beadcrumbs/v1.1.0/scripts/install.sh | bash
bdc clone "git+file://$(git rev-parse --git-common-dir)/../../../.git" || bdc init
```

Delta already configures a `local` remote pointing at the user's own repository, so its sync target
is a local path — no network, no token. This is the cleanest of the three.

**All harnesses** — durable completion, unchanged from today plus one flag:

```
bdc hooks install --auto-harvest    # chained pre-push and post-merge shims
```

with the `pre-push` trigger extended to run `bdc sync --push` after a successful harvest.

## 6. Phased plan

| Phase | Contents | Verification |
|---|---|---|
| **1 — v1.0.1** | Deterministic `ref_`/`pp_` ids; `harness` column; migration from schema *n* | Two-clone divergence fixture on the existing `main_test.go` harness: both reference the same file, push and pull through a `file://` remote, assert `dolt_conflicts` and `dolt_constraint_violations` are empty. This is the test that fails today. |
| **2 — v1.1.0a** | `internal/store/dolt/remote.go` (~250 lines, sibling of `backup.go`); `bdc remote`, `push`, `pull`, `sync` | Round-trip through `git+file://`; assert `refs/dolt/data` present on the remote and absent from a plain `git clone`. |
| **3 — v1.1.0b** | `bdc clone` reusing `restore.go`'s aside-and-swap; runs before `no_ledger_uninitialized` | Fresh directory, no ledger, `bdc clone` → crumb count matches source. Kill mid-clone → original state intact. |
| **4 — v1.1.0c** | Conflict reporting; `bdc doctor` reads `dolt_conflicts` / `dolt_constraint_violations`; sync exit codes | Force a cell conflict deliberately; assert non-zero exit, and that `doctor` names it. |
| **5 — docs** | Harness recipes; correct `hooks.md` on Amp's plugin API; note `--visible` under Delta's `core.worktree` layout | — |

Phases 2–4 are the "two to three days of mechanics" the prior research scoped. Phase 1 is the one
with real design content, and it gates everything.

## 7. Risks

- **Conductor Cloud's git auth shim is the highest-risk unknown.** `git` is wrapped
  (`CONDUCTOR_GIT_AUTH_SOCKET`, `CONDUCTOR_REAL_GIT_PATH`) and Dolt's in-process Git client will not
  go through it, so `git+https` may find no credential *even though `git push` works in the same
  shell*. **Probe this before phase 2 ships a `git+https` default there.** Fallbacks: `git+file://`,
  or `git credential fill` at configure time.
- **The stealth leak is real and must be documented, not hidden.** A Git-backed remote publishes
  `refs/heads/__dolt_remote_info__`, visible in every collaborator's `git branch -r`. Users for whom
  that is unacceptable get a separate repository, an `s3://` remote, or `file://` on shared storage.
- **Ephemeral sandboxes lose unsynced work by design.** Conductor Cloud stops at 23 h 50 m and
  survival past that is undocumented; Amp orb disk fate after archive is undocumented. The
  mitigation is behavioural — sync at durable completion — and belongs in the skill body, not only
  in a hook.
- **`bdc gc` after a sync** rewrites the store; its interaction with an existing remote is untested.
  Add a case in phase 4.

## Open decisions

Two, both small, both Brian's:

1. **Does `git+https` from `origin` become the default remote, or must a remote be configured
   explicitly?** Zero-config is the better experience and matches Beads' `bd bootstrap`; explicit
   is the safer default for a tool whose premise is repo-local privacy, given the
   `__dolt_remote_info__` leak.
2. **Does `bdc sync` run automatically at `pre-push` under `--auto-harvest`, or stay manual?**
   Consistent with today's opt-in stance, it should follow the same switch rather than get its own.
