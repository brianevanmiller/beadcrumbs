# beadcrumbs: Intent Evolution Tracker

## Summary

A standalone Git-backed CLI tool for tracking how understanding evolves through dialogues. Works independently, integrates seamlessly with beads when present.

**Architecture:**
- Separate repo: `beadcrumbs`
- Own storage: `.beadcrumbs/`
- Detects and links to `.beads/` when available
- Simple data model: `Insight`, `InsightThread`, `InsightSource`, `InsightType`

**User Direction:**
- Dialogues as input → Insights as output (AI summarizes)
- Graph + Timeline (DAG with timestamps)
- Narrative reconstruction, not "ready work"
- Preserve pivots/decisions, compress discovery chain
- Threads close only after definitive decision/action

---

## The Concept

**Beads**: Tracks WHAT needs to be done (tasks)
**beadcrumbs**: Tracks HOW understanding evolved (journey)

```
Dialogues → AI extracts → Insights (in DAG) → Narrative/Timeline
                              ↓
                         spawns → Beads (when present)
```

The insight contains the summarized understanding - no need to store raw dialogues.

---

## Data Model (Simple)

### `Insight`

The atomic unit - a moment of understanding.

```go
type Insight struct {
    ID          string        `json:"id"`           // e.g., "ins-7f2a"
    Timestamp   time.Time     `json:"timestamp"`    // When understanding occurred

    // Content (self-contained, summarized)
    Content     string        `json:"content"`      // The insight itself
    Summary     string        `json:"summary"`      // One-line summary

    // Classification
    Type        InsightType   `json:"type"`
    Confidence  float32       `json:"confidence"`   // 0.0-1.0

    // Source reference
    Source      InsightSource `json:"source"`

    // Thread membership
    ThreadID    string        `json:"thread_id,omitempty"`

    // Metadata
    Tags        []string      `json:"labels,omitempty"`
    CreatedBy   string        `json:"created_by,omitempty"`
    CreatedAt   time.Time     `json:"created_at"`
}
```

### `InsightType`

Each type represents a moment in the journey of understanding:

```
hypothesis → discovery → question → feedback → pivot → decision
   ↑           ↑           ↑          ↑         ↑        ↑
  start    investigate   doubt    get input  change   commit
```

```go
type InsightType string

const (
    InsightHypothesis InsightType = "hypothesis"  // "I think..." — speculation before evidence
    InsightDiscovery  InsightType = "discovery"   // "I found..." — evidence-based finding
    InsightQuestion   InsightType = "question"    // "What about...?" — open uncertainty
    InsightFeedback   InsightType = "feedback"    // "They said..." — external input received
    InsightPivot      InsightType = "pivot"       // "Actually..." — direction changed
    InsightDecision   InsightType = "decision"    // "We'll do..." — committed to approach
)
```

**Type Usage Guide:**

| Type | Trigger Phrase | When to Use |
|------|----------------|-------------|
| `hypothesis` | "I think...", "Maybe..." | Before evidence, initial guesses |
| `discovery` | "I found...", "It turns out..." | Evidence-based findings (default) |
| `question` | "What about...?", "How do we...?" | Open uncertainties |
| `feedback` | "They said...", "Review feedback:" | External input from others |
| `pivot` | "Actually...", "Wait, it's not..." | Direction changed significantly |
| `decision` | "We'll do...", "Decision:" | Committed to specific approach |

### `InsightSource`

Where the insight came from (reference only, content is in Insight).

```go
type InsightSource struct {
    Type         string   `json:"type"`                    // ai-session|slack|git|human
    Ref          string   `json:"ref,omitempty"`           // Optional external ref
    Participants []string `json:"participants,omitempty"`  // Who was involved
}
```

### `InsightThread`

Groups related insights into a narrative journey.

```go
type InsightThread struct {
    ID                   string    `json:"id"`      // e.g., "thr-9e1b"
    Title                string    `json:"title"`   // "Understanding the auth bug"
    Status               string    `json:"status"`  // active|concluded|abandoned

    // AI-generated summary of current understanding
    CurrentUnderstanding string    `json:"current_understanding,omitempty"`

    CreatedAt            time.Time `json:"created_at"`
    UpdatedAt            time.Time `json:"updated_at"`
}
```

### `Dependency` (for relationships)

Reuse beads-style dependency pattern.

