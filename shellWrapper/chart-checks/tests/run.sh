#!/usr/bin/env bash
#
# chart-checks test harness.
#
# Usage:
#   ./run.sh --chart-checks <path-to-chart-checks>
#
# Every arm asserts the exit code AND the refusal text, so a gate that is merely non-zero
# without saying why does not pass. Sources are local: file:// URLs, a local git repository
# and (CC_TEST_OCI=1) a loopback OCI registry, so the run needs no internet. Under `nix build`
# (the `chart-checks-tests` package) the sandbox has no network at all, which is what proves
# `check` and a stamp-matching `install` stay offline; this harness additionally deletes every
# source after `install` and expects `check` to keep passing.
#
# Needs on PATH: helm, git, jq, yq, curl, tar, sha256sum; plus `registry` and `curl` when
# CC_TEST_OCI=1.

set -euo pipefail
unset CDPATH GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FX="${SCRIPT_DIR}/fixtures"
CC=""

die() {
  printf '❌ harness: %s\n' "$1" >&2
  exit 1
}

while [ "$#" -gt 0 ]; do
  case "$1" in
  --chart-checks)
    [ "$#" -ge 2 ] || die "--chart-checks needs a path"
    CC="$2"
    shift 2
    ;;
  -h | --help)
    printf 'Usage: %s --chart-checks <path-to-chart-checks>\n' "$0"
    exit 0
    ;;
  *) die "unknown argument: $1" ;;
  esac
done
[ -n "${CC}" ] || die "--chart-checks <path> is required"
[ -x "${CC}" ] || die "not executable: ${CC}"

ROOT="$(mktemp -d)"
REG_PID=""
cleanup() {
  if [ -n "${REG_PID}" ]; then kill "${REG_PID}" 2>/dev/null || true; fi
  rm -rf "${ROOT}"
}
trap cleanup EXIT

PASS=0
FAIL=0
OUT=""
RC=0

# run <dir> <args...>: runs chart-checks inside <dir>, capturing RC and OUT (stdout+stderr).
run() {
  local d="$1"
  shift
  RC=0
  OUT="$(cd "${d}" && "${CC}" "$@" 2>&1)" || RC=$?
}

ok() {
  PASS=$((PASS + 1))
  printf '  ✅ %s\n' "$1"
}
bad() {
  FAIL=$((FAIL + 1))
  printf '  ❌ %s\n' "$1"
  printf '%s\n' "${OUT}" | sed 's/^/       | /'
}

# expect <label> <rc> [<text>...]: asserts the last run's exit code and that OUT holds every text.
expect() {
  local label="$1" want="$2" t
  shift 2
  if [ "${RC}" -ne "${want}" ]; then
    bad "${label}: exit ${RC}, wanted ${want}"
    return 0
  fi
  for t in "$@"; do
    if ! grep -qF -- "${t}" <<<"${OUT}"; then
      bad "${label}: output lacks: ${t}"
      return 0
    fi
  done
  ok "${label}"
}

sha() { sha256sum "$1" | cut -d' ' -f1; }

# ----------------------------------------------------------------- sources ----

SRC="${ROOT}/src"
mkdir -p "${SRC}"

# 1. url source: the two CRDs in one file, fetched through file://
cat "${FX}/crds/externalsecret.yaml" <(echo '---') "${FX}/crds/httproute.yaml" >"${SRC}/crds.yaml"
CRDS_SHA="$(sha "${SRC}/crds.yaml")"

# 2. git source: a local repository holding the policy chart, the CRDs and the JSON schema
GITREPO="${SRC}/repo"
mkdir -p "${GITREPO}/chart" "${GITREPO}/crds" "${GITREPO}/schemas"
cp -R --no-preserve=mode "${FX}/policy-chart/." "${GITREPO}/chart/"
cp --no-preserve=mode "${FX}/crds/"*.yaml "${GITREPO}/crds/"
cp --no-preserve=mode "${FX}/schemas/logtoapp.json" "${GITREPO}/schemas/"
git -C "${GITREPO}" init -q
git -C "${GITREPO}" add -A
git -C "${GITREPO}" -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false commit -q -m fixture
COMMIT="$(git -C "${GITREPO}" rev-parse HEAD)"

