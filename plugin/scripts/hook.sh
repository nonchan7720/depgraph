#!/bin/sh
# depgraph plugin: runs `depgraph hook <event>` (session-start | prompt), which
# prints the findings already in Neo4j as hook context. It never blocks the
# session: whatever happens, it exits 0.
#
# It stays quiet where something else already does the same, so the context
# is never added twice:
#   - depgraph is not on PATH;
#   - the project runs depgraph's hooks itself (.claude/settings.json);
#   - session-start, when claude-hook-gate's session_start runs
#     `depgraph hook session-start` (.claude/gate.yaml or .agents/gate.yaml).
event="$1"
case "$event" in
session-start | prompt) ;;
*) exit 0 ;;
esac
command -v depgraph >/dev/null 2>&1 || exit 0
dir="${CLAUDE_PROJECT_DIR:-.}"

for f in "$dir/.claude/settings.json" "$dir/.claude/settings.local.json"; do
	[ -f "$f" ] && grep -Eq 'depgraph[^ ]* hook (session-start|prompt)' "$f" && exit 0
done
if [ "$event" = session-start ]; then
	for f in "$dir/.claude/gate.yaml" "$dir/.claude/gate.yml" "$dir/.agents/gate.yaml" "$dir/.agents/gate.yml"; do
		[ -f "$f" ] && grep -Eq 'depgraph[^ ]* hook session-start' "$f" && exit 0
	done
fi
depgraph hook "$event"
exit 0
