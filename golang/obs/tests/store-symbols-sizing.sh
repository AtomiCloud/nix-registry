#!/usr/bin/env bash
# Offline CLI proofs: green fixtures and named planted errors. No cluster access.
set -euo pipefail
obs=$1
here=$(cd "$(dirname "$0")" && pwd)
fx="$here/fixtures/store-symbols-sizing"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
"$obs" size --req-per-second 1000 --services 30 --pods 90 --profiled-pods 30 >"$work/size"
grep -q '"traffic_cores": 0.27' "$work/size"
grep -q '"stored_series": 81000' "$work/size"
grep -q 'Loki-era' "$work/size"
if "$obs" size --pods 1 --profiled-pods 2 >"$work/bad" 2>&1; then
  echo "FAIL size accepted profiled_pods=2 with pods=1"
  exit 1
fi
grep -q 'profiled_pods=2' "$work/bad"
"$obs" store move tempo --config "$fx/tempo.yaml" --dry-run >"$work/plan"
grep -q '^5. read known data' "$work/plan"
if "$obs" store move tempo --config "$fx/bad-tempo.yaml" --dry-run >"$work/bad" 2>&1; then
  echo "FAIL store move skipped stop_compaction"
  exit 1
fi
for token in bad-tempo.yaml tempo stop_compaction; do grep -q "$token" "$work/bad"; done
"$obs" symbols decode --store "$work/symbols" --file "$fx/unknown.stack" >"$work/stack"
cmp "$fx/unknown.stack" "$work/stack"
if "$obs" symbols put --store "$work/symbols" --file "$fx/unknown.stack" --build-id ../bad --app fixture >"$work/bad" 2>&1; then
  echo "FAIL symbols put accepted ../bad"
  exit 1
fi
for token in unknown.stack ../bad; do grep -q "$token" "$work/bad"; done
echo "PASS store/symbols/size: sizing, dry-run, unknown-id green; 3 named planted failures red"
