#!/usr/bin/env bash
# shellcheck disable=SC2015,SC2016 # `cmd && ok || bad`: ok/bad only echo and count
# Per-feature contracts (obs 0.6): observability/<feature>/ and <feature>/observability/, two features each.
#   features.sh <obs>
# Every claim: the baseline is green and the planted failure is red; the red output names the file, the rule and the token.
set -euo pipefail
OBS=$1
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
base=$(($(date +%s) - 86400))
pass=0
fail=0
ok() {
  echo "PASS $*"
  pass=$((pass + 1))
}
bad() {
  echo "FAIL $*"
  fail=$((fail + 1))
}
rc=0
run() {
  set +e
  "$OBS" "$@" >"$work/out.txt" 2>&1
  rc=$?
  set -e
}
# expect <rc> <token>...: the last run exited with rc and printed every token. verdict <label>: PASS or FAIL.
expect() {
  local want=$1 good=1 t
  shift
  [ "$rc" -eq "$want" ] || {
    good=0
    echo "  exit $rc, want $want"
  }
  for t in "$@"; do
    grep -qF -- "$t" "$work/out.txt" || {
      good=0
      echo "  missing: $t"
    }
  done
  [ "$good" -eq 1 ]
}
absent() {
  local t
  for t in "$@"; do
    if grep -qF -- "$t" "$work/out.txt"; then
      echo "  must be absent: $t"
      return 1
    fi
  done
}
check() { # check <label> <expect args...>: PASS when expect and the absent list hold
  local label=$1
  shift
  if expect "$@"; then ok "$label"; else
    bad "$label"
    sed 's/^/  | /' "$work/out.txt"
  fi
}

# feature <dir> <service> <metric> <alert uid> <reads or "">: one feature's observability folder.
# The metric is a counter <metric> (stored <metric>_total) with one enum label; the alert, the seed and the
# incident use the metric <reads> (a dotted name; default: the feature's own).
feature() {
  local d=$1 svc=$2 metric=$3 uid=$4 reads=$5 read
  [ -n "$reads" ] || reads=$metric
  read=$(printf '%s' "$reads" | tr . _)_total
  mkdir -p "$d/alerts/errors" "$d/incidents"
  cat >"$d/contract.yaml" <<YAML
service: $svc
types: { outcome: { kind: enum, values: [ok, error] } }
metrics: { $metric: { kind: counter, budget: 2, labels: { outcome: outcome } } }
generate:
  - { lang: dotnet, out: Signals, namespace: App.$svc.Signals }
YAML
  cat >"$d/alerts/errors/warning.yaml" <<YAML
apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata: { name: $svc-errors }
spec:
  interval: 1m
  rules:
    - uid: $uid
      title: $svc errors
      condition: C
      for: 1m
      data:
        - refId: A
          datasourceUid: fixture-metrics
          relativeTimeRange: { from: 60, to: 0 }
          model:
            datasource: { type: prometheus, uid: fixture-metrics }
            expr: 'sum(rate($read{outcome="error"}[1m]))'
            instant: true
            range: false
        - { refId: C, datasourceUid: __expr__, model: { type: math, expression: \$A > 0.5 } }
YAML
  cat >"$d/seed.yaml" <<YAML
length: 40m
random_seed: 42
resource: { durian.cloud.landscape: fixture, durian.cloud.platform: fixture, durian.cloud.service: $svc, durian.cloud.module: api }
traffic: { requests: 3/s, shape: flat }
metrics:
  $reads: { type: counter, series: [{ where: { outcome: ok }, share: 1 }, { where: { outcome: error }, share: 0 }] }
incidents: [spike]
YAML
  cat >"$d/incidents/spike.yaml" <<YAML
story: errors burst
window: { start: 10m, duration: 10m }
change: [{ metric: $reads, where: { outcome: error }, rate: 2/s }]
expect: { fire: [$uid], fire_within: 5m, resolve_within: 5m }
YAML
}

