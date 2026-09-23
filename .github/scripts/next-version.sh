#!/usr/bin/env bash
# Prints the tag the next release of HEAD should get, or nothing when no
# commit since the last release warrants one.
#
#   next-version.sh          # decide from the commit messages
#   next-version.sh minor    # force a bump, whatever the commits say
#
# Commits follow Conventional Commits (https://www.conventionalcommits.org):
#
#   feat!: ... / BREAKING CHANGE: in the body   -> major (minor while on v0)
#   feat: ...                                   -> minor
#   fix: ... / perf: ...                        -> patch
#   anything else (docs, chore, ci, test, ...)  -> no release
#
# While on v0 a breaking change bumps the minor version: that is what semver
# says 0.x means, and it stops a refactor! from silently declaring v1.0.0.
# Going to v1 is a decision, made by asking for it: next-version.sh major.
set -euo pipefail

bump="${1:-auto}"
case "$bump" in
  auto | patch | minor | major) ;;
  *)
    echo "usage: $0 [auto|patch|minor|major]" >&2
    exit 2
    ;;
esac

# The newest stable release reachable from HEAD. Prereleases are skipped:
# v0.5.0-rc.1 is a rehearsal for v0.5.0, not a new starting point.
last="$(git tag --merged HEAD --list 'v*' --sort=-v:refname |
  grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n1 || true)"
if [ -n "$last" ]; then
  range="$last..HEAD"
else
  range="HEAD"
  last="v0.0.0"
fi

forced=1
if [ "$bump" = auto ]; then
  forced=0
  bump="none"
  scope='(\([^)]*\))?'
  # -z separates commits with NUL, so a multi-line body stays one record.
  while IFS= read -r -d '' message; do
    subject="${message%%$'\n'*}"
    if grep -Eq "^[a-z]+$scope!:" <<<"$subject" ||
      grep -Eq '^BREAKING[ -]CHANGE:' <<<"$message"; then
      bump="major"
      break
    elif grep -Eq "^feat$scope:" <<<"$subject"; then
      bump="minor"
    elif [ "$bump" = none ] && grep -Eq "^(fix|perf)$scope:" <<<"$subject"; then
      bump="patch"
    fi
  done < <(git log -z --format=%B "$range")
fi

[ "$bump" = none ] && exit 0

IFS=. read -r major minor patch <<<"${last#v}"
case "$bump" in
  major)
    if [ "$major" -eq 0 ] && [ "$forced" -eq 0 ]; then
      minor=$((minor + 1)) patch=0
    else
      major=$((major + 1)) minor=0 patch=0
    fi
    ;;
  minor) minor=$((minor + 1)) patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac

echo "v$major.$minor.$patch"
