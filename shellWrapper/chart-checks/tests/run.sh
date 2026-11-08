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

printf '\n== %d passed, %d failed ==\n' "${PASS}" "${FAIL}"
if [ "${OCI_ON}" != 1 ]; then
  printf 'note: the oci source kind was not exercised (CC_TEST_OCI is unset)\n'
fi
[ "${FAIL}" -eq 0 ]
