#!/usr/bin/env bash
# obs.yaml: the versions and thresholds file. A matching file is green (exit 0); each
# mismatched store version is red (exit 1) and names the obs.yaml key, the version wanted and
# the version embedded; an unknown key is an input error (exit 2) naming file, line and key;
# a missing file is a note and exit 0; the look sandbox refuses a version with no known image
# digest. The same loader serves obs check, obs query and obs alerts.
set -euo pipefail
OBS=$1
here="$(cd "$(dirname "$0")" && pwd)"
fx="$here/fixtures"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
base=$(($(date +%s) - 86400))
run() {
  set +e
  "$OBS" "$@" >"$work/out" 2>&1
  rc=$?
  set -e
}
expect() {
  local want=$1
  shift
  if [ "$rc" -ne "$want" ]; then
    cat "$work/out"
    echo "obs.yaml: exit $rc wanted $want"
    exit 1
  fi
  for token in "$@"; do
    if ! grep -qF -- "$token" "$work/out"; then
      cat "$work/out"
      echo "obs.yaml: missing $token"
      exit 1
    fi
  done
}
absent() {
  if grep -qF -- "$1" "$work/out"; then
    cat "$work/out"
    echo "obs.yaml: must be absent: $1"
    exit 1
  fi
}
# fresh <name>: a writable copy of the good fixture.
fresh() {
  rm -rf "${work:?}/$1"
  mkdir -p "$work/$1"
  cp -R "$fx/good/observability" "$work/$1/observability"
  chmod -R u+w "$work/$1"
}

# ---- baseline: the good fixture's obs.yaml matches this obs --------------------------
run check --dir "$fx/good/observability"
expect 0 'evaluators embedded in this obs:' 'MetricsQL' 'VictoriaMetrics v1.156.0' 'LogsQL' 'VictoriaLogs v1.54.0' \
  'TraceQL' 'Tempo 3.1.1' 'Grafana alert engine' 'Grafana 13.3.0' 'dashboard schema' 'Grafana 13.3.0 schemaVersion 42' \
  'Alloy v1.21.3, Pyroscope 2.4.3' 'every version it names matches' 'thresholds applied: logs, metrics, traces, profiles' 'obs check: ok:'
echo "PASS obs.yaml: obs check prints the embedded evaluator versions and a matching file is green"
for cmd in "query --static" "query --live"; do
  # shellcheck disable=SC2086
  run $cmd --dir "$fx/good/observability" --base "$base"
  expect 0 'versions match the evaluators embedded in this obs' 'thresholds applied: logs, metrics, traces, profiles'
done
run alerts --dir "$fx/alerts/features" --base "$((base - 86400))"
expect 0 'obs alerts:' 'versions match the evaluators embedded in this obs'
echo "PASS obs.yaml: obs query and obs alerts load the same file and its versions match"

# ---- each mismatched version is red --------------------------------------------------
while IFS='|' read -r key wanted embedded; do
  fresh m
  case "$key" in
  dashboard_schema) sed -i "s/^  dashboard_schema: 42/  dashboard_schema: $wanted/" "$work/m/observability/obs.yaml" ;;
  *) sed -i "s/^  $key: .*#/  $key: $wanted #/" "$work/m/observability/obs.yaml" ;;
  esac
  grep -q "^  $key: $wanted" "$work/m/observability/obs.yaml" || {
    echo "obs.yaml: planting $key failed"
    exit 1
  }
  run check --dir "$work/m/observability"
  expect 1 "FAIL $work/m/observability/obs.yaml versions.$key: obs.yaml wants $wanted, this obs embeds $embedded" 'obs check: FAILED: 1 version mismatch(es)'
  absent 'obs check: ok:'
  echo "PASS obs.yaml: $key $wanted is red, names the key, wanted $wanted and embedded $embedded"
done <<'LIST'
victoriametrics|v1.157.0|v1.156.0
victorialogs|v1.53.0|v1.54.0
tempo|3.2.0|3.1.1
grafana|13.4.0|13.3.0
dashboard_schema|43|42
LIST

# The same mismatch stops obs query and obs alerts before any work.
fresh m
sed -i 's/^  victorialogs: .*#/  victorialogs: v1.53.0 #/' "$work/m/observability/obs.yaml"
for cmd in "query --static" "query --live" "alerts"; do
  # shellcheck disable=SC2086
  run $cmd --dir "$work/m/observability" --base "$base"
  expect 1 'versions.victorialogs: obs.yaml wants v1.53.0, this obs embeds v1.54.0' "FAILED: 1 version(s) in $work/m/observability/obs.yaml"
  absent 'ok:'
done
echo "PASS obs.yaml: obs query and obs alerts fail on a mismatched version"

