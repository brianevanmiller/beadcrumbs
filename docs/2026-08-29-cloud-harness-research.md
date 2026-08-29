# Cloud Harness Support — Research

**Date:** 2026-08-29 · **Branch:** `docs/cloud-harness-research`

| Document | Description |
|---|---|
| **[Binary size and cloud-agent operating models](2026-08-29-binary-size-and-cloud-agents-research.md)** | Dolt remotes proven end to end; the `refs` unique-key merge blocker |
| **[Dolt operating model research](2026-08-28-dolt-operating-model-research.md)** | Why embedded Dolt; where the lock constraint came from |
| [Reasoning ledger design](2026-08-27-reasoning-ledger-design.md) | v1 architecture |
| [Stealth mode guide](guides/stealth-mode.md) | Where the ledger lives today |
| [Hooks guide](guides/hooks.md) | Harness session hooks and git shims |
| [Cloud sync design](2026-08-29-cloud-sync-design.md) | The design this research recommends |

The question: how would `bdc` be installed so it runs across VM-sandboxed cloud agents on
**Amp (Orbs)**, **Conductor Cloud**, and **Delta**, keeping one coherent ledger with crumbs
threaded per agent even when many run in parallel — and what is it about Dolt that makes this
not simply a matter of copying a directory.

Everything about the three platforms is quoted from their primary docs. Everything about
Beadcrumbs is read from this branch or measured on this machine. Anything I could not verify is
marked **unverified** rather than guessed.

---

## Part 1 — What Dolt actually constrains

Six properties decide the whole design. None of them is a bug; they are what you buy by putting a
versioned SQL database inside a repository instead of a file.

**1. One writer per directory, per machine.** Embedded Dolt takes an exclusive lock on the database
directory for the entire life of the engine handle. `bdc` is built around that:
`acquireProcessLock` in `internal/store/dolt/lock.go` *panics* on a second `dolt.Open` in one
process, and every command is one short-lived engine — open, one bounded operation, close. Two
`bdc` processes on one ledger serialise; the second waits, then proceeds. This is fine for many
parallel agents **on one machine**, and says nothing at all about two machines.

**2. There is no shared-filesystem story.** That lock is a local file lock. Two machines mounting
one ledger over NFS or a synced folder is not a supported configuration and would corrupt the chunk
journal. Sharing between machines has exactly one supported shape: **each machine keeps its own
ledger, and they exchange data through a remote.**

**3. The ledger cannot be committed to Git.** Settled by measurement in the
[binary-size research](2026-08-29-binary-size-and-cloud-agents-research.md): storage cost is
actually fine, but two branches that each added crumbs produce an unresolvable binary conflict in
`noms/journal.idx`, `noms/manifest`, and the chunk file. Picking a side discards the other
machine's reasoning, and no merge driver can do better without reimplementing Dolt. `bdc gc`
compounds it by rewriting the whole store into freshly-named content-addressed files, so every GC
is a whole-store rewrite Git cannot delta and whose predecessors live in history forever. **The
Dolt store never goes into a commit.** This is the intuition to discard first — "just commit the
ledger" is the obvious idea and it is the wrong one.

**4. A Dolt remote is a real, first-party thing — and one of its backends is a Git repository.**
`dolt remote` accepts `http(s)://`, `aws://`, `s3://` (any S3-compatible store with conditional
writes — R2, MinIO — no DynamoDB table needed), `gs://`, `file://`, `oci`, DoltHub as
`<org>/<repo>`, and `git+file|http|https|ssh://`.

The Git-backed one is the interesting one. Dolt stores its data under **`refs/dolt/data`**, a ref
namespace that `git clone`, `git fetch`, and `git push` never touch. The ledger can therefore ride
along in the *same* GitHub repository as the source code without appearing in any branch, any
diff, or any working tree. Verified end to end in the prior research: push, then `for-each-ref` on
the remote showing `refs/dolt/data`, then `dolt clone` recovering all 31 crumbs — while a plain
`git clone` of the same repository fetches none of it and produces a 232 KB `.git`.

Two measured caveats. The backing Git repository **must already have at least one commit**, and
Dolt creates `refs/heads/__dolt_remote_info__`, which *does* show up in every collaborator's
`git branch -r`. That is the one stealth leak in an otherwise invisible scheme.

