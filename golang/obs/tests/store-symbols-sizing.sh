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
grep -q '"bucket_gb_per_day": 8.7' "$work/size"
grep -q '"victorialogs_disk_gb_per_day": 8.3' "$work/size"
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
"$obs" store move victoriametrics --config "$fx/vm.yaml" --dry-run >"$work/plan"
grep -q '^4. read known data' "$work/plan"
grep -q 'deployment/vminsert -storageNode += \[vmstorage-b-0.vmstorage-b.observability-fixture.svc.cluster.local:8400 vmstorage-b-1.vmstorage-b.observability-fixture.svc.cluster.local:8400\]' "$work/plan"
grep -q 'deployment/vmselect -storageNode += \[vmstorage-b-0.vmstorage-b.observability-fixture.svc.cluster.local:8401' "$work/plan"
if "$obs" store move victoriametrics --config "$fx/bad-vm.yaml" --dry-run >"$work/bad" 2>&1; then
  echo "FAIL store move accepted a serviceName that is not a headless Service of the manifest"
  exit 1
fi
for token in bad-vm-scaled.yaml statefulset/vmstorage-b serviceName vmstorag-b; do grep -q "$token" "$work/bad"; done
"$obs" symbols decode --store "$work/symbols" --file "$fx/unknown.stack" >"$work/stack"
cmp "$fx/unknown.stack" "$work/stack"
"$obs" symbols put --store "$work/symbols" --file "$fx/symbols.elf" --build-id 00112233445566778899aabbccddeeff --app fixture >"$work/key"
OBS_SYMBOLIZER="$work/missing-llvm" "$obs" symbols decode --store "$work/symbols" --file "$fx/unknown.stack" >"$work/stack" 2>"$work/note"
cmp "$fx/unknown.stack" "$work/stack"
[ "$(grep -c llvm-symbolizer "$work/note")" -eq 1 ]
grep -q 'kept as is' "$work/note"
if "$obs" symbols put --store "$work/symbols" --file "$fx/unknown.stack" --build-id ../bad --app fixture >"$work/bad" 2>&1; then
  echo "FAIL symbols put accepted ../bad"
  exit 1
fi
for token in unknown.stack ../bad; do grep -q "$token" "$work/bad"; done
echo "PASS store/symbols/size: sizing, dry-runs (Tempo plan, VictoriaMetrics derived endpoints), unknown-id green; 4 named planted failures red"
