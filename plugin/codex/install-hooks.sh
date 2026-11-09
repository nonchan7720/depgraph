#!/bin/sh
# Copies this plugin's Codex hooks (codex/hooks.json) into a hooks.json that
# Codex reads even where it does not run the hooks a plugin bundles:
#
#   sh install-hooks.sh                  -> $CODEX_HOME/hooks.json (~/.codex/hooks.json)
#   sh install-hooks.sh --repo [--root <dir>]  -> <dir>/.codex/hooks.json (default: the current directory)
#   sh install-hooks.sh --to <hooks.json>
#   sh install-hooks.sh --print [--to <hooks.json>]   -> print instead of writing
#   sh install-hooks.sh --remove [...]   -> take the hooks out again
#
# The work is `depgraph codex install-hooks --plugin-root <this plugin>`; the
# binary is found or installed like the hooks do (../scripts/install-binary.sh),
# so the first session starts with it in the cache.
root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=../scripts/install-binary.sh
if ! . "$root/scripts/install-binary.sh" 2>/dev/null; then
	echo "depgraph: $root/scripts/install-binary.sh not found" >&2
	exit 1
fi
depgraph_resolve "$root" || exit 1
exec "$DEPGRAPH_PATH" codex install-hooks --plugin-root "$root" "$@"
