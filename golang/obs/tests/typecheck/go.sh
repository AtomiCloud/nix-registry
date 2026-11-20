#!/usr/bin/env bash
# Compiles the generated Go file (tests/fixtures/gen/good/gen/go/signals/signals.go) the way a
# consumer uses it: good.go.txt must build and run; every bad-*.go.txt must fail and the compiler
# output must name the token listed in typecheck/go/expect.
#   go.sh [--go <go binary>]
set -euo pipefail
GO=go
[ "${1:-}" = "--go" ] && GO="$2"
here="$(cd "$(dirname "$0")/.." && pwd)"
g="$here/typecheck/go"
# shellcheck disable=SC1091 # helper is relative to this fixture tree
source "$here/typecheck/stubs.bash"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export HOME="$work" GOCACHE="$work/cache" GOPATH="$work/gopath" GOFLAGS=-mod=mod GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0
mkdir -p "$work/signals"
cp "$here/fixtures/gen/good/gen/go/signals/signals.go" "$work/signals/"
printf 'module example.com/app\n\ngo 1.24\n' >"$work/go.mod"
obs_setup_stub go "$work"
cd "$work"
fail=0
cp -f "$g/good.go.txt" main.go
if out=$("$GO" vet ./... 2>&1) && out=$("$GO" run . 2>&1) && grep -q 'stripe checkout.charge.failed error' <<<"$out"; then echo "PASS good.go builds, runs and refuses shard 16"; else
  echo "FAIL good.go: $out"
  fail=1
fi
while read -r f token; do
  cp -f "$g/$f" main.go
  if out=$("$GO" build ./... 2>&1); then
    echo "FAIL $f compiled but must not"
    fail=1
  elif grep -qF -- "$token" <<<"$out"; then echo "PASS $f red, names $token"; else
    echo "FAIL $f red but output lacks $token: $out"
    fail=1
  fi
done <"$g/expect"
# Forging the compiler cannot stop (exported obs.New* constructors): each forge-*.go.txt must compile AND run
# (a refusal never changes the app's result), and its output must show the port's refusal listed in forge-expect.
# A forge-X.early.go.txt companion becomes package example.com/app/early (its init runs before the generated one).
while read -r f token; do
  rm -rf early
  if [ -f "$g/${f%.go.txt}.early.go.txt" ]; then
    mkdir -p early
    cp -f "$g/${f%.go.txt}.early.go.txt" early/early.go
  fi
  cp -f "$g/$f" main.go
  if ! out=$("$GO" run . 2>&1); then
    echo "FAIL $f must compile and run (the port refuses at run time, it never crashes the app): $out"
    fail=1
  elif grep -qF -- "$token" <<<"$out"; then echo "PASS $f compiles, runs, the port refuses: $token"; else
    echo "FAIL $f ran but the port did not refuse it (want: $token): $out"
    fail=1
  fi
done <"$g/forge-expect"
rm -rf early
mkdir -p billing
cp "$here/fixtures/gen/billing/gen/go/signals/signals.go" billing/
sed "s|sha256:HASH|$(obs_port_hash signals/signals.go)|" "$g/multi.go.txt" >main.go
if out=$("$GO" build -o multi . 2>&1); then
  # shellcheck disable=SC2154 # obs_port_cases is supplied by stubs.bash
  for scenario in "${obs_port_cases[@]}"; do
    if out=$(./multi "$scenario" 2>&1); then echo "$out"; else
      echo "FAIL multi $scenario: $out"
      fail=1
    fi
  done
else
  echo "FAIL multi build: $out"
  fail=1
fi
exit $fail
