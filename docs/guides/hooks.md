# Hooks (optional)

**Nothing in the Beadcrumbs workflow depends on a hook.** The portable contract
is the explicit `bdc` commands in `skills/beadcrumbs/SKILL.md`; hooks only make
some of them automatic. Skip this whole page and Beadcrumbs still works.

Two rules hold for every hook below:

- **A hook never fails the operation that triggered it.** `bdc hooks run` exits
  0 for a busy ledger, a missing ledger, and a harvest that could not run,
  writing one line to stderr naming what it skipped. A silent miss is how a
  harvest gets lost, so the line is not optional.
- **Automatic harvesting is opt-in per repository, off by default.** Until you
  run `bdc hooks install --auto-harvest`, every trigger counts what is
  outstanding and writes nothing.

## Durable completion: git hooks

No agent harness has a hook for a pull request being merged. `pre-push` and
`post-merge` are the closest real events, and they are harness-independent.

```
bdc hooks install                  # shims only; harvesting stays manual
bdc hooks install --auto-harvest   # shims, and opt this repository in
bdc hooks uninstall                # remove the shims and opt back out
```

The shims are written to `core.hooksPath` when it is set and
`<git-common-dir>/hooks` otherwise, and they chain:

```sh
prior="$(dirname "$0")/pre-push.beadcrumbs-prior"
status=0
if [ -x "$prior" ]; then "$prior" "$@" || status=$?; fi
if command -v bdc >/dev/null 2>&1; then bdc hooks run pre-push || true; fi
exit $status
```

An existing hook — `bd hooks install` may already own these files — is moved to
`<hook>.beadcrumbs-prior`, runs **first** so it gets the stdin git supplies, and
its exit status is the shim's. `bdc hooks uninstall` restores it.

`bdc hooks install` reports what it did:

```json
{
  "hooks": [
    {"hook": "pre-push",   "path": "…/.git/hooks/pre-push",   "action": "chained"},
    {"hook": "post-merge", "path": "…/.git/hooks/post-merge", "action": "installed"}
  ],
  "chained": ["pre-push"],
  "auto_harvest": false
}
```

`action` is one of `installed`, `chained`, `unchanged` (install) or `removed`,
`restored`, `absent`, `foreign` (uninstall). A `foreign` hook is one bdc did not
write: it is reported and left exactly as it is, `--force` or not.

## Durable completion: CI

A workflow on `pull_request: closed` is the only mechanism guaranteed to observe
a remote merge:

```yaml
on:
  pull_request:
    types: [closed]
jobs:
  harvest:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: bdc harvest --since 24h --json
```

## Session hooks: Claude Code and Codex

Both deliver `{session_id, transcript_path, cwd, hook_event_name}` as JSON on
stdin, so `hooks/bdc-hook.sh` serves both. It reads `session_id` and `cwd`, sets
`BDC_SESSION`, `BDC_ACTOR_KIND=agent`, and `BDC_ACTOR_MODEL` (from the
environment, or `unknown`), and shells out to a documented command. All three
matter: the ledger refuses an agent actor that does not name both a model and a
session. It never reads `transcript_path`: raw transcript is exactly what
Beadcrumbs does not persist.

| Event | Action | Writes? |
|---|---|---|
| `SessionStart` | `bdc prime` — stdout is injected as context | no |
| `PreCompact` | `bdc hooks run pre-compact` | only when opted in |
| `Stop`, `SubagentStop` | `bdc hooks run stop` | never |
| `SessionEnd` | `bdc hooks run session-end` | only when opted in |

`Stop` reports and never writes even with `--auto-harvest` on: a turn ending is
not a durable completion point, and blocking a stop to harvest is worse than the
reminder.

A trigger that may harvest waits up to 60 s for a ledger another `bdc` is
holding, because giving up early is what loses the harvest. `Stop` writes
nothing, so it declines after 2 s instead — which is why its harness timeout can
be short while `PreCompact` and `SessionEnd` need 90. Every trigger exits 0
either way and names what it skipped on stderr.

### Claude Code

Install the plugin, which carries `hooks/hooks.json` alongside the skill:

```
npx skills add brianevanmiller/beadcrumbs@v1.0.0 -y
```

Or wire it by hand in `.claude/settings.json`, using an absolute path to the
script:

```json
{
  "hooks": {
    "PreCompact": [
      {"matcher": "manual|auto",
       "hooks": [{"type": "command", "command": "/path/to/hooks/bdc-hook.sh PreCompact", "timeout": 90}]}
    ]
  }
}
```

### Codex

`~/.codex/hooks.json`, same script, same event names:

```json
{
  "hooks": {
    "PreCompact": [{"hooks": [{"type": "command", "command": "/path/to/hooks/bdc-hook.sh PreCompact"}]}]
  }
}
```

### OpenCode and Amp

Neither exposes a command-hook surface. For those harnesses the skill body is
the whole integration, which is why the skill states the command sequence
literally.

## What a trigger actually does

```
bdc hooks run <pre-push|post-merge|pre-compact|session-end|stop> --json
→ {"hook": "pre-push", "action": "remind", "result": "3 unharvested Crumb(s); run `bdc harvest` …"}
```

| `action` | Meaning |
|---|---|
| `harvest` | the repository opted in; an automatic Harvest weighed the outstanding candidates |
| `remind` | there is outstanding work and this repository harvests manually |
| `skipped` | nothing outstanding, or the ledger was busy, missing, or unreadable |

An automatic Harvest records what it weighed and stops; it never synthesises an
Insight. Synthesis is a judgement, and a hook is not in a position to make one.

## Turning it off

```
bdc hooks uninstall
```

Removes the shims, restores anything they displaced, and sets `harvest.auto`
back to off. It touches nothing already captured, harvested, or promoted —
opting out is a policy change, not a deletion. Remove the harness session-hook
entries by hand; they live in that harness's config, not in the ledger.
