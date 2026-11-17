#!/usr/bin/env bash
# OBS_VERIFY_PRUNE=1 self-check: lazy pruning must give the same answer as OBS_NO_PRUNE=1.
# Baseline green on the shared fixture, planted red for the union bug (a query-bearing
# pipe reads events the root filter does not name).
set -euo pipefail
OBS=$1
here="$(cd "$(dirname "$0")" && pwd)"
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
    echo "alerts-verify-prune: exit $rc wanted $want"
    exit 1
  fi
  for token in "$@"; do
    if ! grep -qF -- "$token" "$work/out"; then
      cat "$work/out"
      echo "alerts-verify-prune: missing $token"
      exit 1
    fi
  done
}

# One fixed base for the planted runs, in the past, on a minute boundary.
base=$(($(date +%s) / 60 * 60 - 21600))

# Baseline: the shared fixture is the same with and without pruning.
cp -R "$here/fixtures/alerts/features" "$work/ok"
chmod -R u+w "$work/ok"
OBS_VERIFY_PRUNE=1 run alerts --dir "$work/ok" --base "$base"
expect 0 'failed=0' 'verify prune: base='"$base"
grep -qE 'verify prune: .*compared=[1-9][0-9]* .*mismatches=0' "$work/out"

# Planted red (always): the pruned run reads a seed whose error share differs, so the
# two evaluations disagree. The report names alert, run, minute and the rule's query.
sed "s/{ where: { outcome: error }, share: 0 }/{ where: { outcome: error }, share: 1 }/" "$work/ok/seed.yaml" >"$work/seed-planted.yaml"
cmp -s "$work/ok/seed.yaml" "$work/seed-planted.yaml" && {
  echo 'alerts-verify-prune: plant did not change the seed'
  exit 1
}
OBS_VERIFY_PRUNE_PLANT_SEED="$work/seed-planted.yaml" OBS_VERIFY_PRUNE=1 run alerts --dir "$work/ok" --base "$base"
expect 1 'MISMATCH ' 'alert ' 'run baseline' 'minute ' 'rule '
grep -qE 'verify prune: .*mismatches=[1-9][0-9]*' "$work/out"

# Union: a query-bearing pipe reads events the root filter never names (here fixture.ok).
# If lazy loading drops them, the pruned and the full evaluation differ and the
# self-check must be red, naming the rule; if it keeps them, both agree and it is green.
# Either way the self-check's answer must equal what two plain runs show.
cp -R "$here/fixtures/alerts/features" "$work/union"
chmod -R u+w "$work/union"
u="$work/union"
sed -i 's/logs: { Failed: { event: fixture.failed, level: error } }/logs: { Failed: { event: fixture.failed, level: error }, Ok: { event: fixture.ok, level: info } }/' "$u/contract.yaml"
sed -i 's/from: { metric: fixture.requests, where: { outcome: error } } }\]/from: { metric: fixture.requests, where: { outcome: error } } }, { event: fixture.ok, level: info, from: { metric: fixture.requests, where: { outcome: ok } } }]/' "$u/seed.yaml"
sed -i 's#_stream:{"service.name"="checkout"} "durian.cloud/event":=fixture.failed customer_tier:=gold "timeout" | stats count() as hits#"durian.cloud/event":=fixture.failed | union ("durian.cloud/event":=fixture.ok) | stats count() as hits#' "$u/alerts/logs/critical.yaml"
grep -qF 'union ("durian.cloud/event":=fixture.ok)' "$u/alerts/logs/critical.yaml"
grep -qF 'fixture.ok' "$u/seed.yaml"
run alerts --dir "$u" --base "$base" --steps "$work/union-pruned.json"
OBS_NO_PRUNE=1 run alerts --dir "$u" --base "$base" --steps "$work/union-full.json"
OBS_VERIFY_PRUNE=1 run alerts --dir "$u" --base "$base"
if cmp -s "$work/union-pruned.json" "$work/union-full.json"; then
  grep -qE 'verify prune: .*mismatches=0' "$work/out" || {
    cat "$work/out"
    echo 'alerts-verify-prune: union agrees but the self-check reports a mismatch'
    exit 1
  }
  echo 'alerts-verify-prune: union rows kept by lazy loading: pruned == full, green'
else
  expect 1 'MISMATCH steps alert logs' 'run baseline' 'minute ' 'rule logs queries: alerts/logs/critical.yaml rule logs query A' 'union ("durian.cloud/event":=fixture.ok)'
  grep -qE 'verify prune: .*mismatches=[1-9][0-9]*' "$work/out"
  echo 'alerts-verify-prune: union rows dropped by lazy loading: pruned != full, red as required'
fi

echo 'obs alerts verify-prune tests: baseline green, seed plant red, union consistent'
