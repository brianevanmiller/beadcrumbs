# Linear Integration Guide

Connect beadcrumbs to [Linear](https://linear.app) to link reasoning threads to issues and automatically post insight summaries when threads close.

---

## What It Does

- **Auto-link threads to issues**: Use `--thread linear:ENG-456` and bdc creates a thread mapped to that Linear issue, fetching the issue title automatically
- **Auto-push summaries**: When you close a thread linked to a Linear issue, bdc posts a comment with your decisions, pivots, and key discoveries
- **Manual push**: Post a thread summary to its linked Linear issue anytime with `bdc linear push`
- **Status tracking**: See all threads linked to Linear issues with `bdc linear status`

---

## Prerequisites

Install one of the supported Linear CLI tools:

| Tool | Install | Binary |
|------|---------|--------|
| **@schpet/linear-cli** (recommended) | `brew install schpet/tap/linear` | `linear` |
| **linear-cli** (Rust) | `cargo install linear-cli` | `linear-cli` |
| **Linearis** (Node.js) | `npm install -g czottmann/linearis` | `linearis` |

bdc auto-detects which tool is installed. If multiple are present, it prefers them in the order listed above.

---

## Setup

### 1. Authenticate the CLI

Follow your chosen CLI tool's auth flow:

```bash
# For @schpet/linear-cli
linear auth login
```

### 2. Run bdc linear setup

```bash
bdc linear setup
```

This auto-detects the installed CLI tool and reports what it found:

```
Checking for Linear CLI tools...
  Found: schpet (/opt/homebrew/bin/linear) — authenticated
Stored schpet as default Linear tool.
```

### 3. (Optional) Set an API key

If your CLI tool needs an API key passed as an environment variable, configure it per-repo:

```bash
bdc linear config api_key "lin_api_your_key_here"
```

API key precedence (highest wins):
1. `BDC_LINEAR_API_KEY` env var
2. `LINEAR_API_KEY` env var
3. `bdc linear config api_key` (per-repo config)

### 4. (Optional) Configure auto-push

Auto-push is enabled by default. When you close a concluded thread linked to a Linear issue, bdc posts a summary comment. To disable:

```bash
bdc linear config auto_push false
```

---

## Usage

### Create a thread linked to a Linear issue

**Option A** — auto-create on first capture:

```bash
bdc capture --thread linear:ENG-456 \
  --hypothesis "Batch import might hit Lambda timeout" \
  --author cc:opus-4.6
```

This creates a thread, links it to ENG-456, and fetches the issue title from Linear to name the thread (e.g., "ENG-456: Implement batch import pipeline").

**Option B** — explicit thread creation:

```bash
bdc thread new "Batch import pipeline" --linear ENG-456
```

### Link an existing thread to a Linear issue

```bash
bdc linear link <thread-id> ENG-456

# Or use the generic thread link command (works with any system)
bdc thread link <thread-id> linear:ENG-456
```

### Link a thread to both Linear and a bead

A thread can be linked to multiple external systems simultaneously:

```bash
# At creation time
bdc thread new "My feature" --linear ENG-456 --bead bd-abc1

# Or incrementally
bdc thread link <thread-id> bd-abc1
```

### Push a summary to Linear

Post the thread's decisions, pivots, and discoveries as a comment on the linked issue:

```bash
bdc linear push <thread-id>
```

The summary includes:
- Decisions made
- Pivots taken
- Key discoveries
- Current understanding (if set)
- Total insight count

### Auto-push on thread close

When you close a thread with `bdc thread close`, bdc automatically posts a summary to the linked Linear issue if:
- The thread is linked to a Linear issue
- The close status is `concluded` (the default)
- Auto-push is enabled (the default)

```bash
# This triggers auto-push
bdc thread close thr-xxxx

# This does NOT trigger auto-push (abandoned status)
bdc thread close thr-xxxx --status abandoned
```

### Check integration status

```bash
bdc linear status
```

Shows:
- Configured CLI tool and binary path
- API key (masked) and its source (env var or config)
- Auto-push setting
- All threads currently linked to Linear issues

---

## Configuration Reference

All configuration is per-repository (stored in `.beadcrumbs/beadcrumbs.db`).

| Key | Description | Default |
|-----|-------------|---------|
| `cli_tool` | Which adapter to use (`schpet`, `finesssee`, `linearis`) | Auto-detected |
| `cli_path` | Override binary path (skips detection) | — |
| `api_key` | API key passed as `LINEAR_API_KEY` to the CLI | — |
| `auto_push` | Post summary comment on thread close (`true`/`false`) | `true` |

```bash
# Get a config value
bdc linear config cli_tool

# Set a config value
bdc linear config auto_push false
```

---

## Troubleshooting

### CLI not detected

```
No Linear CLI detected. To enable Linear integration, install one:
  brew install schpet/tap/linear        (recommended)
  cargo install linear-cli              (Rust alternative)
  npm install -g czottmann/linearis     (Node alternative)
```

Ensure the binary is on your `PATH`. Run `which linear` (or `which linear-cli` / `which linearis`) to verify.

### Authentication failing

Run your CLI's auth command directly to verify credentials:

```bash
linear auth login      # @schpet/linear-cli
linear-cli auth        # Rust linear-cli
linearis auth          # Linearis
```

Then re-run `bdc linear setup` to confirm.

### Auto-push not firing

Check these conditions:
1. Thread must be linked to a Linear issue (`bdc linear status` shows linked threads)
2. Thread must close with `concluded` status (the default) — `abandoned` does not trigger push
3. Auto-push must be enabled: `bdc linear config auto_push` should return `true` or be unset
4. Linear CLI must be authenticated: `bdc linear status` should show "authenticated"

### Wrong CLI tool detected

If you have multiple Linear CLI tools installed and bdc is using the wrong one:

```bash
bdc linear config cli_tool schpet    # Force a specific adapter
bdc linear config cli_path /path/to/binary  # Or override the binary path entirely
```

---

**Related guides:** [AI Agent Guide](../../BDC_GUIDE.md) | [Stealth Mode](stealth-mode.md) | [Lifecycle Guide](lifecycle.md) | [Project Config](project-config.md)