```go
type Dependency struct {
    From      string         `json:"from"`       // ins-xxx or bead-xxx
    To        string         `json:"to"`         // ins-yyy or bead-yyy
    Type      DependencyType `json:"type"`
    CreatedAt time.Time      `json:"created_at"`
}

type DependencyType string

const (
    // Insight → Insight
    DepBuildsOn    DependencyType = "builds-on"    // Extends understanding
    DepSupersedes  DependencyType = "supersedes"   // Replaces/corrects
    DepContradicts DependencyType = "contradicts"  // Unresolved tension

    // Insight → Bead (when beads present)
    DepSpawns      DependencyType = "spawns"       // Led to task creation

    // Bead → Insight (when beads present)
    DepInformedBy  DependencyType = "informed-by"  // Task informed by insight
)
```

---

## Storage

### Directory Structure
```
.beadcrumbs/
  insights.jsonl      # All insights
  threads.jsonl       # Threads
  deps.jsonl          # Dependencies/relationships
  beadcrumbs.db           # SQLite for queries
  config.yaml         # Settings
  hooks/              # Git hooks
```

### Git-Backed
Same pattern as beads:
- Pre-commit: SQLite → JSONL
- Post-merge/checkout: JSONL → SQLite
- JSONL is append-friendly for merges

---

## CLI: `beadcrumbs` (alias: `bdc`)

### Quick Capture

```bash
# Capture an insight
bdc capture "Found: cache invalidation only fails on stale reads"
bdc capture "Should we use Redis or Memcached?" --type=question
bdc capture "Decision: use Redis for its pub/sub" --type=decision --thread=thr-xxx

# Shorthand types (in journey order)
bdc capture "..." --hypothesis   # "I think..." — speculation
bdc capture "..." --discovery    # "I found..." — evidence (default)
bdc capture "..." --question     # "What about...?" — uncertainty
bdc capture "..." --feedback     # "They said..." — external input
bdc capture "..." --pivot        # "Actually..." — direction change
bdc capture "..." --decision     # "We'll do..." — commitment
```

### Import & Extract

```bash
# Import and extract from dialogue file
bdc import <file>                      # Auto-detect, AI extracts insights
bdc import <file> --thread=thr-xxx     # Add to existing thread
bdc import <file> --dry-run            # Preview extractions

# Source types
bdc import --ai-session <file>         # Claude/ChatGPT transcript
bdc import --slack <export-or-url>     # Slack export or thread URL
bdc import --git-pr <number>           # GitHub PR discussion
```

### Thread Management

```bash
# Create thread
bdc thread new "Understanding the auth system"

# View thread
bdc thread show <thread-id>
bdc thread list [--status=active|concluded|abandoned]

# Close thread (only after definitive decision/action)
bdc thread close <thread-id>
bdc thread close <thread-id> --status=abandoned
```

### Relationships

```bash
# Link insights
bdc link <ins-a> --builds-on=<ins-b>
bdc link <ins-a> --supersedes=<ins-b>
bdc link <ins-a> --contradicts=<ins-b>

# Link to beads (when beads present)
bdc link <ins-id> --spawns=<bead-id>
```

### Narrative Reconstruction

```bash
# Timeline view (chronological)
bdc timeline [<thread-id>]
# Output (symbols: ○=hypothesis/discovery, ?=question, »=feedback, ●=PIVOT, ◆=DECISION):
# 2024-01-15 10:30  ○ "Bug reports: slow login" [hypothesis]
# 2024-01-15 14:22  ○ "Traced to session validation" [discovery]
# 2024-01-15 16:00  ? "What about token refresh?" [question]
# 2024-01-16 08:00  » "Security team: check JWT expiry" [feedback]
# 2024-01-16 09:15  ● "Actually JWT, not session" [PIVOT]
# 2024-01-16 11:00  ◆ "Upgrade to JWT v3" [DECISION]
#                      └── spawns: bead-7f2a

# Narrative view (AI-generated story)
bdc story <thread-id>
# Output:
# The journey began on Jan 15 with slow login reports. Initial
# investigation pointed to session validation, but a key pivot
# came when we discovered JWT token handling was the root cause...

# Graph view
bdc graph <thread-id>

# Filter by type
bdc pivots [<thread-id>]      # Show only pivots
bdc decisions [<thread-id>]   # Show only decisions
bdc feedback [<thread-id>]    # Show only external feedback
bdc questions [--unresolved]  # Show open questions
```

### Query & Search

```bash
bdc list [--thread=<id>] [--type=<type>] [--since=1w]
bdc show <insight-id>
bdc search "cache invalidation"
bdc recent [--since=1w]
```

### Beads Integration (when `.beads/` present)

```bash
# Trace: what insights led to this bead?
bdc trace <bead-id>
# Shows the insight chain that spawned this task

# Create bead from insight (invokes bd create)
bdc spawn <insight-id> --title="Upgrade JWT library"
# Equivalent to: bd create --title="..." --informed-by=<insight-id>
```

---

## Key Design Principles