**5. Merge is per-cell, and our schema has exactly one place where that is not enough.** Dolt
detects conflicts at cell level: a conflict needs two operations setting the same (row, column) to
different values. Every Beadcrumbs id is a locally-minted UUIDv7 (`internal/ledger/ids.go`), so two
clones insert disjoint rows and cannot conflict — verified, two clones × three private crumbs each
merged to 36 rows with `dolt_conflicts` empty.

The exception is a **natural-key unique index**. `refs` carries
`UNIQUE KEY uq_refs_identity (kind, locator, workspace)`. Two machines that both reference
`internal/parse.go` mint two different `ref_…` UUIDs for one identity; the rows merge, the
constraint does not, and the pull ends in `CONSTRAINT VIOLATION`. `uq_pp_hash` on
`promotion_proposals` has the same shape. `uq_crumbs_hash_session (content_hash, session_id)` is
the same shape but much less likely to fire, because parallel agents have different session ids —
which is, incidentally, the correct behaviour.

**This is the single blocker that must be fixed before any sync ships**, and the fix is a schema
decision rather than a sync feature: mint identity-bearing ids **deterministically** from a hash of
the natural key, so both machines produce the same primary key and the merge becomes an idempotent
no-op.

**6. Credentials and egress.** A remote needs non-interactive authentication and a network path.
`git+https://` wants a token or credential helper; DoltHub wants a `~/.dolt/creds/*.jwk` via
`$DOLT_ROOT_PATH`; `s3://` uses the AWS SDK chain. In a cloud sandbox that already holds a Git
token, `git+https://` is by a wide margin the least new machinery.

One thing that only shows up when you run it: `bdc` sets its commit identity through the connector
(`open.go`: `CommitName = "beadcrumbs"`), not in `.dolt/config.json`, which is literally `{}`. Sync
must be **in-process** — anything shelling out to a `dolt` binary fails with
`fatal: empty ident name not allowed`. In-process is also free: `dolt_remote`, `dolt_push`,
`dolt_pull`, `dolt_fetch`, and `dolt_clone` are already registered in the module `bdc` links, and
the cloud/RPC stack is already 12% of the binary at zero marginal cost.

### What v1 has today

`bdc backup <url>` and `bdc restore <url> --force` — a whole-database aside-and-swap with no merge.
There is no `sync`, `push`, `pull`, `remote`, or `clone` in `newRootCommand`. **Two machines today
means one machine's ledger overwrites the other's.**

---

## Part 2 — The three harnesses

### Amp — Orbs

**Architecture.** One VM per thread, provisioned by Amp.

