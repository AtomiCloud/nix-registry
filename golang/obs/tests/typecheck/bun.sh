#!/usr/bin/env bash
# Type-checks the generated Bun file (tests/fixtures/gen/good/gen/bun/src/signals/signals.ts) with tsc, strict:
#   bun.sh [--tsc <tsc binary>]      (default: tsc on PATH)
# good.ts (a consumer sketch incl. the trace-port constraint) must compile and run with every call Ok; every
# bad-*.ts must fail and the compiler output must name the bad token listed in tests/typecheck/bun/expect;
# every forge-*.ts (structural forging the compiler cannot stop) must compile, run, and show the port's
# refusal listed in forge-expect. Runs use the node that tsc itself runs on (nixpkgs typescript wraps node).
# Runs in the nix check (nixpkgs.typescript is an input of the obs-tests derivation); by hand:
# `nix shell nixpkgs#typescript -c tests/typecheck/bun.sh`.
set -euo pipefail
TSC=tsc
[ "${1:-}" = "--tsc" ] && TSC="$2"
here="$(cd "$(dirname "$0")/.." && pwd)"
g="$here/typecheck/bun"
# shellcheck disable=SC1091 # helper is relative to this fixture tree
source "$here/typecheck/stubs.bash"
fx="$here/fixtures/gen/good/gen/bun/src/signals"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp "$fx/signals.ts" "$work/signals.ts"
cp "$here/fixtures/gen/billing/gen/bun/src/signals/signals.ts" "$work/billing.ts"
cp "$g"/*.ts "$work/"
obs_setup_stub bun "$work"
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

# ---- run: good.ts and every forge-*.ts, emitted as CommonJS next to the runnable stub (index.js)
NODE=$(command -v node || true)
if [ -z "$NODE" ]; then
  tscbin=$(readlink -f "$(command -v "$TSC")")
  NODE=$(sed -n 's|^exec "\([^"]*/bin/node\)".*|\1|p' "$tscbin" | head -1)
fi
if [ -z "$NODE" ] || [ ! -x "$NODE" ]; then
  echo "FAIL no node found (neither on PATH nor in the tsc wrapper): the forge cases cannot run"
  exit 1
fi
printf '{"type":"commonjs"}\n' >package.json
emit=(--strict --noUncheckedIndexedAccess --exactOptionalPropertyTypes --target es2022 --module commonjs --moduleResolution node10 --skipLibCheck --outDir out)
runcase() { # file -> stdout+stderr of node; exit status of compile or run
  rm -rf out
  "$TSC" "${emit[@]}" signals.ts "$1" >out.txt 2>&1 || return 1
  "$NODE" "out/${1%.ts}.js" >out.txt 2>&1
}
if runcase good.ts && grep -q 'good: every valid call Ok, dropped 0' out.txt; then echo "PASS good.ts runs: every valid call Ok, dropped 0"; else
  echo "FAIL good.ts run"
  cat out.txt
  fail=1
fi
while read -r f token; do
  if ! runcase "$f"; then
    echo "FAIL $f must compile and run (the port refuses at run time, it never throws to the app)"
    cat out.txt
    fail=1
  elif grep -qF -- "$token" out.txt; then echo "PASS $f compiles, runs, the port refuses: $token"; else
    echo "FAIL $f ran but the port did not refuse it (want: $token)"
    cat out.txt
    fail=1
  fi
done <"$g/forge-expect"
sed "s|sha256:HASH|$(obs_port_hash signals.ts)|" multi.ts >multi.tmp
mv multi.tmp multi.ts
if "$TSC" "${emit[@]}" signals.ts billing.ts multi.ts >out.txt 2>&1; then
  # shellcheck disable=SC2154 # obs_port_cases is supplied by stubs.bash
  for scenario in "${obs_port_cases[@]}"; do
    if out=$("$NODE" out/multi.js "$scenario" 2>&1); then echo "$out"; else
      echo "FAIL multi $scenario: $out"
      fail=1
    fi
  done
else
  echo "FAIL multi compile"
  cat out.txt
  fail=1
fi
exit $fail
