# Dolt Operating Model — Research and Decision

**Ticket:** `bdc-7ah.1` · **Date:** 2026-08-28 · **Status:** resolved

| Document | Description |
|---|---|
| **[Reasoning Ledger Design](2026-08-27-reasoning-ledger-design.md)** | Approved v1 architecture this decision unblocks |
| [Reasoning Ledger Wayfinder](reasoning-ledger-wayfinder.md) | Portable dependency path |

## Decision

**Embedded Dolt via `github.com/dolthub/driver`, one short-lived engine session per `bdc`
invocation.** The ledger lives at `$(git rev-parse --path-format=absolute --git-common-dir)/beadcrumbs`.

`dolt sql-server` is rejected for v1: it requires users to install Dolt separately *and* run a
resident daemon per repository, which is strictly more setup than the embedded model, and its
Unix-socket path fails outright under long repository paths on macOS (proof below).

Windows is **not supported** in v1. See [Windows](#windows).

## Proof

Disposable Go module (`doltproof`) exercising both models against a Git fixture with a linked
worktree. Host: macOS 25.6 arm64, go1.27.0, dolt 2.3.1, icu4c@78 78.3.

### Contract results

| Requirement | Embedded driver | `dolt sql-server` |
|---|---|---|
| Create DB in repo-local dir | ✅ `CREATE DATABASE` creates `<dir>/bdc/.dolt` | ✅ `--data-dir`, dir per DB |
| Schema create | ✅ | ✅ |
| Constraint enforcement | ✅ `Error 1062: duplicate unique key given: [A,0]` | ✅ `Error 1062 (HY000): duplicate unique key given: [A,0]` |
| Concurrent serialized writes, 2 processes | ✅ 50+50 rows, `count=100`, zero loss | ✅ 50+50 rows, `count=100`, zero loss |
| Concurrent writes, 8 short-lived processes | ✅ 80/80 rows, 1.00 s wall, max lock wait 872 ms | not measured |
| Reopen | ✅ | ✅ |
| Backup | ✅ `CALL DOLT_BACKUP('add'\|'sync')` in-process | ✅ same (server-side) |
| Restore | ✅ `CALL DOLT_BACKUP('restore','file://…','bdc')` in-process, history preserved | ✅ `dolt backup restore` |
| Worktree discovery | ✅ `--git-common-dir` resolves identically from root and linked worktree | ✅ same (discovery is Git-side) |
| Stealth (Git-invisible) | ✅ `git status --porcelain` empty in root *and* worktree | ⚠️ server writes `config.yaml` + `.doltcfg/` into the data dir |
| Crash safety (SIGKILL mid-write) | ✅ 240 rows, `MAX(seq)=239` — contiguous, no torn commit; reopen 22 ms | not measured |
| Cold open / connect | 14–31 ms engine open; 40–50 ms full process wall | 0.2–0.5 ms connect (daemon already resident) |
| Binary size (stripped) | 106 MB dynamic ICU · 141 MB static ICU | pure-Go client, but **+ 117 MB `dolt` install** |

Open time is flat at scale: 18–20 ms on a 4,910-row / 39 MB ledger, same as on a 105-row ledger.

### Concurrency model (the one real constraint)

Embedded Dolt takes an **exclusive write lock on the database directory for the entire lifetime of
the open engine**. Contention is handled by `Config.BackOff`, not by queuing inside the engine.

- A second process that opens while a 4,000-row writer holds the lock blocked for the writer's
  full duration: `count=4240 open_ms=16751.4`.
- Eight short-lived writers (open → 10 inserts → close) all completed in 1.00 s wall with no
  losses; the worst open wait was 872 ms.
- Opening an already-served directory fails cleanly, not destructively:
  `failed to load database "bdc": the database is locked by another dolt process`.
- The `dolt` CLI **reads** fine while an embedded writer holds the lock (read 4,899 rows live), so
  user inspection with `dolt sql` is unaffected.

**Consequence for the storage module:** each `bdc` command must open, run one bounded transaction,
and close. A long-lived agent session must not hold the engine open across turns. Set
`Config.BackOff` with a bounded `MaxElapsedTime` and surface exhaustion as a structured
"ledger busy" error.

### Housekeeping

Per-transaction inserts grow the journal fast — 4,910 rows reached 39 MB. `CALL DOLT_GC()` reclaimed
it to **188 KB in 83 ms**. GC must be a scheduled maintenance operation in the storage module; the
server model gets this automatically via `auto_gc_behavior`.

### `dolt sql-server` disqualifiers

- **Unix socket path length.** macOS `sun_path` is 104 bytes. A 126-byte repo-local socket path
  produced `listen unix …/bdc.sock: bind: invalid argument` and the server exited. Repo-local
  sockets are therefore not reliable; a TCP port would need allocation and collision handling.
- **Lifecycle.** The server records `pid:port:uuid` in `.dolt/sql-server.info`; the `dolt` CLI reads
  it and auto-proxies. Beadcrumbs would have to own daemon start, stop, orphan reaping, and stale
  `.info` cleanup across worktrees.
- **Setup cost.** Requires `dolt` on `PATH`. `brew deps dolt` → `icu4c@78`; the Homebrew `dolt`
  binary dynamically links `libicu{i18n,uc,data}.78.dylib`. This does not remove the ICU dependency,
  it relocates it into a second install the user must perform.

## Requirements

### go.mod

```
go 1.26.2   // minimum — enforced by the dependency, not by language features

require (
    github.com/dolthub/driver v1.88.1
    github.com/cenkalti/backoff/v4 v4.2.1   // Config.BackOff
)
```

The floor is hard. With an older toolchain:
`go: github.com/dolthub/driver@v1.88.1 requires go >= 1.26.2 (running go 1.25.0)`.
This is a jump from the repo's current `go 1.22`. `driver` pulls in
`github.com/dolthub/dolt/go v0.40.5-0.20260507221239-14b38e279fc6` and ~150 indirect modules.

### Build prerequisites (contributors and CI)

CGO is mandatory. There is **no ICU-free build tag** — `github.com/dolthub/go-icu-regex` binds ICU4C
unconditionally, and `icu_static` only changes the linker line. `CGO_ENABLED=0` fails on both
`go-mysql-server/internal/regex` (build constraints exclude all files) and `dolthub/gozstd`.

Without ICU headers the build fails at `file.cpp:3:10: fatal error: 'unicode/regex.h' file not found`.

| Platform | Prerequisite | Flags |
|---|---|---|
| macOS | `brew install icu4c` | `CGO_CPPFLAGS=-I$(brew --prefix icu4c)/include CGO_LDFLAGS=-L$(brew --prefix icu4c)/lib` |
| Linux | `libicu-dev` | usually on the default search path |
| Windows | MSYS2 UCRT64, `pacboy icu:p toolchain:p pkg-config:p` | build under the msys2 shell |

Sources: `go-icu-regex` README; `dolthub/driver` `.github/workflows/test.yml` (matrix:
ubuntu/macos/windows, with the macOS `brew --cellar icu4c` flag step and the `msys2/setup-msys2`
Windows step).

**Cross-compilation is not available.** `CGO_ENABLED=1 GOOS=linux` and `GOOS=windows` both fail on
`runtime/cgo` without a cross C toolchain. Releases must build on native runners, or reproduce
Dolt's own cross-build (`go/utils/publishrelease/buildindocker.sh`: musl/mingw/clang-19 cross
toolchains plus a prebuilt static-ICU tarball).

