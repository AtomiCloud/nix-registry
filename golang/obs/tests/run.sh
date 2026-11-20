#!/usr/bin/env bash
# shellcheck disable=SC2015 # `cmd && ok || bad`: ok/bad only echo and count, so the C-branch-after-A case cannot happen
# End-to-end test of the obs binary, run inside a nix build (no network, no containers).
#   run.sh --obs <bin> --src <tool source dir> [--out <package output>]
#
# The good fixtures run green; every planted failure (tests/fixtures/bad-*) runs red and the
# output names the file, the rule or panel and the offending token. Each fixture's `expect`
# file lists what must be in the output.
set -euo pipefail
while [ $# -gt 0 ]; do
  case "$1" in
  --obs)
    OBS="$2"
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
fx="$here/fixtures"
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

# The seed's time line must end in the past; a fixed base makes two runs comparable.
base=$(($(date +%s) - 86400))

# run <name> <args...>: run obs, keep the output in $work/out.txt, set rc.
run() {
  set +e
  "$OBS" "$@" >"$work/out.txt" 2>&1
  rc=$?
  set -e
}

# ---- version -------------------------------------------------------------------------
run version
[ "$rc" -eq 0 ] && grep -q '^obs [0-9]' "$work/out.txt" && ok "version" || bad "version: $(cat "$work/out.txt")"
# obs's own version, set once in default.nix for the whole obs-full release (not the registry release).
grep -qx 'obs 0.5.0' "$work/out.txt" && ok "version prints obs 0.5.0" || bad "version is not 0.5.0: $(head -1 "$work/out.txt")"
grep -q 'VictoriaMetrics v1.156.0' "$work/out.txt" && grep -q 'VictoriaLogs v1.54.0 (tag commit 6433dae5' "$work/out.txt" &&
  ok "version prints every engine pin" || bad "version engine pins"
run
[ "$rc" -eq 2 ] && ok "no command is a usage error (exit 2)" || bad "no command exit $rc"
run query
[ "$rc" -eq 2 ] && ok "query without --static/--live is a usage error" || bad "query without mode exit $rc"

# ---- good fixtures: green ------------------------------------------------------------
for f in good sample-app; do
  for mode in static live; do
    run query "--$mode" --dir "$fx/$f/observability" --base "$base"
    [ "$rc" -eq 0 ] && grep -q "obs query --$mode: ok:" "$work/out.txt" && ok "$f $mode exit 0" || bad "$f $mode exit $rc: $(cat "$work/out.txt")"
  done
done

# Seed data and results are deterministic: the same seed and base give the same output.
run query --live --dir "$fx/good/observability" --base "$base" --names
cp "$work/out.txt" "$work/live1.txt"
run query --live --dir "$fx/good/observability" --base "$base" --names
cmp -s "$work/live1.txt" "$work/out.txt" && ok "live output is identical on a second run" || bad "live output differs between runs"

# Sample-app has no contract.yaml: the event rule is skipped with one note per log alert.
run query --static --dir "$fx/sample-app/observability"
n=$(grep -c 'event rule skipped: no contract.yaml' "$work/out.txt" || true)
[ "$n" -eq 1 ] && ok "sample-app: one 'event rule skipped' note for its one log alert" || bad "sample-app event notes: $n"
# ... and the rule is switchable: forced on, the same alert (no event filter) fails.
run query --static --dir "$fx/sample-app/observability" --event-rule on
[ "$rc" -eq 1 ] && grep -q '\[logsql-alert-needs-event\]' "$work/out.txt" && ok "--event-rule on fails a log alert with no event filter" || bad "--event-rule on: rc=$rc"
run query --static --dir "$fx/bad-event-missing/observability" --event-rule off
[ "$rc" -eq 0 ] && ok "--event-rule off passes the contract-present fixture" || bad "--event-rule off: rc=$rc"

# The sample-app live run must really read the seed: the crash alert's rows come from the seeded panic line.
run query --live --dir "$fx/sample-app/observability" --base "$base"
grep -q "pod-crashloop.*threshold-removed" "$work/out.txt" && ok "sample-app: the threshold in the metrics alert is run without its comparison" || bad "threshold note missing"

# --live treats a log alert's filter on a free-form field as `obs query --static` and `obs alerts` do: assumed true,
# dropped, with the same note. A copy of fixtures/good whose alert also filters on user_tier (not in the seed).
cp -R "$fx/good" "$work/assumed"
chmod -R u+w "$work/assumed"
sed -i 's/"durian.cloud\/event":="checkout.charge.failed" | stats/"durian.cloud\/event":="checkout.charge.failed" user_tier:=gold | stats/' "$work/assumed/observability/alerts/failed-logs/warning.yaml"
grep -q 'user_tier:=gold' "$work/assumed/observability/alerts/failed-logs/warning.yaml" && ok "assumed-filter fixture planted (user_tier:=gold)" || bad "assumed-filter fixture not planted"
for mode in static live; do
  run query "--$mode" --dir "$work/assumed/observability" --base "$base"
  [ "$rc" -eq 0 ] && grep -q 'assumed true (free-form fields): user_tier:=gold' "$work/out.txt" &&
    ok "assumed filter: $mode exits 0 and prints the assumed-true note" || bad "assumed filter $mode: exit $rc: $(cat "$work/out.txt")"
