# Binary Size and Cloud-Agent Operating Models — Research

**Date:** 2026-08-29 · **Branch:** `feat/dolt-reasoning-ledger-v1` (PR #18)

| Document | Description |
|---|---|
| **[Dolt operating model research](2026-08-28-dolt-operating-model-research.md)** | Why embedded Dolt; where the size and lock constraints came from |
| **[Reasoning ledger design](2026-08-27-reasoning-ledger-design.md)** | v1 architecture |
| [Stealth mode guide](guides/stealth-mode.md) | Where the ledger lives today |
| [Dolt reasoning ledger v1 plan](2026-08-28-dolt-reasoning-ledger-v1-plan.md) | Implementation slices |

Two questions. Q1: why is `bdc` this big, and is Beads the same. Q2: how do agents on other
machines and in cloud sandboxes share a ledger.

Every number below is command output from this machine: macOS 25.6 arm64, go1.27.0,
dolt 2.3.1, `bd` 1.2.2 (Homebrew), icu4c@78 78.3.

---

## Q1 — Binary size

### Measured

| Binary | Bytes | Link | Stripped |
|---|---:|---|---|
| `./bdc` from `make binary` | 148,611,730 | dynamic ICU | **no** — `__DWARF` present, 147,645 symbols |
| `bdc -ldflags="-s -w"` | 107,692,434 | dynamic ICU | yes |
| `bdc -tags icu_static -ldflags="-s -w"` (the release build) | 142,353,458 | none — system libs only | yes |
| `/opt/homebrew/bin/bd` (Beads 1.2.2) | 135,222,384 | dynamic ICU | yes — 902 symbols, no `__DWARF` |
| `/opt/homebrew/bin/dolt` (2.3.1) | 116,829,456 | dynamic ICU | yes — 910 symbols |

The 100–135 MB range in the question is three different builds. `make binary` is a debug build
and is the largest thing on the list at 149 MB; `scripts/build-release.sh` already passes
`-tags icu_static -trimpath -ldflags "-s -w"` and produces 142 MB.

**README drift:** `README.md` says "~135 MB". The static build measures 142.4 MB today.

### What dominates

Mach-O segments of `./bdc` (`size -m`):

| Section | Size | What it is |
|---|---:|---|
| `__text` | 34.9 MB | machine code |
| `__gopclntab` | 31.1 MB | Go PC-to-line tables — scales with function count, not with logic |
| `__noptrdata` | 22.9 MB | static data (below) |
| `__DWARF` (compressed) | ~21.8 MB | debug info; `-w` drops it |
| `__LINKEDIT` | 14.2 MB | mostly the symbol table; `-s` drops most of it |
| `__go_type` | 9.5 MB | reflection type descriptors |
| `__rodata` | 7.2 MB | |
| `__noptrbss` | 33.7 MB | zero-fill, **not on disk** (32 MB of it is `crypto/internal/fips140/drbg.memory`) |

**The single biggest identifiable line item is not code.** `go tool nm -size`, `D`-class symbols:

```
21.15 MB  92.9%  github.com/dolthub/go-mysql-server/sql/encodings
```

277 `..gobytes` blobs — MySQL character-set and collation tables. 21.2 MB of the binary, ~15% of
the release artifact, is MySQL charset data that a single-database repo-local ledger will never
consult beyond `utf8mb4_0900_bin`.

Machine code (`__text`, 33.3 MB) by module:

| Module | `__text` | Share |
|---|---:|---:|
| `dolthub/go-mysql-server` | 6.28 MB | 18.8% |
| Go stdlib + runtime | 5.58 MB | 16.7% |
| `aws/aws-sdk-go-v2` | 4.57 MB | 13.7% |
| `dolthub/dolt` | 4.21 MB | 12.6% |
| `envoyproxy/go-control-plane` | 2.86 MB | 8.6% |
| `dolthub/vitess` | 1.15 MB | 3.5% |
| grpc + protobuf | 1.76 MB | 5.3% |
| C/cgo glue (ICU regex, gozstd) | 0.68 MB | 2.0% |

So the shape is: **SQL engine ≈ 28 MB (code + charset tables) > Dolt storage ≈ 5.5 MB > cloud
object-store SDKs ≈ 13 MB > ICU**. Summed across non-BSS symbols, the cloud/RPC stack
(aws, azure, gcp, oci, aliyun, grpc, protobuf, xds, otel) is **13.1 MB — 12%**. That matters for
Q2: Dolt's remote backends are already linked in, at zero marginal cost.

ICU is a link-time choice, not a code-size problem: dynamic 107.7 MB → static 142.4 MB, a
**+34.7 MB** delta, almost all of it `libicudata`. The static build is the right trade — it is the
difference between a binary that runs anywhere and one that hard-codes
`/opt/homebrew/opt/icu4c@78/lib/…` (`otool -L`).

### Compression

| Artifact | Raw | `gzip -9` | `xz -9` |
|---|---:|---:|---:|
| `bdc` static, stripped (shipped) | 142.4 MB | **50.9 MB** | **26.2 MB** |
| `bdc` dynamic, stripped | 107.7 MB | 37.4 MB | 17.6 MB |
| `bd` 1.2.2 | 135.2 MB | 45.4 MB | — |

`scripts/build-release.sh` writes `tar -czf`. Switching release archives to `tar.xz` halves the
download, 51 MB → 26 MB, for one line in the build script and one in `install.sh`. That is the
only size lever worth pulling.

UPX is **not** recommended — not measured here, and the reasons are structural: it rewrites the
Mach-O with a self-extracting stub, which invalidates code signing and lands the binary squarely in
the Gatekeeper slow path `scripts/install.sh` already works around, and it decompresses the whole
image on every invocation against a cold-start budget currently measured in tens of milliseconds.

### Does size matter here

Not much, and not where you would guess. `bdc` is installed once per machine, not per repository;
the ledger itself is small (a fresh one is 200 KB, 540 KB after 30 captures, 260 KB after
`bdc gc`). The one place the number bites is **ephemeral cloud sandboxes and CI that install per
run** — which is exactly Q2. There, the 51 MB → 26 MB archive change is worth more than any
build-flag tuning.

### Is Beads the same

Yes, and slightly worse. `go version -m /opt/homebrew/bin/bd`:

```
path  github.com/steveyegge/beads/cmd/bd
dep   github.com/dolthub/dolt/go      v0.40.5-0.20260605230755-1bf533220ab0
dep   github.com/dolthub/driver/v2    v2.1.4
dep   github.com/dolthub/go-mysql-server v0.20.1-0.20260605175459-433dbaebc97f
dep   github.com/dolthub/go-icu-regex v0.0.0-20260412212219-49724d547866
```

**Beads 1.2.2 embeds Dolt through the same driver family Beadcrumbs uses** — `dolthub/driver/v2`
v2.1.4 against `dolthub/dolt/go` (a June 2026 pseudo-version; Beadcrumbs pins a May one). Same
`go-mysql-server`, same `go-icu-regex`, and the Homebrew build links ICU dynamically
(`otool -L` shows `libicui18n.78.dylib` and siblings), so it is *not* portable off a machine with
`icu4c@78` at that prefix — the same trap the operating-model doc documents for a default `bdc`
build.

`bd` is 135 MB against `bdc`'s 108 MB at equal build flags, on 213 embedded dependencies versus
154. The extra ~27 MB is Beads' own surface, not Dolt: a Bubble Tea TUI stack
(`charm.land/bubbletea/v2`, `lipgloss`, `glamour`, `huh`), `alecthomas/chroma`,
`anthropics/anthropic-sdk-go`, `google/go-github`.

The operating models differ where it counts: Beads' `bd init` defaults to **embedded Dolt**
(`.beads/embeddeddolt/`) but keeps `bd init --server` for an external `dolt sql-server`
(`.beads/dolt/`), and `bd dolt start|stop|status` manages that daemon's lifecycle. Beadcrumbs has
no server mode at all. Beads pays for that flexibility in the exact ways the operating-model doc
predicted: its troubleshooting docs describe sandbox environments where it cannot control the
server, and its own guidance is "never run raw `dolt` CLI commands while the Dolt server is
running. It causes journal corruption."

---

## Q2 — Cloud agents across worktrees and machines

### What v1 does today

Read from `internal/store/dolt/discover.go`, `cmd/bdc/init.go`, and `bdc --help`.

**Stealth (default).** `<git-common-dir>/beadcrumbs/bdc/.dolt`. Verified on a scratch repo:

```
.git/beadcrumbs/bdc/.dolt/config.json
.git/beadcrumbs/bdc/.dolt/repo_state.json
.git/beadcrumbs/bdc/.dolt/noms/manifest
.git/beadcrumbs/bdc/.dolt/noms/journal.idx
.git/beadcrumbs/bdc/.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv   ← chunk journal
$ git status --porcelain      (empty)
```

**`--visible` does not mean committed.** `Location.VisibleDir()` is `<main-worktree>/.beadcrumbs`,
and `addExclude` appends `/.beadcrumbs/` to `.git/info/exclude`. The directory is inside the
working tree; Git is told to ignore it, in a file that is itself never committed and is shared
across worktrees. Nothing in v1 puts the ledger into a commit. `--visible` buys visibility to
*other tools* — a file browser, a backup script — and nothing else.

**Sync surface today:** `bdc backup <url>` (`CALL DOLT_BACKUP('sync-url', ?)`) and
`bdc restore <url> --force` (`CALL DOLT_BACKUP('restore', ?, ?)`). Restore is a whole-database
aside-and-swap. There is no merge, no `bdc sync`, no `push`, no `pull`, no `remote`. Two machines
today means one machine's ledger overwrites the other's.

### Committing the Dolt store to Git: confirmed a bad idea

Not for the reason usually given. Storage is fine at this scale — ten sequential snapshots of a
540 KB ledger packed to 240 KB, because the chunk journal is append-only and Git deltas it well.

The reason is that **it cannot merge**. Two worktrees, three captures each, committed as two
branches:

```
$ git merge A
warning: Cannot merge binary files: bdc/.dolt/noms/journal.idx (HEAD vs. A)
CONFLICT (content): Merge conflict in bdc/.dolt/noms/journal.idx
CONFLICT (content): Merge conflict in bdc/.dolt/noms/manifest
warning: Cannot merge binary files: bdc/.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv
CONFLICT (content): Merge conflict in bdc/.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv
Automatic merge failed
```

There is no resolution: picking a side discards the other machine's crumbs, and no merge driver
can do better without reimplementing Dolt. Two further strikes — `bdc gc` rewrites the store into
freshly-named content-addressed `.darc` files (`519,390 → 245,759` bytes in 42 ms), so every GC is
a whole-store rewrite that Git cannot delta and whose predecessors live in history forever; and
committing the store defeats the stealth default outright.

**Deny.** The Dolt store never goes into a Git commit.

### The good version of that idea: Git-backed Dolt remotes

Dolt supports using an ordinary Git repository as a remote's backing store, keeping its data under
`refs/dolt/data` — a ref namespace `git clone`, `git fetch`, and `git push` never touch. This is
first-party and present in the exact module Beadcrumbs already links
(`libraries/doltcore/env/git_remote_url.go`: `git+file`, `git+http`, `git+https`, `git+ssh`).
Verified end to end here:

```
$ dolt remote add gitorigin git+file:///tmp/gitremote.git
$ dolt push gitorigin main
 * [new branch]          main -> main

$ git --git-dir=/tmp/gitremote.git for-each-ref
351dc2ea…  commit  refs/dolt/data
b40690be…  commit  refs/heads/__dolt_remote_info__
d3b7ede7…  commit  refs/heads/main

$ dolt clone git+file:///tmp/gitremote.git bdc
356 of 356 chunks complete.
$ dolt sql -q "select count(*) from crumbs"     →  31
```

A plain `git clone` of that repository does **not** fetch the ledger (`refs/dolt/data` is absent
from the clone's refs; `.git` is 232 KB). Two caveats, both measured: the remote repository must
already have a commit (`git remote has no branches: cannot push … initialize the repository with
an initial branch/commit first`), and Dolt creates `refs/heads/__dolt_remote_info__`, which *does*
show up in every collaborator's `git branch -r`. That is the one stealth leak in an otherwise
invisible scheme.

Other schemes are equally available, from the `dolt remote` help text: `http`, `https`, `aws://`,
`s3://` (any S3-compatible store with conditional writes — R2, MinIO — no DynamoDB table needed),
`gs://`, `file://`, and `oci` registered in `dbfactory` though undocumented in the help. DoltHub is
the default host when a remote is given as `<org>/<repo>`; public databases are free, private free
to 100 MB then $5/month.

### Merge semantics for this schema

Dolt's docs state the rule: "Conflicts are detected on a cell-level"; a conflict requires "two
operations modify the same row, column pair to be different values".

Every Beadcrumbs primary key is a UUIDv7 minted locally
(`internal/ledger/ids.go`, `mint`). Two clones therefore insert into disjoint row identities and
cannot produce a cell conflict. Measured: two clones with three private crumbs each, pushed and
pulled through a `file://` remote —

```
$ dolt pull origin main
Merge branch 'main' of file:///tmp/bdcremote into main
$ select count(*) from crumbs     →  36        (30 shared + 3 + 3)
$ select * from dolt_conflicts               (empty)
$ select * from dolt_constraint_violations   (empty)
```

**The one real hazard is a natural-key unique index, and it reproduces.** `refs` carries
`UNIQUE KEY uq_refs_identity (kind, locator, workspace)`. Two machines that both reference
`internal/parse.go` mint two different `ref_…` UUIDs for one identity. Rows merge; constraints do
not:

```
$ dolt pull origin main
Auto-merging refs
CONSTRAINT VIOLATION (content): Merge created constraint violation in refs
Automatic merge failed; 1 table(s) are unmerged.

$ select * from dolt_constraint_violations
+-------+----------------+
| table | num_violations |
| refs  | 2              |
```

`uq_pp_hash` on `promotion_proposals` and `uq_crumbs_hash_session` have the same shape. The fix is
a schema decision, not a sync feature: **mint identity-bearing ids deterministically** — a hash of
`kind|locator|workspace` — so both machines produce the same primary key and the merge becomes an
idempotent no-op. That is a migration, so it should be decided before sync ships, not after.

One more thing found by running it: `bdc` sets the commit identity through the connector
(`open.go`: `CommitName = "beadcrumbs"`, `CommitEmail = "bdc@localhost"`), not in
`.dolt/config.json` — which is literally `{}`. The `dolt` CLI consequently refuses to merge in that
directory (`fatal: empty ident name not allowed`) until a local config is added. In-process sync
inherits identity for free; anything that shells out to `dolt` would not.

### In-process feasibility: proven

All the procedures needed are registered in the Dolt module `bdc` already depends on
(`dprocedures/init.go`: `dolt_remote`, `dolt_push`, `dolt_pull`, `dolt_fetch`, `dolt_clone`,
`dolt_merge`, `dolt_backup`). A throwaway module against `github.com/dolthub/driver v1.88.1` — the
exact dependency in `go.mod` — run against a real `bdc` ledger:

```
DOLT_REMOTE add -> <nil>
DOLT_PUSH  -> status=0 message="To file:///tmp/premote\n * [new branch]  main -> main"
DOLT_PULL  -> fast_forward=0 conflicts=0 message="merge successful"
crumbs: 31 -> 32
```

No `dolt` binary, no daemon, no new dependency, no new bytes: the object-store and RPC backends
are already 12% of the binary.

### What a cloud mode needs

1. **Remote configuration that survives having no ledger.** `repo_config` lives *inside* the
   ledger, which a fresh sandbox does not have. So: `$BDC_REMOTE`, falling back to deriving
   `git+ssh://…` from `git remote get-url origin`. Zero config for the common case.
2. **`bdc clone` / `bdc bootstrap`** — the cloud-agent entry point, and the only new command that
   must run *before* `no_ledger_uninitialized`. `CALL DOLT_CLONE` into a staging directory, then
   the aside-and-swap `restore.go` already implements.
3. **`bdc remote add|list|remove`, `bdc push`, `bdc pull`, `bdc sync`** (pull-then-push), each one
   engine-open → one bounded operation → close, with their own lock budget the way `backup`,
   `restore`, and `gc` already set theirs.
4. **Conflict reporting as a first-class exit code.** `DOLT_PULL` returns `conflicts`; constraint
   violations arrive separately. Surface both, and teach `bdc doctor` to read `dolt_conflicts` and
   `dolt_constraint_violations`.
5. **Non-interactive credentials.** `DOLT_REMOTE_PASSWORD` with `--user` for a sql-server
   remotesapi; `$DOLT_ROOT_PATH` pointing at a mounted `~/.dolt/creds/*.jwk` for DoltHub; the AWS
   SDK chain for `s3://`/`aws://`; ssh-agent or a credential helper for `git+ssh`/`git+https`. For
   a cloud sandbox that already has a Git token, `git+https://` is the least new machinery.
6. **Decide the stealth trade.** A Git-backed remote publishes
   `refs/heads/__dolt_remote_info__` to everyone who clones. Stealth-critical users get a
   separate repo, an `s3://` remote, or a `file://` remote on shared storage.

**Scope: roughly two to three days of mechanics.** `internal/store/dolt/remote.go` (~250 lines,
structurally a sibling of `backup.go`), a clone/bootstrap path reusing `restore.go`'s swap
(~150), `cmd/bdc/sync.go` (~250), doctor additions (~80), and a two-clone divergence fixture on the
existing `main_test.go` harness. The schedule risk is not the code — it is item 5's credential
matrix and the deterministic-ref-id migration, which is a design decision, not an implementation.

### Alternatives, and why not

| Option | Verdict |
|---|---|
| Shared `dolt sql-server` on a host | Rejected as the *primary store* in the operating-model doc, and nothing has changed. As a *remote* it is viable — `--remotesapi-port` serves clone/pull/push, `--remotesapi-readonly` restricts it — but it is a service to operate. |
| Commit a `.beadcrumbs` JSONL export to Git | This is the design Beads itself demoted. Their docs: "It is not the canonical cross-machine sync channel… JSONL import is upsert-only; it cannot infer that records absent from an export were deleted, pruned, or simply never exported." It also discards Dolt history, which is the provenance model. |
| DoltHub hosted | Fine as an option, wrong as a default for a tool whose premise is repo-local. Free public, 100 MB free private, then $5/month. |

---

## How Beads solves this

Verified by running `bd` 1.2.2 locally; docs read at `github.com/gastownhall/beads` `main`
(the `steveyegge/beads` URL 301-redirects there; the Go module path is still `steveyegge`).

`bd sync`, `bd remote`, `bd push`, `bd pull`, `bd cloud`, `bd serve`, and `bd daemon` **do not
exist** — every one returns `unknown command`. Sync lives entirely under `bd dolt`:

```
$ bd dolt --help
Version control:
  bd dolt commit       Commit pending changes
  bd dolt push         Push commits to Dolt remote
  bd dolt pull         Pull commits from Dolt remote
Remote management:
  bd dolt remote add <name> <url>   Add a Dolt remote
```

Their sync doc states the model: "Cross-machine sync uses Dolt remotes: `bd dolt push` /
`bd dolt pull`. For normal git-hosted projects, the Dolt remote can be the same `origin` URL used
for source code. Dolt stores issue history under `refs/dolt/data`, separate from source branches" —
and `bd init` auto-detects `git remote get-url origin` to configure it. JSONL is explicitly
demoted: "`.beads/issues.jsonl` is an export… not the canonical cross-machine sync channel."

The fresh-machine entry point is `bd bootstrap`, and its help text is the design worth copying:

> Bootstrap auto-detects the right action: • If `sync.remote` is configured: clones from the
> remote • If git origin has Dolt data (`refs/dolt/data`): clones from git and wires origin for
> future push/pull … This is the recommended command for: • Setting up beads on a fresh clone •
> **Recovering after moving to a new machine** … Non-interactive mode … **also auto-detected when
> stdin is not a terminal or `CI=true` is set.**

Worktrees are a non-problem for them for the same reason as for Beadcrumbs — "All worktrees in the
same repository use the same beads workspace", via git-common-dir discovery. Notably, Beads once
had the *other* half of Beadcrumbs' design and removed it: "Older beads versions had an
experimental `sync.branch` workflow that created hidden worktrees such as
`.git/beads-worktrees/<branch>/`. That workflow has been removed."

**Cloud agents are Beads' documented gap.** There is no mention of Devin, Claude Code cloud, Codex
cloud, Codespaces, or dev containers anywhere in the repo, and no example CI workflow that
bootstraps a database and pushes results back. What exists is primitives: `--sandbox` ("disables
Dolt auto-push"), `--readonly` ("for worker sandboxes"), `--server-socket` for sandboxes "where
file-level access control is simpler than network allowlists", and a troubleshooting note that
"bd auto-detects sandboxed environments and prints `Sandbox detected, using direct mode`", with the
resolution being "sync manually once outside the sandbox". Their FAQ answer for CI is just "run
commands — embedded mode is the default".

## Recommendation

1. **Mirror Beads on the mechanism, because it is Dolt's own mechanism and the code is already
   linked.** One Dolt remote per repository, defaulted from `git remote get-url origin` as
   `git+ssh://…`, data under `refs/dolt/data`. Implement it in-process through `DOLT_REMOTE` /
   `DOLT_PUSH` / `DOLT_PULL` — proven above — never by shelling out to `dolt`.
2. **Do not mirror their command surface.** `bd dolt push` leaks the storage engine into the CLI.
   Beadcrumbs' vocabulary should be `bdc sync` (pull, then push), with `bdc push` / `bdc pull` for
   the halves and `bdc clone` as the fresh-sandbox entry point.
3. **Fix ref identity first.** Deterministic ids for tables with natural-key unique indexes
   (`refs`, `promotion_proposals`) turn the only reproducible merge failure into a no-op. It is a
   migration; do it before sync ships.
4. **Take the lane Beads left open.** The cloud-agent story —
   `bdc clone` at session start, `bdc sync` at harvest, non-interactively, in a container with a
   Git token and nothing else — is documented nowhere in Beads. It is a small amount of code on
   top of item 1 and the clearest differentiator available.
5. **Ship `tar.xz` release archives** (51 MB → 26 MB) and correct the README's 135 MB to 142 MB.
   That is the whole of the size work worth doing; the binary is what embedded Dolt costs, Beads
   pays 135 MB for the same thing, and it is amortised per machine rather than per repository.