# 3. oci source: a loopback registry holding the packaged policy chart
OCI_ON=0
if [ "${CC_TEST_OCI:-}" = "1" ]; then
  cat >"${ROOT}/registry.yml" <<EOF
version: 0.1
storage:
  inmemory: {}
http:
  addr: 127.0.0.1:5077
EOF
  registry serve "${ROOT}/registry.yml" >"${ROOT}/registry.log" 2>&1 &
  REG_PID=$!
  for _ in $(seq 1 100); do
    curl -fs http://127.0.0.1:5077/v2/ >/dev/null 2>&1 && break
    sleep 0.1
  done
  curl -fs http://127.0.0.1:5077/v2/ >/dev/null || {
    cat "${ROOT}/registry.log" >&2
    die "loopback registry did not start"
  }
  export HELM_CACHE_HOME="${ROOT}/helm/cache" HELM_CONFIG_HOME="${ROOT}/helm/config" HELM_DATA_HOME="${ROOT}/helm/data"
  helm package "${FX}/policy-chart" --destination "${SRC}/pkg" >/dev/null
  helm push "${SRC}"/pkg/fixture-policies-0.1.0.tgz oci://127.0.0.1:5077/charts --plain-http >/dev/null
  mkdir -p "${SRC}/pulled"
  helm pull oci://127.0.0.1:5077/charts/fixture-policies --version 0.1.0 --destination "${SRC}/pulled" --plain-http >/dev/null
  OCI_SHA="$(sha "${SRC}/pulled/fixture-policies-0.1.0.tgz")"
  OCI_ON=1
fi

# A fresh consumer repository per arm. Prints its path.
new_repo() { # name
  local d="${ROOT}/repos/$1"
  rm -rf "${d}"
  mkdir -p "${d}/manifests"
  cp --no-preserve=mode "${FX}/manifests/"*.yaml "${d}/manifests/"
  printf '%s' "${d}"
}

# Writes the standard chart-checks.yaml into $1: policies from oci (or git when there is no
# registry), CRDs from a url, a git subpath and a JSON-schema entry.
write_config() { # dir
  local d="$1"
  {
    echo 'kubernetes: "1.30"'
    echo 'policies:'
    if [ "${OCI_ON}" = 1 ]; then
      cat <<EOF
  - name: vanadium
    oci: oci://127.0.0.1:5077/charts/fixture-policies
    version: 0.1.0
    sha256: "${OCI_SHA}"
    plainHttp: true
EOF
    else
      cat <<EOF
  - name: vanadium
    git: file://${GITREPO}
    commit: "${COMMIT}"
    subpath: chart
EOF
    fi
    cat <<EOF
crds:
  - name: es-and-gateway
    url: file://${SRC}/crds.yaml
    version: v1
    sha256: "${CRDS_SHA}"
  - name: logto-operator
    git: file://${GITREPO}
    commit: "${COMMIT}"
    subpath: schemas/logtoapp.json
    format: json-schema
    group: cloud.example.io
    kind: LogtoApp
    crdVersion: v1
    temporary: true
    reason: no LogtoApp CRD is published yet
EOF
  } >"${d}/chart-checks.yaml"
}

M=manifests

printf '\n== usage ==\n'
D="$(new_repo usage)"
run "${D}"
expect "no arguments" 2 "Usage:"
run "${D}" frobnicate
expect "unknown command" 2 "unknown command: frobnicate"
write_config "${D}"
run "${D}" check
expect "check without files" 2 "check needs at least one manifest file"
run "${D}" check "${M}/nope.yaml"
expect "check with a missing file" 2 "not a file"
run "${D}" --version
expect "--version" 0 "chart-checks "

printf '\n== config refusals (exit 3) ==\n'
D="$(new_repo refuse)"
write_config "${D}"
sed -i 's/^    sha256: .*$//' "${D}/chart-checks.yaml"
run "${D}" install
expect "missing sha256 pin is refused" 3 "no sha256 pin"
if [ ! -e "${D}/.chart-checks" ]; then ok "a refused config installs nothing"; else bad "a refused config left .chart-checks behind"; fi

write_config "${D}"
sed -i 's/^    commit: .*$//' "${D}/chart-checks.yaml"
run "${D}" install
expect "missing commit pin is refused" 3 "no commit pin"

