#!/usr/bin/env bash
# Flat alert tier files (the platform standard's alerts/<slug>/<severity>.yaml: one flat YAML map).
# obs reads them as the rule the standard's chart renders, in every command that reads alerts, and still
# reads the GrafanaAlertRuleGroup CR. Baseline green (fixtures/ok-flat-alert) + each planted failure red,
# naming the file, the rule id and the bad token.
set -euo pipefail
OBS=$1
here="$(cd "$(dirname "$0")" && pwd)"
fx="$here/fixtures"
work="$(mktemp -d)"
trap 'rm -rf "${work:?}"' EXIT
base=1700000000
seed="$fx/shared-seed.yaml"
run() {
  set +e
  "$OBS" "$@" >"$work/out" 2>&1
  rc=$?
  set -e
}
expect() {
  local label=$1 want=$2
  shift 2
  if [ "$rc" -ne "$want" ]; then
    cat "$work/out"
    echo "flat: $label: exit $rc wanted $want"
    exit 1
  fi
  for token in "$@"; do
    if ! grep -qF -- "$token" "$work/out"; then
      cat "$work/out"
      echo "flat: $label: missing $token"
      exit 1
    fi
  done
  echo "PASS flat: $label"
}
# fresh <name>: a writable copy of the flat fixture's observability folder.
fresh() {
  rm -rf "${work:?}/$1"
  mkdir -p "$work/$1"
  cp -R "$fx/ok-flat-alert/observability" "$work/$1/observability"
  chmod -R u+w "$work/$1"
}
crit=alerts/charge-errors/critical.yaml
logs=alerts/error-logs/warning.yaml

# ---- baseline: a flat metrics tier and a flat logs tier are green in every reading command ---------
dir="$fx/ok-flat-alert/observability"
run query --static --dir "$dir" --seed "$seed" --base "$base"
expect "query --static reads flat tier files (2 queries, 0 failures)" 0 "obs query --static: ok: 2 queries (1 logs, 1 metrics), 0 failures" \
  "alerts/error-logs/warning.yaml rule payments-checkout-error-logs-warning query A"
run query --live --dir "$dir" --seed "$seed" --base "$base"
expect "query --live runs both flat queries on the seed" 0 "obs query --live: ok"
run check --dir "$dir"
expect "check reads flat tier files" 0 "obs check: ok" "queries: 2 queries"
run alerts --dir "$dir" --seed "$seed" --base "$base"
# the engines evaluated the rendered rules: rule name = <platform>-<service>-<slug>-<severity>; the exact rule is asserted in internal/files/flat_test.go
expect "alerts evaluates the rendered rules by their uid" 0 "ok alerts/charge-errors/critical.yaml rule payments-checkout-charge-errors-critical window baseline expect quiet: quiet" \
  "ok alerts/error-logs/warning.yaml rule payments-checkout-error-logs-warning window baseline expect quiet: quiet" "obs alerts: alerts=2" "failed=0"

# ---- the CR form of the same rule still reads (no change for a repo that renders its own CR) ---------
fresh cr
rm "${work:?}/cr/observability/$crit"
cat >"$work/cr/observability/$crit" <<'CR'
apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata:
  name: payments-checkout-charge-errors-critical
spec:
  folderRef: payments-checkout-folder
  interval: 1m
  rules:
    - uid: payments-checkout-charge-errors-critical
      title: 🚨 Charge errors high
      condition: B
      for: 5m
      noDataState: OK
      execErrState: Error
      data:
        - refId: A
          relativeTimeRange: { from: 600, to: 0 }
          datasourceUid: metrics
          model: { expr: 'sum(rate(checkout_charges_total{outcome="error"}[5m])) > 0.01', instant: true }
        - refId: B
          datasourceUid: __expr__
          model: { type: math, expression: 'is_number($A) || is_nan($A) || is_inf($A)' }
      labels: { severity: critical }
CR
run query --static --dir "$work/cr/observability" --seed "$seed" --base "$base"
expect "a CR file beside a flat file still reads" 0 "obs query --static: ok: 2 queries"