1. **Simplicity** - Core types are minimal, evolve as needed
2. **Self-contained** - Insights have summarized content, no raw storage
3. **AI-native** - AI extracts from dialogues, generates narratives
4. **Git-backed** - Same collaboration model as beads
5. **Beads-compatible** - Seamless integration, graceful without
6. **Preservation** - Pivots and decisions are sacred, discoveries compress

---

## Amendment: Author Tracking & Extended Features (v0.0.1)

*Added January 2026*

### Author Tracking (Simplified)

Insights track who captured them and who endorsed them:

```go
// Added to Insight struct
AuthorID   string   `json:"author_id,omitempty"`   // Who captured/recorded this insight
EndorsedBy []string `json:"endorsed_by,omitempty"` // Who endorsed this insight
```

**Semantic distinction:**
- `AuthorID` = who captured/recorded this insight (e.g., "brian", "cc:opus-4.5")
- `EndorsedBy` = who endorsed/approved the insight
- `Source.Participants` = who was in the original conversation the insight came from

> **Design Note:** An `IntentAuthor` table with canonical IDs and alias mapping was
> originally implemented but abandoned in favor of simplicity. The overhead of
> managing a separate author table with O(n) alias lookups didn't justify the
> benefit. Instead, author attribution uses simple strings stored directly on
> insights. Users who need cross-platform identity mapping can use consistent
> naming conventions (e.g., always use "brian" or "cc:opus-4.5").

### AI Agent Naming Convention

AI agents use the format `tool:model`:

| Tool | Model | Example |
|------|-------|---------|
| Claude Code | Opus 4.5 | `cc:opus-4.5` |
| Claude Code | Sonnet 4 | `cc:sonnet-4` |
| Codex CLI | GPT-5.2 | `codex:gpt-5.2-codex-xhigh` |
| Cursor | Claude Sonnet | `cursor:claude-sonnet-4` |

### Endorsements

The `--endorsed-by` flag tracks who signed off on an insight:

```bash
bdc capture --decision "Use Redis for caching" \
    --author brian \
    --endorsed-by alice \
    --endorsed-by cc:opus-4.5
```

### Settable Timestamps

When importing historical data (Slack archives, old transcripts), preserve original timing:

```bash
# Set base timestamp for import
bdc import session.txt --timestamp="2024-01-15T10:30:00Z"
bdc import session.txt --timestamp="2024-01-15"
bdc import session.txt --timestamp="Jan 15, 2024"

# Capture with specific timestamp
bdc capture "Found the bug" --timestamp="2d ago"
bdc capture "Decision made" --timestamp="2024-01-15 14:30:00"
```

Supported formats:
- RFC3339: `2024-01-15T10:30:00Z`
- DateTime: `2024-01-15 10:30:00`
- Date only: `2024-01-15`
- Relative: `2h ago`, `1d ago`, `1w ago`

### Thread Association

The `--thread` flag accepts multiple reference types:

```bash
# Direct thread ID
bdc capture --thread thr-xxx "..."

# Bead ID (auto-creates/links thread for this bead)
bdc capture --thread bd-a1b2 "..."

# External references
bdc capture --thread linear:ENG-456 "..."
bdc capture --thread github:owner/repo#42 "..."
bdc capture --thread jira:PROJ-123 "..."
bdc capture --thread notion:page-id "..."
```

### Stealth Mode

For local-only usage without committing to the repository:

```bash
bdc init --stealth
```

This:
- Adds `.beadcrumbs/` to `.git/info/exclude` (local gitignore)
- Stores `stealth_mode=true` in config
- Skips git hook installation during `bdc prime`

### Prime Command

Set up the environment after init or on a new machine:

```bash
bdc prime
```

This:
- Installs git hooks (post-commit, post-merge, post-checkout)
- Verifies database integrity
- Skips hooks if stealth mode is enabled

### Storage Schema

```
.beadcrumbs/
  insights.jsonl      # All insights (with author_id, endorsed_by)
  threads.jsonl       # Threads
  deps.jsonl          # Dependencies/relationships
  beadcrumbs.db           # SQLite with migrations
  config.yaml         # Settings
```

#### SQLite Schema

```sql
-- Key-value config storage
CREATE TABLE config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Extended insights columns
ALTER TABLE insights ADD COLUMN author_id TEXT;
ALTER TABLE insights ADD COLUMN endorsed_by TEXT DEFAULT '[]';
```

### Migration System

Uses versioned migrations:

```
001_initial_schema
002_insights_author_id
003_insights_endorsed_by
004_config_table
```

---

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 0.0.2 | Jan 2026 | Added `feedback` InsightType for external input (code reviews, user testing, stakeholder requests) |
| 0.0.1 | Jan 2026 | Initial release with author tracking, timestamps, stealth mode |
