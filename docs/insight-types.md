# Insight Types: A Mental Model

## The Journey of Understanding

When working on software problems, understanding evolves through predictable stages. beadcrumbs captures this journey with six insight types, each representing a distinct moment in the evolution of understanding:

```
hypothesis → discovery → question → feedback → pivot → decision
   ↑           ↑           ↑          ↑         ↑        ↑
  start    investigate   doubt    get input  change   commit
```

This isn't a strict linear flow—real investigations loop back, branch, and jump around. But these six types cover the full vocabulary of how understanding changes.

---

## Type Definitions

### hypothesis

**Symbol:** ○ (open circle)
**Trigger phrases:** "I think...", "Maybe...", "Could be...", "My guess is..."

**When to use:** Before you have evidence. Initial guesses, assumptions, and starting points for investigation.

**Example:**
```bash
bdc capture --hypothesis "I think the bug is in the cache layer"
bdc capture --hypothesis "Maybe the API timeout is too short"
```

**Not this:** Don't use for findings you've verified—that's a discovery. A hypothesis is speculation; a discovery is evidence.

---

### discovery

**Symbol:** ○ (open circle)
**Trigger phrases:** "I found...", "Discovered...", "It turns out...", "The data shows..."

**When to use:** Evidence-based findings from investigation. Facts you've verified through exploration, testing, or analysis.

**Example:**
```bash
bdc capture --discovery "Found: race condition in the retry logic"
bdc capture --discovery "Database queries take 3s under load"
```

**Note:** This is the **default type**. If you don't specify a type, beadcrumbs assumes discovery—because most captured insights are findings from investigation.

---

### question

**Symbol:** ? (question mark)
**Trigger phrases:** "What about...?", "How do we...?", "Why does...?", "Should we...?"

**When to use:** Open uncertainties that need resolution. Questions drive investigation forward and create tension in the narrative.

**Example:**
```bash
bdc capture --question "How should we handle timeout errors?"
bdc capture --question "What if the cache is stale?"
```

**Special feature:** Use `bdc questions --unresolved` to find questions that haven't been superseded by later insights.

---

### feedback

**Symbol:** » (chevron)
**Trigger phrases:** "They said...", "Review feedback:", "User reported...", "Team discussed..."

**When to use:** External input from others. This includes code reviews, user testing results, stakeholder requests, AI critiques, or team discussions.

**Example:**
```bash
bdc capture --feedback "PR review: should use dependency injection"
bdc capture --feedback "User testing: 3/5 users confused by wizard"
bdc capture --feedback "PM requested: must support mobile before launch"
bdc capture --feedback "Claude suggested simplifying to 3 endpoints"
```

**Not this:** Don't use for your own findings—that's a discovery. Feedback is input from *others* that informs your understanding.

**Why it matters:** Feedback represents a distinct category of insight. Unlike discoveries (which you find yourself), feedback comes from external sources. It might lead to a pivot or decision, but it isn't the change itself—it's the input that informs the change.

---

### pivot

**Symbol:** ● (filled circle)
**Trigger phrases:** "Actually...", "Wait, it's not...", "We need to change...", "I was wrong about..."

**When to use:** Direction changed. Understanding shifted significantly. You realized something fundamental about the problem or approach.

**Example:**
```bash
bdc capture --pivot "Actually, it's not the cache—it's the database connection pool"
bdc capture --pivot "Need to rethink the architecture entirely"
```

**Sacred:** Pivots are preserved in narrative reconstruction—they show where thinking changed. These are the "plot twists" in your journey of understanding.

---

### decision

**Symbol:** ◆ (diamond)
**Trigger phrases:** "We'll do...", "Decision:", "Going with...", "Committed to..."

**When to use:** Committed to a specific approach or solution. This is the resolution point—where uncertainty becomes action.

**Example:**
```bash
bdc capture --decision "Decision: implement connection pooling with max 50 connections"
bdc capture --decision "Going with Redis for caching due to pub/sub needs"
```

**Threads:** Decisions often conclude investigation threads. They mark where exploration ends and execution begins.

---

## Choosing the Right Type

| Situation | Type | Why |
|-----------|------|-----|
| Starting an investigation | `hypothesis` or `question` | You're forming initial ideas or identifying unknowns |
| Found something through exploration | `discovery` | You verified it yourself through investigation |
| Received input from someone else | `feedback` | External input, not your own finding |
| Realized previous understanding was wrong | `pivot` | Direction changed, not just new info |
| Ready to commit to an approach | `decision` | Moving from exploration to action |

### Distinguishing Similar Types

**hypothesis vs. discovery:**
- Hypothesis: "I think the bug is in auth" (speculation)
- Discovery: "Found the bug in auth middleware" (verified)

**discovery vs. feedback:**
- Discovery: "I found a memory leak in the worker" (you found it)
- Feedback: "Security team reported a memory leak" (they told you)

**feedback vs. pivot:**
- Feedback: "Code review suggested using Redis" (input received)
- Pivot: "Actually, we should use Redis instead of Memcached" (direction changed)

**pivot vs. decision:**
- Pivot: "Need to rethink the caching strategy" (direction changed)
- Decision: "We'll use Redis with 1-hour TTL" (committed to approach)

---

## Timeline Display

In timeline view, types display with their symbols:

```
2024-01-15 10:30  ○  "Bug reports: slow login" [hypothesis]
2024-01-15 14:22  ○  "Traced to session validation" [discovery]
2024-01-15 16:00  ?  "What about token refresh?" [question]
2024-01-16 08:00  »  "Security team: check JWT expiry" [feedback]
2024-01-16 09:15  ●  "Actually JWT, not session" [PIVOT]
2024-01-16 11:00  ◆  "Upgrade to JWT v3" [DECISION]
```

Note that `pivot` and `decision` display in UPPERCASE to emphasize their significance as structural moments in the narrative.

---

## Commands by Type

```bash
bdc pivots [thread-id]       # Show only pivots
bdc decisions [thread-id]    # Show only decisions
bdc feedback [thread-id]     # Show only external feedback
bdc questions [--unresolved] # Show open questions
bdc list --type=hypothesis   # Filter by any type
```

---

## Best Practices

1. **Default to discovery** — Most insights are findings. Only use other types when they clearly fit.

2. **Capture pivots immediately** — When your understanding shifts, capture it right away. Pivots are the most valuable insights for narrative reconstruction.

3. **Distinguish feedback from discovery** — Did you find it, or did someone tell you? This distinction matters for understanding how knowledge flowed.

4. **Don't overthink it** — If you're unsure, `discovery` is almost always fine. The type system is meant to help, not slow you down.

5. **Use questions to drive investigation** — Explicitly capturing questions helps identify what's still unknown and tracks resolution over time.
