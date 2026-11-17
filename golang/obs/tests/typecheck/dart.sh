#!/usr/bin/env bash
# Checks the generated Dart (tests/fixtures/gen/{good,stress}/gen/dart/lib/src/signals/signals.g.dart) with
# the dart SDK: it must analyze clean (strict casts, inference and raw types) and be what `dart format`
# writes. For the good repo: good.dart (a consumer) must analyze clean and run; every bad-*.dart must fail
# analysis and the output must name the diagnostic listed in typecheck/dart/expect.
#   dart.sh [--dart <dart binary>]      (default: dart on PATH)
# Not part of the nix sandbox check: the dart SDK is not in the obs test inputs (CI runs it with
# `nix shell nixpkgs#dart -c tests/typecheck/dart.sh`).
set -euo pipefail
DART=dart
[ "${1:-}" = "--dart" ] && DART="$2"
here="$(cd "$(dirname "$0")/.." && pwd)"
g="$here/typecheck/dart"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export HOME="$work/home" DART_SUPPRESS_ANALYTICS=true
mkdir -p "$HOME"
fail=0
for repo in good stress; do
  pkg=checkout_signals
  [ "$repo" = stress ] && pkg=pay_signals
  d="$work/$repo"
  mkdir -p "$d/lib/src/signals" "$d/bin" "$d/bad"
  cp "$here/fixtures/gen/$repo/gen/dart/lib/src/signals/signals.g.dart" "$d/lib/src/signals/"
  printf 'name: %s\npublish_to: none\nenvironment:\n  sdk: ^3.7.0\n' "$pkg" >"$d/pubspec.yaml"
  printf 'analyzer:\n  language:\n    strict-casts: true\n    strict-inference: true\n    strict-raw-types: true\n' >"$d/analysis_options.yaml"
  (cd "$d" && "$DART" pub get --offline >/dev/null 2>&1)
  if out=$(cd "$d" && "$DART" analyze 2>&1) && grep -q 'No issues found' <<<"$out"; then
    echo "PASS $repo: signals.g.dart analyzes clean (strict casts, inference, raw types)"
  else
    echo "FAIL $repo: signals.g.dart does not analyze clean: $(head -4 <<<"$out")"
    fail=1
  fi
  if (cd "$d" && "$DART" format --output=none --set-exit-if-changed lib >/dev/null 2>&1); then
    echo "PASS $repo: signals.g.dart is what dart format writes"
  else
    echo "FAIL $repo: dart format would change signals.g.dart"
    fail=1
  fi
done
d="$work/good"
cp "$g/good.dart" "$d/bin/app.dart"
if out=$(cd "$d" && "$DART" analyze 2>&1) && grep -q 'No issues found' <<<"$out" &&
  run=$(cd "$d" && "$DART" run bin/app.dart 2>&1) &&
  grep -q 'checkout.charges checkout.charge.failed error' <<<"$run" && grep -q '2026-11-17' <<<"$run"; then
  echo "PASS good.dart analyzes clean and runs"
else
  echo "FAIL good.dart: $(head -3 <<<"${out:-}") ${run:-}"
  fail=1
fi
while read -r f token; do
  cp "$g/$f" "$d/bad/case.dart"
  out=$(cd "$d" && "$DART" analyze bad/case.dart 2>&1) || true
  if ! grep -q ' error ' <<<"$out"; then
    echo "FAIL $f analyzed clean but must not"
    fail=1
  elif grep -qF -- "$token" <<<"$out"; then echo "PASS $f red, names $token"; else
    echo "FAIL $f red but output lacks $token: $(head -3 <<<"$out")"
    fail=1
  fi
done <"$g/expect"
exit $fail
