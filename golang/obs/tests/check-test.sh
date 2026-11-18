#!/usr/bin/env bash
# shellcheck disable=SC2015,SC2016 # `cmd && ok || bad`: ok/bad only echo and count; $A in sed is the literal Grafana query reference
# obs check (limits + versions + the query rules of obs query --static) and obs test (obs query --live +
# obs alerts + obs links + "every incident is caught by an alert"), end to end, plus the proof that
# the old commands print what they printed before (compat.sh).
#   check-test.sh <obs>
# Every claim: baseline green, planted failure red, and the red output names the file, the rule and the token.
set -euo pipefail
OBS=$1
here=$(cd "$(dirname "$0")" && pwd)
fx="$here/fixtures"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
base=$(($(date +%s) - 86400))
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
rc=0
run() {
  set +e
  "$OBS" "$@" >"$work/out.txt" 2>&1
  rc=$?
  set -e
}
# expect <rc> <token>...: the last run exited with rc and printed every token.
expect() {
  local want=$1 good=1
  shift
  [ "$rc" -eq "$want" ] || {
    good=0
    echo "  exit $rc, want $want"
  }
  local t
  for t in "$@"; do
    grep -qF -- "$t" "$work/out.txt" || {
      good=0
      echo "  missing: $t"
    }
  done
  [ "$good" -eq 1 ]
}
absent() {
  local t
  for t in "$@"; do
    if grep -qF -- "$t" "$work/out.txt"; then
      echo "  must be absent: $t"
      return 1
    fi
  done
}
# check <label> <cmd...>: print PASS or FAIL with the output.
verdict() {
  local label=$1
  shift
  if "$@"; then ok "$label"; else
    bad "$label"
    sed 's/^/  | /' "$work/out.txt" | head -40
  fi
}
fresh() { # fresh <name>: a writable copy of the test/good fixture
  rm -rf "${work:?}/$1"
  cp -R "$fx/test/good" "$work/$1"
  chmod -R u+w "$work/$1"
}

# ---- the old commands print what they printed before ----------------------------------------------
bash "$here/compat.sh" "$OBS" && ok "compat: query --static, query --live, alerts and links are byte-identical to 0.4 (tests/compat.sh)" || bad "compat (tests/compat.sh)"

# ---- obs check: green --------------------------------------------------------------------------------
run check --dir "$fx/test/good/observability"
verdict "check: a good folder is green, prints the limits and one query total" expect 0 'metrics total: worst case 2 series per landscape' 'obs check: ok: 1 metrics, 0 log events, 0 spans, 1 types, contract hash' '; queries: 1 queries (1 metrics), 0 failures, 0 warnings'
verdict "check: one summary line" test "$(grep -c '^obs check: ' "$work/out.txt")" -eq 1
run check --dir "$fx/good/observability" --names
verdict "check: --names prints the names each query reads" expect 0 'obs check: ok:' 'checkout_charges_total'
run check --dir "$fx/gen/good/observability"
verdict "check: a folder with no queries is still fine" expect 0 'obs check: ok:' '; queries: 0 queries (), 0 failures, 0 warnings'
# every static fixture of obs query --static is red (or green) under obs check, with the same rule and token
checked=0
for d in "$fx"/bad-* "$fx"/ok-*; do
  name=$(basename "$d")
  mode=$(sed -n 's/^mode: //p' "$d/expect")
  [ "$mode" = live ] && continue
  want=$(sed -n 's/^exit: //p' "$d/expect")
  run check --dir "$d/observability"
  toks=()
  while IFS= read -r line; do
    case "$line" in
    "contains: obs query") ;; # the old command's own summary
    "contains: "*) toks+=("${line#contains: }") ;;
    esac
  done <"$d/expect"
  verdict "check $name: exit $want and the query rule, file and token" expect "$want" "${toks[@]}"
  checked=$((checked + 1))
done
[ "$checked" -ge 25 ] && ok "check ran $checked static fixtures" || bad "check ran only $checked static fixtures"

# ---- obs check: the flags of obs query --static keep their meaning -----------------------------------------
run check --dir "$fx/bad-event-missing/observability"
verdict "check: a log alert with no contract event is red ([logsql-alert-needs-event] family)" expect 1 'obs check: FAILED:' 'query failures'
run check --dir "$fx/bad-event-missing/observability" --event-rule off
verdict "check: --event-rule off passes it" expect 0 'obs check: ok:'
run check --dir "$fx/good/observability" --event-rule bogus
verdict "check: --event-rule bogus is a usage error" expect 2 '--event-rule "bogus"'
run check --dir "$fx/good/observability" --contracts none
verdict "check: --contracts none is accepted" expect 0 'obs check: ok:'
run check --dir "$fx/good/observability" --vl-settings "$fx/good/observability/obs.yaml" --tempo-config "$fx/good/observability/obs.yaml"
verdict "check: --vl-settings and --tempo-config are accepted (they apply to the queries that need them, as in obs query --static)" expect 0 'obs check: ok:'

