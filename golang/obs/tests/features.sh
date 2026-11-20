#!/usr/bin/env bash
# shellcheck disable=SC2015,SC2016 # `cmd && ok || bad`: ok/bad only echo and count
# Per-feature contracts (obs 0.6): observability/<feature>/ and <feature>/observability/, two features each.
#   features.sh <obs>
# Every claim: the baseline is green and the planted failure is red; the red output names the file, the rule and the token.
set -euo pipefail
OBS=$1
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
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
  # a clash between A and B fails the repository gate (obs check, from any folder) but not a third folder's own runs
  case "$layout" in i) FC=$R/observability/billing ;; ii) FC=$R/App/Modules/Billing/observability ;; esac
  feature "$FC" billing billing.charged billing-errors ""
  run check --dir "$FC"
  check "[$label] obs check is the repository gate: a clash between two other features fails it from a third" 1 '[rule-id-clash]' "$rel_a" "$rel_b" 'alert rule id clashes across the repository'
  run alerts --dir "$FC" --base "$base"
  check "[$label] obs alerts judges its own folder: a clash between A and B does not fail C" 0
  absent '[rule-id-clash]' || bad "[$label] obs alerts on C printed a clash it is not part of"
  run test --dir "$FC" --base "$base"
  check "[$label] obs test judges its own folder: a clash between A and B does not fail C" 0 'obs test: ok'
  run alerts --dir "$FA" --base "$base"
  check "[$label] obs alerts on a folder in the clash fails" 2 '[rule-id-clash]' "$rel_a" "$rel_b"
  run test --dir "$FA" --base "$base"
  check "[$label] obs test on a folder in the clash fails" 2 '[rule-id-clash]' "$rel_a" "$rel_b"
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

# ---- layout (i): the top observability/ folder has no contract.yaml; its alerts read the features' metrics --------------
rootalert() { # rootalert <expr>: a service-wide alert in observability/alerts/overview
  mkdir -p "$R/observability/alerts/overview"
  cat >"$R/observability/alerts/overview/warning.yaml" <<YAML
apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata: { name: overview }
spec:
  interval: 1m
  rules:
    - uid: overview-errors
      title: overview errors
      condition: C
      for: 1m
      data:
        - refId: A
          datasourceUid: fixture-metrics
          relativeTimeRange: { from: 60, to: 0 }
          model:
            datasource: { type: prometheus, uid: fixture-metrics }
            expr: '$1'
            instant: true
            range: false
        - { refId: C, datasourceUid: __expr__, model: { type: math, expression: \$A > 0.5 } }
YAML
}
repo "root" i nfctag-errors friend-errors friend.invites ""
rootalert 'sum(rate(nfctag_scans_total{outcome="error"}[1m])) + sum(rate(friend_invites_total{outcome="error"}[1m]))'
run check --dir "$R/observability"
check "root folder with no contract: an alert reading two features' metrics passes obs check" 0 'obs check: ok' 'contract.yaml not found'
run query --static --dir "$R/observability"
check "root folder with no contract: ... and obs query --static" 0 'obs query --static: ok'
rootalert 'sum(rate(nfctag_scan_total{outcome="error"}[1m]))'
run check --dir "$R/observability"
check "root folder with no contract: a misspelt feature metric is [contract-undeclared-metric]" 1 '[contract-undeclared-metric]' 'nfctag_scan_total' 'alerts/overview/warning.yaml'
run query --static --dir "$R/observability"
check "root folder with no contract: ... in obs query --static too" 1 '[contract-undeclared-metric]' 'nfctag_scan_total'
run check --dir "$R/observability" --contracts none
check "root folder with no contract and --contracts none: no names are read (as in 0.5)" 0 'obs check: ok'
rootalert 'sum(rate(nfctag_scans_total{outcome="wrong"}[1m]))'
run check --dir "$R/observability"
check "root folder with no contract: a label value outside the feature contract's type is red" 1 'wrong'
# a repository with no contract anywhere keeps 0.5: nothing is read
rm -f "$FA/contract.yaml" "$FB/contract.yaml"
rootalert 'sum(rate(nfctag_scan_total{outcome="error"}[1m]))'
run check --dir "$R/observability"
check "no contract anywhere: nothing is checked, as in 0.5" 0 'obs check: ok' 'contract.yaml not found'
absent '[contract-undeclared-metric]' || bad "no contract anywhere: names were checked"
# an invalid feature contract is reported when the root reads it
repo "root-bad" i nfctag-errors friend-errors friend.invites ""
rootalert 'sum(rate(friend_invites_total[1m]))'
sed -i 's/budget: 2/budget: 1/' "$FB/contract.yaml"
run check --dir "$R/observability"
check "root folder with no contract: an invalid feature contract is red and names its file" 1 'observability/friend/contract.yaml' '[over-budget]'
# a root folder with its own contract keeps its own behaviour (names of the other contracts merged, as before)
repo "root-own" i nfctag-errors friend-errors friend.invites ""
cp "$FA/contract.yaml" "$R/observability/contract.yaml"
sed -i 's/^service: nfctag/service: root/; s/nfctag.scans/root.scans/' "$R/observability/contract.yaml"
rootalert 'sum(rate(friend_invites_total[1m]))'
run check --dir "$R/observability"
check "root folder with its own contract: reads the features' metrics (as in 0.5)" 0 'obs check: ok'

