#!/usr/bin/env bash
# Proves the recorded dh output the tests replay is still what the real dh prints. Not part of
# the nix check (it needs dh, built from the same durian-hypermedia tag): run it by hand after a
# dh change, from anywhere, with dh on PATH or DH=<command>.
set -euo pipefail
cd "$(dirname "$0")/../src"
dh="${DH:-dh}"
rc=0
while read -r page; do
  [ -f "$page.dherr" ] && continue
  if ! diff <("$dh" json "$page" | jq .) "$page.dhjson" >/dev/null; then
    echo "STALE $page: run tests/record-dh.sh"
    rc=1
  fi
done < <(find testdata/dh -name '*.dh.md' | sort)
[ "$rc" = 0 ] && echo "recordings match dh"
exit "$rc"
