#!/usr/bin/env bash
# shellcheck disable=SC2015 # `cmd && ok || bad`: ok/bad only echo and count, so the C-branch-after-A case cannot happen
# End-to-end test of the ul binary, run inside a nix build (no network).
#   run.sh --ul <bin> --src <tool source dir> [--out <package output>]
# Everything here drives the BUILT binary: the clean fixture is green, every planted failure
# is red with its one code, an add/session round trip works, gen detects drift, and the
# commands stay fast on 1,000 words in 100 files.
set -euo pipefail
while [ $# -gt 0 ]; do
  case "$1" in
  --ul)
    UL="$2"
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
work="$(mktemp -d)"
export HOME="$work/home"
export UL_STATE_DIR="$work/state"
mkdir -p "$HOME"
unset UL_SESSION FY_SESSION_ID
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
# expect_exit <want> <name> <command...>: run, compare the exit code, keep stdout in $out, stderr in $err
expect_exit() {
  local want="$1" name="$2" got=0
  shift 2
  out=$("$@" 2>"$work/err") || got=$?
  err=$(cat "$work/err")
  [ "$got" = "$want" ] && ok "$name" || bad "$name: exit $got, want $want
$out
$err"
}

"$UL" --version | grep -q '^ul [0-9]' && ok version || bad version

# --- the clean fixture is green ---------------------------------------------------------
clean="$work/clean"
cp -r "$SRC/testdata/clean" "$clean"
chmod -R u+w "$clean"
expect_exit 0 "clean: check" "$UL" check --root "$clean"
[ -z "$out" ] && ok "clean: check prints nothing" || bad "clean: check printed: $out"
expect_exit 0 "clean: check json" "$UL" check --root "$clean" --format json
[ "$(jq -r '.problems | length' <<<"$out")" = 0 ] && ok "clean: no problems in json" || bad "clean: json problems: $out"
expect_exit 0 "clean: gen --check (committed UL.md are current)" "$UL" gen --check --root "$clean"
expect_exit 0 "clean: all json" "$UL" all --root "$clean"
if jq -e '.schema == "ul/v1" and (.words | length) == 9 and (.namespaces | length) == 3 and .session == null and (.problems | length) == 0' >/dev/null <<<"$out"; then
  ok "clean: ul/v1 shape"
else bad "clean: ul/v1 shape: $out"; fi
jq -e '[.words[] | select(.id == "durian.cloud/engineering/standards/OOP#OCP")][0] | .breadcrumb == ["durian.cloud","engineering","standards","OOP"] and .replacedBy == null and .overrides == null and (.mentions | length) == 1' >/dev/null <<<"$out" && ok "clean: OCP fields" || bad "clean: OCP fields"
expect_exit 0 "clean: all md" "$UL" all --root "$clean" --format md
grep -q '^durian.cloud > shop > billing > pay$' <<<"$out" && ok "clean: breadcrumb line" || bad "clean: breadcrumb line"
expect_exit 0 "clean: lookup by alias" "$UL" lookup "open-close principle" --root "$clean" --format json
[ "$(jq -r '.matches[0].word' <<<"$out")" = OCP ] && ok "clean: lookup finds OCP" || bad "clean: lookup: $out"
expect_exit 1 "clean: lookup not found" "$UL" lookup Plugn --root "$clean"
grep -q 'near: Plugin' <<<"$err" && ok "clean: near matches" || bad "clean: near matches: $err"
expect_exit 0 "clean: lookup --from ranks the closest namespace" "$UL" lookup plugin --root "$clean" --from plugins/pay/x.go --format json
[ "$(jq -r '.matches[0].namespace' <<<"$out")" = durian.cloud/engineering/standards/OOP ] && ok "clean: --from order" || bad "clean: --from order: $out"

