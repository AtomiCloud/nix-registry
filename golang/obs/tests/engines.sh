#!/usr/bin/env bash
# obs engines --check-chart: a chart that matches the pins is green; a drifted chart is red and
# names file, key, chart value and pin; a folder with no engine is an input error (exit 2).
set -euo pipefail
OBS=$1
here="$(cd "$(dirname "$0")" && pwd)"
fx="$here/fixtures/engines"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
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
    echo "engines: exit $rc wanted $want"
    exit 1
  fi
  for token in "$@"; do
    if ! grep -qF -- "$token" "$work/out"; then
      cat "$work/out"
      echo "engines: missing $token"
      exit 1
    fi
  done
}

run engines --check-chart "$fx/matching"
expect 0 'obs engines --check-chart: ok: 6 match(es)' 'Pyroscope 2.4.3'

# The chart fixture must equal the pins: a pin bump without the chart is red here too.
run engines --check-chart "$fx/drifted"
expect 1 'values.yaml:' 'tempo.tempo.tag' 'chart 3.1.0' 'Tempo 3.1.1' 'pyroscope.image' 'chart 2.4.2' 'Pyroscope 2.4.3' '[engine-pin-drift]' '2 drift(s)'

# A pin that moves while the chart stays: plant it in a copy of the matching chart.
cp -R "$fx/matching" "$work/c"
chmod -R u+w "$work/c"
sed -i 's/tag: 13.3.0/tag: 13.2.0/' "$work/c/values.yaml"
run engines --check-chart "$work/c"
expect 1 'values.yaml:' 'grafana.image.tag' 'chart 13.2.0' 'Grafana 13.3.0'

# A chart dependency version is a chart version, never an engine version.
cp "$fx/matching/values.yaml" "$work/c/values.yaml"
sed -i 's/version: 9.9.9/version: 1.0.0/' "$work/c/Chart.yaml"
run engines --check-chart "$work/c"
expect 0 'ok:'

run engines --check-chart "$fx/none"
expect 2 'no engine found'
run engines --check-chart "$work/missing"
expect 2 'missing'
run engines
expect 2 '--check-chart'
echo 'obs engines tests: matching green, drifted red, no-engine exit 2'
