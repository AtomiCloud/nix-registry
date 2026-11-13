#!/usr/bin/env bash
# shellcheck disable=SC2015 # `cmd && ok || bad`: ok/bad only echo and count, so the C-branch-after-A case cannot happen
# shellcheck disable=SC2016 # the literal string "$schema" is grepped on purpose
# End-to-end test of the problemgen binary, run inside a nix build (no network).
#   run.sh --problemgen <bin> --src <tool source dir>
set -euo pipefail
while [ $# -gt 0 ]; do
  case "$1" in
  --problemgen)
    PG="$2"
    shift 2
    ;;
  --src)
    SRC="$2"
    shift 2
    ;;
  *)
    echo "unknown arg $1"
    exit 2
    ;;
  esac
done
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
pass=0
fail=0
ok() {
  echo "PASS $*"
  pass=$((pass + 1))
}
bad() {
  echo "FAIL $*"
  fail=$((fail + 1))
}

"$PG" --version | grep -q '^problemgen ' && ok version || bad version

# Every planted bad definition is refused with the code it names.
for f in "$SRC"/testdata/bad/*.yaml; do
  want=$(head -1 "$f" | sed 's/^# expect: //')
  if out=$(cd "$(dirname "$f")" && "$PG" lint "$(basename "$f")" 2>&1); then
    bad "lint accepted $f"
  elif grep -q " $want " <<<"$out"; then ok "lint $want $(basename "$f")"; else bad "lint $f: want $want, got: $out"; fi
done

# Every diff pair is refused with its code; ok-* pass.
for d in "$SRC"/testdata/diff/*/; do
  name=$(basename "$d")
  if out=$("$PG" diff --old "$d/old.yaml" "$d/new.yaml" 2>&1); then
    case "$name" in ok-*) ok "diff $name" ;; *) bad "diff accepted $name" ;; esac
  else
    want=${name%%-*}
    case "$name" in ok-*) bad "diff refused $name: $out" ;; *) grep -q " $want " <<<"$out" && ok "diff $name" || bad "diff $name: $out" ;; esac
  fi
done

# generate writes every output; verify passes; any edit makes verify fail.
cp -r "$here/fixture" "$work/fx"
chmod -R u+w "$work/fx"
(cd "$work/fx" && "$PG" lint defs/conf.v1.problems.yaml >/dev/null && "$PG" generate >/dev/null) && ok generate || bad generate
(cd "$work/fx" && "$PG" verify >/dev/null) && ok "verify clean" || bad "verify clean"
echo "// hand edit" >>"$work/fx/out/go/confv1/problems.gen.go"
(cd "$work/fx" && ! "$PG" verify >/dev/null 2>&1) && ok "verify detects a hand edit" || bad "verify missed a hand edit"
(cd "$work/fx" && "$PG" generate >/dev/null && sed -i 's/status: 422/status: 409/' defs/conf.v1.problems.yaml && ! "$PG" verify >/dev/null 2>&1) &&
  ok "verify detects a definition change" || bad "verify missed a definition change"
grep -q 'name: zinc.conf.{{ $landscape }}.v1' "$work/fx/out/record/conf.v1.yaml" && ok "record dotted name" || bad "record name"
grep -q '^  module: conf$' "$work/fx/out/record/conf.v1.yaml" && ok "record module field" || bad "record module"
"$PG" schema | grep -q '"\$schema"' && ok schema || bad schema

echo "problemgen tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