write_config "${D}"
sed -i 's/^    reason: .*$//' "${D}/chart-checks.yaml"
run "${D}" install
expect "temporary without a reason is refused" 3 "temporary entries need a reason"

write_config "${D}"
sed -i 's/^kubernetes: .*/kubernetes: "1.31"/' "${D}/chart-checks.yaml"
run "${D}" install
expect "a Kubernetes version with no embedded schemas is refused" 3 "not supported"

write_config "${D}"
sed -i 's/^    version: v1$/    version: v1\n    sha265: typo/' "${D}/chart-checks.yaml"
run "${D}" install
expect "an unknown key (a typo) is refused" 3 "unknown key sha265"

write_config "${D}"
sed -i 's/^    sha256: \(.*\)$/    sha256: \1\n    git: file:\/\/\/nowhere/' "${D}/chart-checks.yaml"
run "${D}" install
expect "two sources in one entry are refused" 3 "exactly one of oci, url, git, path"

printf '\n== sha256 and commit pins ==\n'
D="$(new_repo mismatch)"
write_config "${D}"
sed -i "s/^    sha256: \"${CRDS_SHA}\"$/    sha256: \"$(printf '0%.0s' $(seq 1 64))\"/" "${D}/chart-checks.yaml"
run "${D}" install
expect "sha256 mismatch on install is refused" 4 "sha256 mismatch"
if [ ! -e "${D}/.chart-checks" ]; then ok "a failed install leaves no .chart-checks"; else bad "failed install left .chart-checks"; fi
if compgen -G "${D}/.chart-checks.stage.*" >/dev/null; then bad "failed install left a stage directory"; else ok "a failed install leaves no stage directory"; fi

D="$(new_repo badcommit)"
write_config "${D}"
sed -i "s/^    commit: \"${COMMIT}\"$/    commit: \"$(printf '1%.0s' $(seq 1 40))\"/" "${D}/chart-checks.yaml"
run "${D}" install
expect "an unknown pinned commit is refused" 4 "cannot fetch commit"

printf '\n== install and check (every source kind) ==\n'
D="$(new_repo main)"
write_config "${D}"
run "${D}" install
expect "install" 0 "policies/vanadium: installed" "crds/es-and-gateway: installed (2 CRD(s))" "crds/logto-operator: installed"
if [ -f "${D}/.chart-checks/stamp" ]; then ok "install writes the stamp"; else bad "no stamp"; fi

run "${D}" check "${M}/good.yaml"
expect "good fixture passes (core kinds, ExternalSecret, HTTPRoute, LogtoApp)" 0 "1 file(s) passed"

run "${D}" check "${M}/bad-latest-image.yaml"
expect ":latest image is red (kyverno)" 1 "no-latest-tag" "refused"

run "${D}" check "${M}/bad-core-unknown-field.yaml"
expect "unknown field on a core kind is red (schema)" 1 "unknown field" "bogusField"

run "${D}" check "${M}/bad-crd-unknown-field.yaml"
expect "unknown field on a CRD kind is red (schema)" 1 "unknown field" "bogusField"

run "${D}" check "${M}/bad-logtoapp-unknown-field.yaml"
expect "unknown field on the JSON-schema kind is red" 1 "unknown field" "surprise"

run "${D}" check "${M}/bad-logtoapp-missing-field.yaml"
expect "missing required field on the JSON-schema kind is red" 1 "resourceRefs"

run "${D}" check "${M}/bad-logtoapp-no-spec.yaml"
expect "a JSON-schema kind with no spec is red (root required carried over)" 1 "spec"

run "${D}" check "${M}/good.yaml" "${M}/bad-latest-image.yaml"
expect "several files: one bad file is red" 1 "no-latest-tag"

printf '\n== VAP bindings are ignored ==\n'
# The fixture chart carries a ValidatingAdmissionPolicyBinding naming a policy that does not
# exist. The passing run above only holds if the binding never reached kyverno; assert it again
# from the rendered side, so the proof does not rest on kyverno tolerating a stray binding.
if helm template x "${FX}/policy-chart" | grep -q "^kind: ValidatingAdmissionPolicyBinding"; then ok "the policy chart does render a binding"; else bad "fixture chart lost its binding"; fi
run "${D}" check "${M}/good.yaml"
expect "good fixture still passes with a binding in the chart" 0 "passed"

