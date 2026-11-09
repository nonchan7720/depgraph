#!/bin/bash
# Updates the depgraph plugin in Codex and copies the hooks of its newest
# installed version into ~/.codex/hooks.json, in one command:
#
#   curl -fsSL https://raw.githubusercontent.com/nonchan7720/depgraph/main/plugin/codex/setup.sh | bash
#   ... | bash -s -- --repo        -> arguments go to install-hooks.sh (--repo, --to, --print, --remove)
#   ... | bash -s -- --system      -> into /etc/codex/hooks.json (managed hooks; see below)
#
# 1. Update (skipped with --no-update, or without codex): `codex plugin
#    marketplace upgrade depgraph` (on failure `codex plugin marketplace add
#    nonchan7720/depgraph`), then `codex plugin add depgraph@depgraph`.
#    DEPGRAPH_CODEX_BIN names the codex executable.
# 2. Find the newest version under
#    ${CODEX_HOME:-~/.codex}/plugins/cache/depgraph/depgraph/<version>/, or use
#    DEPGRAPH_PLUGIN_DIR (a plugin directory: an install or plugin/ of a checkout;
#    no update then).
# 3. Run its codex/install-hooks.sh. The exit code is install-hooks.sh's.
#
# Where Codex runs only managed hooks (allow_managed_hooks_only = true in
# /etc/codex/requirements.toml), or with --system (the policy can also come
# from an MDM profile or the ChatGPT workspace, which this cannot see), the
# hooks go into the system layer instead, /etc/codex/hooks.json, which Codex
# runs without a trust prompt: install-hooks.sh prints that file merged with
# the hooks as you, and only putting it in place runs as root (sudo, when the
# directory is not writable). The copy in ~/.codex/hooks.json is then removed,
# so one copy runs. With --remove the hooks leave the system file. An explicit
# --repo / --to / --print is a target of its own and turns this off.
# DEPGRAPH_CODEX_SYSTEM_DIR replaces /etc/codex, DEPGRAPH_SUDO names sudo.
#
# Read from a pipe: it never reads stdin and does not rely on $0.
set -uo pipefail

MARKETPLACE_SOURCE="nonchan7720/depgraph"
MARKETPLACE_NAME="depgraph"
PLUGIN_NAME="depgraph"
INSTALL_REL="codex/install-hooks.sh"
CODEX_BIN="${DEPGRAPH_CODEX_BIN:-codex}"

do_update=1
system=0 # 1: --system, 2: detected
remove=0
explicit=0
fwd=()
for a in "$@"; do
	case "$a" in
	--no-update) do_update=0 ;;
	--system) system=1 ;;
	*)
		case "$a" in
		--remove) remove=1 ;;
		--repo | --to | --to=* | --print) explicit=1 ;;
		esac
		fwd+=("$a")
		;;
	esac
done
SYSTEM_DIR="${DEPGRAPH_CODEX_SYSTEM_DIR:-/etc/codex}"
SYSTEM_DIR="${SYSTEM_DIR%/}"
if [ "$system" -eq 1 ] && [ "$explicit" -eq 1 ]; then
	printf 'depgraph: --system writes %s/hooks.json; it takes no --repo, --to or --print\n' "$SYSTEM_DIR" >&2
	exit 1
fi
if [ "$system" -eq 0 ] && [ "$explicit" -eq 0 ] && [ -f "$SYSTEM_DIR/requirements.toml" ] &&
	grep -Eq '^[[:space:]]*allow_managed_hooks_only[[:space:]]*=[[:space:]]*true([[:space:]]|#|$)' "$SYSTEM_DIR/requirements.toml"; then
	system=2
fi

run_codex() {
	printf 'depgraph: running codex %s\n' "$*" >&2
	"$CODEX_BIN" "$@" </dev/null # never hand the piped script to codex
}

update_note=""
update_plugin() {
	if ! command -v "$CODEX_BIN" >/dev/null 2>&1; then
		printf 'depgraph: codex not found in PATH; using the installed plugin\n' >&2
		update_note="the plugin update was skipped (codex not found)"
		return 0
	fi
	if ! run_codex plugin marketplace upgrade "$MARKETPLACE_NAME" && ! run_codex plugin marketplace add "$MARKETPLACE_SOURCE"; then
		printf 'depgraph: could not update the %s marketplace; using the installed plugin if any\n' "$MARKETPLACE_NAME" >&2
		update_note="the plugin update failed (marketplace upgrade/add)"
		return 0
	fi
	if ! run_codex plugin add "$PLUGIN_NAME@$MARKETPLACE_NAME"; then
		printf 'depgraph: codex plugin add failed; using the installed plugin if any\n' >&2
		update_note="the plugin update failed (codex plugin add)"
	fi
}

if [ -n "${DEPGRAPH_PLUGIN_DIR:-}" ]; then
	ROOT="${DEPGRAPH_PLUGIN_DIR%/}"
	if [ ! -f "$ROOT/$INSTALL_REL" ]; then
		printf 'depgraph: DEPGRAPH_PLUGIN_DIR=%s has no %s\n' "$DEPGRAPH_PLUGIN_DIR" "$INSTALL_REL" >&2
		exit 1
	fi