# ---- obs check: limits and queries fail in one run, one summary -------------------------------------------
fresh both
sed -i 's/budget: 2/budget: 1/' "$work/both/observability/contract.yaml"
mkdir -p "$work/both/observability/alerts/broken"
sed 's/sum(rate(fixture_requests_total{outcome="error"}\[1m\]))/sum(rate(fixture_requests_total{outcome="error"}[1m])/; s/uid: metric/uid: broken/; s/name: fixture-metric/name: fixture-broken/' "$fx/test/good/observability/alerts/metric/critical.yaml" >"$work/both/observability/alerts/broken/critical.yaml"
run check --dir "$work/both/observability"
verdict "check: an over-budget metric AND a broken query are both red in one run" expect 1 '[over-budget]' 'alerts/broken/critical.yaml' '[metricsql-syntax]' 'obs check: FAILED:'
verdict "check: exactly one summary line when both halves fail" test "$(grep -c '^obs check: ' "$work/out.txt")" -eq 1
# a bad obs.yaml version and a clean folder: red from the versions half alone
cp -R "$fx/good" "$work/ver"
chmod -R u+w "$work/ver"
sed -i 's/^  grafana: 13.3.0/  grafana: 12.0.0/' "$work/ver/observability/obs.yaml"
run check --dir "$work/ver/observability"
verdict "check: an obs.yaml version mismatch is red and the query total is still printed" expect 1 'versions.grafana' 'obs check: FAILED: 1 version mismatch(es)' 'queries: 7 queries'
# a folder with no contract.yaml: the limits are skipped with a note, the queries are still checked
run check --dir "$fx/sample-app/observability"
verdict "check: no contract.yaml skips the limits with a note and checks the queries" expect 0 'contract.yaml not found: limits' 'obs check: ok: no contract.yaml, limits not checked; queries: '
run check --dir "$fx/bad-metricsql-syntax/observability"
verdict "check: no contract.yaml and a bad query is red" expect 1 '[metricsql-syntax]' 'obs check: FAILED: 1 query failures'
# an invalid contract is reported once (the query half does not print it again)
cp -R "$fx/gen/bad-over-budget" "$work/ob"
chmod -R u+w "$work/ob"
run check --dir "$work/ob/observability"
verdict "check: an invalid contract is red and reported once, not again as a query rule" expect 1 '[over-budget]' 'obs check: FAILED:'
verdict "check: ... no [contract] finding from the query half" absent '[contract]'
# speed: gen + check on the fixtures (the numbers in the README are measured with `time` on the 4 h fixture)
start=$(date +%s%N)
run gen --check --dir "$fx/gen/good/observability" --lang go
run check --dir "$fx/good/observability"
ms=$((($(date +%s%N) - start) / 1000000))
[ "$ms" -lt 3000 ] && ok "check: gen --check + check on the fixtures took ${ms} ms (limit 3000 ms here; measured under 1 s on a laptop)" || bad "check: gen + check took ${ms} ms"

# ---- obs test: green ---------------------------------------------------------------------------------------
run test --dir "$fx/test/good/observability" --base "$base"
verdict "test: a good folder is green with one total per part" expect 0 'obs test: queries: ok: 1 queries' 'obs test: alerts: ok: 1 alerts (0 recording rules), 2 runs, 2 expectations, 0 failed' 'obs test: links: skipped: the seed has no traces' 'obs test: incidents: ok: 1 incidents, 1 caught, 0 uncaught' 'obs test: ok'
run test --dir "$fx/links/good" --base "$base"
verdict "test: links part runs when the seed has traces" expect 0 'obs test: links: ok: trace-to-logs=2 metric-to-traces=2 span-to-profile=2, 0 failures' 'obs test: incidents: ok: 0 incidents, 0 caught, 0 uncaught' 'obs test: ok'
run test --dir "$fx/test/good/observability" --base "$base" --out "$work/results.json" --workers 1
verdict "test: --out and --workers reach obs alerts" test -s "$work/results.json"
run test --dir "$fx/test/good/observability" --base "$base" --names
verdict "test: --names prints the names each query reads" expect 0 'fixture_requests_total'
run test --dir "$fx/test/good/observability" --event-rule bogus
verdict "test: --event-rule bogus is a usage error" expect 2 '--event-rule "bogus"'
run test --dir "$fx/test/good/observability" --contracts bogus
verdict "test: --contracts bogus is a usage error" expect 2 '--contracts "bogus"'
run test --dir "$fx/test/good/observability" --workers -1
verdict "test: a negative --workers is a usage error" expect 2 'negative --workers'
run test --dir "$work/nowhere/observability"
verdict "test: a folder with no seed is an input error" expect 2 'obs test'

