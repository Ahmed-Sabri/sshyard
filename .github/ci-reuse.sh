#!/usr/bin/env bash
# Prints the commit whose full ci.yml run on main a tag's commit can reuse,
# or nothing when the tag has to be tested itself: the nearest of the
# commit and its first-parent ancestors whose push run on main passed with
# every job (a run still going is waited for, up to 40 minutes), as long as
# the tag's commit differs from it only in mygo.json's version and in files
# ci.yml's paths-ignore skips. bump.sh's release commit is such a commit.
# Usage: ci-reuse.sh <owner/repo> <commit>   (needs gh, jq and the history)
set -euo pipefail

[ $# -eq 2 ] || { printf 'Usage: %s <owner/repo> <commit>\n' "$(basename "$0")" >&2; exit 2; }
repo="$1" head="$2"
deadline=$((SECONDS + 2400))

# The files paths-ignore in ci.yml skips; keep both lists the same
ignored() {
  case "$1" in
    website/* | docs/* | *.md | LICENSE | .github/workflows/website.yml) return 0 ;;
  esac
  return 1
}

# Whether the tag's commit differs from $1 only where no check or build
# looks. --no-renames: a file moved into an ignored path is also removed.
untouched() {
  local files file
  files="$(git diff --no-renames --name-only "$1" "$head")" || return 1
  while IFS= read -r file; do
    [ -n "$file" ] || continue
    if [ "$file" = mygo.json ]; then
      [ "$(git show "$1:mygo.json" | jq -S 'del(.version)')" = "$(git show "$head:mygo.json" | jq -S 'del(.version)')" ] || return 1
    elif ! ignored "$file"; then
      return 1
    fi
  done <<<"$files"
}

# Whether the latest push run of ci.yml on main for $1 passed with every
# job run: a release commit's run skips its jobs and does not count
passed() {
  local run id status conclusion
  while :; do
    run="$(gh api "repos/$repo/actions/workflows/ci.yml/runs?head_sha=$1&branch=main&event=push&per_page=1" \
      --jq '.workflow_runs[0] // empty | "\(.id) \(.status) \(.conclusion)"')" || return 1
    [ -n "$run" ] || return 1
    read -r id status conclusion <<<"$run"
    [ "$status" != completed ] || break
    [ "$SECONDS" -lt "$deadline" ] || { printf 'Gave up waiting for CI run %s on %s\n' "$id" "$1" >&2; return 1; }
    printf 'Waiting for CI run %s on %s (%s)\n' "$id" "$1" "$status" >&2
    sleep 30
  done
  [ "$conclusion" = success ] &&
    [ "$(gh api "repos/$repo/actions/runs/$id/jobs?per_page=100" \
      --jq '.jobs | length > 0 and all(.conclusion == "success")')" = true ]
}

for sha in $(git rev-list --first-parent --max-count=20 "$head"); do
  if ! untouched "$sha"; then
    printf '%s changes files the checks use since %s\n' "$head" "$sha" >&2
    exit 0
  fi
  if passed "$sha"; then
    printf '%s\n' "$sha"
    exit 0
  fi
done
printf 'No passed CI run on main to reuse for %s\n' "$head" >&2