> "Orbs are remote machines where Amp agents work without using your computer. Every orb thread gets
> a fresh, isolated environment with your code, plugins, development tools, and the context of the
> thread that created it."
> — [/docs/orbs](https://ampcode.com/docs/orbs)

> "An orb is a remote machine that Amp creates for a thread."
> — [/docs/orbs/getting-started](https://ampcode.com/docs/orbs/getting-started)

The compute is e2b: *"e2b provides ephemeral compute instances for Amp orbs"*
([/security](https://ampcode.com/security)). Orbs do not share a disk —

> "The new thread gets its own conversation, context window, working copy, and orb. It does not
> share uncommitted files automatically."
> — [/docs/orbs/agent-to-agent](https://ampcode.com/docs/orbs/agent-to-agent)

Parallelism is a stated feature (*"Orbs are infinite"*), rate-limited to *"a burst of 20 metered
orbs"*, then one every five minutes. Every size ships **60 GB of disk**.

**Persistence.** Within a thread, the disk survives sleep/wake: *"the orb goes to sleep when the
agent is done. When you send the next message, it wakes up with your files and services still in
place."* Across threads, the cache is a **snapshot**, not a volume:

> "A snapshot is a saved copy of a prepared orb. It contains the repository and anything changed by
> `.agents/setup`, such as installed software." … "Amp can reuse a matching project snapshot for up
> to 72 hours."
> — [/docs/orbs/customizing](https://ampcode.com/docs/orbs/customizing)

Whether an orb's disk survives thread archive or delete is **unverified** — no doc states it.
Treat a ledger inside an orb as ephemeral.

**Install.** Debian 12, `sudo` works, `apt-get` is the documented path for anything missing. Base
image includes git, `gh` (*"preinstalled and authenticated after orb activation"*), tmux, Bun,
Node, npm, pnpm, Yarn, Python, jq, ripgrep, zstd, unzip. Customization is three committed/stored
shell scripts, and this is the whole surface — there is no Dockerfile or devcontainer:

- **`.agents/setup`** (executable, repo root) — *"prepares new project orbs and the snapshots used
  to start them."* Stops at 20 minutes. **This is where `bdc` gets installed**, and it lands in the
  snapshot, so the 142 MB download is paid once per 72 hours rather than once per orb.
- **`.agents/resume`** — runs *"after the first orb activation and on every wake-up"*; Amp waits up
  to **10 seconds**. Good for a fast `bdc sync` pull, too tight for anything slow.
- **Pre-clone** and **pre-setup** scripts stored in project settings.

Lifecycle order is documented: *"runs the pre-clone script, clones or updates the repositories,
then runs the pre-setup script and `.agents/setup` from the repository root."* The repo checkout is
at `/home/user/workspace/repo`.

A 142 MB static binary is **not explicitly blessed** but is strongly supported: 60 GB disk,
documented `curl` installs from third-party hosts, and *"An orb installs whatever the task needs —
browsers, databases, toolchains"*. **CPU architecture, glibc version, and any ICU/CGO note are
undocumented** — Debian 12 implies glibc 2.36 by inference only, and the docs' own Docker snippet
uses `$(dpkg --print-architecture)` rather than hardcoding one. Since `bdc` ships static-ICU
binaries for linux/amd64 and linux/arm64, either is fine, but **the install script must detect the
arch rather than assume it** — which `scripts/install.sh` already does.

**Hooks — and a correction to our own docs.** `docs/guides/hooks.md` currently says: *"OpenCode and
Amp: Neither exposes a command-hook surface."* **That is now wrong.** Amp has a plugin event API:

> `session.start ──▶ agent.start ──▶ tool.call ──▶ tool.result ──▶ agent.end`
> … "`session.start` fires when Amp starts a thread session… There is no `session.end` event." …
> "`agent.end` fires when the agent finishes a turn."
> — [/docs/customize/plugins](https://ampcode.com/docs/customize/plugins)

Plugins are TypeScript/JS modules in `.amp/plugins/` (project). The event names are confirmed
independently in the shipped binary on this machine (`agent.start`, `agent.end`, `session.start`,
`tool.call`, `tool.result`, and `.amp/plugins/` paths). `agent.end` is the analogue of our `Stop`
trigger; `session.start` is the analogue of `SessionStart`. There is **no `session.end`** and no
pre-push hook, so **durable completion on Amp must be the git `pre-push` shim** that
`bdc hooks install` already writes — which is harness-independent and works here unchanged.

**Identity.** Thread ids are `T-<uuid>` and are used as the session id in Amp's own stream-JSON
(`"session_id":"T-f9941a55-…"`). `AMP_ORB=1` is set *"inside every orb"*. `AMP_THREAD_ID` is
documented as injected into **declared services** (*"Every service receives `PORT` and
`AMP_THREAD_ID`"*); that it is also in the *agent's* shell environment is **unverified**, though
the string is present in the CLI binary alongside `AMP_CURRENT_THREAD_ID`. If it turns out absent,
the OIDC token carries `thread_id` as a claim, and `amp orb id-token` mints one.

**Credentials and egress.** `gh` is authenticated, and `origin` is configured for push (the Ship
flow does *"Fetch origin and rebase onto the latest base branch"*). Better still, Amp has
first-class OIDC workload identity — `amp orb id-token --audience …`, with the request credential
at `/run/amp/workload-identity-request-token` *"during project pre-clone, pre-setup, and
`.agents/setup` scripts"*. **No egress allowlist, proxy, or firewall is documented for orbs**;
outbound breadth is strongly implied by documented `apt-get`, third-party `curl`, `gh pr create`,
Tailscale, and AWS/GCP STS calls. I will not claim "unrestricted egress" as a documented fact.

**Skills / AGENTS.md.** Both first-class. Project skills live at `.agents/skills/` — the exact path
`npx skills add` already writes — and Amp *also* reads `.claude/skills/` and `~/.claude/skills/`.
`AGENTS.md` is included from cwd upward, with `AGENT.md`/`CLAUDE.md` as fallbacks.

---

### Conductor

Two different problems wearing one name.

#### Conductor local — already works, zero configuration

Local workspaces are ordinary Git worktrees of the user's own clone:

> "Worktrees share the same Git repository data. They use the same history, refs, remotes, and
> object database, while each workspace has its own checkout on disk."
> — [/docs/concepts/git-worktrees](https://www.conductor.build/docs/concepts/git-worktrees)

Verified on this machine from a Conductor workspace:

```
$ cd ~/conductor/workspaces/balto-pipeline/accra
$ git rev-parse --path-format=absolute --git-common-dir --show-toplevel
/Users/brianmiller/balto/repos/balto-pipeline/.git
/Users/brianmiller/conductor/workspaces/balto-pipeline/accra
```

`--git-common-dir` resolves to the user's real clone, so **every parallel Conductor workspace on a
repository already resolves the same ledger.** Ten agents in ten cities write one Dolt database,
serialised by the directory lock, threaded by `session_id`. This is the case stealth mode was
designed for and it needs no sync, no remote, and no new code.

#### Conductor Cloud — one microVM per workspace, not one shared machine

The guess in the question was "one cloud machine all agents kick off sandboxes from". The docs say
otherwise, and the distinction is the whole story:

> "Each cloud workspace runs in an isolated microVM"
> — [/docs/cloud/cloud-computer](https://www.conductor.build/docs/cloud/cloud-computer#specifications)

8 vCPU, 16 GB RAM, **32 GB ephemeral NVMe**, Amazon Linux 2023, running as Vercel Firecracker
sandboxes in `us-east-1` ([/docs/cloud/faq](https://www.conductor.build/docs/cloud/faq)). The
"Cloud Computer" is **not** a running shared machine — it is a build/image:

> "Each organization shares one Cloud Computer: the repositories, environment variables, secrets,
> and software every new workspace starts with. Change its configuration, then choose **Build
> computer**; new workspaces start from the latest successful build, while existing workspaces keep
> the environment they were created with."
> — [/docs/cloud](https://www.conductor.build/docs/cloud)

Separate microVMs mean separate filesystems, so **no shared Git object database is possible across
cloud workspaces** — each gets its own ledger. (The docs never say this in one sentence; it follows
from the isolation spec. Corroborating detail: `CONDUCTOR_ROOT_PATH` and `CONDUCTOR_WORKSPACE_PATH`
are *the same path* in cloud, where locally they differ — that local difference is precisely the
worktree split.)

One trap worth naming, because it is on this machine and looks like counter-evidence: the
`~/conductor/cloud-workspaces/<uuid>/<repo-slug>/.git/worktrees/<workspace-uuid>` tree **is not the
sandbox layout.** It is the local mirror of the one-way cloud→Mac file sync. Do not read it as a
shared cloud disk.

**Lifetime is the sharpest constraint here.** The cloud ledger is not merely unshared — it dies on
a timer:

> "Conductor puts a cloud workspace to sleep after four hours without agent or terminal activity…
> Separately, every sandbox stops at its maximum lifetime of 23 hours and 50 minutes, even if
> active… Files and chat history survive sleep; running processes don't."
> — [/docs/cloud/working-with-cloud-workspaces](https://www.conductor.build/docs/cloud/working-with-cloud-workspaces#sleep-and-maximum-lifetime)

Whether files survive the 23h50m stop, or archive/unarchive, is **not documented** — only
sleep-survival is. Push at every durable completion point.

**Install.** Two layers, and the split will bite:

> "Use the **Install software** script for slow, repeatable setup that every new workspace should
> inherit: system packages, language runtimes, browser binaries, shared caches." … "The script runs
> once per build from the sandbox home directory… as Bash with `set -euo pipefail` — a failed
> command fails the build."

> "A repository's **Setup script** — edited under that repository in organization settings, or by
> the Admin workspace agent — runs every time a new workspace is created."
> — [/docs/cloud/cloud-computer](https://www.conductor.build/docs/cloud/cloud-computer)

> "Cloud workspaces don't use the setup script in `.conductor/settings.toml` or the legacy
> `conductor.json`; those still configure local workspaces."
> — same page

So a repo-committed recipe covers **local only**; cloud setup lives in organization settings,
outside the repository. The documented way to share one implementation is to commit a script and
call it from both, branching on `CONDUCTOR_IS_LOCAL`.

`bdc` belongs in the **Install software** script (once per build, amortised across every workspace)
rather than the per-workspace Setup script. The docs' own worked example is a `curl … | bash`
install of bun, `sudo`/`dnf` are available, and the base image already carries git, GitHub CLI, jq,
ripgrep, **Node.js 24 and npm** — which makes `npm install -g @beadcrumbs/bdc` the cheapest recipe
on this platform. Mind the documented PATH gotcha: *"The install script is not a login shell, but
workspace setup scripts, run scripts, and cloud terminals are… If an installer changes `PATH`,
export the new path for the rest of the build and append it to `~/.profile`."*

**Identity.** `CONDUCTOR_SESSION_ID` — *"Current session ID. Available only in agent processes, not
setup scripts or terminals."* That caveat matters: a hook running inside the agent process sees it;
a setup script does not. Also `CONDUCTOR_WORKSPACE_NAME`, `CONDUCTOR_WORKSPACE_PATH`, and
`CONDUCTOR_IS_LOCAL` (`0` in cloud, `1` local) — the last is a clean harness marker.

**Credentials — the most likely integration failure on any platform here:**

> "Conductor manages git and GitHub CLI authentication, so don't copy GitHub tokens into Cloud
> Computer variables."
> — [/docs/cloud/environment-variables](https://www.conductor.build/docs/cloud/environment-variables)

`CONDUCTOR_GIT_AUTH_*` is a reserved variable prefix, and the shipped Mac app carries
`CONDUCTOR_GIT_AUTH_SOCKET` and `CONDUCTOR_REAL_GIT_PATH` — i.e. **`git` is wrapped by an auth
shim**. Dolt's in-process Git-remote client would not go through that shim, so a `git+https://`
Dolt remote may find no usable credential *even though `git push` works fine in the same shell*.
This is **untested and is the highest-risk unknown in this document.** Fallbacks: a `git+file://`
remote, or extracting a credential via `git credential fill` before configuring the remote.

**Hooks.** Conductor documents exactly three lifecycle points — setup, run, archive — and the word
"hook" appears nowhere in its docs. But the agent *is* real Claude Code:

> "Conductor comes bundled with its own installation of Claude Code and Codex… You can find them at
> `~/Library/Application Support/com.conductor.app/bin`"
> — [/docs/faq](https://www.conductor.build/docs/faq)

> "Agents can also use instructions stored in your repository or user configuration, such as:
> `AGENTS.md`, `CLAUDE.md`, `.claude/commands`, Skills."
> — [/docs/reference/agent-behavior](https://www.conductor.build/docs/reference/agent-behavior)

Because it runs the genuine binary and loads `.claude/commands`, the existing `hooks/hooks.json`
plausibly fires unmodified — but **`.claude/settings.json` passthrough is never mentioned in the
docs and is unverified.** It is a ten-minute experiment. Do it before promising it.

**Availability.** Cloud shipped 2026-07-30 (v0.78.0), not beta-labelled, requires Pro ($50/mo),
Teams, or Enterprise. GitHub repositories only.

---

### Delta

**What it is.** A standalone desktop app from Zed Industries with its own agent loop — **not** a
wrapper around Claude Code, Codex, or Amp. It drives model providers directly.

> "Delta is a collaborative agent workspace. You describe what you want, an agent works in a
> checkout of your repository, and you review and pull in the results. Delta creates an isolated
> checkout by default, or you can use an existing local checkout."
> — [/docs/getting-started](https://delta.dev/docs/getting-started)

Delta deliberately redefines "worktree" away from the Git meaning, which is the crux:

> "Git also has a feature called worktrees: extra checkouts of one local repository. A Delta
> worktree is a different thing: the shared project history that exists as a checkout on each
> participant's machine."
> — [/docs/concepts/worktrees-and-machines](https://delta.dev/docs/concepts/worktrees-and-machines)

> "Delta stores its bookkeeping in a `.delta` folder inside your repository, whose machine-local
> contents are hidden from git automatically. Managed checkouts for a repository you have on your
> machine live there too, under `.delta/worktrees/`."
> — same page

**What the layout actually is, measured in this repository.** The `.delta/clones/` half is
undocumented — the string appears in none of Delta's twenty doc pages — but it is on disk here, and
it is the thing that decides ledger placement. Each thread gets a **non-bare clone with its Git
directory and working tree split apart**:

```
.delta/clones/<thread-id>/beadcrumbs.git     ← Git directory (core.bare=false, core.worktree set)
.delta/worktrees/<thread-id>/beadcrumbs      ← working tree
```

Run from inside a Delta thread's checkout:

```
$ git rev-parse --path-format=absolute --is-bare-repository --git-common-dir --show-toplevel
false
/Users/brianmiller/oss/beadcrumbs/.delta/clones/mdcmnbnj9jda/beadcrumbs.git
/Users/brianmiller/oss/beadcrumbs/.delta/worktrees/mdcmnbnj9jda/beadcrumbs
```

**Each Delta thread is therefore its own repository, and gets its own ledger.** `--git-common-dir`
returns a per-thread path, so `bdc` resolves
`.delta/clones/<thread-id>/beadcrumbs.git/beadcrumbs/bdc/.dolt` — a fresh, empty ledger per thread,
invisible to the main repo, destroyed when Delta discards the managed checkout. Three Delta threads
in this repository today means three unrelated empty ledgers, plus the real one in
`/Users/brianmiller/oss/beadcrumbs/.git/beadcrumbs`. Nothing is shared, nothing is threaded, nothing
survives. **Delta is the harness where the current behaviour is most quietly wrong** — it does not
error, it just silently starts over each thread.

The clone config also shows the documented two-remote model, and it is a gift:

```
[remote "origin"]  url = https://github.com/brianevanmiller/beadcrumbs.git
[remote "local"]   url = /Users/brianmiller/oss/beadcrumbs/.git
                   receivepack = git -c receive.denyCurrentBranch=updateInstead receive-pack
```

> "**local**: your own repository on your machine. The agent can push a branch to `local` to hand
> you work without a round trip through GitHub."
> — [/docs/concepts/delta-and-git](https://delta.dev/docs/concepts/delta-and-git)

That `local` remote is a **`git+file://` Dolt remote pointing at the user's own repository, for
free** — a Delta thread can sync its ledger straight back into the parent repo's Git object store
over the local filesystem. No network, no token, no DoltHub. Delta is the harness with the
*cleanest* sync story once sync exists.

**One quirk worth recording.** Because the clone sets `core.worktree` rather than using a linked
worktree, `git worktree list --porcelain` reports the *Git directory* as the main worktree. So
`Location.VisibleDir()` resolves to `…/beadcrumbs.git/.beadcrumbs` — inside the Git directory, not
in the working tree. **`--visible` in a Delta checkout is not visible.** Stealth mode is unaffected
and remains the right default.

**Install hook.** Exactly one, and it is enough:

> "If your repository has an executable `.agents/prepare` script at its root, Delta runs it in each
> managed checkout it creates, **before the agent starts working there**. Use it for setup that a
> fresh checkout needs, such as installing dependencies. **Adopted checkouts are skipped.** If the
> script fails, the checkout stays usable and the thread shows the error."
> — [/docs/concepts/worktrees-and-machines](https://delta.dev/docs/concepts/worktrees-and-machines)

Its contract is barely specified: no documented arguments, environment, cwd guarantee beyond "in
each managed checkout", timeout, or output capture. That is the whole extension surface — there are
**no hooks**. Zero occurrences of "hook" across all twenty doc pages: no session-start, no stop, no
pre-push, no post-thread. Durable completion on Delta therefore has to be the git `pre-push` shim
plus the skill's explicit commands.

**Skills.** `.agents/skills/<name>/SKILL.md` in the repository, `~/.agents/skills/<name>/SKILL.md`
personally ([/docs/agents/skills](https://delta.dev/docs/agents/skills)) — the same canonical home
`npx skills add` already writes, so the Beadcrumbs skill installs with no extra work. Note there is
no documented project-level `AGENTS.md`; the docs redirect project instructions to skills.

**Identity.** No thread-id environment variable is documented. `DELTA_CURRENT_THREAD_ID` appears as
a string in the shipped app binary, alongside `DELTA_REMOTE_URL`, `DELTA_SCRATCH_DIR`,
`DELTA_AUTOMATION_SOCKET`, and `DELTA_ENABLE_ACP` — **but whether any is exported into the agent's
or `.agents/prepare`'s environment is unverified.** The thread id is legible from the checkout path
itself (`.delta/worktrees/<id>/`), which is the more reliable source and needs no cooperation from
Delta.

**Cloud.** Rolling out, gated, and explicitly unsandboxed:

> "Cloud execution is rolling out and is not yet available to everyone. Cloud machines run
> Delta-hosted models only; a thread using your own API keys runs the agent locally."
> — [/docs/concepts/worktrees-and-machines](https://delta.dev/docs/concepts/worktrees-and-machines)

> "**Delta does not sandbox agents. An agent has unrestricted access to the device where it runs.**"
> — [/docs/privacy-and-security/agentic-safety](https://delta.dev/docs/privacy-and-security/agentic-safety)

There is an undocumented headless path, confirmed by running the shipped binary:

```
$ delta cli run --help
Run a single agent turn headlessly, share the thread, and print its URL
  --repo <PATH_OR_URL>  Repository to attach as a worktree: a local path or git remote URL
$ delta cli serve --help
Join a shared thread and serve agent turns requested for a remote runner
```

`delta cli serve <JOIN_ID>` is the remote-runner entry point, and is where a cloud recipe would
eventually hang. Nothing about it is documented.

---

## Part 3 — The matrix

| | **Amp (Orbs)** | **Conductor local** | **Conductor Cloud** | **Delta local** | **Delta cloud** |
|---|---|---|---|---|---|
| **Binary install feasible** | Yes. Debian 12, `sudo`+`apt`, 60 GB disk, documented third-party `curl`. Arch/glibc undocumented — rely on `install.sh` detection | Yes — macOS, same as any local install | Yes. Amazon Linux 2023, `sudo`+`dnf`, 32 GB disk; **Node 24 + npm preinstalled ⇒ `npm i -g @beadcrumbs/bdc` is cheapest** | Yes — macOS/Linux/Windows app; macOS in practice | Unverified — gated, no documented environment |
| **Where the ledger would live** | `<orb>/home/user/workspace/repo/.git/beadcrumbs` — per thread, ephemeral, snapshot-cached ≤72 h | `<user's clone>/.git/beadcrumbs` — **one, shared by every workspace** | `<workspace>/.git/beadcrumbs` — per workspace, ephemeral, dies at ≤23 h 50 m | `.delta/clones/<thread>/…git/beadcrumbs` — **per thread, isolated, discarded with the checkout** | Unverified |
| **Session / actor identity** | Thread id `T-<uuid>`; `AMP_ORB=1`; `AMP_THREAD_ID` (documented for services, agent shell unverified); OIDC `thread_id` claim | `CONDUCTOR_SESSION_ID` (agent processes only), `CONDUCTOR_IS_LOCAL=1` | `CONDUCTOR_SESSION_ID`, `CONDUCTOR_IS_LOCAL=0`, `CONDUCTOR_WORKSPACE_NAME` | Thread id from the checkout path; `DELTA_CURRENT_THREAD_ID` unverified | Unverified |
| **Hook points for harvest at durable completion** | git `pre-push` shim (works today); plugin `agent.end` ≈ Stop; `session.start`; **no `session.end`** | Claude Code hooks *if* `.claude/settings.json` passes through (**unverified**); git shims work | Same as local, plus repo **Setup script is org-side, not in-repo** | **None.** `.agents/prepare` at checkout creation only; git `pre-push` shim; skill body | Unverified |
| **Sync mechanism needed** | `bdc clone` in `.agents/setup`, `bdc sync` in `.agents/resume` (≤10 s) + `pre-push`. `git+https` via `gh` token, or OIDC | **None** — shared git-common-dir already | `bdc clone` in Setup script, `bdc sync --push` at `pre-push`. **Credential path via the git auth shim is untested** | `git+file://` to the `local` remote Delta already configures — no network, no token | Unverified |
| **Supported today?** | **No** — needs sync (feature) | **Yes** | **No** — needs sync (feature) | **No** — needs sync; silently starts a fresh ledger per thread | **Unknown** |

**One-line verdicts.** Conductor local works now. Everything else needs the same one feature —
Dolt git-backed remotes with `bdc clone/sync` — plus the deterministic-id migration that makes
merge safe. No platform needs anything platform-specific beyond a five-line setup snippet.

---

## Part 4 — Per-agent threading

The requirement — crumbs threaded and coherent per agent, even with many running in parallel — is a
provenance question, not a storage question, and it is *almost* solved already.

**What v1 records.** Every provenance-bearing table (`crumbs`, `harvests`, `insights`,
`insight_revisions`, `crumb_review_events`, `validations`, `authorities`, `promotion_proposals`,
`promotions`, `receipts`) carries the same four columns:

```sql
actor_id     VARCHAR(255) NOT NULL,
actor_kind   ENUM('human','agent') NOT NULL,
actor_model  VARCHAR(128) NULL,
session_id   VARCHAR(128) NULL,
```

with a CHECK that an `agent` actor must name **both** a model and a session, and
`KEY ix_crumbs_session (session_id, captured_at)` — a session's crumbs are already an indexed,
time-ordered thread. They are set from `$BDC_ACTOR_KIND`, `$BDC_ACTOR_MODEL`, `$BDC_SESSION`
(`cmd/bdc/root.go`) or the matching flags.

**What that buys under parallelism.** `session_id` is the thread key. Ten agents in parallel
against one ledger produce ten interleaved but individually recoverable threads, ordered by
`captured_at`, each labelled with the model that produced it. On one machine the directory lock
serialises the writes. Across machines after a sync the same holds, because ids are locally-minted
UUIDv7 and rows are disjoint. `uq_crumbs_hash_session` is right in both directions: two agents that
independently reach the same conclusion each keep their crumb, while one agent capturing the same
thing twice in one session dedupes to one.

**What is missing.**

**1. There is no `harness` field.** The ledger can say *which model* and *which session*, never
*which platform the session ran on*. With Amp threads, Conductor workspaces, Delta threads, Claude
Code sessions, and Codex sessions all landing in one ledger, `session_id` values are opaque strings
from five id spaces with no way to tell them apart — and no way to ask "what did the cloud agents
conclude last week". A `harness VARCHAR(64) NULL` column, defaulted from an auto-detected env
marker, is a small migration with a large payoff. **This is the one schema gap the cloud story
creates**, distinct from the merge gap in Part 1.

**2. Only two harnesses feed `BDC_SESSION` today.** `hooks/bdc-hook.sh` reads `session_id` from the
stdin JSON that Claude Code and Codex both deliver. Nothing else does:

| Harness | Thread/session id available | Reaches `bdc` today? |
|---|---|---|
| Claude Code | stdin JSON `session_id` | yes, via `bdc-hook.sh` |
| Codex | stdin JSON `session_id` | yes, via `bdc-hook.sh` |
| Conductor (local + cloud) | `CONDUCTOR_SESSION_ID`, agent processes only | not directly; may inherit the Claude Code path (unverified) |
| Amp | thread id `T-<uuid>`; `AMP_THREAD_ID`, `AMP_ORB` | no |
| Delta | checkout path `.delta/worktrees/<id>/`; `DELTA_CURRENT_THREAD_ID` unverified | no |

**3. `BDC_ACTOR_MODEL` is nobody's job.** The hook says it plainly: *"no harness puts the model in
it, so the model comes from the environment when one exports it and is recorded as `unknown`
otherwise."* A ledger full of `actor_model = unknown` cannot answer "which model got this wrong",
which is among the more valuable questions a reasoning ledger can answer. The skill instructing the
agent to export its own model id remains the only reliable source, and should stay prominent in
every harness recipe.

**The fix in one sentence.** A single provenance-detection step inside `bdc` — read the first
matching env marker from an ordered list, set `harness`, `session_id`, and `actor_kind` — replaces
per-harness shell plumbing and is what makes coherent per-agent threads across five platforms true
rather than aspirational.

---

## Part 5 — What this research changes

1. **One feature unlocks all three platforms**: Dolt git-backed remotes with `bdc clone` /
   `bdc sync`, defaulted from `origin`. It was already recommended and proven feasible by the
   [binary-size research](2026-08-29-binary-size-and-cloud-agents-research.md); this document
   confirms nothing about Amp, Conductor, or Delta requires anything beyond it.
2. **Deterministic ids for `refs` and `promotion_proposals` are the blocking prerequisite**, not a
   follow-up. It is a migration and must land before sync ships.
3. **Add a `harness` column** in the same migration. It is cheap now and awkward later.
4. **Conductor local already works** and should be documented as such.
5. **Two doc corrections.** `docs/guides/hooks.md` says Amp exposes no command-hook surface — Amp
   has a plugin event API with `session.start`, `agent.start`, `agent.end`. And `--visible` should
   carry a note that it resolves inside the Git directory under Delta's `core.worktree` layout.
6. **The highest-risk unknown is Conductor Cloud's git auth shim**, which may starve Dolt's
   in-process Git remote of credentials. Test it before committing to a `git+https` default there.

The design that follows from all of this is in
[Cloud sync design](2026-08-29-cloud-sync-design.md).