# ---- obs test: every part can fail, and the output names it -----------------------------------------------
run test --dir "$fx/bad-no-rows/observability" --seed "$fx/shared-seed.yaml" --base "$base"
verdict "test: a query with no rows fails the queries part" expect 1 'alerts/charges/critical.yaml rule charges-critical query A' '[no-rows]' 'checkout_chargez_total' 'obs test: queries: FAILED:' 'obs test: FAILED: queries'
run test --dir "$fx/alerts/features" --base "$base"
verdict "test: a broken link fails the links part" expect 1 'seed.yaml route "POST /charge"' '[trace-to-logs]' 'obs test: links: FAILED: trace-to-logs=1 metric-to-traces=2 span-to-profile=1, 3 failures'
fresh quiet
sed -i 's/\$A > 0.5/$A > 500/' "$work/quiet/observability/alerts/metric/critical.yaml"
run test --dir "$work/quiet/observability" --base "$base"
verdict "test: an alert that does not fire in its incident fails the alerts part" expect 1 'rule metric window spike expect fire' 'obs test: alerts: FAILED:' 'obs test: FAILED:'
verdict "test: ... and the incident it was meant to catch is uncaught, naming the alert" expect 1 'FAIL incidents/spike.yaml incident spike window [10m0s,20m0s): [incident-uncaught]' 'metric (quiet)'
fresh mismatch
mkdir -p "$work/mismatch/observability"
cp "$fx/good/observability/obs.yaml" "$work/mismatch/observability/obs.yaml"
sed -i 's/^  victoriametrics: v1.156.0/  victoriametrics: v1.100.0/' "$work/mismatch/observability/obs.yaml"
run test --dir "$work/mismatch/observability" --base "$base"
verdict "test: an obs.yaml version mismatch stops it before any part runs" expect 1 'versions.victoriametrics' 'obs test: FAILED: 1 version(s)'
verdict "test: ... no part ran" absent 'obs test: queries:' 'obs test: alerts:'

# ---- obs test: every incident is caught by at least one alert (owner pick 2) -------------------------------
# The LLM flow: add an incident the alerts do not catch (red), then add the alert that catches it (green).
fresh llm
cat >"$work/llm/observability/incidents/slow-burn.yaml" <<'YAML'
{
  story: Charge failures at a trickle,
  window: { start: 10m, duration: 10m },
  change: [{ metric: fixture.requests, where: { outcome: error }, rate: 0.1/s }],
  expect: { quiet: [metric], fire_within: 5m, resolve_within: 5m },
}
YAML
sed -i 's/incidents: \[spike\]/incidents: [spike, slow-burn]/' "$work/llm/observability/seed.yaml"
run test --dir "$work/llm/observability" --base "$base"
verdict "test: an incident no alert catches is red, naming the incident file, the incident and its window" expect 1 'FAIL incidents/slow-burn.yaml incident slow-burn window [10m0s,20m0s): [incident-uncaught] expect.fire is empty' 'obs test: incidents: FAILED: 2 incidents, 1 caught, 1 uncaught' 'obs test: FAILED: incidents'
verdict "test: ... and the other parts stay green (the red is the new rule alone)" expect 1 'obs test: queries: ok:' 'obs test: alerts: ok: 1 alerts'
# green: the alert that catches it, listed in expect.fire of both incidents (it fires in both)
mkdir -p "$work/llm/observability/alerts/trickle"
sed 's/uid: metric/uid: trickle/; s/name: fixture-metric/name: fixture-trickle/; s/\$A > 0.5/$A > 0.05/' "$work/llm/observability/alerts/metric/critical.yaml" >"$work/llm/observability/alerts/trickle/critical.yaml"
sed -i 's/fire: \[metric\]/fire: [metric, trickle]/' "$work/llm/observability/incidents/spike.yaml"
sed -i 's/quiet: \[metric\]/fire: [trickle], quiet: [metric]/' "$work/llm/observability/incidents/slow-burn.yaml"
run test --dir "$work/llm/observability" --base "$base"
verdict "test: ... add the alert, list it in expect.fire: green" expect 0 'obs test: alerts: ok: 2 alerts' 'obs test: incidents: ok: 2 incidents, 2 caught, 0 uncaught' 'obs test: ok'
# the same rule when the folder has no alert at all
fresh norules
rm -rf "${work:?}/norules/observability/alerts"
run test --dir "$work/norules/observability" --base "$base"
verdict "test: a folder with no alert rules and an incident is red (not an input error)" expect 1 'FAIL incidents/spike.yaml incident spike window [10m0s,20m0s): [incident-uncaught] the folder has no alert rules' 'obs test: alerts: ok: 0 alerts'
fresh nothing
rm -rf "${work:?}/nothing/observability/alerts" "${work:?}/nothing/observability/incidents"
sed -i '/incidents: \[spike\]/d' "$work/nothing/observability/seed.yaml"
run test --dir "$work/nothing/observability" --base "$base"
verdict "test: no alerts and no incidents has nothing to catch: green" expect 0 'obs test: incidents: ok: 0 incidents, 0 caught, 0 uncaught' 'obs test: ok'
echo "obs check/test tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
