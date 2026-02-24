# Beadcrumbs Lifecycle Guide

Track reasoning and decision evolution across sessions using bdc (beadcrumbs).

---

## When to Use This Guide

- Starting a new work session on a non-trivial task
- Making an architectural or design decision
- Changing direction on an approach
- Resuming work from a previous session
- Wrapping up a branch or PR

---

## Workflow

### Phase 1: Session Start

Open a thread, set origin, and capture initial intent. Do this BEFORE writing code.

```bash
# Set origin to identify this session's insights
bdc origin set claude:<session-id>

# Check for existing active threads (may be resuming)
bdc thread list --status=active

# If resuming, review prior reasoning
bdc timeline <thread-id>
bdc questions --unresolved
```

**Opening a new thread — choose the right reference:**

```bash
# External tracker issue exists (most common):
# Option A: auto-create thread on first capture (thread auto-linked, title fetched from Linear)
bdc capture --thread linear:ENG-456 \
  --hypothesis "Initial approach: use batch processing for import" \
  --author cc:opus-4.6

# Option B: explicit thread creation with --linear flag
bdc thread new "Batch processing for import" --linear ENG-456
bdc capture --thread thr-xxxx \
  --hypothesis "Initial approach: use batch processing for import" \
  --author cc:opus-4.6

# Only a Beads task exists:
bdc capture --thread bd-a1b2 \
  --hypothesis "Might need to split this into two migrations" \
  --author cc:opus-4.6

# Ad-hoc work (no tracker):
bdc thread new "Investigate performance regression in API"
bdc capture --thread thr-xxxx \
  --hypothesis "Suspect N+1 queries in the user endpoint" \
  --author cc:opus-4.6
```

### Phase 2: During Work — Exploration & Evidence

Capture as reasoning evolves. Not every action — only when understanding shifts.

```bash
# Weighing options
bdc capture --thread <ref> \
  --hypothesis "Could normalize data with a batch transform step" \
  --author cc:opus-4.6

# Found evidence
bdc capture --thread <ref> \
  --discovery "Batch transform runs in 200ms for 10k records — acceptable" \
  --author cc:opus-4.6

# Open question
bdc capture --thread <ref> \
  --question "Is the API rate-limited for bulk operations?" \
  --author cc:opus-4.6
```

**When to capture during work:**

| Signal | Type | Example |
|--------|------|---------|
| Considering two approaches | `hypothesis` | "Could use either raw SQL or an ORM migration" |
| Benchmark or test reveals something | `discovery` | "Connection pool handles 50 concurrent queries in 48ms" |
| Blocked on unknown | `question` | "Does the service have access to the external API?" |
| Hit a dead end | `discovery` | "Library doesn't support the feature we need" |

### Phase 3: Key Decision Points

These are the highest-value captures. Always record pivots and decisions.

```bash
# Human changes direction
bdc capture --thread <ref> \
  --feedback "User says: use the managed service, we have an enterprise license" \
  --author <human-name>

# Agent pivots
bdc capture --thread <ref> \
  --pivot "Switching from custom implementation to managed service — handles edge cases natively" \
  --author cc:opus-4.6

# Final decision
bdc capture --thread <ref> \
  --decision "Using managed service for data processing, then custom transform for output" \
  --author cc:opus-4.6
```

**Pivot vs Decision:**
- `pivot` = "We were doing X, now switching to Y" (the *change* is the insight)
- `decision` = "We commit to Y" (the *commitment* is the insight)

A pivot often precedes a decision but not always. Sometimes you pivot without yet deciding.

### Phase 4: Session End

Close the loop. This prevents orphaned threads and ensures future agents can trace reasoning.

```bash
# Capture final outcome
bdc capture --thread <ref> \
  --decision "PR #87 implements batch processing with managed service" \
  --author cc:opus-4.6

# Link to Beads if a task was spawned during this work
bdc link ins-xxxx --spawns=bd-abc1

# Clear origin before ending session
bdc origin clear

# Close the thread
bdc thread close thr-xxxx
```

