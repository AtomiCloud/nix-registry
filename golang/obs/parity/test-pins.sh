#!/usr/bin/env bash
set -euo pipefail
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
pins="$here/../src/internal/pins/pins.json"
bash "$here/check-pins.sh" "$pins" "$here/images.json"
for mutation in \
  '.VictoriaLogs.version="v0.0.0"' \
  '.VictoriaLogs.commit="badcommit"' \
  '.Grafana.image="grafana/grafana:wrong"' \
  '.Grafana.digest="sha256:wrong"' \
  '.VictoriaLogsDatasource.plugin="victoriametrics-logs-datasource@wrong"' \
  'del(.Tempo)' \
  '.Unknown={version:"1"}'; do
  jq "$mutation" "$here/images.json" >"$scratch/images.json"
  if bash "$here/check-pins.sh" "$pins" "$scratch/images.json" >"$scratch/output" 2>&1; then
    echo "images.json: planted bad pin accepted: $mutation" >&2
    exit 1
  fi
  echo "MEASURED rejected images.json planted drift: $mutation"
done