# ---- a service-wide alert in a folder with no contract, and a metric that is in no contract at all -----------------
alertin() { # alertin <folder> <uid> <expr>: one alert in <folder>/alerts/overview
  mkdir -p "$1/alerts/overview"
  cat >"$1/alerts/overview/warning.yaml" <<YAML
apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata: { name: $2 }
spec:
  interval: 1m
  rules:
    - uid: $2
      title: $2
      condition: C
      for: 1m
      data:
        - refId: A
          datasourceUid: fixture-metrics
          relativeTimeRange: { from: 60, to: 0 }
          model:
            datasource: { type: prometheus, uid: fixture-metrics }
            expr: '$3'
            instant: true
            range: false
        - { refId: C, datasourceUid: __expr__, model: { type: math, expression: \$A > 0.5 } }
YAML
}
repo "lenient" i nfctag-errors friend-errors friend.invites ""
alertin "$R/observability" overview-up 'sum(up{job="x"}) + count(kube_pod_status_phase{phase="Running"})'
run check --dir "$R/observability"
check "no contract: a metric in no contract (up, kube_*) is a note, not a failure" 0 'obs check: ok' 'contract-name-unknown' '"kube_pod_status_phase" is in none of the repository'"'"'s contracts'
run query --static --dir "$R/observability"
check "no contract: ... in obs query --static too" 0 'obs query --static: ok'
alertin "$R/observability" overview-up 'sum(rate(nfctag_scan_total[1m]))'
run check --dir "$R/observability"
check "no contract: a near miss names the merged contract files, never a contract that does not exist" 1 '[contract-undeclared-metric]' 'observability/friend/contract.yaml, observability/nfctag/contract.yaml' 'did you mean "nfctag_scans_total"?'
absent '(observability/contract.yaml)' || bad "no contract: the message names observability/contract.yaml"
for expr in 'nfctag.scans_total' 'nfctag_scans' 'nfctag_scans_bytes'; do
  alertin "$R/observability" overview-up "sum(rate(${expr}[1m]))"
  run check --dir "$R/observability"
  check "no contract: the slip $expr (a separator or a suffix) is a near miss" 1 '[contract-undeclared-metric]' 'did you mean "nfctag_scans_total"?'
done
# a contract-less app folder in a monorepo next to an app with a contract
rm -rf "${work:?}/mono"
mkdir -p "$work/mono/.git"
feature "$work/mono/apps/a/observability" a a.reqs a-errors ""
alertin "$work/mono/apps/b/observability" b-errors 'sum(rate(b_reqs_total{code="500"}[1m]))'
run check --dir "$work/mono/apps/b/observability"
check "monorepo: app b with no contract reading its own b_reqs_total stays green (as in 0.5)" 0 'obs check: ok' 'contract-name-unknown'
alertin "$work/mono/apps/b/observability" b-errors 'sum(rate(a_reqs_total[1m]))'
run check --dir "$work/mono/apps/b/observability"
check "monorepo: app b reading a declared metric of app a passes" 0 'obs check: ok'
alertin "$work/mono/apps/b/observability" b-errors 'sum(rate(a_reqs_totl[1m]))'
run check --dir "$work/mono/apps/b/observability"
check "monorepo: a typo of app a's declared metric is red" 1 '[contract-undeclared-metric]' 'did you mean "a_reqs_total"?' 'apps/a/observability/contract.yaml'