**Rule**: Do NOT archive or delete a git branch until the beadcrumbs thread is closed.

**Auto-push**: If the thread is linked to a Linear issue, closing it automatically posts a summary comment (decisions, pivots, discoveries) to the Linear issue. Disable with `bdc linear config auto_push false`.

### Phase 5: PR & Merge

At PR creation:

1. Ensure all active threads for this branch are closed
2. Verify no unresolved `--question` insights remain
3. Final `--decision` insight captures the PR number and outcome

```bash
# Check for open questions
bdc questions --unresolved

# Close any remaining threads
bdc thread list --status=active
bdc thread close thr-xxxx

# Verify clean state
bdc thread list --status=active  # Should be empty for this branch
```

At merge, the JSONL files auto-stage with each commit, so `.beadcrumbs/` data rides along with the merge.

### Phase 6: Cross-Session Resumption

When resuming work in a new session on the same branch:

```bash
# Set origin for the new session
bdc origin set claude:<new-session-id>

# Find active threads
bdc thread list --status=active

# Review prior reasoning (optionally filter by prior session's origin)
bdc timeline thr-xxxx
bdc timeline --origin claude:<old-session-id>

# Check for unresolved questions
bdc questions --unresolved

# Continue capturing from where you left off
bdc capture --thread <ref> \
  --discovery "Found the API key in the secrets manager" \
  --author cc:opus-4.6
```

**Why this matters**: bdc threads survive session boundaries, conversation compaction, and context window resets. A new agent can `bdc timeline` to reconstruct the full reasoning chain without re-exploring.

---

## Anti-Patterns

| Anti-Pattern | Why It's Bad | Do This Instead |
|-------------|-------------|-----------------|
| Skip thread because no tracker task exists | Reasoning is lost for ad-hoc work | Use `bdc thread new "..."` for any non-trivial work |
| Capture every file read/grep | Noise drowns signal | Only capture when understanding shifts |
| Forget to close thread at session end | Orphaned threads accumulate | Close before ending session |
| Use ticket ID as thread name | No context without looking up the ticket | Use outcome-oriented names |
| Never link bdc to bd | Reasoning disconnected from tasks | Use `bdc link --spawns` when insight creates work |
| Capture hypotheses but not decisions | Half the story is missing | Always close the loop with a decision or pivot |

---

## Quick Reference

```bash
# Session lifecycle
bdc origin set claude:<session-id>                   # Set origin at start
bdc thread list --status=active                     # Check for prior work
bdc capture --thread <ref> --<type> "..." --author cc:<model>  # During
bdc origin clear                                     # Clear origin at end
bdc thread close <id>                               # Close thread

# Thread references (prefer in this order)
--thread linear:ENG-456                             # External tracker
--thread bd-a1b2                                    # Beads task
--thread thr-xxxx                                   # Existing thread

# Review
bdc timeline [thread-id]                            # Full chronology
bdc decisions [thread-id]                           # Just decisions
bdc questions --unresolved                          # Open questions

# Cross-reference
bdc link <insight-id> --spawns=<bead-id>            # Link to task
bdc trace <bead-id>                                 # Trace reasoning for a task
bdc spawn <insight-id> --title="..."                # Create task from insight

# Linear integration
bdc linear setup                                    # Detect Linear CLI tools
bdc linear status                                   # Show linked threads, config
bdc linear push <thread-id>                         # Post summary to Linear issue
bdc linear link <thread-id> <issue-id>              # Manually link thread to issue
bdc thread new "Title" --linear ENG-456             # Create thread linked to Linear
```

---

**Related guides:** [AI Agent Guide](../../BDC_GUIDE.md) | [Stealth Mode](stealth-mode.md) | [Project Config](project-config.md) | [Linear Integration](linear.md)
