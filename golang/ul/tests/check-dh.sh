#!/usr/bin/env bash
# Proves the recorded dh output the tests replay is still what the real dh prints. Not part of
# the nix check (it needs dh, built from the same durian-hypermedia tag): run it by hand after a
# dh change, from anywhere, with dh on PATH or DH=<command>.
set -euo pipefail
cd "$(dirname "$0")/../src"
dh="${DH:-dh}"
rc=0
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
while read -r page; do
  [ -f "$page.dherr" ] && continue
  # a file, not a pipe: dh 0.1.0 cuts a large page short when stdout is a pipe
  "$dh" json "$page" >"$tmp"
  if ! diff <(jq . "$tmp") "$page.dhjson" >/dev/null; then
    echo "STALE $page: run tests/record-dh.sh"
    rc=1
  fi
done < <(find testdata/dh -name '*.dh.md' | sort)
[ "$rc" = 0 ] && echo "recordings match dh"
exit "$rc"