# ---- copies of the alerts are not folders: a chart's files/ copy and a folder git ignores --------------------------------
repo "chart" i nfctag-errors friend-errors friend.invites ""
run check --dir "$FA"
check "chart copy: baseline, two features with distinct rule uids" 0 'obs check: ok'
mkdir -p "$R/chart/files/observability/nfctag"
cp -r "$FA/alerts" "$R/chart/files/observability/nfctag/alerts"
run check --dir "$FA"
check "chart copy: with no Chart.yaml the copy is a second folder and the rule uid clashes (the control)" 1 '[rule-id-clash]' 'chart/files/observability/nfctag/alerts/errors/warning.yaml'
printf 'apiVersion: v2\nname: c\nversion: 0.1.0\n' >"$R/chart/Chart.yaml"
run check --dir "$FA"
check "chart copy: below a folder holding a Chart.yaml the alerts copy is skipped" 0 'obs check: ok'
absent '[rule-id-clash]' || bad "chart copy: the copy clashed"
run alerts --dir "$FA" --base "$base"
check "chart copy: obs alerts does not clash with the copy either" 0
if command -v git >/dev/null 2>&1; then
  repo "ignored" i nfctag-errors friend-errors friend.invites ""
  rm -rf "${R:?}/.git"
  git -C "$R" init -q
  mkdir -p "$R/build/observability/nfctag"
  cp -r "$FA/alerts" "$R/build/observability/nfctag/alerts"
  run check --dir "$FA"
  check "ignored copy: a copy in an untracked folder is a second folder (the control)" 1 '[rule-id-clash]' 'build/observability/nfctag/alerts/errors/warning.yaml'
  printf 'build/\n' >"$R/.gitignore"
  run check --dir "$FA"
  check "ignored copy: a folder git ignores is skipped" 0 'obs check: ok'
  run test --dir "$FA" --base "$base"
  check "ignored copy: obs test does not clash with it" 0 'obs test: ok'
else
  echo "SKIP ignored copy: git is not installed"
fi

# a WHOLE observability folder (contract included) copied under a chart's files/ or into an ignored folder is not a second contract
repo "wholecopy" i nfctag-errors friend-errors friend.invites ""
mkdir -p "$R/chart"
printf 'apiVersion: v2\nname: c\nversion: 0.1.0\n' >"$R/chart/Chart.yaml"
mkdir -p "$R/chart/files/observability"
cp -r "$FA" "$R/chart/files/observability/nfctag"
run check --dir "$FA"
check "whole copy under chart/files: no [metric-clash] and no [rule-id-clash] (obs check)" 0 'obs check: ok'
absent '[metric-clash]' '[rule-id-clash]' || bad "whole copy under chart/files clashed"
run alerts --dir "$FA" --base "$base"
check "whole copy under chart/files: obs alerts" 0
run test --dir "$FA" --base "$base"
check "whole copy under chart/files: obs test" 0 'obs test: ok'
rm -f "$R/chart/Chart.yaml"
run check --dir "$FA"
check "whole copy under chart/files with no Chart.yaml: [metric-clash] (the control)" 1 '[metric-clash]'
if command -v git >/dev/null 2>&1; then
  repo "wholeignored" i nfctag-errors friend-errors friend.invites ""
  rm -rf "${R:?}/.git"
  git -C "$R" init -q
  mkdir -p "$R/build/observability"
  cp -r "$FA" "$R/build/observability/nfctag"
  run check --dir "$FA"
  check "whole copy in an untracked folder is a second contract (the control)" 1 '[metric-clash]'
  printf 'build/\n' >"$R/.gitignore"
  run check --dir "$FA"
  check "whole copy in an ignored folder: obs check" 0 'obs check: ok'
  absent '[metric-clash]' '[rule-id-clash]' || bad "whole copy in an ignored folder clashed"
  run alerts --dir "$FA" --base "$base"
  check "whole copy in an ignored folder: obs alerts" 0
  run test --dir "$FA" --base "$base"
  check "whole copy in an ignored folder: obs test" 0 'obs test: ok'
fi
# a chart at the Git ROOT: its files/ copy is skipped, the repository's own observability folders are scanned
repo "rootchart" i nfctag-errors friend-errors friend.invites ""
printf 'apiVersion: v2\nname: c\nversion: 0.1.0\n' >"$R/Chart.yaml"
mkdir -p "$R/files/observability"
cp -r "$FA" "$R/files/observability/nfctag"
run check --dir "$FA"
check "root chart: one source alert and its files/ copy: obs check is green and still reads the other feature" 0 'obs check: ok' '(service friend, read with this one)'
absent '[metric-clash]' '[rule-id-clash]' || bad "root chart: the copy clashed"
run alerts --dir "$FA" --base "$base"
check "root chart: obs alerts" 0
sed -i 's/friend-errors/nfctag-errors/' "$FB/alerts/errors/warning.yaml"
run check --dir "$FA"
check "root chart: two SOURCE folders with one uid still clash (the control)" 1 '[rule-id-clash]' 'observability/friend/alerts/errors/warning.yaml' 'observability/nfctag/alerts/errors/warning.yaml'
absent 'files/observability' || bad "root chart: the copy was named in the clash"