# repo <name> <layout i|ii> <uid A> <uid B> <metric B> <B reads, dotted>: a Git repo with two features, nfctag and friend.
repo() {
  local r=$work/$1 layout=$2
  R=$r
  rm -rf "${r:?}"
  mkdir -p "$r/.git"
  case "$layout" in
  i) FA=$r/observability/nfctag FB=$r/observability/friend ;;
  ii) FA=$r/App/Modules/NfcTag/observability FB=$r/App/Modules/Friend/observability ;;
  esac
  feature "$FA" nfctag nfctag.scans "$3" ""
  feature "$FB" friend "$5" "$4" "$6"

}

for layout in i ii; do
  case "$layout" in
  i) label="observability/<feature>/" ;;
  ii) label="<feature>/observability/" ;;
  esac
  # ---- baseline: each feature on its own ------------------------------------------------
  repo "base-$layout" "$layout" nfctag-errors friend-errors friend.invites ""
  run check --dir "$FA"
  check "[$label] obs check on one feature is green" 0 'obs check: ok' 'metric nfctag.scans'
  # ---- the other contract is read -------------------------------------------------------
  run check --dir "$FA"
  check "[$label] obs check sees the other feature's contract" 0 'metric friend.invites' '(service friend, read with this one)'
  # ---- a metric both declare ------------------------------------------------------------
  repo "clash-$layout" "$layout" nfctag-errors friend-errors nfctag.scans ""
  run check --dir "$FB"
  check "[$label] a metric both features declare is [metric-clash] naming both files" 1 '[metric-clash]' "${FA#"$R"/}/contract.yaml" "${FB#"$R"/}/contract.yaml" 'nfctag_scans_total'
  # ---- an alert of B reads A's metric ---------------------------------------------------
  repo "cross-$layout" "$layout" nfctag-errors friend-errors friend.invites nfctag.scans
  run check --dir "$FB"
  check "[$label] an alert in feature B reading feature A's metric passes obs check" 0 'obs check: ok'
  absent '[contract-undeclared-metric]' || bad "[$label] cross read printed undeclared-metric"
  # ---- obs gen, gen --check, alerts, test, per feature ----------------------------------
  for f in "$FA" "$FB"; do
    run gen --dir "$f" --lang dotnet
    check "[$label] obs gen $(basename "$(dirname "$f")")/$(basename "$f")" 0
    run gen --check --dir "$f" --lang dotnet
    check "[$label] obs gen --check $(basename "$(dirname "$f")")/$(basename "$f")" 0 'obs gen --check: ok'
    run alerts --dir "$f" --base "$base"
    check "[$label] obs alerts $(basename "$(dirname "$f")")/$(basename "$f")" 0
    run test --dir "$f" --base "$base"
    check "[$label] obs test $(basename "$(dirname "$f")")/$(basename "$f")" 0 'obs test: ok'
  done
  # ---- a rule uid both features ship (Grafana keeps one) ------------------------------------
  repo "ids-$layout" "$layout" errors-warning errors-warning friend.invites ""
  rel_a=${FA#"$R"/}/alerts/errors/warning.yaml
  rel_b=${FB#"$R"/}/alerts/errors/warning.yaml
  run check --dir "$FB"
  check "[$label] obs check: one rule uid in two features is red and names both files" 1 '[rule-id-clash]' "$rel_a" "$rel_b" '"errors-warning"' 'alert rule id clashes across the repository'
  run check --dir "$FA"
  check "[$label] obs check from the other feature reports the same clash" 1 '[rule-id-clash]' "$rel_a" "$rel_b"
  run check --dir "$FB" --contracts none
  check "[$label] obs check --contracts none reads this folder only: no clash" 0 'obs check: ok'
  run alerts --dir "$FB" --base "$base"
  check "[$label] obs alerts names both files of a rule uid clash" 2 '[rule-id-clash]' "$rel_a" "$rel_b"
  run test --dir "$FB" --base "$base"
  check "[$label] obs test names both files of a rule uid clash" 2 '[rule-id-clash]' "$rel_a" "$rel_b"
  run test --dir "$FB" --base "$base" --contracts none
  check "[$label] obs test --contracts none reads this folder only: no rule uid clash" 0 'obs test: ok'
  repo "ids-ok-$layout" "$layout" nfctag-errors friend-errors friend.invites ""
  run alerts --dir "$FB" --base "$base"
  check "[$label] distinct rule uids: obs alerts is green" 0
  # ---- service totals ------------------------------------------------------------------------
  repo "svc-$layout" "$layout" nfctag-errors friend-errors friend.invites ""
  run check --dir "$FA"
  check "[$label] two services: the per-contract lines stay" 0 'metrics total: worst case 2 series per landscape' 'metrics total (friend): worst case 2 series per landscape'
  absent 'service total' || bad "[$label] two services printed a service total"
  sed -i 's/^service: friend/service: nfctag/' "$FB/contract.yaml"
  sed -i '1a modules: [nfctag]' "$FA/contract.yaml"
  sed -i '1a modules: [friend]' "$FB/contract.yaml"
  run check --dir "$FA" --landscapes 3
  check "[$label] two contracts of one service: the service total is printed (series 2+2, streams 3 landscapes x 2 modules)" 0 "service total (nfctag, 2 contracts: ${FA#"$R"/}/contract.yaml, ${FB#"$R"/}/contract.yaml): worst case 4 series per landscape; log streams worst case 6" 'metrics total (nfctag): worst case 2 series per landscape'
done

# ---- obs.yaml: a feature folder reads its own, else the nearest enclosing observability folder's -------------
repo "oy" i nfctag-errors friend-errors friend.invites ""
printf 'versions:\n  victoriametrics: v1.156.0\n' >"$R/observability/obs.yaml"
run check --dir "$FA"
check "obs.yaml: observability/<feature> with none of its own reads observability/obs.yaml and says so" 0 "obs.yaml $R/observability/obs.yaml: every version it names matches (the enclosing observability folder" "not found: using $R/observability/obs.yaml, the enclosing observability folder"
printf 'versions:\n  victoriametrics: v1.150.0\n' >"$R/observability/obs.yaml"
run check --dir "$FA"
check "obs.yaml: a wrong version in the enclosing obs.yaml is red and names that file" 1 "FAIL $R/observability/obs.yaml" 'v1.150.0'
printf 'versions:\n  victoriametrics: v1.156.0\n' >"$FA/obs.yaml"
run check --dir "$FA"
check "obs.yaml: the feature's own obs.yaml wins over the enclosing one" 0 "obs.yaml $FA/obs.yaml: every version it names matches"
absent 'enclosing observability folder' || bad "obs.yaml: own file printed the enclosing note"
rm -f "$FA/obs.yaml" "$R/observability/obs.yaml"
run check --dir "$FA"
check "obs.yaml: none anywhere: the built-in values, as in 0.5" 0 'not found: nothing to compare with the versions above; this obs uses its built-in versions and thresholds'
repo "oy2" ii nfctag-errors friend-errors friend.invites ""
run check --dir "$FA"
check "obs.yaml: <feature>/observability with none reads the built-in values" 0 'not found: nothing to compare with the versions above'

# ---- discovery depth and test folders ----------------------------------------------------------
repo "deep" i nfctag-errors friend-errors friend.invites ""
feature "$R/observability/billing/payments" payments payments.charged billing-errors ""
run check --dir "$FA"
check "discovery: a contract two folders below observability/ is found" 0 'metric payments.charged' '(service payments, read with this one)'
feature "$R/observability/fixtures/ignored" ignored ignored.thing ignored-errors ""
feature "$R/observability/testdata/ignored" ignored2 ignored2.thing ignored-errors ""
run check --dir "$FA"
check "discovery: a contract under fixtures or testdata is not part of the repository" 0 'obs check: ok'
absent 'ignored.thing' 'ignored2.thing' '[rule-id-clash]' || bad "discovery: a fixtures contract was read"
mkdir -p "$R/docs/elsewhere"
feature "$R/docs/elsewhere" docs docs.thing docs-errors ""
run check --dir "$FA"
check "discovery: a contract outside any observability folder is not read" 0 'obs check: ok'
absent 'docs.thing' || bad "discovery: a contract outside observability/ was read"
run check --dir "$R/observability"
check "discovery: --dir observability (no contract of its own) still runs" 0 'contract.yaml not found'

echo "features.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