# ---- planted failures: each red, naming the file, the rule id and the bad token -------------------
plant() { # plant <name> <file> <from> <to> <rule> <token>
  fresh "$1"
  sed -i "s|$3|$4|" "$work/$1/observability/$2"
  if cmp -s "$fx/ok-flat-alert/observability/$2" "$work/$1/observability/$2"; then
    echo "flat: $1: the planted edit changed nothing"
    exit 1
  fi
  run query --static --dir "$work/$1/observability" --seed "$seed" --base "$base"
  expect "$1: red, names file, rule and token" 1 "FAIL $2" "[$5]" "$6" ": FAILED:"
}
plant no-expr "$crit" "^expr: .*" "" flat-alert-field-missing '"expr"'
plant bad-datasource "$crit" "^datasource: metrics" "datasource: prometheus" flat-alert-datasource '"prometheus"'
plant long-title "$crit" "^title: .*" "title: A very long title that goes well past forty-eight characters" flat-alert-title-length "characters; the notification title may hold at most 48"
plant landscape "$crit" 'outcome="error"' 'landscape="raichu"' flat-alert-landscape "landscape"
plant panel-word "$crit" "^panel: 2" "panel: overview" flat-alert-panel "overview"
plant bad-for "$crit" "^for: 5m" "for: soon" flat-alert-for '"soon"'
plant bool-modifier "$crit" "> 1000" "> bool 1000" flat-alert-bool "bool"
plant title-emoji "$crit" "^title: .*" "title: 🚨 Charge errors high" flat-alert-title-emoji "🚨"
plant panel-null "$crit" "^panel: 2" "panel: null" flat-alert-panel "null"
plant landscape-logs "$logs" 'severity_text:ERROR' 'landscape:raichu' flat-alert-landscape "landscape"
plant no-summary "$logs" "^summary: .*" "" flat-alert-field-missing '"summary"'
fresh sev-field
echo "severity: critical" >>"$work/sev-field/observability/$crit"
run query --static --dir "$work/sev-field/observability" --seed "$seed" --base "$base"
expect "severity field: red, the file name is the severity" 1 "FAIL $crit" "[flat-alert-field-unknown]" "no severity field"
fresh badname
mv "$work/badname/observability/$crit" "$work/badname/observability/alerts/charge-errors/high.yaml"
run query --static --dir "$work/badname/observability" --seed "$seed" --base "$base"
expect "file name that is not a severity: red" 1 "FAIL alerts/charge-errors/high.yaml" "[flat-alert-file-name]"
fresh noplatform
rm "${work:?}/noplatform/observability/obs.yaml"
run query --static --dir "$work/noplatform/observability" --seed "$seed" --base "$base"
expect "no platform: red, says where to write it" 1 "[flat-alert-identity]" "platform:" "obs.yaml"
fresh longuid
mv "$work/longuid/observability/alerts/charge-errors" "$work/longuid/observability/alerts/a-slug-that-is-too-long-for-a-uid"
run query --static --dir "$work/longuid/observability" --seed "$seed" --base "$base"
expect "uid over 40 characters: red" 1 "[flat-alert-uid-length]" "payments-checkout-a-slug-that-is-too-long-for-a-uid-critical"
# ---- where platform comes from in the two layouts ---------------------------------------------------
# (i) observability/<feature>/ reads the nearest enclosing observability/obs.yaml: platform only in the top file.
rm -rf "${work:?}/lay1"
mkdir -p "$work/lay1/.git" "$work/lay1/observability/checkout"
echo ref >"$work/lay1/.git/HEAD"
echo "platform: payments" >"$work/lay1/observability/obs.yaml"
cp "$fx/ok-flat-alert/observability/contract.yaml" "$work/lay1/observability/checkout/contract.yaml"
cp -R "$fx/ok-flat-alert/observability/alerts" "$work/lay1/observability/checkout/alerts"
run query --static --dir "$work/lay1/observability/checkout" --seed "$seed" --base "$base"
expect "layout (i): platform from the top observability/obs.yaml" 0 "obs query --static: ok: 2 queries" \
  "rule payments-checkout-error-logs-warning query A"
# (ii) <feature>/observability/ never falls back: no platform there is a finding that says where to write it.
rm -rf "${work:?}/lay2"
mkdir -p "$work/lay2/.git" "$work/lay2/observability" "$work/lay2/Checkout/observability"
echo ref >"$work/lay2/.git/HEAD"
echo "platform: payments" >"$work/lay2/observability/obs.yaml" # a decoy above: never read
cp "$fx/ok-flat-alert/observability/contract.yaml" "$work/lay2/Checkout/observability/contract.yaml"
cp -R "$fx/ok-flat-alert/observability/alerts" "$work/lay2/Checkout/observability/alerts"
run query --static --dir "$work/lay2/Checkout/observability" --seed "$seed" --base "$base"
expect "layout (ii): no fallback, flat-alert-identity names where to write platform" 1 "FAIL $crit" "[flat-alert-identity]" \
  "$(platform:) in <observability>/obs.yaml" ": FAILED:"
echo "platform: payments" >"$work/lay2/Checkout/observability/obs.yaml"
run query --static --dir "$work/lay2/Checkout/observability" --seed "$seed" --base "$base"
expect "layout (ii): platform written in the feature's own obs.yaml" 0 "obs query --static: ok: 2 queries"

# for: 1d is a duration for every command: the flat check and obs alerts judge it the same way
fresh for1d
sed -i "s|^for: 5m|for: 1d|" "$work/for1d/observability/$crit"
run query --static --dir "$work/for1d/observability" --seed "$seed" --base "$base"
expect "for: 1d passes the flat file check" 0 "obs query --static: ok"
run alerts --dir "$work/for1d/observability" --seed "$seed" --base "$base"
expect "for: 1d is evaluated by obs alerts" 0 "obs alerts: alerts=2" "failed=0"
# the other reading commands refuse the same file, with the rule id
run check --dir "$work/no-expr/observability"
expect "check: a bad flat file is red" 1 "[flat-alert-field-missing]" "obs check: FAILED"
run alerts --dir "$work/no-expr/observability" --seed "$seed" --base "$base"
expect "alerts: a bad flat file stops it and names the file and rule" 2 "$crit" "[flat-alert-field-missing]"
