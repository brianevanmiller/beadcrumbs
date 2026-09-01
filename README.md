# Beadcrumbs

**A repository-local reasoning ledger for humans and coding agents.**

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Task trackers coordinate work. Beadcrumbs preserves the evidence, intent, and evolving
understanding behind that work — as structured records with provenance, not as a transcript
archive.

```
capture ──▶ Crumb ──▶ harvest ──▶ Insight ──▶ propose ──▶ Proposal ──▶ record ──▶ Receipt
             ↑                      │
             └── reused, never consumed
```

A **Crumb** is one captured fragment: a correction, a discovery, a rejected approach. A
**Harvest** weighs selected Crumbs into a revisioned **Insight**; the Crumbs stay available to
every later Harvest. A **Promotion Proposal** says an Insight is ready to become a durable
record somewhere — an ADR, a comment, a ticket — and `bdc` never performs that external write
itself. A human or agent writes it and records a **Receipt** with the anchor that proves it.

Evidence, numeric confidence, validation verdict, and authority level are four independent axes.
Confidence is written once; review, validation, and authority are append-only. Only a human can
grant `mandatory` authority.

---

## Requirements

| | |
|---|---|
| Platforms | macOS and Linux, `arm64` and `amd64` |
| Windows | **Not supported.** See [Windows](#windows) |
| Git | required — the ledger lives inside the repository's Git directory |
| Binary size | ~142 MB. Embedded Dolt with statically linked ICU. Stated up front because it is not a typo |
| Runtime deps | none for the released binary (`otool -L` / `ldd` show system libraries only) |

## Install

### Released binary (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/brianevanmiller/beadcrumbs/v1.1.0/scripts/install.sh | bash
```

The installer fetches the prebuilt `-tags icu_static` archive for your platform from the GitHub
release, verifies its SHA-256 against the release's `checksums.txt`, and installs one binary. It
does not fall back to a source build: on an unsupported platform it fails with the manual
instructions rather than starting a compile that will not finish.

### From source

`go install` is a **source build**, not a download. Embedded Dolt requires CGO and ICU4C
unconditionally — `github.com/dolthub/go-icu-regex` has no pure-Go fallback and no ICU-free build
tag, so a missing header is a build break (`unicode/regex.h file not found`), never a silent
degrade.

```bash
# macOS
brew install icu4c
CGO_ENABLED=1 \
CGO_CPPFLAGS="-I$(brew --prefix icu4c)/include" \
CGO_LDFLAGS="-L$(brew --prefix icu4c)/lib" \
  go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@v1.1.0

# Debian/Ubuntu — libicu-dev is already on the default search path
sudo apt install libicu-dev
CGO_ENABLED=1 go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@v1.1.0
```

Go 1.26.2 or newer is required. That floor is imposed by `github.com/dolthub/driver`, not chosen.

A source build links ICU dynamically, which on macOS means an absolute Homebrew path
(`/opt/homebrew/opt/icu4c@78/lib/libicui18n.78.dylib`). Such a binary does not run on a machine
without that exact prefix. Copy it between machines and it will fail; use the release archives
for distribution.

### Windows

Not supported in v1. This is a refusal to claim it works, not a claim that it is broken: the
ledger contract has never been proven on Windows hardware, upstream needs an MSYS2/MinGW
toolchain, and upstream's own multi-database lock-contention test is skipped on Windows — the
exact behavior Beadcrumbs depends on. CI has no Windows job and the installers refuse to run
there.

## Quick start

```bash
cd your-repo
bdc init                                     # ledger at <git-common-dir>/beadcrumbs
bdc capture "The bug is in JWT validation, not the session store." --confidence 0.8
bdc crumb list --state candidate
bdc crumb review <crumb-id> --state accepted --rationale "confirmed by the failing test"
bdc harvest --crumb <crumb-id> --title "JWT validation rejects valid tokens" \
  --class learning --content-file notes.md
bdc context                                  # what this repository has concluded
bdc ask                                      # the questions the ledger cannot answer itself
```

Every command accepts `--json`.

## Where the ledger lives

`bdc init` puts the ledger at `$(git rev-parse --git-common-dir)/beadcrumbs`. That path is
identical from the repository root and from every linked worktree, so worktrees share one ledger
with no per-worktree setup, and because it is inside `.git/` it never appears in `git status`.
Stealth is structural, not configured: no ignore file to edit and none to accidentally commit.

`bdc init --visible` places it at `<main-worktree>/.beadcrumbs` instead and adds that path to
`.git/info/exclude`, which is shared across worktrees and never committed. See
[the stealth-mode guide](docs/guides/stealth-mode.md).

## Retention, not erasure

`bdc crumb prune` drops Crumbs from the working set. It is a **retention** operation. Dolt keeps
committed history, so a pruned Crumb remains readable through `dolt_history_*` — pruning removes
it from every query `bdc` answers, not from the database's past. Nothing in Beadcrumbs expires
or deletes automatically, ever.

The consequence: never capture a secret expecting to remove it later. Redaction runs *before*
any write, and a finding it cannot resolve aborts the write with exit 7 and nothing persisted.
That abort is a signal to rewrite the Crumb, not to retry it.

## Agents

The portable contract is the CLI and its JSON — not a hook, not a transcript path, not an API.
Install the skill into any repository:

```bash
npx -y skills add brianevanmiller/beadcrumbs --yes
```

An agent should say who it is before its first write:

```bash
export BDC_ACTOR_KIND=agent BDC_ACTOR_MODEL="<model id>" BDC_SESSION="<session id>"
```

All three or none — an agent actor needs both a model and a session, and declaring `agent`
without them is refused. Undeclared, a run carrying both is recorded as an agent and anything
else as a human, and `human` is the value every authority gate is satisfied by.

It installs to `.agents/skills/beadcrumbs` and symlinks each detected agent directory at it. Pin
the tag (`brianevanmiller/beadcrumbs/tree/v1.1.0/skills/beadcrumbs`) if you need a reproducible
install; the installer tracks a content hash in `skills-lock.json`, not a version.

Optional, and never part of the contract: `bdc hooks install` writes chained `pre-push` and
`post-merge` shims, and [docs/guides/hooks.md](docs/guides/hooks.md) covers per-harness session
hooks. Automatic harvesting is off by default and opted into per repository with
`bdc hooks install --auto-harvest`. Manual is the normal mode, not a degraded one.

## JSON contract

```json
{
  "bdc": "1",
  "command": "reference.list",
  "ok": true,
  "data": {},
  "warnings": [{"code": "beads_unavailable",
                "message": "beads references resolve to their locator; bd is unavailable here: not_installed"}],
  "error": null,
  "meta": {"bdc_version": "1.1.0", "ledger_schema": 3, "generated_at": "2026-08-28T14:00:00.000000Z"}
}
```

JSON goes to stdout, prose to stderr, and the two never mix. On failure `ok` is `false`, `data`
is `null`, and `error` carries `code`, `message`, and `details`. There is no partial envelope.

| Exit | Meaning |
|---|---|
| 0 | success |
| 1 | usage or validation error |
| 2 | not found |
| 3 | policy or authority denied |
| 4 | ledger busy — lock backoff exhausted |
| 5 | no ledger (not a Git repository, or not initialised) |
| 6 | storage or integrity error |
| 7 | redaction abort — nothing persisted |
| 8 | adapter error |

Full command reference: **[BDC_GUIDE.md](BDC_GUIDE.md)**.

## Optional Beads integration

If [Beads](https://github.com/gastownhall/beads) is installed, `bdc` enriches `beads:` references
with title and status through supported `bd --json` commands. It never reads Beads' database
directly. If `bd` is absent, stale, or failing, references still resolve to their locator and the
envelope carries a warning — a missing tracker never blocks a core write.

## Building and testing

```bash
make check      # go build ./... && go vet ./... && go test ./...
make binary     # a local ./bdc for smoke testing
```

The Makefile exports the macOS ICU flags for you. `scripts/build-release.sh` produces the
static-ICU release artifact.

## Documentation

| Document | What it covers |
|---|---|
| [BDC_GUIDE.md](BDC_GUIDE.md) | Every command, its flags, and its `data` shape |
| [skills/beadcrumbs/SKILL.md](skills/beadcrumbs/SKILL.md) | The agent-facing contract |
| [docs/guides/stealth-mode.md](docs/guides/stealth-mode.md) | Ledger location, worktrees, `--visible` |
| [docs/guides/hooks.md](docs/guides/hooks.md) | Optional git and session hooks |

## License

MIT. See [LICENSE](LICENSE).
