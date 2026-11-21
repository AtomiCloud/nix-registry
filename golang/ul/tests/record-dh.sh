#!/usr/bin/env bash
# Records the dh output the tests replay: for every .dh.md page under testdata/dh, writes
# page.dhjson with the real dh (`dh json`, pretty-printed as the packaged dh prints it). Run it
# from golang/ul/src with dh on PATH (or DH=<command>) after a page or the dh version changes;
# tests/check-dh.sh proves the recordings are still what dh prints.
set -euo pipefail
cd "$(dirname "$0")/../src"
dh="${DH:-dh}"
find testdata/dh -name '*.dh.md' | sort | while read -r page; do
  [ -f "$page.dherr" ] && continue
  "$dh" json "$page" | jq . >"$page.dhjson"
  echo "recorded $page"
done
