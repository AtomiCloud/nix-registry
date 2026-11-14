#!/usr/bin/env bash
# shellcheck disable=SC2015 # `cmd && ok || bad`: ok/bad only echo and count, so the C-branch-after-A case cannot happen
# shellcheck disable=SC2016 # the literal string "$schema" is grepped on purpose
# End-to-end test of the problemgen binary, run inside a nix build (no network).
#   run.sh --problemgen <bin> --src <tool source dir> [--out <package output>]
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
  --out)
    OUT="$2"
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

# Every good definition lints clean (e.g. `only:` takes just the selected problems' types).
for f in "$SRC"/testdata/good/*.yaml; do
  (cd "$(dirname "$f")" && "$PG" lint "$(basename "$f")" >/dev/null 2>&1) && ok "lint clean $(basename "$f")" || bad "lint refused good $f"
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
rec="$work/fx/out/record/conf.v1.yaml"
grep -q 'name: "zinc.conf.{{ $landscape }}.v1"' "$rec" && ok "record dotted name" || bad "record name"
grep -q '^  module: "conf"$' "$rec" && ok "record module field (quoted)" || bad "record module"
grep -q 'regexMatch "^\[a-z0-9\]' "$rec" && grep -q '{{ fail ' "$rec" && ok "record validates the landscape" || bad "record landscape check"
grep -Eq '^    - id: "[a-z_0-9]+"$' "$rec" && ! grep -Eq '^ *(id|platform|service|module|version|namespace): [^"]' "$rec" &&
  ok "record strings quoted" || bad "record has an unquoted string"

# The error portal is a run-time value (errorPortal.url): generated code carries none; the
# record reads .Values.errorPortal.url, with the optional config portal as its default.
! grep -rqi 'portal' "$work/fx/out/go" "$work/fx/out/ts" "$work/fx/out/cs" "$work/fx/out/dart" &&
  ok "generated code carries no portal" || bad "generated code bakes a portal"
grep -qF '.Values.errorPortal | default dict' "$rec" && grep -qF 'default "https://errors.atomi.cloud" $errorPortal.url' "$rec" &&
  grep -qF 'errorPortal.url must be an https origin' "$rec" && ok "record reads errorPortal.url (config portal as default)" || bad "record portal input"
(cd "$work/fx" && cp problemgen.yaml cfg.bak && grep -v '^portal:' cfg.bak >problemgen.yaml && "$PG" generate >/dev/null &&
  "$PG" verify >/dev/null && grep -qF 'required "problemgen: .Values.errorPortal.url is required"' out/record/conf.v1.yaml &&
  mv cfg.bak problemgen.yaml && "$PG" generate >/dev/null) &&
  ok "config without portal: record requires errorPortal.url" || bad "config portal not optional"
for id in internal_error dependency_failed route_not_found method_not_allowed payload_too_large unsupported_media_type; do
  grep -qF "\"id\":\"$id\"" "$work/fx/out/catalog/conf.v1.json" && ok "builtin generic $id" || bad "builtin generic $id missing"
done

# The manifest (<config>.outputs) lists what this config generated: dropping an output makes
# verify report the stale file and generate remove it; a hand-made file is never claimed.
(cd "$work/fx" && "$PG" generate >/dev/null && [ -s problemgen.yaml.outputs ] &&
  cp out/go/confv1/problems.gen.go out/go/confv1/hand.gen.go && "$PG" verify >/dev/null &&
  rm out/go/confv1/hand.gen.go) && ok "verify ignores files it did not generate" || bad "verify claimed a file it did not generate"
# A dropped catalog (no ownership header) is reported, never deleted, and stays listed until a
# person removes it; a dropped code output that is unchanged and owned is removed.
(cd "$work/fx" && cp problemgen.yaml cfg.bak && grep -v 'catalog:' cfg.bak >problemgen.yaml &&
  ! "$PG" verify >/dev/null 2>&1 && ! "$PG" generate >gen.log 2>&1 && grep -q 'not removing.*catalog' gen.log &&
  [ -e out/catalog/conf.v1.json ] && ! "$PG" verify >/dev/null 2>&1 && rm out/catalog/conf.v1.json &&
  "$PG" generate >/dev/null && "$PG" verify >/dev/null && mv cfg.bak problemgen.yaml && "$PG" generate >/dev/null) &&
  ok "dropped catalog output: reported, kept listed, never deleted" || bad "stale catalog not handled"
(cd "$work/fx" && cp problemgen.yaml cfg.bak && grep -v 'typescript:' cfg.bak >problemgen.yaml &&
  "$PG" generate >gen.log && grep -q 'removed stale' gen.log && [ ! -e out/ts/conf.v1.problems.gen.ts ] &&
  "$PG" verify >/dev/null && mv cfg.bak problemgen.yaml && "$PG" generate >/dev/null) &&
  ok "dropped code output: owned and unchanged, so generate removes it" || bad "stale code output not removed"
(cd "$work/fx" && cp problemgen.yaml cfg.bak && grep -v 'typescript:' cfg.bak >problemgen.yaml &&
  echo "// edited" >>out/ts/conf.v1.problems.gen.ts && ! "$PG" generate >gen.log 2>&1 &&
  grep -q 'not removing.*content' gen.log && [ -e out/ts/conf.v1.problems.gen.ts ] &&
  rm out/ts/conf.v1.problems.gen.ts && mv cfg.bak problemgen.yaml && "$PG" generate >/dev/null) &&
  ok "an edited stale file is never deleted" || bad "generate deleted an edited file"
sed 's/^    outputs:$/    outputs: {}/; /^      /d' "$work/fx/problemgen.yaml" >"$work/nooutputs.yaml"
out=$("$PG" verify -c "$work/nooutputs.yaml" 2>&1 || true)
grep -q 'has no outputs' <<<"$out" && ok "verify refuses a module without outputs" || bad "verify accepted a module without outputs"
sed '/^modules:/,$d' "$work/fx/problemgen.yaml" >"$work/empty.yaml" && echo "modules: []" >>"$work/empty.yaml"
! "$PG" verify -c "$work/empty.yaml" >/dev/null 2>&1 && ok "verify refuses an empty module list" || bad "verify accepted no modules"

# diff --git: the old definition AND its imports come from the ref; git failures fail closed.
repo="$work/repo"
mkdir -p "$repo"
printf 'apiVersion: problems.atomi.cloud/v1\nkind: ProblemLibrary\nname: shared\nproblems:\n  bad_input: { title: Bad Input, status: 422, recoverable: true, detail: d }\n' >"$repo/shared.yaml"
printf 'apiVersion: problems.atomi.cloud/v1\nkind: ProblemSet\nmodule: notes\nversion: v1\nimports:\n  - { from: ./shared.yaml }\nproblems: {}\n' >"$repo/notes.yaml"
g() { git -C "$repo" -c user.name=t -c user.email=t@e -c commit.gpgsign=false "$@"; }
g init -q && g add . && g commit -q -m base
sed -i 's/status: 422/status: 409/' "$repo/shared.yaml"
out=$("$PG" diff --git HEAD "$repo/notes.yaml" 2>&1) && bad "diff --git missed an imported change" ||
  { grep -q ' D102 ' <<<"$out" && ok "diff --git reads imports at the ref" || bad "diff --git: $out"; }
set +e
"$PG" diff --git no-such-ref "$repo/notes.yaml" >/dev/null 2>&1
[ $? -eq 2 ] && ok "diff --git fails closed on a missing ref" || bad "diff --git accepted a missing ref"
set -e
left=$(find "$repo" -maxdepth 1 -name '.problemgen*' | head -1)
[ -z "$left" ] && ok "diff --git leaves no temp file" || bad "diff --git left a temp file: $left"
if [ -n "${OUT:-}" ]; then
  # the packaged binary finds git with an empty PATH (git is a wrapped runtime input)
  out=$(env -i HOME="$work" "$PG" diff --git HEAD "$repo/notes.yaml" 2>&1) || true
  grep -q ' D102 ' <<<"$out" && ok "diff --git works without git on PATH" || bad "packaged diff --git needs git on PATH: $out"
  for m in gopkg.in/yaml.v3/LICENSE gopkg.in/yaml.v3/NOTICE golang.org/x/text/LICENSE github.com/santhosh-tekuri/jsonschema/v6/LICENSE; do
    [ -s "$OUT/share/licenses/problemgen/$m" ] && ok "notice $m" || bad "missing notice $m"
  done
fi
"$PG" schema | grep -q '"\$schema"' && ok schema || bad schema

echo "problemgen tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
