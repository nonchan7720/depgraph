# shellcheck shell=sh
# Sourced by hook.sh and the depgraph wrapper: finds the depgraph binary of
# this plugin's version and, when it is not there yet, installs it from the
# GitHub release. Ported from claude-hook-gate's scripts/install-binary.sh.
#
#   depgraph_binary <plugin root> [--no-install]
#
# sets DEPGRAPH_PATH to the binary and returns 0, or leaves the reason in
# DEPGRAPH_INSTALL_REASON and returns 1 (call it directly, not in $(...), or
# the variables are lost). --no-install
# only looks (the prompt hook: it runs on every prompt and must stay fast).
#
# Where it looks, in order:
#   1. DEPGRAPH_BIN (an executable file), for development and tests.
#   2. ${DEPGRAPH_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/depgraph}/<version>/depgraph
#      where <version> is "version" of <plugin root>/.claude-plugin/plugin.json
#      (release-please keeps it equal to the binary's tag vX.Y.Z).
#   3. ${DEPGRAPH_RELEASE_BASE_URL:-https://github.com/nonchan7720/depgraph/releases/download}/v<version>/
#      depgraph_<os>_<arch>.tar.gz and checksums.txt (GoReleaser's assets):
#      the sha256 is checked before the archive is extracted into the cache.
#      Without sha256sum or shasum nothing is installed (never unverified).
#
# Reasons: bad-version, unsupported-platform (only linux / darwin x amd64 /
# arm64 are downloaded), no-cache-dir, no-download-tool, no-sha256-tool,
# cache-unwritable, download-failed, checksum-mismatch, extract-failed, and
# not-installed (--no-install and nothing cached).
#
# Cache cleanup: a new install removes the version directories (other than
# the current one) nobody used for 30 days. Every run touches the directory
# of the version it uses, so installs of different versions (several
# projects, an update in progress) do not remove each other's binary.

DEPGRAPH_PATH=""
DEPGRAPH_INSTALL_REASON=""
DEPGRAPH_INSTALL_VERSION=""

# depgraph_install_notice prints the one-line reason on stderr.
depgraph_install_notice() {
	printf 'depgraph: could not install the depgraph v%s binary (%s)\n' "$DEPGRAPH_INSTALL_VERSION" "$DEPGRAPH_INSTALL_REASON" >&2
}

depgraph_fetch() { # <url> <output file>
	if command -v curl >/dev/null 2>&1 && curl -fsSL "$1" -o "$2" >/dev/null 2>&1; then
		return 0
	fi
	if command -v wget >/dev/null 2>&1 && wget -qO "$2" "$1" >/dev/null 2>&1; then
		return 0
	fi
	return 1
}

depgraph_sha256() { # <file> -> lower-case hex
	_dg_out=""
	if command -v sha256sum >/dev/null 2>&1; then
		_dg_out="$(sha256sum "$1" 2>/dev/null)" || return 1
	elif command -v shasum >/dev/null 2>&1; then
		_dg_out="$(shasum -a 256 "$1" 2>/dev/null)" || return 1
	else
		return 1
	fi
	printf '%s' "${_dg_out%% *}" | tr 'A-F' 'a-f'
}

depgraph_install() { # <version> <os> <arch> <cache version dir> <destination>
	_dg_base="${DEPGRAPH_RELEASE_BASE_URL:-https://github.com/nonchan7720/depgraph/releases/download}"
	_dg_base="${_dg_base%/}"
	_dg_archive="depgraph_${2}_${3}.tar.gz"
	_dg_url="$_dg_base/v$1"
	_dg_vdir="$4"
	_dg_bin="$5"

	# check the tools before creating anything
	if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
		DEPGRAPH_INSTALL_REASON=no-download-tool
		return 1
	fi
	if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
		DEPGRAPH_INSTALL_REASON=no-sha256-tool
		return 1
	fi
	if ! mkdir -p "$_dg_vdir" 2>/dev/null; then
		DEPGRAPH_INSTALL_REASON=cache-unwritable
		return 1
	fi
	# work directories left by a hook killed by its timeout (older than 60 minutes)
	find "$_dg_vdir" -mindepth 1 -maxdepth 1 -type d -name '.download.*' -mmin +60 -exec rm -rf {} + >/dev/null 2>&1 || true
	# inside the version directory, so the final mv is a rename on one file
	# system and a hook running in parallel never sees a half-written binary
	if ! _dg_tmp="$(mktemp -d "$_dg_vdir/.download.XXXXXX" 2>/dev/null)"; then
		DEPGRAPH_INSTALL_REASON=cache-unwritable
		return 1
	fi

	_dg_ok=1
	while :; do
		depgraph_fetch "$_dg_url/$_dg_archive" "$_dg_tmp/$_dg_archive" || { DEPGRAPH_INSTALL_REASON=download-failed; break; }
		depgraph_fetch "$_dg_url/checksums.txt" "$_dg_tmp/checksums.txt" || { DEPGRAPH_INSTALL_REASON=download-failed; break; }
		_dg_actual="$(depgraph_sha256 "$_dg_tmp/$_dg_archive")" || { DEPGRAPH_INSTALL_REASON=checksum-mismatch; break; }
		_dg_expected="$(sed -n "s/^\([0-9A-Fa-f]\{64\}\)[[:space:]]\{1,\}\*\{0,1\}$_dg_archive\$/\1/p" "$_dg_tmp/checksums.txt" | head -n 1 | tr 'A-F' 'a-f')"
		if [ -z "$_dg_expected" ] || [ "$_dg_expected" != "$_dg_actual" ]; then
			DEPGRAPH_INSTALL_REASON=checksum-mismatch
			break
		fi
		DEPGRAPH_INSTALL_REASON=extract-failed
		mkdir "$_dg_tmp/extract" 2>/dev/null || break
		tar -xzf "$_dg_tmp/$_dg_archive" -C "$_dg_tmp/extract" depgraph >/dev/null 2>&1 || break
		[ -f "$_dg_tmp/extract/depgraph" ] && [ ! -L "$_dg_tmp/extract/depgraph" ] || break
		chmod 755 "$_dg_tmp/extract/depgraph" 2>/dev/null || break
		mv -f "$_dg_tmp/extract/depgraph" "$_dg_bin" 2>/dev/null || break
		DEPGRAPH_INSTALL_REASON=""
		_dg_ok=0
		break
	done
	rm -rf "$_dg_tmp" 2>/dev/null
	return "$_dg_ok"
}

