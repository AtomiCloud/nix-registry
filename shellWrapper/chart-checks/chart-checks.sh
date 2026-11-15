# shellcheck shell=bash
#
# chart-checks: fetch pinned policy/schema inputs, then validate rendered manifests offline.
#
#   chart-checks install [-c chart-checks.yaml]
#   chart-checks check   [-c chart-checks.yaml] <file>...
#   chart-checks render      [-c chart-checks.yaml] [--release <name>] <chart>
#   chart-checks lint        <chart>
#   chart-checks labels      [-c chart-checks.yaml] <chart> <rendered file>...
#   chart-checks check-chart [-c chart-checks.yaml] [--release <name>] <chart>
#
# The nix wrapper prepends `set -euo pipefail`, `CHART_CHECKS_VERSION=...` and a PATH that
# holds every binary used below (bash, coreutils, curl, git, helm, jq, kubectl-validate,
# kyverno, tar, yq). Nothing is assumed to be on the caller's PATH.
#
# Exit codes
#   0  success (install done / up to date, or every manifest passed)
#   1  a manifest was refused (schema stage or policy stage, or both)
#   2  usage error
#   3  chart-checks.yaml refused (unknown key, missing pin, temporary without a reason, ...)
#   4  install failed (fetch error, sha256/commit mismatch, entry yields nothing)
#   5  tool error (policy chart did not render, no ValidatingAdmissionPolicy, input missing)

# A pre-commit hook (or any caller inside a git operation) may export these; they would
# redirect the `git` calls below into the caller's repository.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GIT_COMMON_DIR

CONFIG="chart-checks.yaml"
STAMP_TAG="chart-checks-stamp-v1"
# Embedded in kubectl-validate: the only Kubernetes versions whose core schemas it carries.
# Any other version makes it download the OpenAPI spec from GitHub, which `check` must never do.
K8S_VERSIONS_RE='^1\.(2[3-9]|30)$'

CC_TMP=""
STAGE=""

cleanup() {
  if [ -n "${STAGE}" ]; then rm -rf "${STAGE}"; fi
  if [ -n "${CC_TMP}" ]; then rm -rf "${CC_TMP}"; fi
}
trap cleanup EXIT

say() { printf '%s\n' "$*" >&2; }
die() {
  local code="$1"
  shift
  printf '❌ chart-checks: %s\n' "$*" >&2
  exit "${code}"
}

