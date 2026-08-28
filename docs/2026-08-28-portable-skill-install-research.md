# Portable Skill Installation and Session Integration

**Date**: 2026-08-28

**Status**: Research complete; resolves [bdc-7ah.2](beads:bdc-7ah.2)

**Design**: [Beadcrumbs as a Repository-Local Reasoning Ledger](2026-08-27-reasoning-ledger-design.md)

---

## Question

What portable installation, discovery, and session-integration contract can a
Beadcrumbs-owned skill rely on across the open Skills installer and the major
agent environments — and where are tool-specific shims unavoidable?

## Decision

Ship **one Agent Skills-spec skill** in this repository at
`skills/beadcrumbs/SKILL.md`, installable with `npx skills add
brianevanmiller/beadcrumbs`. Its portable contract is **explicit `bdc`
commands plus stable JSON**, invoked from the skill body. No harness API, hook
event, or transcript path is part of the contract.

Session hooks are **optional per-harness shims** that only ever shell out to
the same `bdc` commands. The "harvest near durable completion" requirement is
met by **git hooks and CI**, not by agent session hooks — see
[Durable completion](#durable-completion-pr-merge-close) below.

## Evidence

### The installer (`skills` v1.5.23)

Read from a clone of `github.com/vercel-labs/skills` at `main`
(`package.json` → `"version": "1.5.23"`, `bin: { skills, add-skill }`).

Commands, from `README.md`:

```bash
npx skills add <source>        # install
npx skills use <source>        # one-shot, prints skill to stdout (pipeable to `claude`)
npx skills list                # list installed
npx skills find [query]        # search
npx skills update [skills]     # update to latest
npx skills remove [skills]     # uninstall
npx skills init [name]         # scaffold a SKILL.md
```

`add` options (`README.md` → "Options"):

| Option | Meaning |
|---|---|
| `-g, --global` | install to user dir instead of project |
| `-a, --agent <agents...>` | target specific agents (`claude-code`, `codex`, …; `'*'` for all) |
| `-s, --skill <skills...>` | install specific skills by name (`'*'` for all) |
| `-l, --list` | list repo skills without installing |
| `--copy` | copy instead of symlinking |
| `-y, --yes` / `--all` | non-interactive (CI-safe) |

Accepted sources: `owner/repo` shorthand, full GitHub/GitLab URL, any git URL,
a `tree/<ref>/skills/<name>` deep link, a local path, and a direct
`SKILL.md`/archive download URL. Private repos resolve through the git
credential helper, then `gh`, then SSH.

### Install layout: canonical copy + per-agent symlink

`src/installer.ts:98-101`:

```ts
export function getCanonicalSkillsDir(global: boolean, cwd?: string): string {
  const baseDir = global ? homedir() : cwd || process.cwd();
  return join(baseDir, AGENTS_DIR, SKILLS_SUBDIR);   // .agents/skills
}
```

`src/installer.ts:291-300` copies the skill to
`<canonical>/<skill-name>` and then symlinks each selected agent's directory at
it. So a global install produces exactly the convention already in use on this
machine:

```
$ ls -la ~/.claude/skills | head
architect -> ../../.agents/skills/architect
code-review -> ../../.agents/skills/code-review
hypt-build -> ../../.agents/skills/hypt-build
```

`src/installer.ts:214-215` explicitly handles the case where
`~/.claude/skills` *is itself* a symlink to `~/.agents/skills`, so Brian's
existing `~/.agents/` canonical-home convention and the installer agree rather
than fight. Nothing extra is needed to adopt it.

State is tracked in a lock file — `.agents/.skill-lock.json` (project) or
`$XDG_STATE_HOME/skills/.skill-lock.json`, falling back to
`~/.agents/.skill-lock.json` (`src/skill-lock.ts:6-7,62-73`). Each entry
records `source`, `sourceType`, `sourceUrl`, `ref`, `skillPath`,
`skillFolderHash` (GitHub tree SHA of the skill folder), `installedAt`,
`updatedAt` (`src/skill-lock.ts:14-37`). **Updates are ref-and-hash based, not
semver**: `npx skills update` re-fetches when the folder tree SHA changes.
Version discipline therefore has to live in Beadcrumbs' own release notes and
in a `bdc` version check inside the skill, not in installer metadata.

### Agent targets relevant to us

From `src/agents.ts`:

| Agent | Project `skillsDir` | Global `globalSkillsDir` | Detection |
|---|---|---|---|
| `claude-code` (`:152-158`) | `.claude/skills` | `$CLAUDE_CONFIG_DIR`/`~/.claude/skills` | `~/.claude` exists |
| `codex` (`:219-226`) | `.agents/skills` | `$CODEX_HOME`/`~/.codex/skills` | `~/.codex` or `/etc/codex` exists |
| `opencode` (`:531-538`) | `.agents/skills` | `$XDG_CONFIG_HOME/opencode/skills` | `~/.config/opencode` exists |
| `amp` (`:89-96`) | `.agents/skills` | `$XDG_CONFIG_HOME/agents/skills` | `~/.config/amp` exists |

Three of the four use `.agents/skills` as their project directory, i.e. the
canonical directory *is* the agent directory and no symlink is created.

### Repository layout the installer expects

Per `skills.sh/docs/customize` and this repo's own `skills/` directory, a skills
repo is:

```
skills.sh.json                       # optional: display grouping only
skills/<name>/SKILL.md
```

`src/skills.ts:13-40` also treats `.agents/skills`, `.claude/skills`,
`.codex/skills`, … as skill roots when scanning a source, and
`src/skills.ts:176-195` accepts a `--skill`/subpath filter with traversal
guards. A single top-level `skills/beadcrumbs/SKILL.md` is the least surprising
shape and is what the deep-link form (`.../tree/main/skills/beadcrumbs`)
addresses.

### Frontmatter: the Agent Skills spec is the portable subset

From [agentskills.io/specification](https://agentskills.io/specification):

| Field | Required | Constraints |
|---|---|---|
| `name` | Yes | 1–64 chars, lowercase `a-z0-9-`, no leading/trailing/consecutive hyphen, **must match the parent directory name** |
| `description` | Yes | 1–1024 chars; what it does *and* when to use it |
| `license` | No | license name or bundled file |
| `compatibility` | No | ≤500 chars environment requirements |
| `metadata` | No | string→string map, ignored by clients |
| `allowed-tools` | No | space-separated pre-approved tools (experimental) |

Claude Code accepts all six and adds its own (`disable-model-invocation`,
`user-invocable`, `hooks`, `paths`, …), but its own docs warn that non-spec keys
fail validation elsewhere:

> `Unexpected key(s) in SKILL.md frontmatter: argument-hint. Allowed properties
> are: allowed-tools, compatibility, description, license, metadata, name`

**Therefore: the shipped `SKILL.md` uses only the six spec fields.** Anything
Claude Code-specific (hooks, invocation control) goes in a separate optional
plugin/settings snippet, not in the portable skill.

`compatibility` is the right place for the one real prerequisite:
`compatibility: Requires the bdc CLI on PATH (bdc >= 1.0).`

### Discovery per harness

| Harness | Discovers skills in | Invocation |
|---|---|---|
| Claude Code | `~/.claude/skills/<name>/SKILL.md` (personal), `.claude/skills/<name>/SKILL.md` (project, plus every parent up to repo root, plus nested dirs on first file touch), plugin `skills/`, `--add-dir` dirs | `/<name>`, or model-selected from `description` |
| Codex | `$CWD/.agents/skills`, `$REPO_ROOT/.agents/skills`, `$HOME/.agents/skills`, `/etc/codex/skills`, bundled | `/skills`, `$<name>`, or model-selected |
| OpenCode | `.agents/skills` (project), `~/.config/opencode/skills` (global) | per opencode.ai/docs/skills |
| Amp | `.agents/skills` (project), `~/.config/agents/skills` (global) | per ampcode.com/manual#agent-skills |

Claude Code also watches its skill directories and picks up added/edited skills
mid-session without a restart (new top-level directories still need a restart).

One discrepancy worth recording: Codex's own docs list `$HOME/.agents/skills`
as the user-level location, while the installer writes Codex global installs to
`~/.codex/skills`. Both paths are read by *some* version of Codex, and
`src/skills.ts` lists `.codex/skills` as a project root. Global installs of
this skill should be verified against the Codex build in use rather than
assumed.

### Session hooks

Claude Code exposes a large hook surface
([code.claude.com/docs/en/hooks](https://code.claude.com/docs/en/hooks)). The
events that matter for a ledger, with what they can actually do:

| Event | Matchers | Useful property |
|---|---|---|
| `SessionStart` | `startup`, `resume`, `clear`, `compact`, `fork` | plain stdout is injected as context → can inline `bdc prime` |
| `UserPromptSubmit` | — | can add context or rewrite the prompt |
| `Stop` | — | `additionalContext`; **exit 2 blocks the stop** and continues the turn → can force an unharvested-crumbs check |
| `SubagentStop` | agent type | same, per subagent |
| `PreCompact` | `manual`, `auto` | fires *before* context is lost → the natural "harvest now" trigger; exit 2 blocks compaction |
| `PostCompact` | `manual`, `auto` | can re-inject `bdc context` after compaction |
| `SessionEnd` | `clear`, `resume`, `logout`, `prompt_input_exit`, `other` | last-chance flush; cannot block |

Configured in `~/.claude/settings.json`, `.claude/settings.json`,
`.claude/settings.local.json`, plugin `hooks/hooks.json`, or skill frontmatter.
All hooks receive `session_id`, `transcript_path`, `cwd`, `hook_event_name` on
stdin as JSON — which is exactly the provenance triple (`session`, `cwd`,
`actor`) `bdc capture` needs, and is why a shim can stay a one-liner.

Codex has a smaller but shape-compatible set
([learn.chatgpt.com/docs/hooks](https://learn.chatgpt.com/docs/hooks)):
`SessionStart`, `SessionEnd`, `PreToolUse`, `PostToolUse`,
`PermissionRequest`, `PreCompact`, `PostCompact`, `UserPromptSubmit`,
`SubagentStart`, `SubagentStop`, `Stop` — configured in `~/.codex/hooks.json`,
`~/.codex/config.toml` `[hooks]`, or the repo-level equivalents, with the same
stdin JSON convention (`session_id`, `transcript_path`, `cwd`,
`hook_event_name`, `model`).

OpenCode and Amp expose no equivalent documented command-hook surface. For them
the skill body is the whole integration.

### Durable completion (PR merge/close)

**No agent harness has a hook for PR merge or PR close.** The design's "must run
near durable completion points such as PR merge or close" is therefore not
satisfiable by session hooks — `SessionEnd` fires when the *session* ends, which
is neither necessary nor sufficient for a merged PR.

Three mechanisms actually reach that point, in order of portability:

1. **Explicit skill step.** The skill instructs the agent to run
   `bdc harvest` before it opens or merges a PR. Portable, zero config, but
   advisory.
2. **Git hooks.** `pre-push` and `post-merge` are harness-independent and fire on
   the real durable events for local merges and for the push that precedes a PR.
   Beads already ships this pattern — `bd hooks install` chains `pre-commit`,
   `post-merge`, `pre-push`, `post-checkout`, `prepare-commit-msg` through thin
   shims that call `bd hooks run <hook>`. Beadcrumbs should mirror it
   (`bdc hooks install`) rather than invent a new one; the shims must chain, not
   clobber, since `bd`'s hooks may already own those files.
3. **CI on the merge event.** A workflow on `pull_request: closed` /
   `push: main` running `bdc harvest --json` is the only mechanism that is
   *guaranteed* to observe a remote PR merge.

(2) and (3) are the enforceable ones; (1) is the one that works in a fresh
clone with nothing installed. All three call the same command.

## Consequences

- `skills/beadcrumbs/SKILL.md` is the only shipped artifact required for
  cross-agent support; `docs/` gains an optional hooks appendix per harness.
- The skill body must state the `bdc` command sequence literally (capture →
  crumb review → harvest → insight → promote propose → context/handoff), because
  that text is the contract every harness shares.
- `bdc` must degrade loudly-but-safely when absent: the skill has to tell the
  agent how to detect a missing/old `bdc` (a `bdc --version` check) since the
  installer provides no dependency mechanism.
- Optional shims are per-harness files, and the Claude Code one may ship as a
  skills-directory plugin (`.claude-plugin/plugin.json`, which the installer
  already understands via `src/plugin-manifest.ts`) so hooks travel with the
  skill for that harness only.

## Open risks

| Risk | Mitigation |
|---|---|
| Codex global skill path ambiguity (`~/.codex/skills` vs `$HOME/.agents/skills`) | verify against the installed Codex build during Gate 1; prefer project-scoped `.agents/skills` in the prototype |
| Installer updates are tree-SHA based, so a breaking `bdc` change silently reaches old CLIs | version-guard inside the skill body; keep the JSON envelope versioned |
| Git hook installation collides with `bd hooks` | chain through shims; `bdc hooks install` must detect and preserve existing hooks |
| Hooks drift as harnesses change | hooks stay optional and documented as such; nothing in the core workflow may depend on them |

## Related Documentation

| Document | Description |
|---|---|
| **[Reasoning ledger design](2026-08-27-reasoning-ledger-design.md)** | Approved v1 architecture and CLI contract |
| [Reasoning ledger Wayfinder](reasoning-ledger-wayfinder.md) | Portable decision dependency path |
| [Beads JSON contract research](2026-08-28-beads-json-contract-research.md) | Optional tracker integration the skill may reference |
| [Destination model research](2026-08-28-destination-model-research.md) | What the skill's promote step targets |
| [Dolt reasoning ledger v1 plan](2026-08-28-dolt-reasoning-ledger-v1-plan.md) | Implements the skill package and optional hooks (§4) |
