#!/bin/sh
# depgraph plugin: runs `depgraph hook <event>` (session-start | prompt), which
# prints the findings already in Neo4j as hook context. It never blocks the
# session: whatever happens, it exits 0.
#
#   hook.sh <session-start|prompt> [claude|codex]
#
# The second argument is the host: claude (default; hooks/hooks.json) or codex
# (codex/hooks.json, and the copies `depgraph codex install-hooks` writes into
# Codex's config layers). Both read the same output (hookSpecificOutput).
#
# The binary is the one of this plugin's version (install-binary.sh):
# DEPGRAPH_BIN, else the cache, else session-start installs it from the GitHub
# release (prompt only looks, it runs on every prompt), else depgraph on PATH.
# Without any, it does nothing (one line on stderr says why).
#
# It stays quiet where something else already does the same, so the context
# is never added twice:
#   - the project runs depgraph's hooks itself (Claude Code: .claude/settings.json;
#     Codex: a hooks.json / config.toml of the project, the user or the system);
#   - session-start, when claude-hook-gate's session_start runs
#     `depgraph hook session-start` (.claude/gate.yaml or .agents/gate.yaml);
#   - Codex, plugin layer (Codex passes PLUGIN_ROOT only to plugin-bundled
#     hooks): a config layer has a copy of the plugin's hooks whose script
#     exists. Codex runs both, and the copy is the one that also works in
#     builds that do not run plugin hooks, so the copy does the work.
event="$1"
host="${2:-claude}"
case "$event" in
session-start | prompt) ;;
*) exit 0 ;;
esac
case "$host" in
claude | codex) ;;
*) exit 0 ;;
esac
# Codex sets no project directory: hooks run in the session's cwd
dir="${CLAUDE_PROJECT_DIR:-.}"

own='depgraph[^ ]* hook (session-start|prompt)'
if [ "$host" = claude ]; then
	for f in "$dir/.claude/settings.json" "$dir/.claude/settings.local.json"; do
		[ -f "$f" ] && grep -Eq "$own" "$f" && exit 0
	done
else
	codex_home="${CODEX_HOME:-${HOME:-}/.codex}"
	codex_system="${DEPGRAPH_CODEX_SYSTEM_DIR:-/etc/codex}"
	for d in "$dir/.codex" "$codex_home" "$codex_system"; do
		for f in "$d/hooks.json" "$d/config.toml"; do
			[ -f "$f" ] && grep -Eq "$own" "$f" && exit 0
		done
	done
	if [ -n "${PLUGIN_ROOT:-}" ]; then
		# a copy's command, as JSON: "sh \"<root>/scripts/hook.sh\" <event> codex"
		# (the installer refuses a root with " or \, so the root is the part between \" and /scripts)
		for f in "$dir/.codex/hooks.json" "$codex_home/hooks.json" "$codex_system/hooks.json"; do
			[ -f "$f" ] || continue
			copies="$(sed -n 's/.*\\"\([^"\\]*\)\/scripts\/hook\.sh\\" '"$event"' codex".*/\1/p' "$f" 2>/dev/null)"
			[ -n "$copies" ] || continue
			while IFS= read -r copy; do
				[ -f "$copy/scripts/hook.sh" ] && exit 0
			done <<EOF
$copies
EOF
		done
	fi
fi
if [ "$event" = session-start ]; then
	for f in "$dir/.claude/gate.yaml" "$dir/.claude/gate.yml" "$dir/.agents/gate.yaml" "$dir/.agents/gate.yml"; do
		[ -f "$f" ] && grep -Eq 'depgraph[^ ]* hook session-start' "$f" && exit 0
	done
fi
root="${CLAUDE_PLUGIN_ROOT:-${PLUGIN_ROOT:-}}"
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
