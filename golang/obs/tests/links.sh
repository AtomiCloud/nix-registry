#!/usr/bin/env bash
# Offline TraceQL and cross-signal links: baseline green, planted failures red.
set -euo pipefail
obs=$1
if [ -n "${2:-}" ]; then
  test -s "$2/share/licenses/obs/github.com/grafana/tempo/LICENSE"
fi
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp -R "$here/fixtures/links/good" "$work/observability"
chmod -R u+w "$work"
dir="$work/observability"
"$obs" query --static --dir "$dir" --names
"$obs" query --live --dir "$dir" --names
"$obs" links --dir "$dir"

red() {
  local rc=0
  "$@" >"$work/out" 2>&1 || rc=$?
  if [ "$rc" -ne 1 ]; then
    cat "$work/out"
    echo "expected check failure (exit 1), got $rc"
    exit 1
  fi
}
has() { grep -Fq "$1" "$work/out" || {
  cat "$work/out"
  echo "missing: $1"
  exit 1
}; }
changed() {
  if cmp -s "$1" "$2"; then
    echo "planted mutation changed nothing: $2"
    exit 1
  fi
}

cp "$dir/dashboards/traces.json" "$work/traces.json"
sed 's/span.provider=~/span.planted=)/' "$work/traces.json" >"$dir/dashboards/traces.json"
changed "$work/traces.json" "$dir/dashboards/traces.json"
red "$obs" query --static --dir "$dir"
has 'dashboards/traces.json'
has 'panel 7'
has 'traceql-syntax'
has 'planted'
sed 's/span.provider/span.drifted/g' "$work/traces.json" >"$dir/dashboards/traces.json"
changed "$work/traces.json" "$dir/dashboards/traces.json"
red "$obs" query --live --dir "$dir"
has 'dashboards/traces.json'
has 'panel 7'
has 'attribute span.drifted'
cp "$work/traces.json" "$dir/dashboards/traces.json"

# A Tempo alert query runs in --live (it is not counted and skipped): a baseline that runs, a filter
# that matches no seeded span, and a by() on an attribute the seed lacks.
cp "$dir/alerts/trace-rate/warning.yaml" "$work/trace-rate.yaml"
sed 's/span.provider = "stripe"/span.provider = "nobody"/' "$work/trace-rate.yaml" >"$dir/alerts/trace-rate/warning.yaml"
changed "$work/trace-rate.yaml" "$dir/alerts/trace-rate/warning.yaml"
red "$obs" query --live --dir "$dir"
has 'alerts/trace-rate/warning.yaml'
has 'rule trace-rate-warning query A'
has '[no-rows]'
has 'span.provider = "nobody"'
sed 's/| rate()/| rate() by (span.drifted)/' "$work/trace-rate.yaml" >"$dir/alerts/trace-rate/warning.yaml"
changed "$work/trace-rate.yaml" "$dir/alerts/trace-rate/warning.yaml"
red "$obs" query --live --dir "$dir"
has 'alerts/trace-rate/warning.yaml'
has 'rule trace-rate-warning query A'
has 'attribute span.drifted'
cp "$work/trace-rate.yaml" "$dir/alerts/trace-rate/warning.yaml"

cp "$dir/seed.yaml" "$work/seed.yaml"
sed 's/event: charged, from: {/event: charged, from: { where: { provider: missing },/' "$work/seed.yaml" >"$dir/seed.yaml"
changed "$work/seed.yaml" "$dir/seed.yaml"
red "$obs" links --dir "$dir"
has 'seed.yaml'
has 'POST /charge'
has '[trace-to-logs]'
has 'trace_id='
cp "$work/seed.yaml" "$dir/seed.yaml"

sed 's/attributes: {/attributes: {provider: drifted, /' "$work/seed.yaml" >"$dir/seed.yaml"
changed "$work/seed.yaml" "$dir/seed.yaml"
red "$obs" links --dir "$dir"
has 'seed.yaml'
has '[metric-to-traces]'
has 'provider'
has 'drifted'
cp "$work/seed.yaml" "$dir/seed.yaml"

sed '/k8s.pod.name:/d' "$work/seed.yaml" >"$dir/seed.yaml"
changed "$work/seed.yaml" "$dir/seed.yaml"
red "$obs" links --dir "$dir"
has 'seed.yaml'
has 'POST /charge'
has '[span-to-profile]'
has 'k8s.pod.name'
echo 'TraceQL/links: 3 baselines green (one with a Tempo alert run live); 7 planted failures red'