usage() {
  cat <<'EOF'
chart-checks: fetch pinned policy/schema inputs, then validate rendered manifests offline.

Usage:
  chart-checks install [-c chart-checks.yaml]            fetch + verify into .chart-checks/
  chart-checks check   [-c chart-checks.yaml] <file>...  validate rendered manifests offline
  chart-checks check   [-c chart-checks.yaml] --chart <dir> [--stack <name>] <file>...
  chart-checks render      [-c chart-checks.yaml] [--release <name>] <chart>   chart + every values.<name>.yaml -> rendered files
  chart-checks lint        <chart>                                              helm lint
  chart-checks labels      [-c chart-checks.yaml] <chart> <rendered file>...    first-party label check
  chart-checks check-chart [-c chart-checks.yaml] [--release <name>] <chart>    render, lint, install, check + labels per stack
  chart-checks --version | --help

install  Fetches every entry of chart-checks.yaml into .chart-checks/ (next to the config)
         and verifies its sha256 / pinned commit. When the stamp of the list already matches,
         it does nothing and opens no network connection.
check    Reads only .chart-checks/ (and `path` entries); runs `install` first when the folder
         is missing or its stamp does not match the list. Stage 1 validates every file against
         the Kubernetes core schemas plus the installed CRDs (unknown fields are refused);
         stage 2 renders the policy charts, keeps only their ValidatingAdmissionPolicy
         objects and runs `kyverno apply` over the files.

render    Writes <renderDir>/<chart>/default.yaml (values.yaml) and one <stack>.yaml per values.<stack>.yaml
         overlay (rendered on top of values.yaml); renderDir defaults to infra/rendered, next to the config.
labels   Every object of the chart's OWN templates (not its subcharts) carries every label of
         chart.labels (default landscape, platform, service, module) under the prefix read from the
         stack's values with the yq path chart.labelPrefix. Helm test hooks are exempt.
check-chart  Everything above in order: render, helm lint, install, then for every rendered stack
         `check` (policy charts get that stack's chartValues) and `labels`; stops at the first refusal.

Exit codes: 0 ok, 1 manifest refused, 2 usage, 3 config refused, 4 install failed, 5 tool error.
EOF
}

# ---------------------------------------------------------------- config ----

# Prints one line per problem; no output means the configuration is well formed.
VALIDATE_JQ="$(
  cat <<'EOF'
def hex($n): type == "string" and test("^[0-9a-f]{\($n)}$");
def pin($v; $n; $label; $w; $what):
  if $v == null then ["\($w): \($what) has no \($label) pin"]
  elif ($v | type) != "string" then ["\($w): \($label) must be a quoted string (an unquoted hex value can parse as a number)"]
  elif ($v | hex($n)) then []
  else ["\($w): \($label) must be \($n) lowercase hex"] end;
def entry_errors($list):
  (.[$list] // []) as $items
  | if ($items | type) != "array" then ["\($list): must be a list"] else
    ($items | to_entries | map(
      .key as $i | .value as $e
      | ("\($list)[\($i)]" + (if ($e | type) == "object" and ($e.name | type) == "string" then " (\($e.name))" else "" end)) as $w
      | if ($e | type) != "object" then ["\($w): must be a mapping"] else
        (
          (($e | keys) - (["name","oci","url","git","path","version","sha256","commit","subpath","temporary","reason","format","group","kind","crdVersion","plural","plainHttp"] + (if $list == "policies" then ["values","chartValues"] else [] end)))
            | map("\($w): unknown key \(.)")
        ) + (
          if ($e.name | type) != "string" or ($e.name | test("^[a-z0-9][a-z0-9._-]*$") | not)
          then ["\($w): name is required and must match ^[a-z0-9][a-z0-9._-]*$"] else [] end
        ) + (
          (["oci","url","git","path"] | map(select($e[.] != null))) as $src
          | if ($src | length) != 1 then ["\($w): exactly one of oci, url, git, path is required"]
            elif $src[0] == "oci" then
              (if ($e.oci | type) == "string" and ($e.oci | startswith("oci://")) then [] else ["\($w): oci must be an oci:// reference"] end)
              + (if ($e.version | type) == "string" and $e.version != "" then [] else ["\($w): oci entry has no version pin"] end)
              + pin($e.sha256; 64; "sha256"; $w; "oci entry")
            elif $src[0] == "url" then
              (if ($e.url | type) == "string" and $e.url != "" then [] else ["\($w): url must be a string"] end)
              + pin($e.sha256; 64; "sha256"; $w; "url entry")
            elif $src[0] == "git" then
              (if ($e.git | type) == "string" and $e.git != "" then [] else ["\($w): git must be a repository URL"] end)
              + pin($e.commit; 40; "commit"; $w; "git entry")
            else
              (if ($e.path | type) == "string" and $e.path != "" then [] else ["\($w): path must be a non-empty string"] end)
            end
        ) + (
          if $e.subpath != null and (($e.subpath | type) != "string" or ($e.subpath | startswith("/")) or ($e.subpath | test("(^|/)\\.\\.(/|$)")))
          then ["\($w): subpath must be relative and stay inside the source"] else [] end
        ) + (
          if $e.temporary != null and ($e.temporary | type) != "boolean" then ["\($w): temporary must be true or false"]
          elif $e.temporary == true and (($e.reason | type) != "string" or ($e.reason | test("^\\s*$")))
          then ["\($w): temporary entries need a reason"] else [] end
        ) + (
          (if $e.values != null and ($e.values | type) != "object" then ["\($w): values must be a mapping (passed to the policy chart's helm template)"] else [] end)
          + (if $e.chartValues == null then []
             elif ($e.chartValues | type) != "object" or ($e.chartValues | to_entries | any(.value | type != "string" or . == ""))
             then ["\($w): chartValues must map a policy value name to a yq path in the chart values, such as labelPrefix: .labelPrefix"]
             else [] end)
        ) + (
          if $e.format == null then []
          elif $e.format != "json-schema" then ["\($w): format must be json-schema"]
          elif $list != "crds" then ["\($w): format json-schema is only valid under crds"]
          else
            ((["group","kind","crdVersion"] | map(select(($e[.] | type) != "string" or $e[.] == "")))
              | map("\($w): json-schema entry needs \(.)"))
          end
        )
      end
    ) | add // [])
    + ([$items[] | select(type == "object") | .name | select(type == "string")] | group_by(.) | map(select(length > 1)) | map("\($list): duplicate name \(.[0])"))
  end;
def chart_errors:
  if .chart == null then []
  elif (.chart | type) != "object" then ["chart: must be a mapping"]
  else
    (.chart | (keys - ["release","renderDir","labelPrefix","labels"]) | map("chart: unknown key \(.)"))
    + (if (.chart.release // "x") | type != "string" or . == "" then ["chart: release must be a non-empty string"] else [] end)
    + (if (.chart.renderDir // "x") | type != "string" or . == "" or startswith("/") or test("(^|/)\\.\\.(/|$)") then ["chart: renderDir must be a relative path that stays inside the repository"] else [] end)
    + (if (.chart.labelPrefix // "x") | type != "string" or . == "" then ["chart: labelPrefix must be a yq path in the chart values, such as .labelPrefix"] else [] end)
    + (if .chart.labels == null then []
       elif (.chart.labels | type) != "array" or (.chart.labels | length) == 0 or (.chart.labels | any(type != "string" or . == ""))
       then ["chart: labels must be a non-empty list of label names"] else [] end)
    + (if .chart.labels != null and .chart.labelPrefix == null then ["chart: labels needs labelPrefix (the label check is driven by it)"] else [] end)
  end;
(
  if type != "object" then ["top level must be a mapping"] else
    ((keys) - ["kubernetes","policies","crds","chart"] | map("unknown top-level key \(.)"))
    + (if (.kubernetes | type) == "string" then [] else ["kubernetes is required and must be a quoted string such as \"1.30\""] end)
    + entry_errors("policies") + entry_errors("crds") + chart_errors
  end
) | .[]
EOF
)"

CFG_JSON=""
CFG_DIR=""
CC_DIR=""
K8S=""
STAMP=""

load_config() {
  [ -f "${CONFIG}" ] || die 3 "config not found: ${CONFIG}"
  CFG_DIR="$(cd "$(dirname "${CONFIG}")" && pwd)"
  CC_DIR="${CFG_DIR}/.chart-checks"
  CFG_JSON="$(yq -o=json -I=0 '.' "${CONFIG}" 2>/dev/null)" || die 3 "${CONFIG} is not valid YAML"
  local errors
  errors="$(jq -r "${VALIDATE_JQ}" <<<"${CFG_JSON}")"
  if [ -n "${errors}" ]; then
    printf '❌ chart-checks: %s refused:\n' "${CONFIG}" >&2
    while IFS= read -r line; do printf '   - %s\n' "${line}" >&2; done <<<"${errors}"
    exit 3
  fi
  K8S="$(jq -r '.kubernetes' <<<"${CFG_JSON}")"
  if ! [[ ${K8S} =~ ${K8S_VERSIONS_RE} ]]; then
    die 3 "kubernetes \"${K8S}\" is not supported: the offline core schemas cover 1.23 to 1.30 only (anything newer would make the validator download them)"
  fi
  STAMP="${STAMP_TAG} $(jq -S -c . <<<"${CFG_JSON}" | sha256sum | cut -d' ' -f1)"
}

jf() { jq -r --arg k "$2" '.[$k] // empty' <<<"$1"; }

entries() { jq -c --arg l "$1" '.[$l] // [] | .[]' <<<"${CFG_JSON}"; }

stamp_ok() {
  [ -f "${CC_DIR}/stamp" ] && [ "$(cat "${CC_DIR}/stamp")" = "${STAMP}" ]
}

# --------------------------------------------------------------- install ----

verify_sha() { # file expected label
  local got
  got="$(sha256sum "$1" | cut -d' ' -f1)"
  [ "${got}" = "$2" ] || die 4 "$3: sha256 mismatch for $(basename "$1"): pinned $2, got ${got}"
}

SRC_ROOT=""
SRC_DEFAULT_SUB="."

# Fetches one entry's source into $2 and sets SRC_ROOT (a directory or a single file) and
# SRC_DEFAULT_SUB (where, inside it, the entry's content lives unless `subpath` says otherwise).
materialize() { # entry workdir
  local e="$1" w="$2" name
  name="$(jf "$e" name)"
  mkdir -p "${w}/dl" "${w}/src"
  SRC_DEFAULT_SUB="."
  if [ -n "$(jf "$e" oci)" ]; then
    local args tgz dirs
    args=(pull "$(jf "$e" oci)" --version "$(jf "$e" version)" --destination "${w}/dl")
    if [ "$(jf "$e" plainHttp)" = "true" ]; then args+=(--plain-http); fi
    helm "${args[@]}" >"${w}/helm.log" 2>&1 || {
      cat "${w}/helm.log" >&2
      die 4 "${name}: helm pull failed"
    }
    mapfile -t tgz < <(find "${w}/dl" -maxdepth 1 -type f -name '*.tgz')
    [ "${#tgz[@]}" -eq 1 ] || die 4 "${name}: helm pull produced ${#tgz[@]} archives, expected 1"
    verify_sha "${tgz[0]}" "$(jf "$e" sha256)" "${name}"
    tar -xzf "${tgz[0]}" -C "${w}/src"
    mapfile -t dirs < <(find "${w}/src" -mindepth 1 -maxdepth 1 -type d)
    [ "${#dirs[@]}" -eq 1 ] || die 4 "${name}: chart archive does not hold exactly one chart directory"
    SRC_ROOT="${dirs[0]}"
    SRC_DEFAULT_SUB="crds"
  elif [ -n "$(jf "$e" url)" ]; then
    local url fn
    url="$(jf "$e" url)"
    fn="${url##*/}"
    fn="${fn%%\?*}"
    [ -n "${fn}" ] || fn="download"
    curl -fsSL --retry 3 --output "${w}/dl/${fn}" "${url}" || die 4 "${name}: download failed: ${url}"
    verify_sha "${w}/dl/${fn}" "$(jf "$e" sha256)" "${name}"
    case "${fn}" in
    *.tgz | *.tar.gz)
      tar -xzf "${w}/dl/${fn}" -C "${w}/src"
      SRC_ROOT="${w}/src"
      ;;
    *) SRC_ROOT="${w}/dl/${fn}" ;;
    esac
  elif [ -n "$(jf "$e" git)" ]; then
    local commit got
    commit="$(jf "$e" commit)"
    git init -q "${w}/src"
    GIT_TERMINAL_PROMPT=0 git -C "${w}/src" fetch -q --depth 1 "$(jf "$e" git)" "${commit}" ||
      die 4 "${name}: cannot fetch commit ${commit} from $(jf "$e" git)"
    got="$(git -C "${w}/src" rev-parse FETCH_HEAD)"
    [ "${got}" = "${commit}" ] || die 4 "${name}: fetched ${got}, pinned ${commit}"
    git -C "${w}/src" -c advice.detachedHead=false checkout -q "${commit}"
    rm -rf "${w}/src/.git"
    SRC_ROOT="${w}/src"
  else
    die 4 "${name}: path entries are not installed"
  fi
}

# Resolves the entry's content location (SRC_ROOT + subpath) and echoes it.
content_of() { # entry root default-sub
  local sub
  sub="$(jf "$1" subpath)"
  [ -n "${sub}" ] || sub="$3"
  if [ "${sub}" = "." ]; then printf '%s' "$2"; else printf '%s/%s' "$2" "${sub}"; fi
}

# Wraps a whole-object JSON Schema (e.g. a LogtoApp schema) into a CustomResourceDefinition,
# so the schema stage treats it like any installed CRD. The `spec` sub-schema becomes the CRD's
# spec; `const` becomes a one-value `enum`; `additionalProperties: false` is dropped (CRD
# structural schemas forbid it beside `properties`, and unknown fields are refused anyway).
wrap_json_schema() { # entry file
  local e="$1" f="$2" kind plural
  kind="$(jf "$e" kind)"
  plural="$(jf "$e" plural)"
  [ -n "${plural}" ] || plural="$(printf '%s' "${kind}" | tr '[:upper:]' '[:lower:]')s"
  yq -o=json -I=0 '.' "${f}" |
    jq --arg group "$(jf "$e" group)" --arg version "$(jf "$e" crdVersion)" --arg kind "${kind}" --arg plural "${plural}" '
      def clean:
        walk(if type == "object" then
          (del(."$schema", ."$id", ."$comment")
           | (if has("const") then (.enum = [.const] | del(.const)) else . end)
           | (if .additionalProperties == false then del(.additionalProperties) else . end))
        else . end);
      (.properties.spec // error("json-schema has no properties.spec")) as $spec
      | {
          apiVersion: "apiextensions.k8s.io/v1",
          kind: "CustomResourceDefinition",
          metadata: {name: "\($plural).\($group)"},
          spec: {
            group: $group,
            names: {kind: $kind, plural: $plural, singular: ($kind | ascii_downcase), listKind: "\($kind)List"},
            scope: "Namespaced",
            versions: [{
              name: $version, served: true, storage: true,
              schema: {openAPIV3Schema: ({type: "object", properties: {spec: ($spec | clean)}}
                + (if ((.required // []) | index("spec")) != null then {required: ["spec"]} else {} end))}
            }]
          }
        }' | yq -P '.'
}

# Writes the CustomResourceDefinition documents found under $2 (a file or a directory) to
# stdout as one YAML stream. A JSON-schema entry is wrapped instead.
collect_crds() { # entry target label
  local e="$1" t="$2" label="$3" f
  if [ "$(jf "$e" format)" = "json-schema" ]; then
    [ -f "${t}" ] || die 4 "${label}: a json-schema entry must point at one file"
    wrap_json_schema "$e" "${t}" || die 4 "${label}: cannot convert ${t} into a CRD"
    return 0
  fi
  [ -e "${t}" ] || die 4 "${label}: ${t} does not exist in the source"
  local files=()
  if [ -d "${t}" ]; then
    mapfile -t files < <(find "${t}" -type f \( -name '*.yaml' -o -name '*.yml' -o -name '*.json' \) | LC_ALL=C sort)
  else
    files=("${t}")
  fi
  for f in "${files[@]}"; do
    local docs
    docs="$(yq 'select(.kind == "CustomResourceDefinition")' "${f}" 2>/dev/null)" ||
      die 4 "${label}: cannot parse ${f}"
    if [ -n "${docs}" ]; then
      printf '%s\n---\n' "${docs}"
    fi
  done
}

install_policy() { # entry
  local e="$1" name w root
  name="$(jf "$e" name)"
  w="${STAGE}/.work/policies-${name}"
  materialize "$e" "${w}"
  root="$(content_of "$e" "${SRC_ROOT}" ".")"
  # A tarball without `subpath` may wrap the chart in one directory.
  if [ ! -f "${root}/Chart.yaml" ] && [ -z "$(jf "$e" subpath)" ]; then
    local dirs
    mapfile -t dirs < <(find "${root}" -mindepth 1 -maxdepth 1 -type d)
    if [ "${#dirs[@]}" -eq 1 ]; then root="${dirs[0]}"; fi
  fi
  [ -f "${root}/Chart.yaml" ] || die 4 "${name}: no Chart.yaml at the policy chart root"
  mkdir -p "${STAGE}/policies/${name}"
  cp -R --no-preserve=mode "${root}/." "${STAGE}/policies/${name}/"
  say "   policies/${name}: installed"
}

install_crd() { # entry
  local e="$1" name w out
  name="$(jf "$e" name)"
  w="${STAGE}/.work/crds-${name}"
  materialize "$e" "${w}"
  out="${STAGE}/crds/${name}.yaml"
  collect_crds "$e" "$(content_of "$e" "${SRC_ROOT}" "${SRC_DEFAULT_SUB}")" "crds/${name}" >"${out}"
  [ -s "${out}" ] || die 4 "crds/${name}: the source holds no CustomResourceDefinition (a vacuous entry is refused)"
  say "   crds/${name}: installed ($(grep -c '^kind: CustomResourceDefinition' "${out}") CRD(s))"
}

cmd_install() {
  load_config
  if stamp_ok; then
    say "✅ chart-checks: ${CC_DIR} is up to date (stamp matches the list); nothing fetched"
    return 0
  fi
  say "⬇️  chart-checks: installing into ${CC_DIR}"
  STAGE="$(mktemp -d "${CFG_DIR}/.chart-checks.stage.XXXXXX")"
  mkdir -p "${STAGE}/policies" "${STAGE}/crds" "${STAGE}/.work"
  local e
  while IFS= read -r e; do
    [ -z "$(jf "$e" path)" ] || continue
    install_policy "$e"
  done < <(entries policies)
  while IFS= read -r e; do
    [ -z "$(jf "$e" path)" ] || continue
    install_crd "$e"
  done < <(entries crds)
  rm -rf "${STAGE}/.work"
  printf '%s\n' "${STAMP}" >"${STAGE}/stamp"
  rm -rf "${CC_DIR}"
  mv "${STAGE}" "${CC_DIR}"
  STAGE=""
  say "✅ chart-checks: installed"
}

# ----------------------------------------------------------------- check ----

# `kyverno apply` cannot map a CRD kind to its resource name offline unless it is handed that
# CRD, and `--crd-path` takes exactly ONE CustomResourceDefinition file. So the input is split
# into single documents and grouped by kind, and kyverno runs once per kind that an installed
# CRD defines (given that CRD) and once for every other kind. Every policy still sees every
# object, with no kind pre-filter: each policy's own matchConstraints selects.
run_kyverno() { # work files...
  local work="$1" f line key n=0 d rc=0
  shift
  local crdmap="${work}/crdmap" res="${work}/res"
  mkdir -p "${crdmap}" "${res}/_builtin"
  for f in "${work}"/crds/*.yaml; do
    [ -e "${f}" ] || continue
    while IFS= read -r line; do
      key="$(jq -r '"\(.spec.group)__\(.spec.names.kind)"' <<<"${line}")"
      printf '%s\n' "${line}" >"${crdmap}/${key}.yaml"
    done < <(yq -o=json -I=0 'select(.kind == "CustomResourceDefinition")' "${f}")
  done
  for f in "$@"; do
    while IFS= read -r line; do
      n=$((n + 1))
      key="$(jq -r '((.apiVersion // "") | split("/") | if length > 1 then .[0] else "core" end) + "__" + .kind' <<<"${line}")"
      if [ -f "${crdmap}/${key}.yaml" ]; then d="${res}/${key}"; else d="${res}/_builtin"; fi
      mkdir -p "${d}"
      printf '%s\n' "${line}" >"${d}/${n}.yaml"
    done < <(yq -o=json -I=0 'select(.kind != null)' "${f}")
  done
  [ "${n}" -gt 0 ] || die 5 "no Kubernetes object (a document with a kind) in the given files"
  for d in "${res}"/*/; do
    key="$(basename "${d}")"
    local kargs=(apply "${work}/vaps" --resource "${d}" --detailed-results --remove-color)
    if [ "${key}" != "_builtin" ]; then kargs+=(--crd-path "${crdmap}/${key}.yaml"); fi
    if [ -z "$(find "${d}" -type f -name '*.yaml' -print -quit)" ]; then continue; fi
    say "   kyverno: ${key#_}"
    kyverno "${kargs[@]}" || rc=$?
  done
  return "${rc}"
}

cmd_check() {
  load_config
  [ "$#" -gt 0 ] || die 2 "check needs at least one manifest file"
  local f
  for f in "$@"; do [ -f "${f}" ] || die 2 "not a file: ${f}"; done
  if ! stamp_ok; then
    say "ℹ️  chart-checks: ${CC_DIR} is missing or stale; running install first"
    cmd_install
  fi

  local work e name
  # A fresh workspace per call: check-chart runs one `check` per stack, and a leftover object of a
  # previous stack must never be evaluated with this stack's policy values.
  work="$(mktemp -d "${CC_TMP}/check.XXXXXX")"
  mkdir -p "${work}/crds" "${work}/vaps"
  cp "${CC_DIR}"/crds/*.yaml "${work}/crds/" 2>/dev/null || true
  # `path` entries are read in place, so a local edit is seen without re-installing.
  while IFS= read -r e; do
    [ -n "$(jf "$e" path)" ] || continue
    name="$(jf "$e" name)"
    local pdir
    pdir="${CFG_DIR}/$(jf "$e" path)"
    if [ -n "$(jf "$e" subpath)" ]; then pdir="${pdir}/$(jf "$e" subpath)"; fi
    collect_crds "$e" "${pdir}" "crds/${name}" >"${work}/crds/path-${name}.yaml"
    [ -s "${work}/crds/path-${name}.yaml" ] || die 5 "crds/${name}: the path holds no CustomResourceDefinition"
  done < <(entries crds)

  local schema_rc=0 policy_rc=0

  say "🔎 stage 1: schemas (Kubernetes ${K8S} core + installed CRDs), unknown fields refused"
  # An empty KUBECONFIG keeps the validator from ever talking to a cluster.
  KUBECONFIG="${CC_TMP}/no-kubeconfig" kubectl-validate "$@" --local-crds "${work}/crds" --version "${K8S}" ||
    schema_rc=$?

  local count
  count="$(jq '(.policies // []) | length' <<<"${CFG_JSON}")"
  if [ "${count}" -eq 0 ]; then
    say "⏭️  stage 2: no policies configured; skipped"
  else
    say "🔎 stage 2: ValidatingAdmissionPolicy definitions via kyverno apply"
    while IFS= read -r e; do
      name="$(jf "$e" name)"
      local chart sub
      if [ -n "$(jf "$e" path)" ]; then
        chart="${CFG_DIR}/$(jf "$e" path)"
        sub="$(jf "$e" subpath)"
        [ -z "${sub}" ] || chart="${chart}/${sub}"
      else
        chart="${CC_DIR}/policies/${name}"
      fi
      [ -f "${chart}/Chart.yaml" ] || die 5 "policies/${name}: no Chart.yaml in ${chart}"
      # Only definitions: a ValidatingAdmissionPolicyBinding is never handed to kyverno.
      # `values` / `chartValues` of the entry reach the policy chart here (the policy chart is
      # never copied or edited): this is how a stack's label prefix gets to it.
      local vals hargs=()
      vals="$(policy_values_json "$e")" || exit "$?"
      if [ "${vals}" != "{}" ]; then
        printf '%s\n' "${vals}" >"${work}/${name}.values.json"
        hargs=(--values "${work}/${name}.values.json")
      fi
      helm template "${name}" "${chart}" "${hargs[@]}" >"${work}/${name}.rendered.yaml" 2>"${work}/${name}.helm.log" || {
        cat "${work}/${name}.helm.log" >&2
        die 5 "policies/${name}: helm template failed"
      }
      yq 'select(.kind == "ValidatingAdmissionPolicy")' "${work}/${name}.rendered.yaml" >"${work}/vaps/${name}.yaml"
      [ -s "${work}/vaps/${name}.yaml" ] || die 5 "policies/${name}: the chart rendered no ValidatingAdmissionPolicy"
    done < <(entries policies)
    run_kyverno "${work}" "$@" || policy_rc=$?
  fi

  if [ "${schema_rc}" -ne 0 ] || [ "${policy_rc}" -ne 0 ]; then
    say "❌ chart-checks: refused (schema stage exit ${schema_rc}, policy stage exit ${policy_rc})"
    return 1
  fi
  say "✅ chart-checks: $# file(s) passed"
}

# ----------------------------------------------------------------- chart ----
# The commands below render a Helm chart and check it the way a consumer repository does:
# `render` + `lint` + `install` + `check` per values stack + the first-party label check.

CHART_DIR=""   # --chart: the chart whose stack values feed `chartValues`
CHART_STACK="" # --stack: which values stack of that chart (default / <name> of values.<name>.yaml)
RELEASE_FLAG=""

chart_cfg() { jq -r --arg k "$1" '.chart[$k] // empty' <<<"${CFG_JSON}"; }

chart_name() { yq -r '.name' "$1/Chart.yaml" 2>/dev/null || true; }

need_chart_dir() { # dir
  [ -f "$1/Chart.yaml" ] || die 2 "not a chart directory (no Chart.yaml): $1"
}

# The values of one stack: values.yaml, with values.<stack>.yaml merged over it (the same
# merge helm applies for --values values.yaml --values values.<stack>.yaml). Writes a file.
stack_values_file() { # chart stack -> path
  local chart="$1" stack="$2" out overlay="$1/values.$2.yaml"
  out="${CC_TMP}/stack-$(basename "${chart}")-${stack}.yaml"
  if [ "${stack}" = "default" ] || [ ! -e "${overlay}" ]; then
    if [ -f "${chart}/values.yaml" ]; then cp "${chart}/values.yaml" "${out}"; else : >"${out}"; fi
  else
    yq ea 'select(fi == 0) * select(fi == 1)' "${chart}/values.yaml" "${overlay}" >"${out}"
  fi
  printf '%s' "${out}"
}

# The label prefix of one stack, read with the yq path of `chart.labelPrefix`.
stack_prefix() { # chart stack
  local f path v
  path="$(chart_cfg labelPrefix)"
  f="$(stack_values_file "$1" "$2")"
  v="$(yq -r "${path}" "${f}" 2>/dev/null || true)"
  if [ -z "${v}" ] || [ "${v}" = "null" ]; then
    printf '❌ chart-checks: %s/values.yaml has no labelPrefix (chart.labelPrefix: %s, stack %s)\n' "$1" "${path}" "$2" >&2
    return 1
  fi
  printf '%s' "${v}"
}

# The values handed to one policy chart's `helm template` as JSON: its literal `values`, with
# each `chartValues` entry (policy value name -> yq path) read from the chart's stack values.
policy_values_json() { # entry
  local e="$1" name out k path v f
  name="$(jf "$e" name)"
  out="$(jq -c '.values // {}' <<<"$e")"
  if [ "$(jq '(.chartValues // {}) | length' <<<"$e")" -gt 0 ]; then
    [ -n "${CHART_DIR}" ] || die 2 "policies/${name}: chartValues needs the chart; run it through check-chart, or pass --chart <dir> [--stack <name>] to check"
    f="$(stack_values_file "${CHART_DIR}" "${CHART_STACK:-default}")"
    while IFS=$'\t' read -r k path; do
      v="$(yq -o=json -I=0 "${path}" "${f}" 2>/dev/null || true)"
      if [ -z "${v}" ] || [ "${v}" = "null" ]; then
        die 5 "policies/${name}: chartValues.${k}: ${path} is empty in ${CHART_DIR} (stack ${CHART_STACK:-default})"
      fi
      out="$(jq -c --arg k "${k}" --argjson v "${v}" '. + {($k): $v}' <<<"${out}")"
    done < <(jq -r '.chartValues | to_entries[] | [.key, .value] | @tsv' <<<"$e")
  fi
  printf '%s' "${out}"
}

# render <chart>: chart (values.yaml) and each values.<name>.yaml overlay -> <renderDir>/<chart>/<stack>.yaml
cmd_render() { # chart
  local chart="$1" release out stack overlay base
  need_chart_dir "${chart}"
  load_config
  base="$(basename "${chart}")"
  release="${RELEASE_FLAG:-$(chart_cfg release)}"
  [ -n "${release}" ] || release="$(chart_name "${chart}")"
  out="${CFG_DIR}/$(chart_cfg renderDir)"
  [ -n "$(chart_cfg renderDir)" ] || out="${CFG_DIR}/infra/rendered"
  out="${out}/${base}"
  # `render` deletes the *.yaml files of its output directory: it must never be the chart itself.
  mkdir -p "${out}"
  local real_out real_chart
  real_out="$(cd "${out}" && pwd -P)"
  real_chart="$(cd "${chart}" && pwd -P)"
  case "${real_out}/" in
  "${real_chart}/"*) die 3 "${CONFIG}: chart.renderDir resolves to ${real_out}, inside the chart ${real_chart}; refusing to render over the chart's own files" ;;
  esac
  if [ -e "${chart}/values.default.yaml" ]; then
    die 1 "${chart}/values.default.yaml: 'default' is the stack of values.yaml alone, so no overlay may be named default"
  fi
  find "${out}" -maxdepth 1 -name '*.yaml' -delete
  helm template "${release}" "${chart}" >"${out}/default.yaml" || die 1 "helm template failed for ${chart}"
  for overlay in "${chart}"/values.*.yaml; do
    [ -e "${overlay}" ] || continue
    stack="$(basename "${overlay}" .yaml)"
    stack="${stack#values.}"
    helm template "${release}" "${chart}" --values "${chart}/values.yaml" --values "${overlay}" >"${out}/${stack}.yaml" ||
      die 1 "helm template failed for ${chart} with ${overlay}"
  done
  RENDERED_DIR="${out}"
  say "✅ Rendered ${chart} into ${out}"
}

cmd_lint() { # chart
  need_chart_dir "$1"
  helm lint "$1" || die 1 "helm lint refused ${1}"
}

# labels <chart> <rendered file>...: every object from the chart's OWN templates (not its
# subcharts), of any kind, carries every configured label under the stack's prefix. Helm test
# hooks are exempt. Helm marks each document with "# Source: <chart>/templates/..." (own) or
# "# Source: <chart>/charts/<sub>/..." (subchart); it is turned into a key the filter selects on.
cmd_labels() { # chart files...
  local chart="$1" file stack prefix name missing labels
  shift
  need_chart_dir "${chart}"
  load_config
  [ -n "$(chart_cfg labelPrefix)" ] || die 3 "${CONFIG}: the label check needs chart.labelPrefix (a yq path in the chart values)"
  name="$(chart_name "${chart}")"
  labels="$(jq -c '.chart.labels // ["landscape","platform","service","module"]' <<<"${CFG_JSON}")"
  for file in "$@"; do
    [ -f "${file}" ] || die 2 "not a file: ${file}"
    stack="$(basename "${file}" .yaml)"
    prefix="$(stack_prefix "${chart}" "${stack}")" || return 1
    missing="$(
      awk '/^# Source: /{ sub(/^# Source: /, ""); printf "__source: \"%s\"\n", $0; next } { print }' "${file}" |
        yq ea -o=json -I=0 '.' - |
        jq -r --arg prefix "${prefix}" --arg own "${name}/templates/" --argjson labels "${labels}" '
          select(type == "object" and .kind != null and ((.__source // "") | startswith($own)))
          | select(((.metadata.annotations // {})["helm.sh/hook"] // "" | split(",") | map(gsub("^\\s+|\\s+$"; ""))
              | any(. == "test" or . == "test-success" or . == "test-failure")) | not)
          | select((.metadata.labels // {}) as $l | $labels | map($prefix + "/" + .) | any(. as $k | $l | has($k) | not))
          | .__source + " (" + .kind + " " + (.metadata.name // "?") + ")"'
    )" || die 5 "the label check helper (awk, yq or jq) failed on ${file}; refusing to pass unchecked"
    if [ -n "${missing}" ]; then
      printf '❌ %s: first-party objects without all %s/ labels (%s):\n' "${file}" "${prefix}" "$(jq -r 'join(", ")' <<<"${labels}")" >&2
      while IFS= read -r line; do printf '   - %s\n' "${line}" >&2; done <<<"${missing}"
      return 1
    fi
  done
  say "✅ chart-checks: first-party labels present in $# file(s)"
}

# check-chart <chart>: render, helm lint, install, then per rendered stack `check` (the policy
# charts get that stack's chartValues) and the first-party label check; stops at the first refusal.
cmd_check_chart() { # chart
  local chart="$1" f stack
  need_chart_dir "${chart}"
  cmd_render "${chart}"
  cmd_lint "${chart}"
  local files=()
  mapfile -t files < <(find "${RENDERED_DIR}" -maxdepth 1 -name '*.yaml' | LC_ALL=C sort)
  [ "${#files[@]}" -gt 0 ] || die 1 "no rendered YAML in ${RENDERED_DIR}"
  cmd_install
  for f in "${files[@]}"; do
    stack="$(basename "${f}" .yaml)"
    say "== stack ${stack} =="
    CHART_DIR="${chart}" CHART_STACK="${stack}" cmd_check "${f}" || return $?
    if [ -n "$(chart_cfg labelPrefix)" ]; then
      cmd_labels "${chart}" "${f}"
    fi
  done
  say "✅ Helm render checks passed"
}

# ------------------------------------------------------------------ main ----

main() {
  local cmd="" files=()
  [ "$#" -gt 0 ] || {
    usage >&2
    exit 2
  }
  case "$1" in
  -h | --help)
    usage
    exit 0
    ;;
  --version)
    printf 'chart-checks %s\n' "${CHART_CHECKS_VERSION}"
    exit 0
    ;;
  install | check | render | lint | labels | check-chart)
    cmd="$1"
    shift
    ;;
  *)
    usage >&2
    die 2 "unknown command: $1"
    ;;
  esac
  while [ "$#" -gt 0 ]; do
    case "$1" in
    -c | --config)
      [ "$#" -ge 2 ] || die 2 "$1 needs a path"
      CONFIG="$2"
      shift 2
      ;;
    --chart)
      [ "$#" -ge 2 ] || die 2 "$1 needs a chart directory"
      CHART_DIR="$2"
      shift 2
      ;;
    --stack)
      [ "$#" -ge 2 ] || die 2 "$1 needs a stack name"
      CHART_STACK="$2"
      shift 2
      ;;
    --release)
      [ "$#" -ge 2 ] || die 2 "$1 needs a release name"
      RELEASE_FLAG="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    --)
      shift
      files+=("$@")
      break
      ;;
    -*) die 2 "unknown option: $1" ;;
    *)
      files+=("$1")
      shift
      ;;
    esac
  done
  CC_TMP="$(mktemp -d)"
  # helm writes caches/config next to HOME; keep it inside our own scratch directory.
  export HELM_CACHE_HOME="${CC_TMP}/helm/cache" HELM_CONFIG_HOME="${CC_TMP}/helm/config" HELM_DATA_HOME="${CC_TMP}/helm/data"
  case "${cmd}" in
  install)
    [ "${#files[@]}" -eq 0 ] || die 2 "install takes no files"
    cmd_install
    ;;
  check)
    if [ -n "${CHART_STACK}" ] && [ -z "${CHART_DIR}" ]; then die 2 "--stack needs --chart"; fi
    cmd_check "${files[@]}"
    ;;
  render | lint | check-chart)
    [ "${#files[@]}" -eq 1 ] || die 2 "${cmd} takes exactly one chart directory"
    case "${cmd}" in
    render) cmd_render "${files[0]}" ;;
    lint) cmd_lint "${files[0]}" ;;
    check-chart) cmd_check_chart "${files[0]}" ;;
    esac
    ;;
  labels)
    [ "${#files[@]}" -ge 2 ] || die 2 "labels takes a chart directory and at least one rendered file"
    cmd_labels "${files[@]}"
    ;;
  esac
}

main "$@"
