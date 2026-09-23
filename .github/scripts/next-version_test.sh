#!/usr/bin/env bash
# Tests for next-version.sh. Each case builds a throwaway repository, so they
# exercise the real git log / git tag behavior the script depends on.
#
#   bash .github/scripts/next-version_test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/next-version.sh"
# Each case runs in a subshell so its cd and repo don't leak; failures are
# tallied in a file because a subshell can't update a variable out here.
failures="$(mktemp)"

# repo creates an empty repository in a temp dir and cds into it.
repo() {
  cd "$(mktemp -d)"
  git init -q
  git config user.name test
  git config user.email test@example.com
  git config commit.gpgsign false
  git config tag.gpgsign false
}

commit() { git commit -q --allow-empty -m "$1"; }

# expect NAME WANT [ARGS...] runs the script and compares its output.
expect() {
  local name="$1" want="$2"
  shift 2
  local got
  got="$(bash "$script" "$@")"
  if [ "$got" = "$want" ]; then
    echo "ok   $name"
  else
    echo "FAIL $name: got '$got', want '$want'"
    echo "$name" >>"$failures"
  fi
}

(
  repo
  commit "feat: first"
  expect "no tags yet starts from v0.0.0" v0.1.0
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  commit "fix(odm): a bug"
  expect "fix is a patch" v0.4.1
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  commit "perf: faster"
  expect "perf is a patch" v0.4.1
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  commit "fix: a bug"
  commit "feat(cache): new backend"
  expect "feat outranks fix" v0.5.0
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  commit "docs: guide"
  commit "chore: tidy"
  commit "ci: workflow"
  expect "nothing releasable prints nothing" ""
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  expect "no commits since the tag prints nothing" ""
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  commit "refactor(odm)!: ObjectID ids"
  expect "breaking on v0 bumps minor" v0.5.0
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  commit "$(printf 'feat: ids\n\nBREAKING CHANGE: _id is an ObjectID')"
  expect "BREAKING CHANGE footer counts" v0.5.0
)

(
  repo
  commit "feat: first" && git tag v1.2.3
  commit "feat!: new API"
  expect "breaking on v1+ bumps major" v2.0.0
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  commit "docs: guide"
  expect "forced major leaves v0" v1.0.0 major
  expect "forced patch ignores the commits" v0.4.1 patch
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  commit "feat: next" && git tag v0.5.0-rc.1
  commit "fix: a bug"
  expect "prerelease tags are not a base" v0.5.0
)

(
  repo
  commit "feat: first" && git tag v0.9.0
  commit "feat: second" && git tag v0.10.0
  commit "fix: a bug"
  expect "versions sort numerically, not lexically" v0.10.1
)

(
  repo
  commit "feat: first" && git tag v0.4.0
  commit "update feat: flags"
  expect "type must lead the subject" ""
)

(
  repo
  commit "feat: first"
  if bash "$script" bogus >/dev/null 2>&1; then
    echo "FAIL rejects an unknown bump"
    echo "rejects an unknown bump" >>"$failures"
  else
    echo "ok   rejects an unknown bump"
  fi
)

count="$(wc -l <"$failures" | tr -d ' ')"
if [ "$count" -ne 0 ]; then
  echo "$count failure(s)"
  exit 1
fi
