#!/usr/bin/env bash
# Type-checks the generated Bun file (tests/fixtures/gen/good/gen/bun/src/signals/signals.ts) with tsc, strict:
#   bun.sh [--tsc <tsc binary>]      (default: tsc on PATH)
# good.ts (a consumer sketch incl. the trace-port constraint) must compile; every bad-*.ts must fail
# and the compiler output must name the bad token listed in tests/typecheck/bun/expect.
# Runs in the nix check (nixpkgs.typescript is an input of the obs-tests derivation); by hand:
# `nix shell nixpkgs#typescript -c tests/typecheck/bun.sh`.
set -euo pipefail
TSC=tsc
[ "${1:-}" = "--tsc" ] && TSC="$2"
here="$(cd "$(dirname "$0")/.." && pwd)"
g="$here/typecheck/bun"
fx="$here/fixtures/gen/good/gen/bun/src/signals"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp "$fx/signals.ts" "$work/signals.ts"
cp "$g"/*.ts "$work/"
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
done <"$g/expect"
exit $fail