# --- every planted failure is red with its code, and only its code -----------------------
for d in "$SRC"/testdata/bad/*/; do
  name=$(basename "$d")
  want=${name:0:5}
  got=0
  out=$("$UL" check --root "$d" 2>&1) || got=$?
  codes=$(awk '{print $2}' <<<"$out" | sort -u | tr '\n' ' ')
  if [ "$got" = 1 ] && [ "$codes" = "$want " ]; then ok "check $want $name"; else bad "check $name: exit $got, want 1 with only $want, got [$codes]: $out"; fi
done

# --- add / session round trip ------------------------------------------------------------
export UL_SESSION=round-trip
expect_exit 0 "add: a session word" "$UL" add --root "$clean" --word Gizmo --desc "A thing coined in chat." --alias Gadget --see "durian.cloud/engineering/standards/OOP#OCP"
grep -q 'added session#Gizmo' <<<"$out" && ok "add: reports the id" || bad "add: output $out"
expect_exit 0 "add: sharpen a repo word" "$UL" add --root "$clean" --namespace durian.cloud/engineering/standards/OOP --word SRP --desc "One reason to change per module."
expect_exit 0 "session path" "$UL" session path
[ "$out" = "$UL_STATE_DIR/sessions/round-trip.yaml" ] && [ -f "$out" ] && ok "session file lives under UL_STATE_DIR" || bad "session path: $out"
expect_exit 0 "session show" "$UL" session show
grep -q '^namespace: session$' <<<"$out" && grep -q 'Gizmo:' <<<"$out" && ok "session show prints UL.yaml text" || bad "session show: $out"
expect_exit 0 "all with the session layer" "$UL" all --root "$clean"
jq -e '.session == "round-trip" and ([.words[] | select(.id == "session#Gizmo")][0].layer == "session") and ([.words[] | select(.id == "durian.cloud/engineering/standards/OOP#SRP")][0] | .layer == "session" and .overrides == "repo")' >/dev/null <<<"$out" && ok "all: session layer on top, overrides marked" || bad "all: session layer: $out"
expect_exit 0 "lookup finds the session word by alias" "$UL" lookup gadget --root "$clean"
grep -q '^Gizmo' <<<"$out" && ok "lookup: Gizmo" || bad "lookup gadget: $out"
expect_exit 0 "no-session ignores the layer" "$UL" all --root "$clean" --no-session
[ "$(jq -r '.session' <<<"$out")" = null ] && ok "no-session: session null" || bad "no-session: $out"
expect_exit 1 "add: a word that breaks a rule is refused" "$UL" add --root "$clean" --word Bad --desc "x" --see Nope
grep -q 'UL201' <<<"$err" && ok "add: refusal names UL201" || bad "add refusal: $err"
expect_exit 0 "check never sees the session layer" "$UL" check --root "$clean"
expect_exit 0 "gen --check never sees the session layer" "$UL" gen --check --root "$clean"
expect_exit 0 "session clear" "$UL" session clear
[ ! -e "$UL_STATE_DIR/sessions/round-trip.yaml" ] && ok "session file removed" || bad "session file still there"
unset UL_SESSION
expect_exit 2 "add without a session id" "$UL" add --root "$clean" --word W --desc x
expect_exit 0 "all without a session id runs on the repo layer" "$UL" all --root "$clean"
[ "$(jq -r '.session' <<<"$out")" = null ] && ok "all: no session id" || bad "all without session: $out"
FY_SESSION_ID=from-fy "$UL" session path | grep -q 'from-fy.yaml$' && ok "FY_SESSION_ID is the fallback" || bad "FY_SESSION_ID fallback"

# --- gen drift detection -----------------------------------------------------------------
md="$clean/docs/standard/oop/UL.md"
echo "drift" >>"$md"
expect_exit 1 "gen --check detects drift" "$UL" gen --check --root "$clean"
grep -q 'UL301' <<<"$out" && ok "gen --check names UL301" || bad "gen --check output: $out"
expect_exit 1 "check reports UL301 for a stale UL.md" "$UL" check --root "$clean"
expect_exit 0 "gen rewrites" "$UL" gen --root "$clean"
expect_exit 0 "gen --check is green again" "$UL" gen --check --root "$clean"
rm "$md"
expect_exit 1 "gen --check: a missing UL.md is drift" "$UL" gen --check --root "$clean"
expect_exit 0 "gen writes it back" "$UL" gen --root "$clean"
cmp -s "$md" "$SRC/testdata/clean/docs/standard/oop/UL.md" && ok "gen output equals the committed golden UL.md" || bad "gen output differs from the golden UL.md"

# --- speed: 1,000 words in 100 files, every command under the ceiling ---------------------
fast="$work/fast"
for f in $(seq 0 99); do
  d="$fast/area$(printf %03d "$f")"
  mkdir -p "$d"
  {
    printf 'namespace: speed.example/area%03d/part\npremise: Generated area %d\nlanguage:\n' "$f" "$f"
    for w in $(seq 0 9); do
      printf '  Word%03d x%02d:\n    desc: Means thing %d and uses Word%03d x%02d.\n' "$f" "$w" "$w" "$f" $(((w + 1) % 10))
      printf '    aliases: [Alias%03d x%02d]\n    details: |\n      Talks about Word%03d x%02d and Alias%03d x%02d with plenty of filler text around it.\n' "$f" "$w" "$f" $(((w + 3) % 10)) "$f" $(((w + 2) % 10))
      printf '    see: [Word%03d x%02d]\n' "$f" $(((w + 1) % 10))
    done
  } >"$d/UL.yaml"
done
"$UL" gen --root "$fast" >/dev/null
ceiling_ms=2000 # the spec says 1 s; the sandbox shares a busy builder, so the ceiling here is 2 s and the Go test holds the 1 s line
timed() {
  local name="$1" start end ms
  shift
  start=$(date +%s%N)
  "$@" >/dev/null 2>&1 || true
  end=$(date +%s%N)
  ms=$(((end - start) / 1000000))
  if [ "$ms" -lt "$ceiling_ms" ]; then ok "speed $name: ${ms} ms"; else bad "speed $name: ${ms} ms (limit $ceiling_ms)"; fi
}
timed all "$UL" all --root "$fast"
timed lookup "$UL" lookup "alias050 x05" --root "$fast" --from area042/x.go
timed check "$UL" check --root "$fast"
timed gen-check "$UL" gen --check --root "$fast"
[ "$("$UL" all --root "$fast" | jq '.words | length')" = 1000 ] && ok "speed fixture has 1000 words" || bad "speed fixture size"

# --- packaging: licence and notice files of the vendored modules ship ---------------------
if [ -n "${OUT:-}" ]; then
  [ -s "$OUT/share/licenses/ul/modules.txt" ] && grep -q 'gopkg.in/yaml.v3' "$OUT/share/licenses/ul/modules.txt" && ok "licenses: modules.txt" || bad "licenses: modules.txt"
  find "$OUT/share/licenses/ul/gopkg.in/yaml.v3" -iname 'LICENSE*' 2>/dev/null | grep -q . && ok "licenses: yaml.v3 LICENSE" || bad "licenses: yaml.v3 LICENSE"
  find "$OUT/share/licenses/ul/gopkg.in/yaml.v3" -iname 'NOTICE*' 2>/dev/null | grep -q . && ok "licenses: yaml.v3 NOTICE" || bad "licenses: yaml.v3 NOTICE"
  # the wrapper puts git on PATH, so the scan works from a bare environment
  env -i "$OUT/bin/ul" --version | grep -q '^ul ' && ok "wrapper runs in a bare environment" || bad "wrapper in a bare environment"
fi

echo "passed: $pass, failed: $fail"
[ "$fail" = 0 ]
