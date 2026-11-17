#!/usr/bin/env bash
set -euo pipefail
# Checkout must include release tags and full history. Errors fail the gate.
latest=''
tags=$(git tag --list 'v*' --sort=-version:refname --merged HEAD)
for tag in $tags; do
  if [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    latest=$tag
    break
  fi
done
if [[ -z $latest ]]; then
  echo 'No release tag: full obs parity required' >&2
  echo 'run=true'
  exit 0
fi
if git diff --quiet "$latest" HEAD -- golang/obs; then
  echo "obs unchanged since $latest: parity skipped" >&2
  echo 'run=false'
else
  status=$?
  if [[ $status != 1 ]]; then exit "$status"; fi
  echo "obs changed since $latest: full parity required" >&2
  echo 'run=true'
fi
