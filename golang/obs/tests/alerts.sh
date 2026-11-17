#!/usr/bin/env bash
# shellcheck disable=SC2016 # $A is the literal Grafana query reference in the planted mutations.
# Offline evaluator regression: every planted failure names its rule, file and value.
set -euo pipefail
OBS=$1
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp -R "$here/fixtures/alerts/features" "$work/o"
chmod -R u+w "$work/o"
o="$work/o"
run() {
  set +e
  "$OBS" "$@" >"$work/out" 2>&1
  rc=$?
  set -e
}
# The cache invocations below must not inherit the self-check variables: exporting OBS_VERIFY_PRUNE=1
# for the whole script would bypass the result cache they count hits on.
run_cached() {
  set +e
  env -u OBS_VERIFY_FAST -u OBS_VERIFY_SKIP -u OBS_VERIFY_PRUNE "$OBS" "$@" >"$work/out" 2>&1
  rc=$?
  set -e
}
expect() {
  local want=$1
  shift
  if [ "$rc" -ne "$want" ]; then
    cat "$work/out"
    echo "alerts: exit $rc wanted $want"
    exit 1
  fi
  for token in "$@"; do
    if ! grep -qF -- "$token" "$work/out"; then
      cat "$work/out"
      echo "alerts: missing $token"
      exit 1
    fi
  done
}

# Full engine run, pending/recovery/no-data/error paths, and exported recording samples.
OBS_VERIFY_FAST=1 OBS_VERIFY_SKIP=1 OBS_VERIFY_PRUNE=1 run alerts --dir "$o" --steps "$work/steps.json" --recorded "$work/recorded.json"
expect 0 'failed=0' 'mismatches=0' 'assumed true (free-form fields)' 'events=[fixture.failed]'
grep -qE 'verify fast: compared=[1-9][0-9]* mismatches=0' "$work/out"
grep -qE 'verify skip: compared=[1-9][0-9]* mismatches=0' "$work/out"
grep -qE 'verify prune: base=[0-9]+ compared=[1-9][0-9]* query_inputs=[1-9][0-9]* mismatches=0' "$work/out"
for state in Pending Recovering NoData Error; do grep -qF "$state" "$work/steps.json"; done
grep -qF '"__name__":"fixture_recorded"' "$work/recorded.json"

# Persistent result cache: all eight rules hit on the second invocation.
OBS_CACHE_DIR="$work/cache" run_cached alerts --dir "$o"
expect 0 'cached=0' 'failed=0'
OBS_CACHE_DIR="$work/cache" run_cached alerts --dir "$o"
expect 0 'cached=8' 'failed=0'

# Only a changed rule is invalidated; its siblings reuse their own normalized CR inputs.
cp "$o/alerts/metric/critical.yaml" "$work/metric"
sed -i 's/\$A > 0.5/\$A > 0.6/' "$o/alerts/metric/critical.yaml"
OBS_CACHE_DIR="$work/cache" run_cached alerts --dir "$o"
expect 0 'cached=7' 'failed=0'
cp "$work/metric" "$o/alerts/metric/critical.yaml"

# Unused trace sources are projected with their metrics; invalid sources still fail.
cp "$o/seed.yaml" "$work/seed-traces"
awk '
  /^  metrics:/ { metric=1 }
  /^  incidents:/ { print "  traces: [{from: {metric: unused}, server: fixture}]," }
  { print }
  metric && /^    \{/ { print "      unused: {type: counter, series: [{where: {}, share: 1}]},"; metric=0 }
' "$work/seed-traces" >"$o/seed.yaml"
run alerts --dir "$o"
expect 0 'failed=0' 'pruned=1'
sed -i 's/metric: unused/metric: missing-trace-source/' "$o/seed.yaml"
run alerts --dir "$o"
expect 2 'seed.yaml' 'traces[0].from.metric' 'missing-trace-source'
cp "$work/seed-traces" "$o/seed.yaml"

# A wrong threshold is red, and identifies the rule, source file, incident and value.
cp "$o/alerts/logs/critical.yaml" "$work/logs"
sed -i 's/\$A > 20/\$A > 999999/' "$o/alerts/logs/critical.yaml"
run alerts --dir "$o"
expect 1 'alerts/logs/critical.yaml rule logs window spike expect fire' '999999' 'incident [10m0s,20m0s)'
cp "$work/logs" "$o/alerts/logs/critical.yaml"

# A threshold that fires on the baseline must also fail.
sed -i 's/\$A > 0.5/\$A > -1/' "$o/alerts/metric/critical.yaml"
run alerts --dir "$o"
expect 1 'alerts/metric/critical.yaml rule metric window baseline expect quiet' '-1'
cp "$work/metric" "$o/alerts/metric/critical.yaml"

# A pending interval longer than the incident is not accepted as a successful fire.
sed -i 's/for: 1m/for: 100h/' "$o/alerts/metric/critical.yaml"
run alerts --dir "$o"
expect 1 'alerts/metric/critical.yaml rule metric window spike expect fire' '100h'
cp "$work/metric" "$o/alerts/metric/critical.yaml"

# Seed events must be in the contract before a metric or log engine starts.
cp "$o/seed.yaml" "$work/seed"
sed -i 's/fixture.failed/fixture.faile/' "$o/seed.yaml"
run alerts --dir "$o"
expect 1 'seed.yaml' 'fixture.faile' 'did you mean'
cp "$work/seed" "$o/seed.yaml"

# Static refusal uses the same event AST policy as the evaluator.
sed -i 's/fixture.failed/fixture.faile/' "$o/alerts/logs/critical.yaml"
run query --static --dir "$o"
expect 1 'alerts/logs/critical.yaml' 'rule logs query A' 'fixture.faile' 'did you mean'
cp "$work/logs" "$o/alerts/logs/critical.yaml"

# Absence alerts on a healthy seed are judged like thresholds by query --live.
cat >"$o/alerts/metric/critical.yaml" <<'YAML'
apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata: {name: fixture-absence}
spec:
  rules:
    - uid: metric
      condition: C
      data:
        - refId: A
          datasourceUid: fixture-metrics
          model:
            datasource: {type: prometheus, uid: fixture-metrics}
            expr: absent(fixture_requests_total{outcome="error"})
            instant: true
YAML
# Keep this live-only fixture focused on the absence query. The other fixtures
# include an intentional NoData rule and a recording output made by obs alerts.
for d in "$o/alerts"/*; do
  if [ "$(basename "$d")" != metric ]; then rm -rf "$d"; fi
done
run query --live --dir "$o"
expect 0 'absence check(s)' 'obs query --live: ok'

# Future data is refused, rather than silently dropped by the metric store.
run alerts --dir "$here/fixtures/alerts/features" --base "$(($(date +%s) + 86400))"
expect 2 'time line ends in the future'

bash "$here/alerts-verify-prune.sh" "$OBS"
echo 'obs alerts tests: baseline and 7 planted failures passed'
