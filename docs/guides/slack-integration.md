# Slack Integration Guide

Connect a Slack workspace to import channel messages as beadcrumbs insights.

## Overview

The Slack integration allows you to:
- Pull messages from any accessible channel or DM
- Convert them into structured insights with type detection
- Resolve Slack user IDs to display names
- Link Slack content to beadcrumbs threads

Each project connects to its own Slack workspace independently via per-project `.beadcrumbs/beadcrumbs.db` config. Tokens are stored in the database (gitignored), so credentials stay local.

## Setup

### 1. Create a Slack App

1. Go to [https://api.slack.com/apps](https://api.slack.com/apps)
2. Click **Create New App** > **From scratch**
3. Name it (e.g., "Beadcrumbs") and select your workspace
4. Under **OAuth & Permissions**, add these **Bot Token Scopes**:
   - `channels:history` - View messages in public channels
   - `channels:read` - View basic channel info
   - `groups:history` - View messages in private channels
   - `groups:read` - View basic private channel info
   - `im:history` - View direct messages
   - `im:read` - View basic DM info
   - `users:read` - View user display names

5. Under **OAuth & Permissions** > **Redirect URLs**, add:
   ```
   http://127.0.0.1:9876/callback
   ```

6. Note your **Client ID** and **Client Secret** from **Basic Information**

### 2. Configure bdc

```bash
bdc slack config client_id <your-client-id>
bdc slack config client_secret <your-client-secret>
```

### 3. Authenticate

```bash
bdc slack auth
```

This opens your browser for OAuth authorization. After approving, the bot token is stored in your project config automatically.

If the browser doesn't open (e.g., headless environment), the CLI will print the authorization URL for you to visit manually, then prompt you to paste the redirect URL.

### 4. Verify

```bash
bdc slack status
```

## Usage

### List Channels

```bash
bdc slack channels
```

Shows all channels, groups, and DMs the bot can access.

### Fetch Messages

```bash
# Fetch from a channel by name
bdc slack fetch general

# Fetch by channel ID
bdc slack fetch C0123456789

# Fetch messages from the last week
bdc slack fetch engineering --since=2024-01-15

# Fetch with time range
bdc slack fetch engineering --since=2024-01-01 --until=2024-01-31

# Preview without saving
bdc slack fetch engineering --dry-run

# Add to an existing thread
bdc slack fetch engineering --thread=thr-abc1
```

### View Config

```bash
bdc slack config bot_token     # Show stored token (masked)
bdc slack config workspace     # Show workspace ID
```

## Multi-Workspace Support

Each project has its own `.beadcrumbs/beadcrumbs.db`, so Slack credentials are scoped per-project. To connect different projects to different workspaces:

```bash
# In project A
cd /path/to/project-a
bdc slack config client_id <workspace-a-client-id>
bdc slack config client_secret <workspace-a-secret>
bdc slack auth

# In project B
cd /path/to/project-b
bdc slack config client_id <workspace-b-client-id>
bdc slack config client_secret <workspace-b-secret>
bdc slack auth
```

## Security

- Bot tokens are stored in `.beadcrumbs/beadcrumbs.db` which is gitignored by default
- Client secrets are also stored in the config database
- OAuth uses CSRF state verification to prevent attacks
- The local callback server runs on `127.0.0.1` only (not exposed externally)
- The 120-second timeout prevents abandoned auth flows from lingering

## Troubleshooting

**"not authenticated with Slack"**
Run `bdc slack auth` to connect your workspace.

**"channel not found"**
The bot needs to be invited to private channels. For public channels, it should have access automatically. Run `bdc slack channels` to see what's accessible.

**OAuth flow times out**
If the local server can't start (port 9876 busy), the CLI falls back to manual mode where you paste the redirect URL. Check if another process is using port 9876.

**Token expired or invalid**
Re-run `bdc slack auth` to refresh your token.
