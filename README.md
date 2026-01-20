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
```

## Features

* **Git as Database:** Insights stored as JSONL in `.beadcrumbs/`. Versioned like code.
* **AI-Native:** Import from AI session transcripts, auto-extract insights.
* **Narrative Reconstruction:** Timeline, story, and graph views of understanding evolution.
* **Beads Integration:** Link insights to beads tasks with `spawns` and `informed-by` relationships.
* **Pivot Preservation:** Pivots and decisions are sacred; discovery chains compress.

## Essential Commands

| Command | Action |
| --- | --- |
| `bdc capture "..."` | Capture an insight with type flags |
| `bdc timeline` | View chronological journey |
| `bdc pivots` | Show only pivot moments |
| `bdc decisions` | Show only decisions |
| `bdc feedback` | Show only external feedback |
| `bdc questions` | Show open questions |
| `bdc import file.txt` | Import from AI session transcript |

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

Git-backed like beads: JSONL exports on commit, imports on merge.

## Full Command Reference

### Capture & Thread Management
```bash
bdc init                              # Initialize repository
bdc capture "..." [--type=X]          # Capture insight
bdc thread new "title"                # Create narrative thread
bdc thread show <id>                  # Show thread details
bdc thread list [--status=active]     # List threads
bdc thread close <id>                 # Close thread
```

### Viewing & Analysis
```bash
bdc timeline [thread-id]              # Chronological view
bdc pivots [thread-id]                # Filter to pivots
bdc decisions [thread-id]             # Filter to decisions
bdc feedback [thread-id]              # Filter to external feedback
bdc questions [--unresolved]          # Open questions
bdc list [--type=X] [--since=1w]      # List insights
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

## Use Cases

* **Resume interrupted work** — Reconstruct where you left off
* **Document rationale** — Preserve why decisions were made
* **Extract insights from exploration** — Turn vibe-coding sessions into structured knowledge
* **Aid onboarding** — Show newcomers how understanding evolved
* **Long-term memory** — Understand how the product came to be

## License

MIT