### Runtime prerequisites (users)

This is the part that changes the install story, so state it in the README.

- **`go install` does not work out of the box.** It compiles from source and inherits every build
  prerequisite above. Users must install ICU4C and, on macOS, export the CGO flags first.
- **Dynamically linked binaries are not portable.** `otool -L` on a default macOS build shows
  `/opt/homebrew/opt/icu4c@78/lib/libicui18n.78.dylib` and siblings — an absolute, Homebrew-specific,
  ICU-major-pinned path. Such a binary will not run on a machine without `icu4c@78` at that prefix.
- **Static ICU works and is the release path.** Building with `-tags icu_static` against a link
  directory containing only ICU's `.a` files produced a binary with no non-system dylibs, and ICU
  regex still evaluated correctly (`REGEXP_LIKE('abc123','^[a-z]+[0-9]+$')` → `1`). Cost: 141 MB
  stripped vs 106 MB dynamic. Homebrew already ships the needed `libicu{i18n,uc,data}.a`, so this
  works on ordinary native CI runners without Dolt's Docker toolchain.

**Release model:** ship prebuilt `-tags icu_static` binaries for darwin/arm64, darwin/amd64,
linux/amd64, linux/arm64. Document `go install` as a source build with prerequisites.

### Repository layout

Ledger path: `$(git rev-parse --path-format=absolute --git-common-dir)/beadcrumbs`.

