#!/usr/bin/env bash
# shellcheck disable=SC2015 # `cmd && ok || bad`: ok/bad only echo and count
# End-to-end test of `obs assert` (concept section 11.4).
#   assert.sh <obs binary> <fixtures/assert dir>
# Green: series within the contract's worst case (both export forms). Red: every planted hole
# fails naming the series file, the metric, the rule and the bad token.
set -euo pipefail
OBS="$1"
fx="$2"
work="$(mktemp -d)"
pass=0
fail=0
ok() {
  echo "PASS assert: $*"
  pass=$((pass + 1))
}
bad() {
  echo "FAIL assert: $*"
  fail=$((fail + 1))
}
run() {
  set +e
  "$OBS" assert --dir "$fx/observability" "$@" >"$work/out.txt" 2>&1
  rc=$?
  set -e
}
expect() {
  local want=$1 what=$2 miss=""
  shift 2
  [ "$rc" -eq "$want" ] || miss="exit $rc, want $want;"
  for n in "$@"; do grep -qF -- "$n" "$work/out.txt" || miss="$miss missing '$n';"; done
  if [ -z "$miss" ]; then ok "$what"; else
    bad "$what: $miss"
    sed 's/^/  | /' "$work/out.txt"
  fi
}
s="$fx/series"

run --series "$s/series-ok.json" --pods 2
expect 0 "/api/v1/series within the worst case (counter 6, histogram 3 x 14 = 42, per-pod gauge 2 pods, platform metric)" \
  "metric checkout.charges [landscape lab]: 6 series, worst case 6: ok" "metric checkout.charge.duration [landscape lab]: 42 series, worst case 42: ok" \
  "metric checkout.queue.depth [landscape lab]: 4 series, worst case 4: ok" "platform metric" "obs assert: ok:"
run --series "$s/export-ok.jsonl" --pods 2
expect 0 "/api/v1/export lines: same answer" "obs assert: ok:" "6 series, worst case 6: ok"
run --series "$s/series-two-landscapes.json"
expect 0 "two landscapes are counted one by one (6 + 6 is fine, each has worst case 6)" "[landscape prod]: 6 series, worst case 6: ok"
run --series "$s/series-unknown-metric.json"
expect 0 "a metric outside the contract is a warning by default" "warn $s/series-unknown-metric.json metric checkout_secret_total: [metric-not-in-contract]"

run --series "$s/series-bad-value.json"
expect 1 "RED: a provider outside the enum makes 9 series; names file, metric, rule, count and the bad value" \
  "FAIL $s/series-bad-value.json metric checkout.charges: [series-over-worst-case] 9 series" "worst case of 6" '[label-value-outside-type]' '"paypal"'
run --series "$s/export-bad-value.jsonl"
expect 1 "RED: the same hole in /api/v1/export lines" "[series-over-worst-case] 9 series" '"paypal"'
run --series "$s/series-extra-label.json"
expect 1 "RED: a raw call added user_id: names the label" "[series-over-worst-case] 9 series" '[label-not-in-contract] label "user_id"'
run --series "$s/series-pod-label.json"
expect 1 "RED: a pod label left on a business metric: names the pod key" "[series-over-worst-case] 12 series" '[pod-label-on-business-metric] label "k8s_pod_name"'
run --series "$s/series-extra-bucket.json"
expect 1 "RED: a 13th histogram bucket (45 > 42) is counted against the 14-series worst case" "metric checkout.charge.duration: [series-over-worst-case] 45 series" "worst case of 42"
run --series "$s/series-gauge-pods.json"
expect 1 "RED: a per-pod gauge on 2 pods fails with --pods 1 (the pod count comes from the landscape config)" "metric checkout.queue.depth: [series-over-worst-case] 4 series" "x 1 pods"
run --series "$s/series-unknown-metric.json" --unknown fail
expect 1 "RED: --unknown fail turns the unknown metric into a failure" "FAIL $s/series-unknown-metric.json metric checkout_secret_total" "[metric-not-in-contract]"

run --series "$s/not-a-series.json"
expect 2 "input error: not a series export names the file" "not-a-series.json" "neither"
run --series "$s/failed-response.json"
expect 2 "input error: an export that is a failed response" "failed-response.json" "failed response"
run --series "$s/missing.json"
expect 2 "input error: missing file" "missing.json"
run
expect 2 "usage error: no --series" "give --series"
set +e
"$OBS" assert --dir "$work/nowhere" --series "$s/series-ok.json" >"$work/out.txt" 2>&1
rc=$?
set -e
expect 2 "input error: no contract in --dir" "contract.yaml"

echo "assert tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