depgraph_binary() { # <plugin root> [--no-install]
	DEPGRAPH_PATH=""
	DEPGRAPH_INSTALL_REASON=""
	DEPGRAPH_INSTALL_VERSION=""
	if [ -n "${DEPGRAPH_BIN:-}" ] && [ -x "$DEPGRAPH_BIN" ]; then
		DEPGRAPH_PATH="$DEPGRAPH_BIN"
		return 0
	fi

	_dg_manifest="$1/.claude-plugin/plugin.json"
	_dg_version=""
	if [ -f "$_dg_manifest" ]; then
		_dg_version="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$_dg_manifest" 2>/dev/null | head -n 1)"
	fi
	# the version ends up in a path and a URL: only the characters of a version
	case "$_dg_version" in
	'' | *[!0-9A-Za-z.+_-]*)
		DEPGRAPH_INSTALL_VERSION="$(printf '%s' "$_dg_version" | tr -cd '0-9A-Za-z.+_-')"
		DEPGRAPH_INSTALL_REASON=bad-version
		return 1
		;;
	esac
	DEPGRAPH_INSTALL_VERSION="$_dg_version"

	_dg_cache="${DEPGRAPH_CACHE_DIR:-}"
	if [ -z "$_dg_cache" ]; then
		if [ -n "${XDG_CACHE_HOME:-}" ]; then
			_dg_cache="$XDG_CACHE_HOME/depgraph"
		elif [ -n "${HOME:-}" ]; then
			_dg_cache="$HOME/.cache/depgraph"
		else
			DEPGRAPH_INSTALL_REASON=no-cache-dir
			return 1
		fi
	fi
	while [ "${#_dg_cache}" -gt 1 ] && [ "${_dg_cache%/}" != "$_dg_cache" ]; do _dg_cache="${_dg_cache%/}"; done
	_dg_vdir="$_dg_cache/$_dg_version"
	_dg_path="$_dg_vdir/depgraph"

	if [ -x "$_dg_path" ]; then
		# keep the version in use "new", so the cleanup of old versions spares it
		touch "$_dg_vdir" 2>/dev/null || true
		DEPGRAPH_PATH="$_dg_path"
		return 0
	fi
	if [ "${2:-}" = --no-install ]; then
		DEPGRAPH_INSTALL_REASON=not-installed
		return 1
	fi

	case "$(uname -s 2>/dev/null)" in
	Linux) _dg_os=linux ;;
	Darwin) _dg_os=darwin ;;
	*) DEPGRAPH_INSTALL_REASON=unsupported-platform; return 1 ;;
	esac
	case "$(uname -m 2>/dev/null)" in
	x86_64 | amd64) _dg_arch=amd64 ;;
	aarch64 | arm64) _dg_arch=arm64 ;;
	*) DEPGRAPH_INSTALL_REASON=unsupported-platform; return 1 ;;
	esac

	depgraph_install "$_dg_version" "$_dg_os" "$_dg_arch" "$_dg_vdir" "$_dg_path" || return 1
	# only after a new install: remove other versions nobody used for 30 days
	find "$_dg_cache" -mindepth 1 -maxdepth 1 -type d -name '[0-9]*' -mtime +30 ! -path "$_dg_vdir" -exec rm -rf {} + >/dev/null 2>&1 || true
	DEPGRAPH_PATH="$_dg_path"
}

# depgraph_resolve <plugin root> [--no-install]: depgraph_binary, else
# depgraph on PATH (silently: it worked before the plugin installed
# binaries, and works offline). Without either it prints the reason on
# stderr (not for --no-install: the prompt hook stays silent) and returns 1.
# DEPGRAPH_PLUGIN_FALLBACK=0 skips PATH (set by the wrapper when it runs the
# PATH one, so a wrapper linked onto PATH as "depgraph" cannot loop).
depgraph_resolve() {
	depgraph_binary "$@" && return 0
	if [ "${DEPGRAPH_PLUGIN_FALLBACK:-1}" != 0 ] && command -v depgraph >/dev/null 2>&1; then
		DEPGRAPH_PATH="$(command -v depgraph)"
		return 0
	fi
	[ "${2:-}" = --no-install ] || depgraph_install_notice
	return 1
}