printf '\n== offline after install ==\n'
# Remove every source. `check` and a stamp-matching `install` must not need any of them.
rm -rf "${SRC}/crds.yaml" "${GITREPO}"
if [ -n "${REG_PID}" ]; then
  kill "${REG_PID}" && wait "${REG_PID}" 2>/dev/null || true
  REG_PID=""
fi
run "${D}" check "${M}/good.yaml"
expect "check passes with every source gone" 0 "passed"
run "${D}" check "${M}/bad-latest-image.yaml"
expect "check still refuses with every source gone" 1 "no-latest-tag"
run "${D}" install
expect "install with a matching stamp does nothing" 0 "nothing fetched"

printf '\n== a changed list re-installs ==\n'
sed -i 's/^    version: v1$/    version: v1.0/' "${D}/chart-checks.yaml"
run "${D}" install
expect "a changed list re-fetches (and so fails with the sources gone)" 4 "installing"
run "${D}" check "${M}/good.yaml"
expect "check on a stale stamp runs install first" 4 "missing or stale; running install first"

printf '\n== path entries (a repository checking itself) ==\n'
D="$(new_repo path)"
mkdir -p "${D}/chart" "${D}/crds"
cp -R --no-preserve=mode "${FX}/policy-chart/." "${D}/chart/"
cp --no-preserve=mode "${FX}/crds/"*.yaml "${D}/crds/"
mkdir -p "${D}/schemas"
cp --no-preserve=mode "${FX}/schemas/logtoapp.json" "${D}/schemas/"
cat >"${D}/chart-checks.yaml" <<'EOF'
kubernetes: "1.30"
policies:
  - name: self
    path: chart
crds:
  - name: local-crds
    path: crds
  - name: logto
    path: schemas/logtoapp.json
    format: json-schema
    group: cloud.example.io
    kind: LogtoApp
    crdVersion: v1
EOF
run "${D}" check "${M}/bad-latest-image.yaml"
expect "path entries need no separate install step (and still refuse)" 1 "no-latest-tag"
run "${D}" check "${M}/good.yaml"
expect "path entries validate a good fixture" 0 "passed"
sed -i 's/!c.image.endsWith/c.image.endsWith/' "${D}/chart/templates/no-latest-tag.yaml"
run "${D}" check "${M}/good.yaml"
expect "a local policy edit is seen with no re-install" 1 "no-latest-tag"

printf '\n== a policy chart with no VAP is refused ==\n'
D="$(new_repo novap)"
mkdir -p "${D}/chart/templates"
cp --no-preserve=mode "${FX}/policy-chart/Chart.yaml" "${D}/chart/"
cp --no-preserve=mode "${FX}/policy-chart/templates/binding.yaml" "${D}/chart/templates/"
cat >"${D}/chart-checks.yaml" <<'EOF'
kubernetes: "1.30"
policies:
  - name: only-a-binding
    path: chart
EOF
run "${D}" check "${M}/good.yaml"
expect "bindings alone are not a policy set (no vacuous pass)" 5 "rendered no ValidatingAdmissionPolicy"

printf '\n== chart commands: render, lint, labels, check-chart ==\n'
# A repository with an application chart, a policy chart whose VAP depends on its labelPrefix
# value, and a chart-checks.yaml that feeds the stack's prefix to it through chartValues.
chart_repo() { # name
  local d
  d="$(new_repo "$1")"
  mkdir -p "${d}/chart" "${d}/prefix-policy"
  cp -R --no-preserve=mode "${FX}/app-chart/." "${d}/chart/"
  cp -R --no-preserve=mode "${FX}/prefix-policy/." "${d}/prefix-policy/"
  cat >"${d}/chart-checks.yaml" <<'EOF'
kubernetes: "1.30"
chart:
  release: fx
  labelPrefix: .labelPrefix
policies:
  - name: prefix
    path: prefix-policy
    chartValues:
      labelPrefix: .labelPrefix
EOF
  printf '%s' "${d}"
}