# ---- a top-level contract.yaml of something else (OpenAPI) leaves an ordinary repository; an obs contract at the top does not ----
repo "openapi" i nfctag-errors friend-errors friend.invites ""
printf 'openapi: 3.0.0\ninfo: { title: api, version: "1" }\npaths: {}\n' >"$R/contract.yaml"
run check --dir "$FA"
check "top-level OpenAPI contract.yaml: the repository keeps its result (not read as a contract, no failure)" 0 'obs check: ok' '(service friend, read with this one)'
absent 'contract.yaml: ' 'FAIL contract.yaml' || bad "top-level OpenAPI contract.yaml was read as an obs contract"
R=$work/sa-contract/checkout
rm -rf "${work:?}/sa-contract"
mkdir -p "$R/.git"
printf 'service: top\nmetrics: {}\n' >"$R/contract.yaml"
feature "$R/nfctag" nfctag nfctag.scans errors-warning ""
feature "$R/friend" friend friend.invites errors-warning ""
run check --dir "$R/friend"
check "standalone repository recognised by an obs contract at its top (no obs.yaml): features discovered, uid clash red" 1 '(service nfctag, read with this one)' '[rule-id-clash]'

# ---- obs live and obs sandbox say it too, with the same note as obs check ----------------------------------------------------
repo "livenote" ii nfctag-errors friend-errors friend.invites ""
mkdir -p "$R/observability"
printf 'versions:\n  victoriametrics: v1.156.0\n' >"$R/observability/obs.yaml"
run live usage --target local --dir "$FA" --timeout 2s
check "obs live: the same root obs.yaml note on stderr" 2 "note: observability/obs.yaml exists but $FA/obs.yaml does not exist, so it is not used"
run sandbox up --dir "$FA" --runtime none
grep -q 'exists but' "$work/out.txt" && ok "obs sandbox up: the same root obs.yaml note" || bad "obs sandbox up: no root obs.yaml note: $(head -5 "$work/out.txt")"

# an ignored folder is never read (unreadable build/), and a nested repository or submodule is its own boundary
if command -v git >/dev/null 2>&1 && [ "$(id -u)" != 0 ]; then
  repo "unreadable" i nfctag-errors friend-errors friend.invites ""
  rm -rf "${R:?}/.git"
  git -C "$R" init -q
  mkdir -p "$R/build/observability"
  cp -r "$FA" "$R/build/observability/nfctag"
  printf 'build/\n' >"$R/.gitignore"
  chmod 000 "$R/build"
  run check --dir "$FA"
  check "unreadable ignored build/: obs check is green (the folder is never read)" 0 'obs check: ok'
  run alerts --dir "$FA" --base "$base"
  check "unreadable ignored build/: obs alerts is green" 0
  chmod 755 "$R/build"
  printf '' >"$R/.gitignore"
  chmod 000 "$R/build"
  run check --dir "$FA"
  check "unreadable build/ that is NOT ignored: a failure naming it (the control)" 1 'cannot read build'
  chmod 755 "$R/build"
  repo "nested" i nfctag-errors friend-errors friend.invites ""
  rm -rf "${R:?}/.git"
  git -C "$R" init -q
  mkdir -p "$R/tools/other/observability"
  git -C "$R/tools/other" init -q
  cp -r "$FA" "$R/tools/other/observability/nfctag"
  run check --dir "$FA"
  check "nested repository holding a copy: not a second contract, not a second rule uid" 0 'obs check: ok'
  absent '[metric-clash]' '[rule-id-clash]' || bad "nested repository: the copy clashed"
  run test --dir "$FA" --base "$base"
  check "nested repository holding a copy: obs test" 0 'obs test: ok'
else
  echo "SKIP unreadable ignored folder and nested repository: git is not installed or the tests run as root"
fi

# ---- a repository that is itself an observability folder: recognised by content, never by the checkout folder's name -----
for name in observability checkout; do
  R=$work/sa-$name/$name
  rm -rf "${work:?}/sa-$name"
  mkdir -p "$R/.git"
  printf 'versions:\n  victoriametrics: v1.156.0\n' >"$R/obs.yaml"
  feature "$R/nfctag" nfctag nfctag.scans errors-warning ""
  feature "$R/friend" friend friend.invites errors-warning ""
  mkdir -p "$R/examples/demo/alerts/e"
  cp "$R/friend/alerts/errors/warning.yaml" "$R/examples/demo/alerts/e/warning.yaml" # alerts only: not a folder of the repository
  run check --dir "$R/friend"
  check "standalone repository checked out as $name (obs.yaml at its top): features discovered, rule uid clash red" 1 '(service nfctag, read with this one)' '[rule-id-clash]' 'friend/alerts/errors/warning.yaml' 'nfctag/alerts/errors/warning.yaml' "obs.yaml $R/obs.yaml: every version it names matches (the enclosing observability folder"
  absent 'examples/demo' || bad "standalone repository as $name: an examples/ folder with only alerts was counted"
