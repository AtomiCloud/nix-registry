# shellcheck shell=bash
# shellcheck disable=SC2015,SC2154 # sourced by run.sh, which defines ok/bad/run/$OBS/$fx/$work/$rc and the `ok || bad` idiom
# obs check, obs gen and obs gen --check end to end (sourced by run.sh).
#
# tests/fixtures/gen/good is a consumer repo with its generated files committed (the golden files),
# so `obs gen --check` on it is the golden test of every language. Planted failures: tests/fixtures/gen/bad-*.
genlang=()
[ -n "${GEN_LANGS:-}" ] && genlang=(--lang "$GEN_LANGS") # developer shortcut; CI runs every target

# ---- obs check -----------------------------------------------------------------------
run check --dir "$fx/gen/good/observability" --pods 3 --landscapes 2
[ "$rc" -eq 0 ] && grep -q 'worst case 3456 series, budget 5000' "$work/out.txt" && grep -q '^log streams: worst case 4 ' "$work/out.txt" &&
  grep -q 'checkout.runtime.memory (gauge): .*per pod: x 3 pods = 3' "$work/out.txt" && grep -q '^events=\[checkout.charge.succeeded' "$work/out.txt" &&
  ok "obs check prints worst case per metric, worst case x pods, the log stream count and the events" || bad "obs check good: rc=$rc $(cat "$work/out.txt")"

for d in "$fx"/gen/bad-*; do
  name=$(basename "$d")
  want_rc=$(sed -n 's/^exit: //p' "$d/expect")
  run check --dir "$d/observability"
  good=1
  [ "$rc" -eq "$want_rc" ] || good=0
  while IFS= read -r line; do
    case "$line" in
    "contains: "*) grep -qF -- "${line#contains: }" "$work/out.txt" || {
      good=0
      echo "  missing: ${line#contains: }"
    } ;;
    esac
  done <"$d/expect"
  if [ "$good" -eq 1 ]; then ok "obs check $name exit $rc and names the file, rule and token"; else
    bad "obs check $name: exit $rc, want $want_rc"
    sed 's/^/  | /' "$work/out.txt"
  fi
done

# An invalid contract writes nothing.
cp -r "$fx/gen/bad-over-budget" "$work/gbad"
run gen --dir "$work/gbad/observability"
[ "$rc" -eq 1 ] && [ ! -e "$work/gbad/observability/contract" ] && grep -q '\[over-budget\]' "$work/out.txt" && ok "obs gen refuses an invalid contract and writes nothing" || bad "obs gen on an invalid contract: rc=$rc"
mkdir -p "$work/nocontract/observability"
run gen --dir "$work/nocontract/observability"
[ "$rc" -eq 2 ] && ok "obs gen without contract.yaml is an input error (exit 2)" || bad "obs gen no contract: rc=$rc"

# ---- obs gen --check: the committed golden files equal a fresh generate --------------
rm -rf "$work/g1"
cp -r "$fx/gen/good" "$work/g1"
chmod -R u+w "$work/g1"
run gen --check --dir "$work/g1/observability" "${genlang[@]}"
[ "$rc" -eq 0 ] && grep -q 'obs gen --check: ok:' "$work/out.txt" && ok "gen --check: every committed golden file equals a fresh generate" || bad "gen --check on the golden tree: rc=$rc $(cat "$work/out.txt")"

# A hand edit of a generated file is red and says so (the contract hash in its header still matches).
echo "// tampered by hand" >>"$work/g1/gen/go/signals/signals.go"
run gen --check --dir "$work/g1/observability" "${genlang[@]}"
[ "$rc" -eq 1 ] && grep -q 'gen/go/signals/signals.go: \[gen-edited\]' "$work/out.txt" && ok "gen --check: a hand edit is red, naming the file and [gen-edited]" || bad "gen --check hand edit: rc=$rc $(cat "$work/out.txt")"
# ... and so is a hand edit of events.json.
rm -rf "$work/g1b"
cp -r "$fx/gen/good" "$work/g1b"
chmod -R u+w "$work/g1b"
sed -i 's/checkout.charge.failed/checkout.charge.faild/' "$work/g1b/observability/contract/events.json"
run gen --check --dir "$work/g1b/observability" "${genlang[@]}"
[ "$rc" -eq 1 ] && grep -q 'contract/events.json: \[gen-edited\]' "$work/out.txt" && ok "gen --check: a hand edit of events.json is red" || bad "gen --check events edit: rc=$rc"

