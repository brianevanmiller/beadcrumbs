#!/bin/sh
# One shim for every agent-harness session hook. It is deliberately dumb: it
# reads the harness's stdin JSON, sets the provenance environment `bdc` already
# understands, and shells out to a documented command. Nothing in the Beadcrumbs
# workflow depends on it, and it never blocks the harness.
#
# Claude Code and Codex both deliver {session_id, transcript_path, cwd,
# hook_event_name} on stdin, which is why one script serves both. The event is
# also passed as $1 so the dispatch never depends on parsing succeeding.
#
# transcript_path is read from stdin and never used: raw transcript is exactly
# what Beadcrumbs does not persist.

set -u

event="${1:-}"
payload="$(cat)"

# Extract one string field without requiring jq. Failure yields the empty
# string, which is a valid absent value everywhere it is used below.
field() {
	if command -v jq >/dev/null 2>&1; then
		printf '%s' "$payload" | jq -r --arg k "$1" '.[$k] // empty' 2>/dev/null
		return
	fi
	printf '%s' "$payload" | tr ',{}' '\n\n\n' |
		sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" | head -n 1
}

if ! command -v bdc >/dev/null 2>&1; then
	echo "bdc is not on PATH; Beadcrumbs did nothing for $event" >&2
	exit 0
fi

BDC_SESSION="$(field session_id)"
BDC_ACTOR_KIND=agent
export BDC_SESSION BDC_ACTOR_KIND

cwd="$(field cwd)"
[ -n "$cwd" ] && [ -d "$cwd" ] && cd "$cwd"

case "$event" in
SessionStart)
	# Plain stdout from SessionStart is injected as context, so prime is
	# printed rather than captured. It is a read; it writes nothing.
	bdc prime 2>/dev/null || true
	;;
PreCompact)
	bdc hooks run pre-compact || true
	;;
Stop | SubagentStop)
	bdc hooks run stop || true
	;;
SessionEnd)
	bdc hooks run session-end || true
	;;
*)
	echo "bdc-hook: no action for $event" >&2
	;;
esac

# Always. A hook is not permitted to decide whether the session continues.
exit 0