D="$(chart_repo chart-good)"
run "${D}" render chart
expect "render prints where it wrote" 0 "Rendered chart into"
if [ -f "${D}/infra/rendered/chart/default.yaml" ] && [ -f "${D}/infra/rendered/chart/prod.yaml" ]; then ok "render writes default.yaml and one file per values overlay"; else bad "render did not write default.yaml and prod.yaml"; fi
if grep -q "name: 'fx-config'" "${D}/infra/rendered/chart/default.yaml" && grep -q "'prod.example/service'" "${D}/infra/rendered/chart/prod.yaml" && ! grep -q 'prod.example' "${D}/infra/rendered/chart/default.yaml"; then
  ok "the overlay stack is rendered on top of values.yaml, with the configured release"
else bad "rendered stacks are wrong"; fi
touch "${D}/infra/rendered/chart/stale.yaml"
run "${D}" render chart --release other
if [ ! -e "${D}/infra/rendered/chart/stale.yaml" ] && grep -q "name: 'other-config'" "${D}/infra/rendered/chart/default.yaml"; then ok "render drops stale files and --release overrides the config"; else bad "render kept a stale file or ignored --release"; fi

run "${D}" check-chart chart
expect "check-chart passes a good chart (both stacks, test hook exempt)" 0 "== stack default ==" "== stack prod ==" "Helm render checks passed"

run "${D}" check --chart chart --stack prod infra/rendered/chart/prod.yaml
expect "check --chart --stack gives the policy chart that stack's prefix" 0 "passed"
run "${D}" check infra/rendered/chart/prod.yaml
expect "chartValues without --chart is a usage error" 2 "chartValues needs the chart"
run "${D}" check --chart chart --stack default infra/rendered/chart/prod.yaml
expect "the wrong stack's prefix is red (the policy really sees the passed value)" 1 "service-label"

# planted failure: bad values for the policy chart (a literal prefix the chart does not use)
D="$(chart_repo chart-badvalues)"
yq -i 'del(.policies[0].chartValues) | .policies[0].values.labelPrefix = "wrong.example"' "${D}/chart-checks.yaml"
run "${D}" check-chart chart
expect "bad values for the policy chart are red" 1 "service-label" "refused"
yq -i '.policies[0].values.labelPrefix = "fx.example"' "${D}/chart-checks.yaml"
run "${D}" check-chart chart
expect "...and a literal prefix cannot follow the stacks: the prod stack is red" 1 "== stack prod =="

D="$(chart_repo chart-emptypath)"
yq -i '.policies[0].chartValues.labelPrefix = ".nope"' "${D}/chart-checks.yaml"
run "${D}" check-chart chart
expect "a chartValues path that is empty in the chart is a tool error" 5 "chartValues.labelPrefix" "is empty"

# planted failure: a first-party object without one of the labels
D="$(chart_repo chart-nolabel)"
sed -i 's/^moduleLabel: .*/moduleLabel: other/' "${D}/chart/values.yaml"
run "${D}" check-chart chart
expect "a first-party object without a label is red" 1 "first-party objects without all fx.example/ labels" "configmap.yaml (ConfigMap fx-config)"
run "${D}" labels chart infra/rendered/chart/default.yaml
expect "labels runs on its own" 1 "first-party objects without all fx.example/ labels"

D="$(chart_repo chart-customlabels)"
yq -i '.chart.labels = ["landscape", "platform", "service", "module", "team"]' "${D}/chart-checks.yaml"
run "${D}" check-chart chart
expect "chart.labels is the configured list" 1 "without all fx.example/ labels" "team"

# the label rule covers the chart's OWN templates only: an object from a subchart is not checked
D="$(chart_repo chart-subchart)"
mkdir -p "${D}/chart/charts/sub/templates"
printf 'apiVersion: v2\nname: sub\nversion: 0.1.0\n' >"${D}/chart/charts/sub/Chart.yaml"
printf 'dependencies:\n  - name: sub\n    version: 0.1.0\n' >>"${D}/chart/Chart.yaml"
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sub-config\n  labels:\n    fx.example/service: sub\n    prod.example/service: sub\ndata:\n  a: b\n' >"${D}/chart/charts/sub/templates/cm.yaml"
run "${D}" check-chart chart
expect "an object from a subchart is not held to the first-party label rule" 0 "Helm render checks passed"