# Alloy and Pyroscope have no embedded evaluator: a different version is a note, not a failure.
fresh m
sed -i 's/^  alloy: .*#/  alloy: v1.22.0 #/; s/^  pyroscope: .*#/  pyroscope: 2.5.0 #/' "$work/m/observability/obs.yaml"
run check --dir "$work/m/observability"
expect 0 'note' 'versions.alloy: obs.yaml names v1.22.0, the pin of this obs is v1.21.3' 'versions.pyroscope: obs.yaml names 2.5.0, the pin of this obs is 2.4.3' 'obs check: ok:'
echo "PASS obs.yaml: a different Alloy or Pyroscope version is a note (exit 0)"

# ---- unknown key, bad value: input error (exit 2) naming file, line and key ----------
fresh m
sed -i 's/^    max_query_len: 16KiB/    max_query_lenn: 16KiB/' "$work/m/observability/obs.yaml"
line=$(grep -n 'max_query_lenn' "$work/m/observability/obs.yaml" | cut -d: -f1)
for cmd in "check" "query --static" "query --live" "alerts"; do
  extra="--base $base"
  [ "$cmd" = check ] && extra=""
  # shellcheck disable=SC2086
  run $cmd --dir "$work/m/observability" $extra
  expect 2 "$work/m/observability/obs.yaml:$line: unknown key thresholds.logs.max_query_lenn"
done
echo "PASS obs.yaml: an unknown key is exit 2 and names the file, the line and the key (check, query, alerts)"
fresh m
sed -i 's/^    max_query_duration: 30s/    max_query_duration: soon/' "$work/m/observability/obs.yaml"
run check --dir "$work/m/observability"
expect 2 'thresholds.logs.max_query_duration' 'invalid -search.maxQueryDuration'
fresh m
sed -i 's/^  dashboard_schema: 42 .*/  dashboard_schema: forty/' "$work/m/observability/obs.yaml"
run check --dir "$work/m/observability"
expect 2 'versions.dashboard_schema' 'cannot unmarshal'
echo "PASS obs.yaml: a value of the wrong type or form is exit 2 and names the key"

# ---- missing file: a note and the embedded versions, exit 0 --------------------------
fresh m
rm "${work:?}/m/observability/obs.yaml"
run check --dir "$work/m/observability"
expect 0 'evaluators embedded in this obs:' 'MetricsQL' "note: $work/m/observability/obs.yaml not found" 'obs check: ok:'
for cmd in "query --static" "query --live"; do
  # shellcheck disable=SC2086
  run $cmd --dir "$work/m/observability" --base "$base"
  expect 0 ': ok:'
  absent 'obs.yaml'
done
echo "PASS obs.yaml: no file is a note plus the embedded versions (check) and nothing new (query), exit 0"

# ---- thresholds: obs.yaml sets VictoriaLogs limits; --vl-settings overrides -----------
fresh m
sed -i 's/^    max_query_len: 16KiB/    max_query_len: 10/' "$work/m/observability/obs.yaml"
run query --live --dir "$work/m/observability" --base "$base"
expect 1 'search.maxQueryLen=10 exceeded'
printf 'search.maxQueryLen: 16KiB\n' >"$work/vl.yaml"
run query --live --dir "$work/m/observability" --base "$base" --vl-settings "$work/vl.yaml"
expect 0 'obs query --live: ok:'
echo "PASS obs.yaml: thresholds.logs.max_query_len=10 fails a LogsQL query; --vl-settings overrides it"

# ---- sandbox: exactly the named images, nothing guessed ------------------------------
fresh m
sed -i 's/^  pyroscope: .*#/  pyroscope: 2.4.2 #/' "$work/m/observability/obs.yaml"
run sandbox up --dir "$work/m/observability"
expect 2 "$work/m/observability/obs.yaml: versions.pyroscope: 2.4.2 has no known image digest for grafana/pyroscope (known: 2.4.3)"
run look --dir "$work/m/observability" --file dashboards/overview.json
expect 0 'versions.pyroscope: 2.4.2 has no known image digest'
fresh m
sed -i 's/^  tempo: .*#/  tempo: 3.1.0 #/' "$work/m/observability/obs.yaml"
run sandbox up --dir "$work/m/observability"
expect 2 'versions.tempo: 3.1.0 has no known image digest for grafana/tempo (known: 3.1.1)'
fresh m
sed -i 's/^    max_query_len: 16KiB/    nope: 1/' "$work/m/observability/obs.yaml"
run sandbox up --dir "$work/m/observability"
expect 2 'unknown key thresholds.logs.nope'
echo "PASS obs.yaml: obs sandbox up and obs look refuse a version with no known image digest (exit 2 / look prints it) and a bad file"

# ---- obs version lists the Alloy pin -------------------------------------------------
run version
expect 0 'reference Alloy v1.21.3'
echo "PASS obs.yaml: obs version lists the Alloy pin"