else
	[ "$do_update" -eq 1 ] && update_plugin
	CACHE_DIR="${CODEX_HOME:-${HOME:-}/.codex}/plugins/cache/$MARKETPLACE_NAME/$PLUGIN_NAME"
	ROOT=""
	best=(-1 -1 -1) best_pre=""
	for dir in "$CACHE_DIR"/[0-9vV]*/; do
		dir="${dir%/}"
		[ -f "$dir/$INSTALL_REL" ] || continue
		name="${dir##*/}"
		name="${name#[vV]}"
		[[ "$name" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)(-([0-9A-Za-z.-]+))?(\+[0-9A-Za-z.-]+)?$ ]] || continue
		v=($((10#${BASH_REMATCH[1]})) $((10#${BASH_REMATCH[2]})) $((10#${BASH_REMATCH[3]})))
		pre="${BASH_REMATCH[5]}"
		newer=0
		for i in 0 1 2; do
			if [ "${v[$i]}" -ne "${best[$i]}" ]; then
				[ "${v[$i]}" -gt "${best[$i]}" ] && newer=1
				break
			fi
			if [ "$i" -eq 2 ]; then # same X.Y.Z: a release is newer than its prereleases
				if [ -z "$pre" ] && [ -n "$best_pre" ]; then
					newer=1
				elif [ -n "$pre" ] && [ -n "$best_pre" ] && [[ "$pre" > "$best_pre" ]]; then
					newer=1
				fi
			fi
		done
		if [ "$newer" -eq 1 ]; then
			ROOT="$dir" best=("${v[@]}") best_pre="$pre"
		fi
	done
	if [ -z "$ROOT" ]; then
		{
			printf 'depgraph: no installed depgraph plugin found in %s\n' "$CACHE_DIR"
			[ -n "$update_note" ] && printf 'Note: %s.\n' "$update_note"
			printf 'Install it first:\n  codex plugin marketplace add %s\n' "$MARKETPLACE_SOURCE"
			printf 'then install depgraph from /plugins in Codex, and run this again.\n'
		} >&2
		exit 1
	fi
fi

printf 'depgraph: using %s\n' "$ROOT" >&2
[ "$system" -eq 0 ] && exec sh "$ROOT/$INSTALL_REL" ${fwd[@]+"${fwd[@]}"}

# the system layer: print the merged file as the user, put it in place as root
target="$SYSTEM_DIR/hooks.json"
if [ "$system" -eq 2 ]; then
	printf 'depgraph: Codex runs only managed hooks here (allow_managed_hooks_only in %s/requirements.toml); installing into %s\n' "$SYSTEM_DIR" "$target" >&2
fi
tmp="$(mktemp "${TMPDIR:-/tmp}/depgraph-hooks.XXXXXX")" || exit 1
trap 'rm -f "$tmp"' EXIT
if [ "$remove" -eq 1 ]; then
	sh "$ROOT/$INSTALL_REL" --print --remove --to "$target" >"$tmp" || exit 1
else
	sh "$ROOT/$INSTALL_REL" --print --to "$target" >"$tmp" || exit 1
fi
if [ -f "$target" ] && cmp -s "$tmp" "$target"; then
	printf 'depgraph: %s is already up to date\n' "$target" >&2
else
	chmod 644 "$tmp"
	parent="$SYSTEM_DIR"
	while [ ! -d "$parent" ]; do parent="$(dirname "$parent")"; done
	if [ -w "$parent" ] && { [ ! -e "$target" ] || [ -w "$target" ]; }; then
		mkdir -p "$SYSTEM_DIR" && cp "$tmp" "$target" && chmod 644 "$target" || exit 1
	else
		SUDO="${DEPGRAPH_SUDO:-sudo}"
		printf 'depgraph: writing %s needs administrator rights; running %s\n' "$target" "$SUDO" >&2
		# sudo asks for the password on the terminal, also under `curl ... | bash`
		if ! { "$SUDO" mkdir -p "$SYSTEM_DIR" && "$SUDO" install -m 0644 "$tmp" "$target"; }; then
			keep="${TMPDIR:-/tmp}/depgraph-hooks.json"
			cp "$tmp" "$keep" 2>/dev/null
			{
				printf 'depgraph: could not write %s. Ask an administrator to run:\n' "$target"
				printf '  sudo mkdir -p %s && sudo install -m 0644 %s %s\n' "$SYSTEM_DIR" "$keep" "$target"
			} >&2
			exit 1
		fi
	fi
	if [ "$remove" -eq 1 ]; then
		printf 'depgraph: removed the depgraph hooks from %s\n' "$target" >&2
	else
		printf 'depgraph: installed the depgraph hooks into %s (managed: Codex runs them without /hooks review)\n' "$target" >&2
	fi
fi
# one copy: the user layer's goes (it would run too once the policy is lifted)
[ "$remove" -eq 1 ] || sh "$ROOT/$INSTALL_REL" --remove || true
