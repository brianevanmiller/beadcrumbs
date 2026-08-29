# Where the ledger lives

`bdc init` puts the ledger inside the repository's Git directory:

```
$(git rev-parse --path-format=absolute --git-common-dir)/beadcrumbs/bdc/.dolt
```

That is the default and there is no flag to turn it on. Two properties fall out of the path
itself rather than out of configuration, which is why it is the default:

- **Git cannot see it.** The ledger is inside `.git/`, so `git status` is unaffected with no
  ignore file to edit and none to accidentally commit.
- **Worktrees share it.** `--git-common-dir` returns the same absolute path from the repository
  root and from every linked worktree, so a worktree resolves the same ledger with no
  per-worktree setup and no bookkeeping.

## Discovery

Every command resolves the ledger the same way. There is no search path, no walk-up, and no
environment override:

1. `git rev-parse --path-format=absolute --git-common-dir` → the shared Git directory.
2. `git worktree list --porcelain`, first `worktree` line → the main worktree.
3. Look for `<git-common-dir>/beadcrumbs` and `<main-worktree>/.beadcrumbs`.

Inherited `GIT_DIR`, `GIT_WORK_TREE`, and friends are stripped before those calls, and the
resolved root is then asserted to contain the working directory — a hostile environment cannot
redirect the ledger to another repository. Outside a Git repository, and inside a bare
repository, `bdc` fails with exit 5 rather than guessing.

**Two ledgers is an integrity error, not a coin flip.** If both paths hold a Dolt database,
`bdc` refuses with `integrity_two_ledgers` and names both. Remove one.

## `--visible`

```bash
bdc init --visible
```

Places the ledger at `<main-worktree>/.beadcrumbs` and appends `/.beadcrumbs/` to
`.git/info/exclude`. `info/exclude` is shared across worktrees and is never committed, so
`git status` stays clean everywhere without touching a tracked `.gitignore`.

Use it when you want the ledger directory visible to your tools — a file browser, a backup
script, an editor's tree. It is not a "shared with the team" mode: the ledger is still local, and
nothing in v1 commits it.

`--stealth` and `--visible` are mutually exclusive, and neither converts an existing ledger.
There is no `bdc stealth` / `bdc unstealth`; to move, `bdc backup` and then `bdc restore` into
the other mode.

## Backups

The ledger is not in your repository's history, so nothing backs it up for you.

```bash
bdc backup file:///path/to/backups/repo-ledger
bdc restore file:///path/to/backups/repo-ledger --force
```

Backup carries Dolt history, not just the current head. Restore stages into a temporary directory
and swaps atomically: a killed restore leaves the original ledger intact, and `bdc doctor`
reports any leftovers.

## Checking

```bash
bdc doctor --verbose
```

`ledger_path` in the output is the resolved directory. `bdc doctor` also reports the schema
version, the chunk-journal size (`bdc gc` reclaims it), and whether an interrupted restore left
anything behind.

---

**Related:** [Command reference](../../BDC_GUIDE.md) · [Hooks](hooks.md)
