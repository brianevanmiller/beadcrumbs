# BDC (Beadcrumbs) Guide for AI Agents

This file contains canonical bdc (beadcrumbs) workflow instructions for AI agents.
Use bdc to capture the *reasoning* behind work — not the work itself (that's what bd is for).

> **For project-specific instructions**, see AGENTS.md in the repository root.
> This file only covers bdc tool usage, not project-specific workflows.

---

## Insight Tracking with bdc (beadcrumbs)

**IMPORTANT**: This project uses **bdc (beadcrumbs)** to track reasoning and understanding. bd tracks *what* you're doing; bdc tracks *why*.

### Why bdc?

- Narrative-aware: Captures the evolution of understanding across sessions
- Git-backed: SQLite database with auto-sync, portable across machines
- Agent-optimized: Structured insight types, thread refs, author attribution
- Beads-integrated: Link insights to tasks with `spawns` and `trace`
- Prevents lost context: Reasoning survives conversation compaction and session boundaries

### Quick Start

**Open a thread:**
```bash
bdc thread new "Implement address fuzzy matching"
```

**Capture insights:**
```bash
bdc capture --thread <ref> --hypothesis "Levenshtein might work for street matching" --author cc:opus-4.6
bdc capture --thread <ref> --decision "Using USPS standardization + exact match" --author cc:opus-4.6
```

**Close when done:**
```bash
bdc thread close <thread-id>
```

### Insight Types

- `hypothesis` - Speculation before evidence (weighing an approach)
- `discovery` - Evidence-based finding (something confirmed or disproven)
- `question` - Open uncertainty (needs resolution)
- `feedback` - External input received (human adjusting specs or direction)
- `pivot` - Direction changed (approach abandoned for a new one)
- `decision` - Committed to approach (final call made)

### Author Convention

- AI agents: `--author cc:<model>` (e.g., `--author cc:opus-4.6`, `--author cc:sonnet-4`)
- Human: `--author brian`
- Use the author that *initiated* the insight, not who typed it

### Thread Reference Priority

The `--thread` flag accepts multiple reference formats. Prefer in this order:

1. **Task tracker ref** if using an external tracker: `--thread linear:ENG-456`, `--thread jira:PROJ-123`, `--thread gh:42`
   - For Linear refs, bdc auto-creates a thread linked to the issue and fetches the issue title
2. **Bead ID** if a bd issue exists: `--thread bd-a1b2`
   - Creates a real thread with an external ref mapping (system: "bead")
3. **Thread ID** if resuming existing: `--thread thr-xxxx`
4. **Descriptive title** as fallback when creating: `bdc thread new "Fix auth timeout bug"`

### Multi-System Linking

A single thread can be linked to multiple external systems simultaneously. This is useful when a Linear ticket represents an epic/feature and beads represent implementation subtasks:

```bash
# Create thread linked to both Linear and a bead in one command
bdc thread new "Implement caching layer" --linear ENG-456 --bead bd-abc1

# Or link incrementally — start with one, add the other later
bdc capture --thread linear:ENG-456 --hypothesis "..." --author cc:opus-4.6
bdc thread link thr-xxxx bd-abc1

# Individual insights can also spawn beads via dependencies (separate mechanism)
bdc link ins-xxxx --spawns=bd-def2
```

Thread-level links (via `--thread`, `--linear`, `--bead`, or `bdc thread link`) associate the whole thread with an external system. Dependency links (via `bdc link --spawns`) create causal relationships between specific insights and specific beads. Both mechanisms coexist and serve different purposes.

### Workflow for AI Agents

1. **Session start**: Open a thread and capture initial intent
   ```bash
   # Option A: auto-create thread from tracker ref (recommended for Linear users)
   bdc capture --thread linear:ENG-456 --hypothesis "Redis might be overkill, in-memory LRU could suffice" --author cc:opus-4.6

   # Option B: explicit thread creation with --linear flag
   bdc thread new "Implement caching layer for API" --linear ENG-456
   bdc capture --thread thr-xxxx --hypothesis "Redis might be overkill" --author cc:opus-4.6

   # Option C: standalone thread (no tracker)
   bdc thread new "Implement caching layer for API"
   bdc capture --thread <ref> --hypothesis "Redis might be overkill" --author cc:opus-4.6
   ```

2. **During session**: Capture as reasoning evolves — always use `--thread`
   ```bash
   # Weighing approaches
   bdc capture --thread <ref> --hypothesis "Could use node-cache for simplicity" --author cc:opus-4.6

   # Found evidence
   bdc capture --thread <ref> --discovery "Benchmarks show node-cache handles 10k keys under 50ms" --author cc:opus-4.6

   # Human adjusts specs
   bdc capture --thread <ref> --feedback "Need TTL support, 1-hour expiry minimum" --author brian

   # Open question
   bdc capture --thread <ref> --question "Should cache invalidate on webhook or poll?" --author cc:opus-4.6

   # Direction changed
   bdc capture --thread <ref> --pivot "Switching to Redis — TTL + pub/sub invalidation needed" --author cc:opus-4.6

   # Final call
   bdc capture --thread <ref> --decision "Using Redis with 1-hour TTL and pub/sub invalidation" --author cc:opus-4.6
   ```

3. **Session end / PR creation**: Capture outcome and close thread
   ```bash
   bdc capture --thread <ref> --decision "PR #42 implements Redis caching with pub/sub invalidation" --author cc:opus-4.6
   bdc thread close <thread-id>
   ```

4. **Rule**: Do NOT archive or delete a git branch until the beadcrumbs thread is closed with final insights recorded.

   **Auto-push**: If the thread is linked to a Linear issue, closing it automatically posts a summary comment (decisions, pivots, discoveries) to the issue. See the [Linear Integration Guide](docs/guides/linear.md).

5. **Cross-session resumption**: When resuming work in a new session
   ```bash
   # Find active threads
   bdc thread list --status=active

   # Review prior reasoning
   bdc timeline <thread-id>

   # Check for unresolved questions
   bdc questions --unresolved

   # Continue capturing on the existing thread
   bdc capture --thread <ref> --discovery "Found the root cause" --author cc:<model>
   ```

6. **Project-specific lifecycle**: For projects with a beadcrumbs lifecycle guide (see `docs/guides/lifecycle.md`), follow that for detailed phase-by-phase integration.

### What to Capture

| Scenario | Type | Author |
|----------|------|--------|
| Human explains reasoning or adjusts specs | `--feedback` | `--author brian` |
| AI weighs a possible approach | `--hypothesis` | `--author cc:<model>` |
| AI finds evidence for or against something | `--discovery` | `--author cc:<model>` |
| Open uncertainty or question | `--question` | either |
| Direction changes | `--pivot` | whoever initiated it |
| Committed approach or final call | `--decision` | whoever made the call |

### What NOT to Capture

Do not create beadcrumbs for:

- Routine tool calls (file reads, grep, glob, running builds)
- Minor formatting or whitespace changes
- Simple acknowledgments ("OK", "got it", "done")
- Restating what the user said without adding new reasoning
- Mechanical steps (installing deps, running migrations) unless the *result* reveals something unexpected
- Single-line bug fixes with obvious cause and solution

### Integration with Beads (bd)

Beadcrumbs tracks reasoning; Beads tracks tasks. There are two ways to link them:

**Thread-level linking** — associate a reasoning thread with a bead task:
```bash
# Via --thread flag (auto-creates thread and mapping)
bdc capture --thread bd-abc1 --hypothesis "..." --author cc:opus-4.6

# Via thread creation flag
bdc thread new "Implement caching" --bead bd-abc1

# Via generic thread link command
bdc thread link thr-xxxx bd-abc1
```

**Dependency linking** — connect a specific insight to a bead it spawned:
```bash
# An insight led to creating a task
bdc link ins-7f2a --spawns=bd-abc1

# Trace what reasoning led to a specific task
bdc trace bd-abc1

# Create a task directly from an insight
bdc spawn ins-7f2a --title="Implement exponential backoff for retries"
```

Both mechanisms work together. Thread links say "this reasoning is about bd-abc1". Dependency links say "this specific insight produced bd-abc1".

### Essential Commands

```bash
# Threads
bdc thread new "<title>"                    # Start a narrative thread
bdc thread list --status=active             # See open threads
bdc thread close <id>                       # Conclude a thread

# Capturing insights
bdc capture --thread <ref> --<type> "..."   # Record an insight
bdc capture --thread <ref> --hypothesis "..." --author cc:opus-4.6
bdc capture --thread <ref> --decision "..." --author brian

# Viewing
bdc timeline [thread-id]                    # Chronological view
bdc decisions [thread-id]                   # Filter to decisions only
bdc pivots [thread-id]                      # Filter to pivots only
bdc questions --unresolved                  # Open questions needing answers

# Linking to beads
bdc link <id> --spawns=<bead-id>            # Link insight to task (dependency)
bdc trace <bead-id>                         # Trace reasoning chain for a task
bdc spawn <insight-id> --title="..."        # Create task from insight
bdc thread link <thread-id> <ref>           # Link thread to any external ref

# Setup
bdc init                                    # Initialize in a new repo
bdc init --stealth                          # Local-only (not tracked in git)
bdc prime                                   # Install hooks, verify DB

# Linear integration (see docs/guides/linear.md)
bdc linear setup                            # Detect and configure Linear CLI
bdc linear status                           # Show integration status
bdc linear push <thread-id>                 # Post summary to Linear issue
bdc linear link <thread-id> <issue-id>      # Link thread to Linear issue
bdc thread new "title" --linear ENG-456     # Create thread linked to Linear
```

### Important Rules

- ✅ Use bdc for ALL reasoning and understanding tracking
- ✅ Always use `--thread` to associate captures with context
- ✅ Always use `--author` for attribution (cc:<model> or brian)
- ✅ Open a thread at session start, close at session end
- ✅ Close threads before archiving or deleting branches
- ✅ Link insights to beads when they spawn work (`bdc link --spawns`)
- ❌ Do NOT capture routine tool calls or mechanical steps
- ❌ Do NOT capture simple acknowledgments or restated info
- ❌ Do NOT skip the thread close at session end
- ❌ Do NOT use bdc for task tracking (use bd for that)