`--git-common-dir` returns the same absolute path from the repository root and from a linked
worktree, so worktrees share one ledger with no extra bookkeeping. Because the path is inside
`.git/`, `git status` is unaffected with no ignore-file edits — stealth is structural rather than
configured. Verified clean from both root and worktree.

If a visible ledger is ever wanted, `printf '/.beadcrumbs/\n' >> .git/info/exclude` also hides it,
and `info/exclude` is shared across worktrees. Both variants were verified; the in-`.git` path is
the v1 choice because it needs no file the user could commit away.

## Windows

**Not supported in v1.** Not a claim that it is broken — a refusal to claim it works:

1. No proof was run on Windows hardware, so the ledger contract is unverified there.
2. Upstream requires an MSYS2/MinGW toolchain. A plain Windows Go install cannot build this.
3. Upstream's own `TestMultiDBLockContention` is skipped on Windows: *"file handle cleanup is racey
   with `t.TempDir()`"* — the exact lock behavior Beadcrumbs depends on is the one upstream does not
   assert there.

Dolt itself does ship Windows releases (mingw + `icu_static` cross-build), so support is reachable.
Revisit once the contract proof can run on a Windows runner.

## Rejected

| Option | Why not |
|---|---|
| `dolt sql-server` + `go-sql-driver/mysql` | Requires a second install *and* a per-repo daemon. Repo-local Unix sockets fail past 104 bytes on macOS. Daemon lifecycle, port allocation, and stale-PID reaping become Beadcrumbs' problem. Pollutes the data dir with `config.yaml` and `.doltcfg/`. |
| Shelling out to the `dolt` CLI | Same external-install requirement as the server, plus string-parsing a CLI as the storage interface. No transaction boundaries across invocations. |
| Pure-Go Dolt (no CGO) | Does not exist. `CGO_ENABLED=0` fails in `go-mysql-server/internal/regex` and `gozstd`; `go-icu-regex` has no pure-Go fallback tag. |
| Keeping `modernc.org/sqlite` | Pure Go and 10× smaller (13.5 MB stripped today vs 141 MB), but gives up the versioned history, branch/merge, and `DOLT_BACKUP` primitives the design's provenance and supersession model is built on. |

## Open risks

1. **Binary size.** 141 MB stripped, versus 13.5 MB for the current `bdc`. Unavoidable with embedded Dolt (the `dolt`
   CLI is 117 MB). Accept, or reconsider if distribution size becomes a blocker.
2. **Toolchain floor.** `go 1.26.2` is recent; contributors on older toolchains are locked out until
   they upgrade.
3. **Session-scoped locking.** The design must forbid holding the engine open across agent turns.
   This is an invariant to assert at runtime, not a convention.
4. **GC scheduling.** Journal growth is aggressive under per-transaction commits. `DOLT_GC()` fixes
   it cheaply but must actually be scheduled.
5. **Windows.** Unverified; explicitly out of scope for v1.

## Related Documentation

| Document | Description |
|---|---|
| **[Reasoning ledger design](2026-08-27-reasoning-ledger-design.md)** | Approved v1 architecture this operating model serves |
| [Reasoning ledger Wayfinder](reasoning-ledger-wayfinder.md) | Portable decision dependency path |
| [Dolt reasoning ledger v1 plan](../agent-docs/2026-08-28-dolt-reasoning-ledger-v1-plan.md) | Implements this operating model as `internal/store/dolt` (§1.3) |
| [Binary size and cloud-agent operating models](2026-08-29-binary-size-and-cloud-agents-research.md) | Measures the size risk above; adds the multi-machine remote model |
