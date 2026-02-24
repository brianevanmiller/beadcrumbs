# Beadcrumbs

**Git-backed insight tracking for AI agents and developers.**

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/brianevanmiller/beadcrumbs)](https://goreportcard.com/report/github.com/brianevanmiller/beadcrumbs)

Beadcrumbs provides a persistent, structured memory of insights and understandings derived through human and agentic dialogue. It gives your coding agents access to the critical discoveries, insights and decisions powering the product.

[Beads](https://github.com/steveyegge/beads) tracks tasks without losing **work context** of what was done and what needs to be done. Beadcrumbs tracks the **understanding context** and ultimate intent behind these tasks.

## Quick Start

```bash
# Install (macOS/Linux)
curl -fsSL https://raw.githubusercontent.com/beadcrumbs/beadcrumbs/main/scripts/install.sh | bash

# Initialize
bdc init

# Capture insights
bdc capture "Found: the bug is in JWT validation, not sessions" --pivot

# Set up Claude Code integration (optional)
bdc setup claude
```

## Features

* **Git as Database:** Insights stored as JSONL in `.beadcrumbs/`. Versioned like code.
* **AI-Native:** Import from AI session transcripts, auto-extract insights.
* **Narrative Reconstruction:** Timeline, story, and graph views of understanding evolution.
* **Beads Integration:** Link insights to beads tasks with `spawns` and `informed-by` relationships.
* **Linear Integration:** Link threads to Linear issues and auto-post insight summaries on thread close.
* **Pivot Preservation:** Pivots and decisions are sacred; discovery chains compress.

## Essential Commands

| Command | Action |
| --- | --- |
| `bdc capture "..."` | Capture an insight with type flags |
| `bdc origin set <id>` | Set session origin identifier |
| `bdc origins` | List all origins with insight counts |
| `bdc timeline` | View chronological journey |
| `bdc pivots` | Show only pivot moments |
| `bdc decisions` | Show only decisions |
| `bdc feedback` | Show only external feedback |
| `bdc questions` | Show open questions |
| `bdc import file.txt` | Import from AI session transcript |
| `bdc locate` | Find databases reachable from CWD |
| `bdc linear setup` | Configure Linear integration |
| `bdc linear status` | Show Linear integration status |

## Insight Types & Mental Model

Beadcrumbs tracks how understanding evolves through dialogues. Each insight type represents a moment in the journey:

```
hypothesis → discovery → question → feedback → pivot → decision
   ↑           ↑           ↑          ↑         ↑        ↑
  start    investigate   doubt    get input  change   commit
```

| Type | Trigger | Symbol | Description |
|------|---------|--------|-------------|
| `hypothesis` | "I think..." | ○ | Speculation before evidence |
| `discovery` | "I found..." | ○ | Evidence-based finding |
| `question` | "What about...?" | ? | Open uncertainty |
| `feedback` | "They said..." | » | External input received |
| `pivot` | "Actually..." | ● | Direction changed |
| `decision` | "We'll do..." | ◆ | Committed to approach |

```bash
bdc capture "Might be a caching issue" --hypothesis
bdc capture "Found: race condition in Redis pub/sub" --discovery
bdc capture "How should we handle timeouts?" --question
bdc capture "Code review: add retry logic" --feedback
bdc capture "Actually, it's not Redis—it's our retry logic" --pivot
bdc capture "Decision: implement exponential backoff" --decision
```

## Installation

**curl (recommended):**
```bash
curl -fsSL https://raw.githubusercontent.com/beadcrumbs/beadcrumbs/main/scripts/install.sh | bash
```

**npm:**
```bash
npm install -g @beadcrumbs/bdc
```

**Go:**
```bash
go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@latest
```

**From source:**
```bash
git clone https://github.com/brianevanmiller/beadcrumbs.git
cd beadcrumbs
go build -o bdc ./cmd/bdc/
sudo mv bdc /usr/local/bin/
```

## Beads Integration

beadcrumbs works seamlessly alongside [beads](https://github.com/steveyegge/beads):

```bash
# Link an insight to a bead task
bdc link ins-7f2a --spawns=bd-abc1

# Trace what insights led to a bead
bdc trace bd-abc1

# Create a bead from an insight
bdc spawn ins-7f2a --title="Implement exponential backoff"
```

## Timeline View

```
2024-01-15 10:30  ○ "Bug reports: slow login" [hypothesis]
2024-01-15 14:22  ○ "Traced to session validation" [discovery]
2024-01-15 16:00  ? "What about token refresh?" [question]
2024-01-16 08:00  » "Security team: check JWT expiry" [feedback]
2024-01-16 09:15  ● "Actually JWT, not session" [PIVOT]
2024-01-16 11:00  ◆ "Upgrade to JWT v3" [DECISION]
                     └── spawns: bd-7f2a
```

## Storage

```
.beadcrumbs/
  insights.jsonl    # All insights
  threads.jsonl     # Narrative threads
  deps.jsonl        # Relationships
  beadcrumbs.db     # SQLite for queries
```

Git-backed like beads: JSONL exports on commit, imports on merge. Use `bdc init --stealth` for local-only mode that doesn't touch your repo. See the [Stealth Mode Guide](docs/guides/stealth-mode.md) for details and mode switching.

## Git Worktree Support

bdc automatically resolves the database from git worktrees, nested directories, or the main repo — no configuration needed. All worktrees share the main repo's database via `git rev-parse --git-common-dir`. If a worktree has its own `.beadcrumbs/`, it takes precedence (closest wins).

If automatic resolution fails (e.g., CWD is a workspace parent), use `bdc locate` to find reachable databases and set `BDC_DB_PATH`. See the [Stealth Mode Guide](docs/guides/stealth-mode.md#how-it-works-with-git-worktrees) for the full worktree topology.

## Full Command Reference

### Capture & Thread Management
```bash
bdc init                              # Initialize repository
bdc capture "..." [--type=X]          # Capture insight
bdc capture "..." --origin claude:id  # Capture with explicit origin
bdc thread new "title"                # Create narrative thread
bdc thread new "title" --linear ENG-456 --bead bd-abc1  # Multi-system linking
bdc thread link <id> <ref>            # Link thread to any external ref
bdc thread show <id>                  # Show thread details
bdc thread list [--status=active]     # List threads
bdc thread close <id>                 # Close thread
```

### Origin Tracking
```bash
bdc origin set <system:id>            # Set origin for this session
bdc origin show                       # Show current origin
bdc origin clear                      # Clear origin
bdc origins                           # List all origins with counts
```

### Viewing & Analysis
```bash
bdc timeline [thread-id]              # Chronological view
bdc timeline --origin <system:id>     # Filter by origin
bdc pivots [thread-id]                # Filter to pivots
bdc decisions [thread-id]             # Filter to decisions
bdc feedback [thread-id]              # Filter to external feedback
bdc questions [--unresolved]          # Open questions
bdc list [--type=X] [--since=1w]      # List insights
bdc list --origin <system:id>         # Filter by origin
bdc show <id>                         # Show insight details
```

### Relationships
```bash
bdc link <id> --builds-on=<id>        # Extends understanding
bdc link <id> --supersedes=<id>       # Replaces/corrects
bdc link <id> --contradicts=<id>      # Unresolved tension
bdc link <id> --spawns=<bead-id>      # Led to task
```

### Import
```bash
bdc import file.txt                   # Import AI session
bdc import slack-export/              # Import Slack export
bdc import file --dry-run             # Preview extraction
```

### Beads Integration
```bash
bdc trace <bead-id>                   # Trace insight chain
bdc spawn <insight-id> --title="..."  # Create task from insight
```

### Database & Setup
```bash
bdc locate                            # Find databases reachable from CWD
bdc prime                             # Output AI workflow context
bdc setup claude                      # Configure Claude Code hooks
bdc stealth / unstealth               # Switch between local-only and git-tracked mode
bdc stealth --status                  # Show current mode
```

See [Stealth Mode Guide](docs/guides/stealth-mode.md) for mode switching details.

### Linear Integration
```bash
bdc linear setup                      # Detect and configure Linear CLI
bdc linear status                     # Show integration status
bdc linear link <thread-id> <issue>   # Link thread to Linear issue
bdc linear push <thread-id>           # Post summary to Linear issue
bdc linear config <key> [value]       # Get/set Linear config
bdc thread new "title" --linear ENG-456  # Create thread linked to issue
```

See [Linear Integration Guide](docs/guides/linear.md) for full setup and troubleshooting.

## Use Cases

* **Resume interrupted work** — Reconstruct where you left off
* **Document rationale** — Preserve why decisions were made
* **Extract insights from exploration** — Turn vibe-coding sessions into structured knowledge
* **Aid onboarding** — Show newcomers how understanding evolved
* **Long-term memory** — Understand how the product came to be

## Project Setup Guides

* **[AI Agent Guide](BDC_GUIDE.md)** — Copy into your CLAUDE.md or AI agent config for automatic bdc usage
* **[Lifecycle Guide](docs/guides/lifecycle.md)** — 6-phase workflow from session start to cross-session resumption
* **[Project Config Template](docs/guides/project-config.md)** — Author naming, thread conventions, signal vs noise guidance
* **[Insight Types Deep Dive](docs/insight-types.md)** — When to use each of the 6 insight types
* **[Linear Integration Guide](docs/guides/linear.md)** — Connect bdc to Linear for bi-directional issue linking
* **[Stealth Mode Guide](docs/guides/stealth-mode.md)** — Local-only usage, worktree support, and mode switching
* **[Pre-commit Framework Config](docs/guides/pre-commit-config.yaml)** — Alternative hook config for pre-commit users

## License

MIT