# planted failure: helm lint (a two-part chart version: helm template renders it, lint refuses it)
D="$(chart_repo chart-lint)"
sed -i 's/^version: .*/version: 0.1/' "${D}/chart/Chart.yaml"
run "${D}" lint chart
expect "lint refuses a chart helm lint rejects" 1 "ERROR" "helm lint refused"
run "${D}" check-chart chart
expect "check-chart stops at helm lint" 1 "helm lint refused"
if grep -q '== stack' <<<"${OUT}"; then bad "check-chart went on to the stacks after a lint error"; else ok "check-chart does not reach the stacks after a lint error"; fi

D="$(chart_repo chart-usage)"
run "${D}" check-chart
expect "check-chart needs a chart" 2 "takes exactly one chart directory"
run "${D}" render nope
expect "render of a missing chart is a usage error" 2 "not a chart directory"
run "${D}" labels chart
expect "labels needs rendered files" 2 "at least one rendered file"
run "${D}" check --stack prod infra/rendered/chart/prod.yaml
expect "--stack needs --chart" 2 "--stack needs --chart"
sed -i '/^  labelPrefix:/d' "${D}/chart-checks.yaml"
run "${D}" check-chart chart
expect "without chart.labelPrefix the label check is off (chartValues still resolve)" 0 "Helm render checks passed"
run "${D}" labels chart infra/rendered/chart/default.yaml
expect "labels without chart.labelPrefix in the config is refused" 3 "needs chart.labelPrefix"

# review fixes: a stack never inherits a previous stack's objects; render never overwrites the chart
D="$(chart_repo chart-leak)"
rm "${D}/chart/templates/test-hook.yaml"
cat >"${D}/chart/templates/extra.yaml" <<'EOF'
{{- if eq .Values.labelPrefix "fx.example" }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: extra
  labels:
    fx.example/landscape: lab
    fx.example/platform: fx
    fx.example/service: extra
    fx.example/module: main
{{- end }}
EOF
run "${D}" check-chart chart
expect "an object only the default stack renders is never judged with the prod stack's policy values" 0 "Helm render checks passed"

D="$(chart_repo chart-overlap)"
yq -i '.chart.renderDir = "."' "${D}/chart-checks.yaml"
run "${D}" render chart
expect "a renderDir that resolves to the chart itself is refused" 3 "inside the chart"
if [ -f "${D}/chart/Chart.yaml" ] && [ -f "${D}/chart/values.yaml" ]; then ok "...and the chart's own files are untouched"; else bad "render deleted the chart's files"; fi

D="$(chart_repo chart-defaultoverlay)"
printf 'labelPrefix: other.example\n' >"${D}/chart/values.default.yaml"
run "${D}" render chart
expect "an overlay named default is refused" 1 "no overlay may be named default"

printf '\n== chart config refusals (exit 3) ==\n'
D="$(chart_repo chart-refuse)"
yq -i '.chart.bogus = 1' "${D}/chart-checks.yaml"
run "${D}" render chart
expect "an unknown chart key is refused" 3 "chart: unknown key bogus"
D="$(chart_repo chart-refuse2)"
yq -i '.policies[0].values = "x"' "${D}/chart-checks.yaml"
run "${D}" render chart
expect "policy values must be a mapping" 3 "values must be a mapping"
D="$(chart_repo chart-refuse3)"
yq -i '.policies[0].chartValues.labelPrefix = 3' "${D}/chart-checks.yaml"
run "${D}" render chart
expect "chartValues must map to yq paths" 3 "chartValues must map"
D="$(chart_repo chart-refuse4)"
yq -i '.chart.labels = ["a"] | del(.chart.labelPrefix)' "${D}/chart-checks.yaml"
run "${D}" render chart
expect "chart.labels without chart.labelPrefix is refused" 3 "labels needs labelPrefix"
D="$(chart_repo chart-refuse5)"
yq -i '.chart.renderDir = "../out"' "${D}/chart-checks.yaml"
run "${D}" render chart
expect "renderDir may not leave the repository" 3 "renderDir must be a relative path"

printf '\n== %d passed, %d failed ==\n' "${PASS}" "${FAIL}"
if [ "${OCI_ON}" != 1 ]; then
  printf 'note: the oci source kind was not exercised (CC_TEST_OCI is unset)\n'
fi
[ "${FAIL}" -eq 0 ]
