#!/usr/bin/env bash
set -euo pipefail
script=$(cd "$(dirname "$0")" && pwd)/release-changes.sh
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
cd "$scratch"
git init -q
mkdir -p golang/obs
echo first >golang/obs/example
git add .
# Fixture history uses the invoking environment's configured identity and signing.
git commit -S -qm 'test: fixture initial'
test "$(bash "$script")" = run=true
git tag v1.0.0
test "$(bash "$script")" = run=false
echo other >other
git add .
git commit -S -qm 'test: unrelated change'
test "$(bash "$script")" = run=false
echo changed >>golang/obs/example
git add .
git commit -S -qm 'test: obs change'
test "$(bash "$script")" = run=true
# Major/minor aliases must not hide changes since the last actual release.
git tag v2
git tag v2.0
test "$(bash "$script")" = run=true
git tag v2.0.0
test "$(bash "$script")" = run=false
echo 'Release gate: no tag, unchanged, unrelated, obs change, aliases, latest tag PASS'