# A contract change with no regenerate is red: every file's header hash is the old one.
rm -rf "$work/g2"
cp -r "$fx/gen/good" "$work/g2"
chmod -R u+w "$work/g2"
sed -i 's/values: \[stripe, adyen, paypal\]/values: [stripe, adyen, paypal, square]/' "$work/g2/observability/contract.yaml"
run gen --check --dir "$work/g2/observability" "${genlang[@]}"
[ "$rc" -eq 1 ] && grep -q 'gen/go/signals/signals.go: \[gen-stale\]' "$work/out.txt" && grep -q 'contract/aggregation.yaml: \[gen-stale\]' "$work/out.txt" &&
  grep -q 'contract/events.json: \[gen-stale\]' "$work/out.txt" && ok "gen --check: a contract change with no regenerate is red ([gen-stale] on every file)" || bad "gen --check stale: rc=$rc $(cat "$work/out.txt")"
# obs gen fixes it, and the new enum value reaches the generated code.
run gen --dir "$work/g2/observability" "${genlang[@]}"
[ "$rc" -eq 0 ] && grep -q 'ProviderSquare' "$work/g2/gen/go/signals/signals.go" && ok "obs gen regenerates after a contract change (new value square is in the Go types)" || bad "obs gen regenerate: rc=$rc"
run gen --check --dir "$work/g2/observability" "${genlang[@]}"
[ "$rc" -eq 0 ] && ok "gen --check is green again after obs gen" || bad "gen --check after regenerate: rc=$rc"

# A missing generated file is red.
rm -rf "$work/g3"
cp -r "$fx/gen/good" "$work/g3"
chmod -R u+w "$work/g3"
rm "$work/g3/observability/contract/events.json"
run gen --check --dir "$work/g3/observability" "${genlang[@]}"
[ "$rc" -eq 1 ] && grep -q 'contract/events.json: \[gen-missing\]' "$work/out.txt" && ok "gen --check: a missing file is red ([gen-missing])" || bad "gen --check missing: rc=$rc"

# Deterministic: two clean generates give the same bytes.
for n in d1 d2; do
  rm -rf "${work:?}/$n"
  mkdir -p "$work/$n/observability"
  cp "$fx/gen/good/observability/contract.yaml" "$work/$n/observability/"
  cp "$fx/gen/good/"{go.mod,package.json,pubspec.yaml,Signals.csproj} "$work/$n/"
  run gen --dir "$work/$n/observability" "${genlang[@]}"
done
diff -r "$work/d1" "$work/d2" >/dev/null && ok "obs gen is deterministic: two runs write identical files" || bad "obs gen output differs between runs"
# ... and equals the committed golden tree.
diff -r "$work/d1/gen" "$fx/gen/good/gen" >/dev/null 2>&1 || [ -n "${GEN_LANGS:-}" ] && ok "obs gen from the fixture contract equals the committed golden files" || bad "fresh generate differs from the golden tree"

# ---- the generated code compiles in its language -------------------------------------
for tc in "$here"/typecheck/*.sh; do
  lang=$(basename "$tc" .sh)
  case "$lang" in
  go) tool=go ;;
  bun) tool=tsc ;;
  dotnet) tool=dotnet ;;
  dart) tool=dart ;;
  *) tool="$lang" ;;
  esac
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "SKIP generated $lang code compile check: $tool not on PATH"
    continue
  fi
  if OBS_GEN_BINARY="$OBS" bash "$tc" >"$work/tc-$lang.txt" 2>&1 && ! grep -q '^FAIL' "$work/tc-$lang.txt"; then
    ok "generated $lang code compiles and every planted bad consumer is red ($(grep -c '^PASS' "$work/tc-$lang.txt") checks)"
  else
    bad "generated $lang typecheck"
    sed 's/^/  | /' "$work/tc-$lang.txt"
  fi
done

# Generator fuzz: tricky names (keywords, core types, case collisions, wide numbers) must be refused before any
# write, or generate code that compiles. A language whose compiler is not on PATH is skipped (fuzz.sh says which).
if bash "$here/fuzz.sh" --obs "$OBS" >"$work/fuzz.txt" 2>&1 && ! grep -q '^FAIL' "$work/fuzz.txt"; then
  ok "generator fuzz: every tricky contract is refused before writing or compiles ($(grep -c '^PASS' "$work/fuzz.txt") checks, $(grep -c '^SKIP' "$work/fuzz.txt") skipped)"
else
  bad "generator fuzz"
  grep -v '^PASS' "$work/fuzz.txt" | sed 's/^/  | /'
fi

# The concept 3.1 example verbatim: `user_id: free` on a span attribute, with no `free` type declared.
run check --dir "$fx/gen/ok-concept-example/observability"
[ "$rc" -eq 0 ] && ok "obs check accepts the concept 3.1 span example (free without a declaration)" || bad "concept 3.1 example: rc=$rc $(cat "$work/out.txt")"
