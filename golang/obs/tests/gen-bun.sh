#!/usr/bin/env bash
# Type-checks the Bun golden file (tests/golden/bun/signals.ts.golden) with tsc, strict:
#   gen-bun.sh [--tsc <tsc binary>]      (default: tsc on PATH)
# good.ts (a consumer sketch incl. the trace-port constraint) must compile; every bad-*.ts must fail
# and the compiler output must name the bad token listed in typecheck/expect.
# Not part of the nix sandbox check: typescript is not in the obs test inputs (CI runs it with
# `nix shell nixpkgs#typescript -c tests/gen-bun.sh`).
set -euo pipefail
TSC=tsc
[ "${1:-}" = "--tsc" ] && TSC="$2"
here="$(cd "$(dirname "$0")" && pwd)"
g="$here/golden/bun"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp "$g/signals.ts.golden" "$work/signals.ts"
cp "$g"/typecheck/*.ts "$work/"
cd "$work"
flags=(--noEmit --strict --noUncheckedIndexedAccess --exactOptionalPropertyTypes --target es2022 --module esnext --moduleResolution bundler --skipLibCheck)
fail=0
if "$TSC" "${flags[@]}" signals.ts good.ts >out.txt 2>&1; then echo "PASS good.ts compiles"; else
  echo "FAIL good.ts"
  cat out.txt
  fail=1
fi
while read -r f token; do
  if "$TSC" "${flags[@]}" signals.ts "$f" >out.txt 2>&1; then
    echo "FAIL $f compiled but must not"
    fail=1
  elif grep -qF -- "$token" out.txt; then
    echo "PASS $f red, names $token"
  else
    echo "FAIL $f red but output lacks $token"
    cat out.txt
    fail=1
  fi
done <"$g/typecheck/expect"
exit $fail
