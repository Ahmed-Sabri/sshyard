#!/usr/bin/env bash
# Starts a stable release: sets the version in mygo.json, commits it and
# tags v<version>; with -p it pushes the branch and the tag, and the tag
# runs ci.yml and release.yml, which publishes the release once both pass.
# Usage: bump.sh -v 1.0.2 [-p]
set -euo pipefail

usage() {
  printf 'Usage: %s -v 1.0.2 [-p]\n' "$(basename "$0")" >&2
  exit 2
}

VERSION=""
PUSH=0

while getopts ":v:p" opt; do
  case "$opt" in
    v) VERSION="$OPTARG" ;;
    p) PUSH=1 ;;
    *) usage ;;
  esac
done
shift $((OPTIND - 1))

[[ $# -eq 0 ]] || usage
[[ -n "$VERSION" ]] || usage
[[ "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
  printf 'Version must use x.y.z format, got: %s\n' "$VERSION" >&2
  exit 2
}

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
  printf 'Tracked working tree is not clean. Commit or stash changes first.\n' >&2
  git status --short --untracked-files=no >&2
  exit 1
fi

CONFIG="mygo.json"
TAG="v$VERSION"

if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
  printf 'Local tag already exists: %s\n' "$TAG" >&2
  exit 1
fi

if git remote get-url origin >/dev/null 2>&1 &&
  git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1; then
  printf 'Remote tag already exists: %s\n' "$TAG" >&2
  exit 1
fi

# Only the version line changes, keeping the rest of mygo.json as written.
# Updates are offered only to a higher version, so it must grow.
CURRENT="$(python3 - "$CONFIG" "$VERSION" <<'PY'
import json, re, sys
path, version = sys.argv[1:]
with open(path) as f:
    text = f.read()
current = json.loads(text)["version"]
if current == version:
    raise SystemExit(f"{path} is already at {version}.")
def parts(v):
    return tuple(int(n) for n in v.split("."))
try:
    if parts(version) <= parts(current):
        raise SystemExit(f"{version} is not above the current version {current}.")
except ValueError:
    raise SystemExit(f"{path} has a version that is not x.y.z: {current}")
new, n = re.subn(r'("version"\s*:\s*")[^"]*(")', r"\g<1>" + version + r"\g<2>", text, count=1)
if n != 1 or json.loads(new)["version"] != version:
    raise SystemExit(f"Could not set the version in {path}.")
with open(path, "w") as f:
    f.write(new)
print(current)
PY
)"

git add "$CONFIG"
# cliff.toml skips this exact subject, keeping it out of the release notes
git commit -m "chore(release): bump version to $VERSION"
git tag -a "$TAG" -m "Release $TAG"

if [[ "$PUSH" -eq 1 ]]; then
  BRANCH="$(git branch --show-current)"
  [[ -n "$BRANCH" ]] || {
    printf 'Cannot push from a detached HEAD.\n' >&2
    exit 1
  }

  git push origin "$BRANCH"
  git push origin "$TAG"
fi

printf 'Bumped %s to %s, committed and tagged %s.\n' "$CURRENT" "$VERSION" "$TAG"
if [[ "$PUSH" -eq 0 ]]; then
  printf 'Push with: git push origin HEAD && git push origin %s\n' "$TAG"
fi