done
R=$work/sa-none/observability
rm -rf "${work:?}/sa-none"
mkdir -p "$R/.git"
feature "$R/nfctag" nfctag nfctag.scans errors-warning ""
feature "$R/friend" friend friend.invites errors-warning ""
run check --dir "$R/friend"
check "a checkout folder named observability with nothing at its top is not an observability folder (the name counts for nothing)" 0 'obs check: ok'
absent '[rule-id-clash]' 'read with this one' || bad "checkout named observability with nothing at its top: its features were read"

# ---- obs.yaml: layout (ii) and a monorepo's apps keep 0.5, with one note when the root's file is not used ----------------
repo "note" ii nfctag-errors friend-errors friend.invites ""
mkdir -p "$R/observability"
printf 'versions:\n  victoriametrics: v1.156.0\n' >"$R/observability/obs.yaml"
run check --dir "$FA"
check "root obs.yaml: a <feature>/observability folder keeps 0.5 (built-in values) and one stderr note says the root file is not used" 0 'obs check: ok' 'not found: nothing to compare' 'note: observability/obs.yaml exists but '"$FA"'/obs.yaml does not exist, so it is not used' "copy observability/obs.yaml to $FA/obs.yaml or add one"
n=$(grep -c 'observability/obs.yaml exists but' "$work/out.txt" || true)
[ "$n" -eq 1 ] && ok "root obs.yaml: the note is printed once" || bad "root obs.yaml: the note was printed $n times"
set +e
"$OBS" check --dir "$FA" 2>"$work/err.txt" >"$work/outonly.txt"
set -e
grep -q 'exists but' "$work/err.txt" && ! grep -q 'exists but' "$work/outonly.txt" && ok "root obs.yaml: the note goes to stderr" || bad "root obs.yaml: the note is not on stderr alone"
run alerts --dir "$FA" --base "$base"
check "root obs.yaml: obs alerts says it too, once" 0 'exists but '
printf 'versions:\n  victoriametrics: v1.156.0\n' >"$FA/obs.yaml"
run check --dir "$FA"
check "root obs.yaml: a folder with its own obs.yaml prints no note" 0 "obs.yaml $FA/obs.yaml: every version it names matches"
absent 'exists but' || bad "root obs.yaml: a note was printed although the folder has its own obs.yaml"
repo "note-i" i nfctag-errors friend-errors friend.invites ""
printf 'versions:\n  victoriametrics: v1.156.0\n' >"$R/observability/obs.yaml"
run check --dir "$FA"
check "root obs.yaml: layout (i) inherits it, so there is no note" 0 'the enclosing observability folder'
absent 'exists but' || bad "root obs.yaml: a note was printed although the file was used"
# an inherited obs.yaml that is a symlink to a file outside the Git root is not read
repo "link" i nfctag-errors friend-errors friend.invites ""
printf 'versions:\n  victoriametrics: v1.150.0\n' >"$work/outside-obs.yaml"
ln -s "$work/outside-obs.yaml" "$R/observability/obs.yaml"
run check --dir "$FA"
check "obs.yaml symlink to a file outside the Git root is not read (built-in values)" 0 'obs check: ok' 'not found: nothing to compare'
absent 'v1.150.0' 'exists but' || bad "obs.yaml symlink leaving the repository was read or noted"
rm -f "$R/observability/obs.yaml"
cp "$work/outside-obs.yaml" "$R/observability/obs.yaml"
run check --dir "$FA"
check "obs.yaml: the same content as a file in the repository is read (the control)" 1 'v1.150.0'

# ---- a symlinked --dir is the folder it points to --------------------------------------------------------------------------
repo "alias" i nfctag-errors friend-errors friend.invites ""
ln -s observability/nfctag "$R/alias"
run check --dir "$R/alias"
check "symlinked --dir: obs check is green and does not clash with itself" 0 'obs check: ok'
absent '[metric-clash]' '[rule-id-clash]' || bad "symlinked --dir: obs check clashed with itself"
run alerts --dir "$R/alias" --base "$base"
check "symlinked --dir: obs alerts is green" 0
run test --dir "$R/alias" --base "$base"
check "symlinked --dir: obs test is green" 0 'obs test: ok'

echo "features.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
