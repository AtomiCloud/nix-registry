#!/usr/bin/env bash
# Compatibility of the old commands (obs 0.4 -> 0.5): `obs query --static`, `obs query --live`,
# `obs alerts` and `obs links` must print byte-identical output (stdout, stderr and exit code) for
# the same arguments before and after the regroup. a consumer repository's obs-query.sh calls `obs query ...`.
#
#   compat.sh <obs>             compare each run with its golden file in tests/fixtures/compat-golden
#   compat.sh <obs> --capture   rewrite the golden files (only ever with the 0.4 binary, i.e. the
#                               one built from the commit before the regroup)
#
# Every run is `cd tests/fixtures && obs <cmd> --dir <fixture>/... --base <fixed>`: relative paths
# and a fixed seed start keep the output the same on every machine.
set -euo pipefail
obs=$1
mode=${2:-compare}
here=$(cd "$(dirname "$0")" && pwd)
fx="$here/fixtures"
golden="$here/fixtures/compat-golden"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
base=1700000000 # 14 Nov 2023: the seed's time line ends in the past on every machine

pass=0
fail=0
# case <name> <args...>: run obs in the fixtures folder; compare with (or write) the golden file.
case_() {
  local name=$1
  shift
  local rc=0
  (cd "$fx" && "$obs" "$@" >"$work/stdout" 2>"$work/stderr") || rc=$?
  {
    echo "args: $*"
    echo "exit: $rc"
    echo "--- stdout"
    cat "$work/stdout"
    echo "--- stderr"
    cat "$work/stderr"
  } >"$work/got"
  if [ "$mode" = "--capture" ]; then
    mkdir -p "$golden"
    cp "$work/got" "$golden/$name.out"
    return
  fi
  if [ ! -f "$golden/$name.out" ]; then
    echo "FAIL compat $name: no golden file"
    fail=$((fail + 1))
  elif cmp -s "$work/got" "$golden/$name.out"; then
    pass=$((pass + 1))
  else
    echo "FAIL compat $name: output differs from the 0.4 golden file"
    diff "$golden/$name.out" "$work/got" | head -20 | sed 's/^/  | /'
    fail=$((fail + 1))
  fi
}

# obs query --static: the good fixtures, every planted failure and every ok- fixture.
static_dirs=(good sample-app links/good)
for d in "$fx"/bad-* "$fx"/ok-*; do static_dirs+=("$(basename "$d")"); done
for f in "${static_dirs[@]}"; do
  if [ -d "$fx/$f/observability" ]; then dir="$f/observability"; else dir="$f"; fi
  case_ "query-static__${f//\//_}" query --static --dir "$dir" --seed shared-seed.yaml --base "$base"
done
case_ "query-static__sample-app-names" query --static --dir sample-app/observability --names
case_ "query-static__sample-app-event-on" query --static --dir sample-app/observability --event-rule on
case_ "query-static__bad-event-missing-off" query --static --dir bad-event-missing/observability --event-rule off
case_ "query-static__good-contracts-none" query --static --dir good/observability --contracts none

# obs query --live
for f in good sample-app links/good bad-no-rows bad-dotted-underscore bad-dotted-label-values ok-dotted-labels; do
  if [ -d "$fx/$f/observability" ]; then dir="$f/observability"; else dir="$f"; fi
  seed=()
  case "$f" in bad-* | ok-*) seed=(--seed shared-seed.yaml) ;; esac
  case_ "query-live__${f//\//_}" query --live --dir "$dir" "${seed[@]}" --base "$base"
done
case_ "query-live__good-names" query --live --dir good/observability --base "$base" --names

# obs alerts
for f in good sample-app links/good; do
  case_ "alerts__${f//\//_}" alerts --dir "$f/observability" --base "$base"
done
case_ "alerts__alerts-features" alerts --dir alerts/features --base "$base"
case_ "alerts__links-good-flat" alerts --dir links/good --base "$base"

# obs links
case_ "links__links-good" links --dir links/good --base "$base"
case_ "links__good" links --dir good/observability --base "$base"

# usage errors keep their text and exit code
case_ "usage__query-no-mode" query
case_ "usage__alerts-extra-arg" alerts extra
case_ "usage__links-extra-arg" links extra

if [ "$mode" = "--capture" ]; then
  echo "compat: captured $(find "$golden" -name '*.out' | wc -l) golden files in $golden"
  exit 0
fi
echo "compat: $pass identical, $fail different"
[ "$fail" -eq 0 ]