done
# Red: with the event rule off nothing is assumed, the filter stays, and --live fails no-rows naming the field.
run query --live --dir "$work/assumed/observability" --base "$base" --event-rule off
[ "$rc" -eq 1 ] && grep -q '\[no-rows\].*user_tier' "$work/out.txt" && ok "assumed filter kept (--event-rule off): live fails no-rows naming user_tier" || bad "--event-rule off live: exit $rc: $(cat "$work/out.txt")"

# --live does not fail an alert query over a recording rule's output; it names the rule and says obs alerts checks it.
# A copy of the alerts features fixture with only the recording rule and the alert that reads its output.
cp -R "$fx/alerts/features" "$work/rec"
chmod -R u+w "$work/rec"
for d in "$work/rec/alerts"/*; do
  case "$(basename "$d")" in record-rule | recorded) ;; *) rm -rf "${d:?}" ;; esac
done
run query --live --dir "$work/rec" --base "$base"
[ "$rc" -eq 0 ] && grep -q 'alerts/recorded/critical.yaml rule recorded query A: \[recording-rule\] reads recording rule fixture_recorded: checked by obs alerts' "$work/out.txt" &&
  ok "recording-rule read: exit 0 and a named note (file, rule, recording rule)" || bad "recording-rule read: exit $rc: $(cat "$work/out.txt")"
# Red: without the recording rule the metric is just missing from the seed, and --live fails no-rows naming it.
rm -rf "${work:?}/rec/alerts/record-rule"
run query --live --dir "$work/rec" --base "$base"
[ "$rc" -eq 1 ] && grep -q '\[no-rows\].*fixture_recorded' "$work/out.txt" && ! grep -q 'recording-rule' "$work/out.txt" &&
  ok "recording-rule read: a metric no rule records still fails no-rows" || bad "unrecorded metric live: exit $rc: $(cat "$work/out.txt")"

# A wrong label spelling fails once, even in a live run that also applies the static rules: two wrong labels
# (a selector and a grouping) in one query are exactly two failures, not four.
run query --live --dir "$fx/bad-dotted-underscore/observability" --seed "$fx/shared-seed.yaml" --base "$base"
[ "$rc" -eq 1 ] && grep -q ': FAILED: .* 2 failures' "$work/out.txt" && ! grep -q '\[label-spelling\]' "$work/out.txt" &&
  ok "bad-dotted-underscore (live): two wrong labels are exactly two failures, each reported once" || bad "bad-dotted-underscore live count: exit $rc: $(cat "$work/out.txt")"
run query --live --dir "$fx/bad-dotted-label-values/observability" --seed "$fx/shared-seed.yaml" --base "$base"
[ "$rc" -eq 1 ] && grep -q ': FAILED: .* 1 failures' "$work/out.txt" &&
  ok "bad-dotted-label-values (live): the wrong variable label fails once" || bad "bad-dotted-label-values live count: exit $rc: $(cat "$work/out.txt")"

# ---- planted failures: each red, naming file + rule/panel + the bad token ------------
for d in "$fx"/bad-* "$fx"/ok-*; do
  name=$(basename "$d")
  mode=$(sed -n 's/^mode: //p' "$d/expect")
  want_rc=$(sed -n 's/^exit: //p' "$d/expect")
  modes="$mode"
  [ "$mode" = both ] && modes="static live"
  for m in $modes; do
    run query "--$m" --dir "$d/observability" --seed "$fx/shared-seed.yaml" --base "$base"
    good=1
    [ "$rc" -eq "$want_rc" ] || good=0
    while IFS= read -r line; do
      case "$line" in
      "contains: "*) grep -qF -- "${line#contains: }" "$work/out.txt" || {
        good=0
        echo "  missing: ${line#contains: }"
      } ;;
      "absent: "*) ! grep -qF -- "${line#absent: }" "$work/out.txt" || {
        good=0
        echo "  must be absent: ${line#absent: }"
      } ;;
      esac
    done <"$d/expect"
    if [ "$good" -eq 1 ]; then ok "$name ($m) exit $rc and names the file, rule and token"; else
      bad "$name ($m): exit $rc, want $want_rc"
      sed 's/^/  | /' "$work/out.txt"
    fi
  done
done

# A planted failure must also fail the OTHER fixtures' good copy: the baseline stays green
# (checked above), so each red result is caused by the planted file alone.

# ---- the vendored VictoriaLogs tree equals the tag, except the one marked file -------
(cd "$SRC/third_party/VictoriaLogs" && grep -v ' lib/logstorage/obs_fieldnames.go$' ../VictoriaLogs.sha256 | sha256sum -c --quiet) &&
  ok "third_party/VictoriaLogs equals tag v1.54.0 (6433dae5) in every listed file" || bad "third_party tree differs from the tag"
extra=$(cd "$SRC/third_party/VictoriaLogs" && find . -type f | sed 's#^\./##' | sort | comm -13 <(sed 's/^[0-9a-f]*  //' ../VictoriaLogs.sha256 | sort) - | grep -v '^lib/logstorage/obs_fieldnames.go$' || true)
[ -z "$extra" ] && ok "no file in third_party/VictoriaLogs besides the tag's and the marked one" || bad "unexpected files in third_party: $extra"
grep -q 'OBS CHANGE' "$SRC/third_party/VictoriaLogs/lib/logstorage/obs_fieldnames.go" && ok "the one added file is marked as an obs change" || bad "added file not marked"
# Planted drift: a copy of the tree with one byte changed must fail the same check.
cp -r "$SRC/third_party/VictoriaLogs" "$work/tp"
chmod -R u+w "$work/tp"
echo "// tamper" >>"$work/tp/lib/logstorage/parser.go"
(cd "$work/tp" && grep -v ' lib/logstorage/obs_fieldnames.go$' "$SRC/third_party/VictoriaLogs.sha256" | sha256sum -c --quiet >/dev/null 2>&1) &&
  bad "a tampered tree passed the drift check" || ok "a tampered third_party tree fails the drift check"

# ---- obs profile: summarize, baseline, compare (tests/profile.sh) --------------------------
bash "$here/profile.sh" "$OBS" "$fx/profile" && ok "profile tests (tests/profile.sh)" || bad "profile tests (tests/profile.sh)"
# ---- obs assert: series per metric against the contract (tests/assert.sh) ------------------
bash "$here/assert.sh" "$OBS" "$fx/assert" && ok "assert tests (tests/assert.sh)" || bad "assert tests (tests/assert.sh)"

# ---- licences ship with the binary --------------------------------------------------
if [ -n "${OUT:-}" ]; then
  # buildGoModule installs every main package. Development-only generators must
  # stay out of the shipped package: obs is one executable.
  only_obs() { [ "$(ls -1 "$1/bin")" = obs ] && [ -x "$1/bin/obs" ]; }
  only_obs "$OUT" && ok "single-binary: bin/obs is the only executable" || bad "single-binary: unexpected file in $OUT/bin"
  mkdir -p "$work/binary-shape/bin"
  cp "$OUT/bin/obs" "$work/binary-shape/bin/obs"
  alias_dir="$work/binary-shape"
  only_obs "$alias_dir" && ok "single-binary: copied baseline is green" || bad "single-binary: copied baseline failed"
  touch "$alias_dir/bin/gen"
  only_obs "$alias_dir" && bad "single-binary: planted bin/gen passed" || ok "single-binary: planted bin/gen is refused"
  lic="$OUT/share/licenses/obs"
  [ -s "$lic/LICENSE" ] && grep -q 'GNU AFFERO GENERAL PUBLIC LICENSE' "$lic/LICENSE" && ok "AGPL-3.0 text ships in share/licenses/obs" || bad "AGPL LICENSE missing in output"
  [ -s "$lic/modules.txt" ] && ok "vendor/modules.txt ships" || bad "modules.txt missing"
  [ -s "$lic/github.com/VictoriaMetrics/VictoriaLogs/LICENSE" ] && ok "VictoriaLogs LICENSE ships" || bad "VictoriaLogs LICENSE missing"
fi

# ---- obs check, obs gen, generated code (gen.sh)
# shellcheck disable=SC1091 # path is dynamic; gen.sh is checked on its own
. "$here/gen.sh"
bash "$here/store-symbols-sizing.sh" "$OBS" && ok "store/symbols/size fixtures" || bad "store/symbols/size fixtures"

# shellcheck source=/dev/null
. "$here/sandbox.sh"

bash "$here/links.sh" "$OBS" "${OUT:-}"
bash "$here/engines.sh" "$OBS" && ok "engines --check-chart (tests/engines.sh)" || bad "engines --check-chart (tests/engines.sh)"
bash "$here/obsyaml.sh" "$OBS" && ok "obs.yaml versions and thresholds (tests/obsyaml.sh)" || bad "obs.yaml versions and thresholds (tests/obsyaml.sh)"
bash "$here/contract-source.sh" "$OBS" && ok "source: contract entries, estimate labels, keep-list (tests/contract-source.sh)" || bad "source: contract entries, estimate labels, keep-list (tests/contract-source.sh)"
bash "$here/live.sh" "$OBS" && ok "obs live read-only commands (tests/live.sh)" || bad "obs live read-only commands (tests/live.sh)"
bash "$here/flat.sh" "$OBS" && ok "flat alert tier files (tests/flat.sh)" || bad "flat alert tier files (tests/flat.sh)"
bash "$here/check-test.sh" "$OBS" && ok "obs check (limits, versions, queries), obs test (live queries, alerts, links, incidents), old commands byte-identical (tests/check-test.sh, tests/compat.sh)" || bad "obs check / obs test (tests/check-test.sh)"
echo "obs tests: $pass passed, $fail failed"
bash "$here/alerts.sh" "$OBS"
[ "$fail" -eq 0 ]
