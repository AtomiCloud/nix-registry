#!/usr/bin/env bash
set -euo pipefail
unset GIT_DIR GIT_INDEX_FILE GIT_WORK_TREE
script=$(cd "$(dirname "$0")" && pwd)/release-changes.sh
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
cd "$scratch"
git init -q
# Synthetic commit objects keep this test independent of CI identity/signing
# configuration. They are fixture data in an isolated repository, never pushed.
fixture_commit() {
  local tree parent commit
  tree=$(git write-tree)
  parent=$(git rev-parse --verify HEAD 2>/dev/null || true)
  commit=$(
    {
      echo "tree $tree"
      if [[ -n $parent ]]; then echo "parent $parent"; fi
      echo 'author Fixture <fixture@example.invalid> 1700000000 +0000'
      echo 'committer Fixture <fixture@example.invalid> 1700000000 +0000'
      echo
      echo fixture
    } | git hash-object -t commit -w --stdin
  )
  git update-ref HEAD "$commit"
}
mkdir -p golang/obs
echo first >golang/obs/example
git add .
fixture_commit
test "$(bash "$script")" = run=true
git tag v1.0.0
test "$(bash "$script")" = run=false
echo other >other
git add .
fixture_commit
test "$(bash "$script")" = run=false
echo changed >>golang/obs/example
git add .
fixture_commit
test "$(bash "$script")" = run=true
# Major/minor aliases must not hide changes since the last actual release.
git tag v2
git tag v2.0
test "$(bash "$script")" = run=true
git tag v2.0.0
test "$(bash "$script")" = run=false
echo 'Release gate: no tag, unchanged, unrelated, obs change, aliases, latest tag PASS'
