# Beadcrumbs: Project Configuration Template

> Copy and adapt this template for your project's agent docs or CLAUDE.md.
> Core CLI commands are injected by `bdc prime` hook at session start.
> This doc covers project-specific conventions.

## Author Naming

| Author | `--author` value | When to use |
|--------|-----------------|-------------|
| Human developer | `--author <your-name>` | Human-initiated insights, feedback, decisions |
| Claude (default) | `--author cc:opus-4.6` | Default AI agent work |
| Claude Sonnet | `--author cc:sonnet-4` | Lighter AI agent tasks |
| Other AI tools | `--author <tool>:<model>` | e.g., `codex`, `cursor:claude-sonnet-4` |

Use the author that *initiated* the insight, not who typed it. If a human
gives direction and the agent captures it, use `--author <human> --feedback`.

## Thread Reference Priority

The `--thread` flag accepts multiple reference formats. bdc does NOT require a Beads issue — it works directly with external tracker refs.

**Prefer in this order:**

1. **External tracker ref** (most common): `--thread linear:ENG-456`, `--thread jira:PROJ-123`, `--thread gh:42`
2. **Bead ID** (if using beads): `--thread bd-a1b2`
3. **Thread ID** (resuming existing): `--thread thr-xxxx`
4. **New thread** (ad-hoc work): `bdc thread new "Descriptive title"`

## Thread Naming

Name threads for the *outcome* being pursued, not the ticket ID.

| Good | Bad |
|------|-----|
| `"Implement caching layer for API"` | `"PROJ-567"` |
| `"Debug auth timeout on login"` | `"Work on auth"` |
| `"Design billing integration"` | `"Billing stuff"` |

## Integration Model

bdc works with any combination of task trackers. A single thread can link to multiple systems simultaneously:

| Scenario | Thread Reference | Example |
|----------|-----------------|---------|
| **External tracker only** (most common) | `--thread linear:ENG-456` | Feature work, bug fixes |
| **Beads only** | `--thread bd-a1b2` | Quick implementation tasks |
| **Tracker + Beads** (multi-system) | `--linear ENG-456 --bead bd-abc1` | Epic with beads subtasks |
| **Neither** | `bdc thread new "..."` | Ad-hoc exploration, spike work |

### Multi-System Linking

When work has both a high-level tracker ticket and implementation-level bead tasks, link the thread to both:

```bash
# Create thread linked to both systems
bdc thread new "Batch import pipeline" --linear ENG-456 --bead bd-abc1

# Or link incrementally
bdc capture --thread linear:ENG-456 --hypothesis "..." --author cc:opus-4.6
bdc thread link thr-xxxx bd-abc1

# Generic linking works with any external ref format
bdc thread link thr-xxxx jira:PROJ-123
bdc thread link thr-xxxx github:myorg/myrepo#42
```

### Linking Between Systems

```bash
# Thread-level: associate reasoning thread with a bead
bdc capture --thread bd-abc1 --decision "Using batch processing" --author cc:opus-4.6

# Dependency-level: insight spawns a Beads task
bdc link ins-7f2a --spawns=bd-abc1

# Trace reasoning behind a task (shows both thread and dependency links)
bdc trace bd-abc1

# Create a Beads task directly from an insight
bdc spawn ins-7f2a --title="Add retry logic for batch insert failures"
```

## Origin Conventions

Origins identify *where* insights came from — which AI session, Notion doc, Slack thread, or other external context produced them. This enables "session reload" (finding all insights from a given session) and cross-session traceability.

### Naming Format

Origins follow a `system:identifier` convention:

| System | Example Origin | Source Type |
|--------|---------------|-------------|
| Claude session | `claude:sess_abc123` | `ai-session` |
| Cursor session | `cursor:workspace-id` | `ai-session` |
| Codex session | `codex:run-456` | `ai-session` |
| Warp terminal | `warp:session-789` | `ai-session` |
| Gemini session | `gemini:conv-xyz` | `ai-session` |
| Zed session | `zed:workspace-id` | `ai-session` |
| OpenCode session | `opencode:sess-abc` | `ai-session` |
| Notion page | `notion:page-xyz` | `human` |
| Slack thread | `slack:C0123-1234567` | `human` |
| Basecamp card | `basecamp:12345` | `human` |

### Context File vs Explicit Flag

**Context file** (`bdc origin set`) — best for session-scoped origins that apply to all captures in a session:

```bash
bdc origin set claude:sess_abc123
# All subsequent captures auto-populate origin
bdc capture --thread <ref> --hypothesis "..." --author cc:opus-4.6
```

**Explicit flag** (`--origin`) — best for one-off captures from a specific external source:

```bash
bdc capture --thread <ref> --origin notion:page-xyz \
  --feedback "Design spec updated with new requirements" --author brian
```

**Resolution precedence**: `--origin` flag > `BDC_ORIGIN` env var > `.beadcrumbs/origin` file

### Querying by Origin

```bash
# Find all insights from a specific session
bdc list --origin claude:sess_abc123
bdc timeline --origin claude:sess_abc123

# Discover all origins with counts
bdc origins
```

## When bdc Adds Value vs Noise

### Capture (worth the overhead)

- Choosing between two architectural approaches
- Discovering a library limitation or API behavior
- Human changing requirements mid-session
- Abandoning an approach after evidence
- Making a final technical decision
- Finding something unexpected during exploration

### Skip (noise, not signal)

- Routine file reads, grep, glob operations
- Running builds, tests, linters
- Minor formatting or whitespace changes
- Restating what the user said without adding reasoning
- Single-line bug fixes with obvious cause
- Simple acknowledgments

**Rule of thumb**: If a future agent would benefit from knowing *why* you
made this choice (not just *what* you did), capture it.

## Auto-Staging

`.beadcrumbs/insights.jsonl`, `.beadcrumbs/threads.jsonl`, and
`.beadcrumbs/deps.jsonl` are automatically staged on every commit via a
pre-commit hook installed by `bdc init`. This keeps the JSONL
files in sync with the SQLite DB.

To skip the hook for a single commit:

```bash
SKIP=beadcrumbs git commit -m "message"
```

## Linear Integration

If your project uses Linear, bdc can link threads to issues and auto-post summaries.

### Setup

```bash
brew install schpet/tap/linear    # Install a Linear CLI (recommended)
linear auth login                 # Authenticate
bdc linear setup                  # Auto-detect and configure
```

### Thread Creation

```bash
# Auto-create on first capture (recommended)
bdc capture --thread linear:ENG-456 --hypothesis "..." --author cc:opus-4.6

# Or explicit creation with --linear flag
bdc thread new "Feature title" --linear ENG-456
```

Using `--thread linear:ENG-456` auto-creates a thread linked to the issue and fetches the issue title from Linear.

### Auto-Push on Close

When a concluded thread linked to a Linear issue is closed, bdc posts a summary comment containing decisions, pivots, and discoveries. Disable with:

```bash
bdc linear config auto_push false
```

For full setup and troubleshooting, see the [Linear Integration Guide](linear.md).

## More Guides

- [Lifecycle Guide](lifecycle.md) — Session-by-session workflow from start to close
- [Stealth Mode Guide](stealth-mode.md) — Local-only usage, worktree support, and mode switching
- [Linear Integration](linear.md) — Full Linear setup, auto-push, troubleshooting
