# Stealth Mode Guide

Stealth mode lets you use beadcrumbs locally without committing anything to the repository. Your `.beadcrumbs/` directory stays invisible to `git status` and your teammates.

## When to Use Stealth Mode

- **Evaluating beadcrumbs** before proposing it to your team
- **Personal reasoning logs** you don't want in the shared repo
- **Client repos** where you can't modify the tracked file tree
- **Quick experiments** where full git integration is overkill

## Setup

```bash
cd /path/to/your/repo
bdc init --stealth
bdc setup claude          # Global Claude Code hooks (recommended)
```

That's it. All `bdc` commands work normally from here.

### What `--stealth` Does

1. Creates `.beadcrumbs/` with the database and empty JSONL files (same as normal init)
2. Adds `.beadcrumbs/` to `.git/info/exclude` (local-only gitignore, never committed)
3. Sets `stealth_mode=true` in the database config
4. **Skips** `.gitignore` modifications (nothing committed)
5. **Skips** git hook installation (no pre-commit/post-merge/post-checkout hooks)

### What It Does NOT Do

- Does not modify any tracked files (no `.gitignore` changes, no hook scripts in `.git/hooks/`)
- Does not affect other developers or CI
- Does not prevent you from using `bdc` commands -- everything works identically

## How It Works with Git Worktrees

If your project uses git worktrees (e.g., via [Conductor](https://github.com/AstroMillennial/conductor)), stealth mode works seamlessly across all worktrees without any per-worktree setup.

### The Topology

```
Main repo (has .beadcrumbs/)
  /Users/you/repos/my-project/
  ├── .beadcrumbs/beadcrumbs.db    ← single source of truth
  ├── .git/info/exclude            ← contains ".beadcrumbs/" (stealth)
  └── src/...

Worktrees (no .beadcrumbs/, resolved automatically)
  /Users/you/workspaces/my-project/
  ├── feature-branch-1/            ← bdc resolves to main repo's DB
  ├── feature-branch-2/            ← bdc resolves to main repo's DB
  └── bugfix-branch/               ← bdc resolves to main repo's DB
```

### Why It Works

When you run any `bdc` command from a worktree, the resolution chain is:

1. Walk up from CWD looking for `.beadcrumbs/beadcrumbs.db` -- not found in worktree
2. Run `git rev-parse --git-common-dir` -- returns the main repo's `.git` path
3. Take the parent of that path -- that's the main repo root
4. Check for `.beadcrumbs/beadcrumbs.db` there -- found

All reads and writes flow back to the single database in the main repo.

### AI Agent Awareness

Three mechanisms ensure AI agents in worktrees know about bdc:

| Mechanism | Scope | What It Does |
|-----------|-------|-------------|
| `~/.claude/settings.json` hooks | Global | `bdc prime` fires on SessionStart and PreCompact in every Claude Code session |
| `~/.claude/CLAUDE.md` | Global | Contains the full bdc workflow guide -- agents know how to use bdc commands |
| `CLAUDE.md` in repo | Per-project | Tracked in git, so worktrees check it out automatically |

No per-worktree configuration is needed. An agent starting in any worktree will:
1. Receive `bdc prime` output (via global SessionStart hook)
2. Read the bdc workflow instructions (via global CLAUDE.md)
3. Successfully run all `bdc` commands (via git-common-dir DB resolution)

## Switching Modes

### Stealth to Normal (`bdc unstealth`)

When you're ready to share beadcrumbs with your team:

```bash
bdc unstealth
```

This will:
1. Remove `.beadcrumbs/` from `.git/info/exclude`
2. Add SQLite database entries to `.gitignore` (JSONL files get tracked)
3. Install git hooks (pre-commit, post-commit, post-merge, post-checkout)
4. Update the config to `stealth_mode=false`
5. Export current insights to JSONL for version control

After unstealthing, your next `git add` and `git commit` will include the `.beadcrumbs/` JSONL files. Teammates who `git pull` can run `bdc import` to rebuild their local database.

### Normal to Stealth (`bdc stealth`)

If you installed bdc normally but want to go local-only:

```bash
bdc stealth
```

This will:
1. Add `.beadcrumbs/` to `.git/info/exclude`
2. Remove beadcrumbs entries from `.gitignore`
3. Remove beadcrumbs git hooks
4. Update the config to `stealth_mode=true`
5. Run `git rm --cached` on `.beadcrumbs/` JSONL files (unstages without deleting)

After stealthing, `.beadcrumbs/` files won't appear in `git status` and won't be included in future commits. Your existing data is preserved locally.

## Checking Current Mode

```bash
bdc stealth --status
```

Outputs one of:
- `stealth` -- currently in stealth mode
- `normal` -- currently in normal (git-tracked) mode

## FAQ

**Q: Can I stealth on one machine and normal on another?**
Yes. Stealth mode is stored in the local SQLite database and `.git/info/exclude`, neither of which is shared via git. Each clone/machine has its own mode.

**Q: What happens to my existing insights when switching modes?**
Nothing is lost. The SQLite database is unchanged. When going stealth-to-normal, `bdc unstealth` exports to JSONL so your insights become git-trackable. When going normal-to-stealth, `bdc stealth` unstages the JSONL files but doesn't delete them.

**Q: Do Claude Code hooks work in both modes?**
Yes. The `bdc setup claude` hooks are stored in `~/.claude/settings.json` (global) and fire in every session regardless of stealth mode. The `bdc prime` command finds `.beadcrumbs/` via the same walk-up + git-common-dir resolution.

**Q: What if I'm not in a git repo at all?**
`bdc init --stealth` gracefully skips the `.git/info/exclude` setup and prints a notice. The database is still created and usable. This is useful for non-git projects or standalone directories.
