#!/bin/sh
# depgraph plugin: runs `depgraph hook <event>` (session-start | prompt), which
# prints the findings already in Neo4j as hook context. It never blocks the
# session: whatever happens, it exits 0.
#
# The binary is the one of this plugin's version (install-binary.sh):
# DEPGRAPH_BIN, else the cache, else session-start installs it from the GitHub
# release (prompt only looks, it runs on every prompt), else depgraph on PATH.
# Without any, it does nothing (one line on stderr says why).
#
# It stays quiet where something else already does the same, so the context
# is never added twice:
#   - the project runs depgraph's hooks itself (.claude/settings.json);
#   - session-start, when claude-hook-gate's session_start runs
#     `depgraph hook session-start` (.claude/gate.yaml or .agents/gate.yaml).
event="$1"
case "$event" in
session-start | prompt) ;;
*) exit 0 ;;
esac
dir="${CLAUDE_PROJECT_DIR:-.}"

for f in "$dir/.claude/settings.json" "$dir/.claude/settings.local.json"; do
	[ -f "$f" ] && grep -Eq 'depgraph[^ ]* hook (session-start|prompt)' "$f" && exit 0
done
if [ "$event" = session-start ]; then
	for f in "$dir/.claude/gate.yaml" "$dir/.claude/gate.yml" "$dir/.agents/gate.yaml" "$dir/.agents/gate.yml"; do
		[ -f "$f" ] && grep -Eq 'depgraph[^ ]* hook session-start' "$f" && exit 0
	done
fi
root="${CLAUDE_PLUGIN_ROOT:-}"
[ -n "$root" ] || root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=install-binary.sh
. "$root/scripts/install-binary.sh" 2>/dev/null || exit 0
if [ "$event" = session-start ]; then
	depgraph_resolve "$root" || exit 0
else
	depgraph_resolve "$root" --no-install || exit 0
fi
"$DEPGRAPH_PATH" hook "$event"
exit 0
