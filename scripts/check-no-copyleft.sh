#!/usr/bin/env bash
# Fails if a depgraph binary carries material from the copyleft grammars that
# gotreesitter vendors (caddy, disassembly, jq: GPL-3.0; nim: MPL-2.0).
# depgraph never uses them, but gotreesitter's shared grammars/runtime package
# registers their scanner metadata and lex-state tables from init, so they are
# linked unless the build uses the tags set in mise.toml (GOFLAGS):
#   gotreesitter_no_copyleft      drops the scanner sources and metadata
#   grammar_subset[_<language>]   drops the remaining generated tables
#
#   scripts/check-no-copyleft.sh [binary]   (default: bin/depgraph)
#
# First it builds the module without those tags and checks that the markers
# are found there, so a silently broken check cannot pass. Run from the repo root.
set -euo pipefail

bin=${1:-bin/depgraph}
symbols='grammars/runtime\.(caddy|disassembly|nim|jq)[A-Z]'
strings='tree-sitter-(caddy|disassembly|nim|jq)([^a-z0-9_-]|$)'

# markers prints what was found in a binary, one per line.
markers() {
  go tool nm "$1" | grep -E -o "$symbols[A-Za-z]*" || true
  LC_ALL=C grep -a -E -o "$strings" "$1" || true
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
GOFLAGS= go build -o "$tmp/untagged" .
if [ -z "$(markers "$tmp/untagged")" ]; then
  echo "check-no-copyleft: found no copyleft markers even in an untagged build; the check is broken" >&2
  exit 1
fi

[ -f "$bin" ] || { echo "check-no-copyleft: $bin not found" >&2; exit 1; }
# Both tags are required: the lex-state tables are left out only by
# grammar_subset. Release binaries are stripped (-s -w), so the symbol search
# below finds nothing in them; the build info kept in the binary does not lie.
tags=$(go version -m "$bin" | sed -n 's/.*-tags=\([^[:space:]]*\).*/\1/p' | tr ',' '\n')
for tag in gotreesitter_no_copyleft grammar_subset; do
  if ! grep -qx "$tag" <<<"$tags"; then
    echo "check-no-copyleft: $bin was not built with -tags $tag (see GOFLAGS in mise.toml)" >&2
    exit 1
  fi
done
found=$(markers "$bin" | sort -u)
if [ -n "$found" ]; then
  echo "check-no-copyleft: $bin contains copyleft grammar material:" >&2
  echo "$found" >&2
  exit 1
fi
echo "$bin: no copyleft grammar material"
