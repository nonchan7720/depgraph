#!/usr/bin/env bash
# Scan real open-source repositories with a depgraph binary and print, per
# repository, the findings per rule and the findings of the rules under review.
# Used to judge a rule before it ships: see .claude/skills/validate-rules/SKILL.md.
#
#   scripts/validate-repos.sh [options] [name...]
#
#   name        repositories of the corpus below (default: all), or a group:
#               rails, python, go
#   --rule R    print the findings of rule R (repeatable)
#   --limit N   at most N findings per rule and repository (default 20)
#   --bin FILE  the depgraph binary (default bin/depgraph)
#   --dir DIR   where the repositories are cloned (default
#               ${XDG_CACHE_HOME:-~/.cache}/depgraph/validate); a clone is reused
#   --counts F  also write "repo<TAB>rule<TAB>count" lines to F (to diff two binaries)
#
# Every repository is scanned as its own throwaway project (zz-validate-<name>),
# removed afterwards, in the Neo4j the binary would use anyway (NEO4J_URI, or
# its own container). Nothing is written into the clones (--claude-md "").
set -euo pipefail

# group name repository: shallow clones of the default branch
CORPUS="
rails  mastodon   mastodon/mastodon
rails  discourse  discourse/discourse
rails  lobsters   lobsters/lobsters
python django     django/django
python saleor     saleor/saleor
go     kubernetes kubernetes/kubernetes
"

bin=bin/depgraph
dir="${XDG_CACHE_HOME:-$HOME/.cache}/depgraph/validate"
limit=20
counts=""
rules=()
names=()
while [ $# -gt 0 ]; do
  case "$1" in
    --rule) rules+=("$2"); shift 2 ;;
    --limit) limit="$2"; shift 2 ;;
    --bin) bin="$2"; shift 2 ;;
    --dir) dir="$2"; shift 2 ;;
    --counts) counts="$2"; shift 2 ;;
    -h | --help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) echo "unknown option $1" >&2; exit 2 ;;
    *) names+=("$1"); shift ;;
  esac
done
[ -x "$bin" ] || { echo "$bin: not built (mise run build)" >&2; exit 2; }
bin="$(cd "$(dirname "$bin")" && pwd)/$(basename "$bin")"
mkdir -p "$dir"
[ -z "$counts" ] || : > "$counts"

selected() { # group name
  [ ${#names[@]} -eq 0 ] && return 0
  local n
  for n in "${names[@]}"; do
    if [ "$n" = "$1" ] || [ "$n" = "$2" ]; then return 0; fi
  done
  return 1
}

status=0
while read -r group name repo; do
  [ -n "$group" ] || continue
  selected "$group" "$name" || continue
  root="$dir/$name"
  if [ ! -d "$root/.git" ]; then
    echo "cloning $repo ..." >&2
    git clone -q --depth 1 "https://github.com/$repo" "$root"
  fi
  project="zz-validate-$name"
  echo "== $name ($repo @ $(git -C "$root" rev-parse --short HEAD))"
  if ! out="$("$bin" scan --root "$root" --project "$project" --reset --claude-md "" 2>&1)"; then
    echo "$out" | tail -5
    echo "scan failed"
    status=1
    continue
  fi
  echo "$out" | grep -E '^(scanned|findings)' || true
  # "  [severity] rule: message" -> count per rule
  per_rule="$(echo "$out" | sed -n 's/^  \[[a-z]*\] \([a-z-]*\): .*/\1/p' | sort | uniq -c | awk '{print $2 "\t" $1}')"
  [ -z "$per_rule" ] || echo "$per_rule" | sed 's/^/  /'
  if [ -n "$counts" ] && [ -n "$per_rule" ]; then
    echo "$per_rule" | sed "s/^/$name\t/" >> "$counts"
  fi
  for r in ${rules[@]+"${rules[@]}"}; do
    echo "  -- $r"
    echo "$out" | grep -F "] $r: " | head -n "$limit" | cut -c1-400 || true
  done
  "$bin" projects rm "$project" > /dev/null 2>&1 || true
done <<< "$CORPUS"
exit $status
