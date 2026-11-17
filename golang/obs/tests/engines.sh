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

run engines --check-chart "$fx/digest-ok"
expect 0 'digest sha256:bf6675' 'pin Pyroscope digest'
run engines --check-chart "$fx/digest-drift"
expect 1 'values.yaml:2 pyroscope.image' 'chart digest sha256:0000' 'pin Pyroscope digest sha256:bf6675' '[engine-pin-drift]'
# A digest-only image with no digest recorded for the engine cannot prove the version: red, never a pass.
run engines --check-chart "$fx/digest-only-no-pin" --pins "$fx/pins-pyroscope-no-digest.json"
expect 1 'values.yaml:2 pyroscope.image' 'sha256:0000' 'pinned by digest only; cannot prove version 2.4.3'
run engines --check-chart "$fx/tag-no-repository"
expect 1 'values.yaml:' 'victoria-logs-single.server.image.tag' 'chart v1.50.0' 'VictoriaLogs v1.54.0'
run engines --check-chart "$fx/missing-tag"
expect 1 'values.yaml:' 'grafana.image' 'cannot prove version 13.3.0' '[engine-pin-missing]'
run engines --check-chart "$fx/registry-port"
expect 1 'pyroscope.image' 'chart 2.4.0' 'Pyroscope 2.4.3'

# Proof is per dependency alias and per image: one matched occurrence never hides an unresolved one.
run engines --check-chart "$fx/two-aliases-ok"
expect 0 'ok: 2 match(es)'
run engines --check-chart "$fx/two-aliases-missing"
expect 1 'Chart.yaml:' ' second:' '[engine-pin-missing]' 'cannot prove version 13.3.0'
run engines --check-chart "$fx/cluster-ok"
expect 0 'ok: 3 match(es)'
run engines --check-chart "$fx/cluster-missing"
expect 1 'values.yaml:' 'victoria-metrics-cluster.vmselect.image' '[engine-pin-missing]' 'cannot prove version v1.156.0'
# The upstream tempo chart keeps its image at tempo.tag, so as a dependency it is tempo.tempo.tag.
run engines --check-chart "$fx/tempo-chart-tag-ok"
expect 0 'tempo.tempo.tag' 'ok: 1 match(es)'
run engines --check-chart "$fx/tempo-chart-tag-drift"
expect 1 'values.yaml:3 tempo.tempo.tag' 'chart 3.1.0' 'Tempo 3.1.1'
# Only the dependency's own engine image proves it: a Tempo image in a Grafana sidecar does not hide the missing Grafana version,
# and an empty own image string fails like an empty map.
run engines --check-chart "$fx/foreign-sidecar"
expect 1 'Chart.yaml:' ' grafana:' '[engine-pin-missing]' 'cannot prove version 13.3.0'
run engines --check-chart "$fx/empty-string"
expect 1 'values.yaml:' 'victoria-metrics-cluster.vmselect.image' '[engine-pin-missing]'
# Only the engine's own image keys count: a sidecar or renderer tag is never compared as Grafana.
run engines --check-chart "$fx/sidecar-tag"
expect 0 'ok: 1 match(es)'

run engines --check-chart "$fx/none"
expect 2 'no engine found'
run engines --check-chart "$work/missing"
expect 2 'missing'
run engines
expect 2 '--check-chart'
echo 'obs engines tests: matching green, drifted red, no-engine exit 2'
