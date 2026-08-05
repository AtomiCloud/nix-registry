# shellcheck shell=bash
#
# dlint — repo-agnostic repository linters.
#
# This file is INLINED into the packaged entrypoint (see ./default.nix), which
# supplies the shebang, the runtime PATH and DLINT_VERSION, and which shellchecks
# the result at build time. It therefore carries a `shellcheck shell=` directive
# instead of a shebang, and must stay self-contained. That directive exists for
# linting this file on its own — in the packaged script it is inert, because the
# generated shebang has already settled the dialect by the time it appears.
#
# Every repository-specific fact comes from the consuming repository's own
# configuration file, never from a constant baked into this script.
#
# Two conventions hold throughout, both learned from real defects:
#
#   * `var="$(cmd)"` under `set -e` aborts BEFORE any guard that inspects
#     `var`, so a refusal can exit non-zero with no message at all. Every
#     capture below therefore carries its own `||` handler, and the config
#     readers print their reason inside the subshell before exiting.
#   * A negative assertion must never be wrapped in an existence check. An
#     absent subject, an absent configuration section and an empty subject list
#     each get their own loud outcome; none of them is a pass. Where a repository
#     can legitimately have zero subjects, saying so is a declaration
#     (`requireSubjects: false`), never a silent default.

set -euo pipefail

DLINT_VERSION="${DLINT_VERSION:-0.0.0-dev}"
DLINT_CONFIG_DEFAULT=".dlint.json"

# 0 conforms, or the check is explicitly disabled
readonly EXIT_VIOLATION=1     # the repository breaks the rule
readonly EXIT_USAGE=2         # wrong invocation
readonly EXIT_CONFIG_ABSENT=3 # nothing declared the subject; never a pass
readonly EXIT_CONFIG_INVALID=4
readonly EXIT_TOOL=5 # dlint could not complete the inspection

CHECK=""
CFG_FILE=""
WORK_DIR=""

# --------------------------------------------------------------------------- #
# reporting
# --------------------------------------------------------------------------- #

log_info() { printf 'ℹ️ %s\n' "$1"; }
ok() { printf '✅ %s\n' "$1"; }
skipped() { printf '⏭️ %s\n' "$1"; }

refuse() {
  printf '❌ %s\n' "$1" >&2
  exit "${EXIT_VIOLATION}"
}

usage_error() {
  printf '❌ %s\n' "$1" >&2
  printf "Run 'dlint --help' for the checks dlint supports.\n" >&2
  exit "${EXIT_USAGE}"
}

config_absent() {
  printf '❌ %s\n' "$1" >&2
  exit "${EXIT_CONFIG_ABSENT}"
}

config_invalid() {
  printf '❌ %s\n' "$1" >&2
  exit "${EXIT_CONFIG_INVALID}"
}

tool_failure() {
  printf '❌ %s\n' "$1" >&2
  exit "${EXIT_TOOL}"
}

usage() {
  cat <<EOF
dlint ${DLINT_VERSION} — repo-agnostic repository linters

Usage:
  dlint <check> [args]
  dlint --help
  dlint --version

Checks (there are exactly six; there is no 'all'):
  action-pins <trusted|non-trusted>
      Every GitHub Action reference carries the pin its authored trust
      classification demands: a major tag for trusted actions, an exact 40-hex
      SHA plus a trailing tag comment for non-trusted ones.
  exec-bits
      Every tracked shell script is executable.
  ci-wiring
      Every orchestrator job calls a repository-local reusable workflow, and
      every reusable workflow calls a CI entrypoint that exists and is
      executable.
  skills-fresh
      Regenerate the vendored tree, then refuse if the worktree moved.
  toolchain-smoke
      Verify that every binary a repository declares for one named shell resolves
      in the environment where dlint is invoked.
  no-custom-derivations
      Every declared nix file stays a plain declarative list: no custom derivation
      builder appears in it. The vocabulary of builders is printed with the result.

Configuration:
  All six checks read ONE file: \$DLINT_CONFIG, or ./${DLINT_CONFIG_DEFAULT}.
  dlint is run from the repository root and reads every path relative to it.

    {
      "schemaVersion": 1,
      "checks": {
        "action-pins":  { "trustMap": "config/action-trust.json" },
        "exec-bits":    {},
        "ci-wiring":    {
          "entrypointPattern": "scripts/ci/[A-Za-z0-9._-]+[.]sh",
          "orchestrators": [".github/workflows/ci.yaml"]
        },
        "skills-fresh": {
          "regenerate": "bash scripts/local/skills-sync.sh",
          "paths": [".claude/skills/vendor"],
          "ignore": [".claude/skills/vendor/.gitkeep"]
        },
        "toolchain-smoke": {
          "shell": "default",
          "binaries": ["bash", "git"]
        }
      }
    }

  A check with no section in '.checks' is an ERROR, not a pass. To turn a check
  off, say so: "exec-bits": false. Likewise, a check that finds NO subject at all
  refuses; if a repository legitimately has none, say that too:
  "exec-bits": { "requireSubjects": false }.

Exit codes:
  0  conforms, or the check is explicitly disabled
  1  the repository breaks the rule
  2  usage error
  3  the configuration, or a subject it declares, is absent
  4  the configuration is invalid
  5  dlint could not complete the inspection
EOF
}

# --------------------------------------------------------------------------- #
# configuration
# --------------------------------------------------------------------------- #

require_git_repo() {
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 ||
    tool_failure "dlint ${CHECK} needs a git work tree; '${PWD}' is not inside one"
}

# Loads the configuration and selects this check's section. Exits 3 when the
# file or the section is absent, 4 when either is malformed, and 0 when the
# section is an explicit `false`.
load_check_config() {
  CHECK="$1"
  CFG_FILE="${DLINT_CONFIG:-${DLINT_CONFIG_DEFAULT}}"

  [ -f "${CFG_FILE}" ] ||
    config_absent "dlint configuration '${CFG_FILE}' is missing, so dlint cannot know this repository's layout. Create it (see 'dlint --help') or point DLINT_CONFIG at it. dlint never guesses, and never passes a check it could not configure."

  jq empty "${CFG_FILE}" >/dev/null 2>&1 ||
    config_invalid "dlint configuration '${CFG_FILE}' is not valid JSON"

  local version=""
  version="$(jq -r '.schemaVersion // empty' "${CFG_FILE}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.schemaVersion'"
  [ "${version}" = "1" ] ||
    config_invalid "dlint configuration '${CFG_FILE}': '.schemaVersion' must be 1, found '${version:-<absent>}'"

  local checks_type=""
  checks_type="$(jq -r '.checks | type' "${CFG_FILE}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks'"
  [ "${checks_type}" = "object" ] ||
    config_invalid "dlint configuration '${CFG_FILE}': '.checks' must be an object, found ${checks_type}"

  local section_type=""
  section_type="$(jq -r --arg c "${CHECK}" '.checks[$c] | type' "${CFG_FILE}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"]'"

  case "${section_type}" in
  object) ;;
  null)
    config_absent "dlint configuration '${CFG_FILE}' declares no '.checks[\"${CHECK}\"]' section, so the ${CHECK} check has no subject. Declare it, or disable the check on purpose with '\"${CHECK}\": false'. An absent section is never a pass."
    ;;
  boolean)
    local enabled=""
    enabled="$(jq -r --arg c "${CHECK}" '.checks[$c]' "${CFG_FILE}")" ||
      config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"]'"
    [ "${enabled}" = "false" ] ||
      config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${CHECK}\"]' is true, which configures nothing. Use an object, or false to disable the check."
    skipped "dlint ${CHECK} is disabled by '\"${CHECK}\": false' in '${CFG_FILE}'"
    exit 0
    ;;
  *)
    config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${CHECK}\"]' must be an object, or false to disable the check; found ${section_type}"
    ;;
  esac
}

# cfg_string <key> [default] -> value on stdout.
# Call as a plain assignment: the reason is printed inside the subshell and
# `set -e` propagates the exit code, so a bad key can never read as an empty
# value.
cfg_string() {
  local key="$1" type="" value=""
  type="$(jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k] | type' "${CFG_FILE}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"].${key}'"

  if [ "${type}" = "null" ]; then
    if [ "$#" -ge 2 ]; then
      printf '%s' "$2"
      return 0
    fi
    config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${CHECK}\"].${key}' is required. It is repository-specific, so dlint has no default for it."
  fi

  [ "${type}" = "string" ] ||
    config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${CHECK}\"].${key}' must be a string, found ${type}"
  value="$(jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k]' "${CFG_FILE}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"].${key}'"
  [ -n "${value}" ] ||
    config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${CHECK}\"].${key}' must not be empty"
  printf '%s' "${value}"
}

# cfg_array <key> [optional] -> one entry per line on stdout.
# Call it directly with a redirection, NOT through $( ) or a process
# substitution: an exit from here must take the whole run down rather than
# leave a caller holding an empty list.
cfg_array() {
  local key="$1" type=""
  type="$(jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k] | type' "${CFG_FILE}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"].${key}'"

  if [ "${type}" = "null" ]; then
    [ "$#" -ge 2 ] ||
      config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${CHECK}\"].${key}' is required. It is repository-specific, so dlint has no default for it."
    return 0
  fi

  [ "${type}" = "array" ] ||
    config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${CHECK}\"].${key}' must be an array of strings, found ${type}"
  jq -e --arg c "${CHECK}" --arg k "${key}" \
    '([.checks[$c][$k][] | select((type != "string") or (length == 0))] | length) == 0' \
    "${CFG_FILE}" >/dev/null 2>&1 ||
    config_invalid "dlint configuration '${CFG_FILE}': every entry of '.checks[\"${CHECK}\"].${key}' must be a non-empty string"
  jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k][]' "${CFG_FILE}" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"].${key}'"
}

# cfg_bool <key> <default> -> "true" or "false" on stdout.
cfg_bool() {
  local key="$1" fallback="$2" type=""
  type="$(jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k] | type' "${CFG_FILE}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"].${key}'"

  if [ "${type}" = "null" ]; then
    printf '%s' "${fallback}"
    return 0
  fi

  [ "${type}" = "boolean" ] ||
    config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${CHECK}\"].${key}' must be true or false, found ${type}"
  jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k]' "${CFG_FILE}" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"].${key}'"
}

count_lines() {
  local count=""
  count="$(wc -l <"$1" | tr -d '[:space:]')" ||
    tool_failure "dlint ${CHECK}: could not count the lines of '$1'"
  printf '%s' "${count:-0}"
}

# --------------------------------------------------------------------------- #
# action-pins
# --------------------------------------------------------------------------- #

check_action_pins() {
  local mode="${1:-}"
  [ "$#" -le 1 ] ||
    usage_error "action-pins takes exactly one argument, the trust class to enforce"
  case "${mode}" in
  trusted | non-trusted) ;;
  "") usage_error "action-pins needs a mode: 'trusted' or 'non-trusted'" ;;
  *) usage_error "action-pins mode must be 'trusted' or 'non-trusted', got '${mode}'" ;;
  esac

  load_check_config action-pins

  local map_file="" workflows_dir="" require_subjects=""
  map_file="$(cfg_string trustMap)"
  workflows_dir="$(cfg_string workflowsDir '.github/workflows')"
  require_subjects="$(cfg_bool requireSubjects true)"

  [ -f "${map_file}" ] ||
    config_absent "action-pins: the trust map '${map_file}' declared in '${CFG_FILE}' does not exist. Every action reference needs an authored trust classification, so dlint refuses rather than pass an unclassified tree."
  [ -d "${workflows_dir}" ] ||
    config_absent "action-pins: the workflow directory '${workflows_dir}' declared in '${CFG_FILE}' does not exist"

  jq -e '.schemaVersion == 1
      and (.actions | type == "object")
      and ([.actions[] | select(. != "trusted" and . != "non-trusted")] | length == 0)' \
    "${map_file}" >/dev/null 2>&1 ||
    config_invalid "action-pins: '${map_file}' has an invalid schema or classification; it needs schemaVersion 1 and an '.actions' object mapping every action to \"trusted\" or \"non-trusted\""

  local matches="${WORK_DIR}/action-uses.txt" rg_status=0
  rg -n --no-heading --hidden \
    '^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+[^[:space:]]+' \
    "${workflows_dir}" >"${matches}" || rg_status=$?
  [ "${rg_status}" -le 1 ] ||
    tool_failure "action-pins: could not scan '${workflows_dir}' for action references (rg exit ${rg_status})"

  local file="" line="" body="" raw="" reference="" action="" ref="" comment=""
  local classification="" inspected=0 in_mode=0
  while IFS=: read -r file line body; do
    [ -n "${body:-}" ] || continue

    raw="$(printf '%s' "${body#*uses:}" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')" ||
      tool_failure "action-pins: could not normalise ${file}:${line}"
    # A local action (`uses: ./...`) has nothing to pin.
    [ "${raw#./}" = "${raw}" ] || continue

    inspected=$((inspected + 1))
    reference="$(printf '%s' "${raw%%#*}" | sed -E 's/[[:space:]]+$//')" ||
      tool_failure "action-pins: could not normalise ${file}:${line}"
    reference="${reference#\"}"
    reference="${reference%\"}"
    reference="${reference#\'}"
    reference="${reference%\'}"
    action="${reference%@*}"
    ref="${reference##*@}"

    classification="$(jq -r --arg action "${action}" '.actions[$action] // empty' "${map_file}")" ||
      config_invalid "action-pins: could not read the classification of '${action}' from '${map_file}'"
    [ -n "${classification}" ] ||
      refuse "${file}:${line}: action '${action}' has no authored trust classification"
    [ "${classification}" = "${mode}" ] || continue
    in_mode=$((in_mode + 1))

    if [ "${mode}" = "trusted" ]; then
      [[ ${ref} =~ ^v[1-9][0-9]*$ ]] ||
        refuse "${file}:${line}: trusted action '${action}' must use a major pin, got '${ref}'"
    else
      [[ ${ref} =~ ^[0-9a-fA-F]{40}$ ]] ||
        refuse "${file}:${line}: non-trusted action '${action}' must use an exact SHA, got '${ref}'"
      comment="${raw#*#}"
      [ "${comment}" != "${raw}" ] ||
        refuse "${file}:${line}: non-trusted SHA pin needs its tag as a trailing comment"
      [[ ${comment} =~ v[0-9]+([.][0-9x]+)* ]] ||
        refuse "${file}:${line}: trailing comment must name the source tag"
    fi
  done <"${matches}"

  log_info "action references inspected: ${inspected} (${mode}: ${in_mode})"
  if [ "${inspected}" -eq 0 ] && [ "${require_subjects}" = "true" ]; then
    refuse "no action reference under ${workflows_dir}; the ${mode} pin check would pass vacuously. If this repository legitimately has none, declare it: '.checks[\"action-pins\"].requireSubjects': false"
  fi
  ok "${mode} action pins conform"
}

# --------------------------------------------------------------------------- #
# exec-bits
# --------------------------------------------------------------------------- #

check_exec_bits() {
  [ "$#" -eq 0 ] || usage_error "exec-bits takes no arguments, got '$1'"

  load_check_config exec-bits
  require_git_repo

  local require_subjects=""
  require_subjects="$(cfg_bool requireSubjects true)"

  local globs_file="${WORK_DIR}/exec-globs.txt"
  cfg_array globs optional >"${globs_file}"
  local -a globs=()
  mapfile -t globs <"${globs_file}"
  # '*.sh' is a language convention rather than a repository one, so it is the
  # one default here; anything repo-shaped must be declared.
  [ "${#globs[@]}" -gt 0 ] || globs=('*.sh')

  local listing="${WORK_DIR}/tracked-shells.txt"
  git ls-files -z -- "${globs[@]}" >"${listing}" ||
    tool_failure "exec-bits: could not list tracked files (git ls-files)"

  local file="" count=0
  while IFS= read -r -d '' file; do
    count=$((count + 1))
    [ -e "${file}" ] ||
      refuse "'${file}' is tracked but missing from the worktree"
    [ -x "${file}" ] ||
      refuse "'${file}' is tracked but not executable"
  done <"${listing}"

  log_info "tracked shell scripts inspected: ${count} (globs: ${globs[*]})"
  if [ "${count}" -eq 0 ] && [ "${require_subjects}" = "true" ]; then
    refuse "no tracked file matches ${globs[*]}; the executable-shell check would pass vacuously. If this repository legitimately has none, declare it: '.checks[\"exec-bits\"].requireSubjects': false"
  fi
  ok "Tracked shell scripts are executable"
}

# --------------------------------------------------------------------------- #
# ci-wiring
# --------------------------------------------------------------------------- #

check_ci_wiring() {
  [ "$#" -eq 0 ] || usage_error "ci-wiring takes no arguments, got '$1'"

  load_check_config ci-wiring

  local workflows_dir="" pattern=""
  workflows_dir="$(cfg_string workflowsDir '.github/workflows')"
  pattern="$(cfg_string entrypointPattern)"

  local orchestrators_file="${WORK_DIR}/orchestrators.txt"
  cfg_array orchestrators >"${orchestrators_file}"
  local -a orchestrators=()
  mapfile -t orchestrators <"${orchestrators_file}"
  [ "${#orchestrators[@]}" -gt 0 ] ||
    config_invalid "ci-wiring: '.checks[\"ci-wiring\"].orchestrators' must name at least one workflow; an empty list would let this check pass without inspecting anything"

  [ -d "${workflows_dir}" ] ||
    config_absent "ci-wiring: the workflow directory '${workflows_dir}' declared in '${CFG_FILE}' does not exist"

  # 1. Every CI entrypoint a workflow names exists and is executable.
  local raw_refs="${WORK_DIR}/entrypoint-refs.raw" refs="${WORK_DIR}/entrypoint-refs.txt"
  local rg_status=0
  rg -o --no-filename --hidden -- "${pattern}" "${workflows_dir}" >"${raw_refs}" || rg_status=$?
  [ "${rg_status}" -le 1 ] ||
    tool_failure "ci-wiring: could not scan '${workflows_dir}' for CI entrypoints (rg exit ${rg_status})"
  sort -u "${raw_refs}" >"${refs}" ||
    tool_failure "ci-wiring: could not sort the CI entrypoint references"

  local script=""
  while IFS= read -r script; do
    [ -n "${script}" ] || continue
    [ -f "${script}" ] ||
      refuse "workflow references missing script '${script}'"
    [ -x "${script}" ] ||
      refuse "workflow script '${script}' is not executable"
  done <"${refs}"

  # 2. Every orchestrator job resolves to a repository-local reusable workflow
  #    that calls one of those entrypoints.
  local orchestrator="" jobs_file="${WORK_DIR}/jobs.txt"
  local job="" reusable="" target="" job_count=0 total_jobs=0 match_status=0
  for orchestrator in "${orchestrators[@]}"; do
    [ -f "${orchestrator}" ] ||
      refuse "declared orchestrator '${orchestrator}' does not exist"

    yq -r '(.jobs // {}) | to_entries[] | [.key, (.value.uses // "")] | @tsv' \
      "${orchestrator}" >"${jobs_file}" ||
      tool_failure "ci-wiring: could not read the jobs of '${orchestrator}' (yq)"

    job_count=0
    while IFS=$'\t' read -r job reusable; do
      [ -n "${job}" ] || continue
      job_count=$((job_count + 1))

      [ -n "${reusable}" ] ||
        refuse "'${orchestrator}' job '${job}' must call a reusable workflow"
      case "${reusable}" in
      "./${workflows_dir}/"*) ;;
      *) refuse "'${orchestrator}' job '${job}' must call a repository-local reusable workflow under './${workflows_dir}/', got '${reusable}'" ;;
      esac

      target="${reusable#./}"
      [ -f "${target}" ] ||
        refuse "'${orchestrator}' references missing reusable workflow '${target}'"

      match_status=0
      rg -q -- "${pattern}" "${target}" || match_status=$?
      [ "${match_status}" -le 1 ] ||
        tool_failure "ci-wiring: could not scan '${target}' for a CI entrypoint (rg exit ${match_status})"
      [ "${match_status}" -eq 0 ] ||
        refuse "reusable workflow '${target}' does not call a CI entrypoint matching '${pattern}'"
    done <"${jobs_file}"

    # An orchestrator with no jobs would satisfy every assertion above without
    # inspecting anything, which is the vacuous pass this check exists to avoid.
    [ "${job_count}" -gt 0 ] ||
      refuse "'${orchestrator}' declares no jobs, so its wiring cannot be checked"
    total_jobs=$((total_jobs + job_count))
  done

  local ref_count=""
  ref_count="$(count_lines "${refs}")"
  log_info "CI entrypoint references: ${ref_count}"
  log_info "orchestrator jobs inspected: ${total_jobs} (orchestrators: ${#orchestrators[@]})"
  ok "Workflow jobs resolve to existing CI scripts"
}

# --------------------------------------------------------------------------- #
# skills-fresh
# --------------------------------------------------------------------------- #

check_skills_fresh() {
  [ "$#" -eq 0 ] || usage_error "skills-fresh takes no arguments, got '$1'"

  load_check_config skills-fresh
  require_git_repo

  local regenerate=""
  regenerate="$(cfg_string regenerate)"

  local paths_file="${WORK_DIR}/subject-paths.txt"
  cfg_array paths >"${paths_file}"
  local -a paths=()
  mapfile -t paths <"${paths_file}"
  [ "${#paths[@]}" -gt 0 ] ||
    config_invalid "skills-fresh: '.checks[\"skills-fresh\"].paths' must name at least one tracked path; an empty list would let this check pass without a subject"

  local ignore_file="${WORK_DIR}/ignore.txt"
  cfg_array ignore optional >"${ignore_file}"

  # The consuming repository owns the regeneration; dlint owns "regenerate,
  # then refuse if the tree moved".
  local regen_status=0
  bash -c "${regenerate}" || regen_status=$?
  [ "${regen_status}" -eq 0 ] ||
    refuse "the regeneration command '${regenerate}' failed (exit ${regen_status}); freshness cannot be judged from a failed regeneration"

  local tracked="${WORK_DIR}/tracked.txt"
  git ls-files -- "${paths[@]}" >"${tracked}" ||
    tool_failure "skills-fresh: could not list tracked subjects (git ls-files)"

  # Ignored entries (a .gitkeep keeps a directory alive) can never witness
  # stale content, so they are not subjects. grep answers 1 for "no line
  # survived" and 2 or more for a real failure; a broken filter must never read
  # as "nothing to check".
  local subjects="${WORK_DIR}/subjects.txt" filter_status=0
  if [ -s "${ignore_file}" ]; then
    grep -F -x -v -f "${ignore_file}" "${tracked}" >"${subjects}" || filter_status=$?
    [ "${filter_status}" -le 1 ] ||
      tool_failure "skills-fresh: could not filter the tracked subjects (grep exit ${filter_status})"
  else
    cp "${tracked}" "${subjects}" ||
      tool_failure "skills-fresh: could not copy the tracked subject list"
  fi

  local subject_count=""
  subject_count="$(count_lines "${subjects}")"
  log_info "tracked subjects: ${subject_count} (paths: ${paths[*]})"
  if [ "${subject_count}" -eq 0 ]; then
    refuse "no tracked subject under ${paths[*]}; the freshness check would pass vacuously"
  fi

  local porcelain="${WORK_DIR}/porcelain.txt"
  git status --porcelain=v1 --untracked-files=all -- "${paths[@]}" >"${porcelain}" ||
    tool_failure "skills-fresh: could not inspect the subject status (git status)"

  # The index is the proposed tree, so an index-only entry (`A `, `M `, `R `) is
  # the canonical regeneration a commit is about to record. Real drift is
  # anything the worktree disagrees on after regeneration: a nonblank worktree
  # column, which also covers every `??` untracked regeneration.
  local drift="${WORK_DIR}/drift.txt" drift_status=0
  grep -E '^.[^ ] ' "${porcelain}" >"${drift}" || drift_status=$?
  [ "${drift_status}" -le 1 ] ||
    tool_failure "skills-fresh: could not filter the subject status (grep exit ${drift_status})"

  local status_count="" drift_count=""
  status_count="$(count_lines "${porcelain}")"
  drift_count="$(count_lines "${drift}")"
  log_info "subject status entries: ${status_count}"
  log_info "subject drift entries: ${drift_count}"

  if [ "${drift_count}" -ne 0 ]; then
    printf '❌ %s\n' "vendored tree is stale; re-run '${regenerate}' and stage the result:" >&2
    cat "${drift}" >&2
    exit "${EXIT_VIOLATION}"
  fi

  ok "Vendored tree is fresh"
}

# --------------------------------------------------------------------------- #
# toolchain-smoke
# --------------------------------------------------------------------------- #

check_toolchain_smoke() {
  [ "$#" -eq 0 ] || usage_error "toolchain-smoke takes no arguments, got '$1'"

  load_check_config toolchain-smoke

  local shell=""
  shell="$(cfg_string shell)"
  [[ ${shell} =~ ^[A-Za-z0-9._-]+$ ]] ||
    config_invalid "toolchain-smoke: '.checks[\"toolchain-smoke\"].shell' must name one shell with letters, digits, '.', '_' or '-', got '${shell}'"

  local binaries_file="${WORK_DIR}/toolchain-binaries.txt"
  cfg_array binaries >"${binaries_file}"
  local -a binaries=()
  mapfile -t binaries <"${binaries_file}"
  [ "${#binaries[@]}" -gt 0 ] ||
    config_invalid "toolchain-smoke: '.checks[\"toolchain-smoke\"].binaries' must name at least one binary; an empty list would inspect no toolchain"

  local binary="" resolved="" count=0
  for binary in "${binaries[@]}"; do
    [[ ${binary} =~ ^[A-Za-z0-9._+-]+$ ]] ||
      config_invalid "toolchain-smoke: binary '${binary}' is not a command name"
    resolved="$(command -v -- "${binary}" 2>/dev/null || true)"
    [ -n "${resolved}" ] ||
      refuse "declared shell '${shell}' is missing binary '${binary}'"
    count=$((count + 1))
  done

  log_info "toolchain binaries inspected: ${count} (declared shell: ${shell})"
  ok "Declared shell '${shell}' resolves every required binary"
}

# --------------------------------------------------------------------------- #
# no-custom-derivations
# --------------------------------------------------------------------------- #

# The builders that make a Nix file a custom BUILD rather than a declarative
# list. This is a convention of the Nix language, not of any repository, which is
# why it has a default at all — the same reason '*.sh' is exec-bits' one default.
# A repository may widen or narrow it with 'forbid'.
#
# The list is deliberately several DIFFERENT WORDS for one concept rather than
# several spellings of one word. Redundancy across spellings is not coverage
# across vocabulary: a template that avoided 'overrideAttrs' could still carry a
# custom build through 'runCommand' or 'symlinkJoin', and a sweep for the first
# would report a clean tree with a straight face.
# Ordered most specific first, so a refusal names the builder actually written
# rather than a shorter token inside it. Members that are already substrings of
# another member are left out ('stdenv.mkDerivation' is caught by 'mkDerivation',
# 'runCommandLocal' by 'runCommand'); every member here is a distinct way to
# write a build, not a variant spelling of one.
readonly DLINT_DERIVATION_BUILDERS="overrideAttrs overrideDerivation mkDerivation runCommand buildEnv symlinkJoin writeShellApplication writeShellScriptBin writeScriptBin writeTextFile derivation"

check_no_custom_derivations() {
  [ "$#" -eq 0 ] || usage_error "no-custom-derivations takes no arguments, got '$1'"

  load_check_config no-custom-derivations

  local require_subjects=""
  require_subjects="$(cfg_bool requireSubjects true)"

  local paths_file="${WORK_DIR}/nocustom-paths.txt"
  cfg_array paths >"${paths_file}"
  local -a paths=()
  mapfile -t paths <"${paths_file}"
  [ "${#paths[@]}" -gt 0 ] ||
    config_invalid "no-custom-derivations: '.checks[\"no-custom-derivations\"].paths' must name at least one path; an empty list would let this check pass without inspecting anything"

  # An explicitly EMPTY vocabulary is refused rather than defaulted: a check that
  # forbids nothing is the inert check this whole tool is shaped to avoid.
  local forbid_file="${WORK_DIR}/nocustom-forbid.txt"
  cfg_array forbid optional >"${forbid_file}"
  local -a forbid=()
  mapfile -t forbid <"${forbid_file}"
  if [ "$(jq -r '.checks["no-custom-derivations"].forbid | type' "${CFG_FILE}")" = "array" ]; then
    [ "${#forbid[@]}" -gt 0 ] ||
      config_invalid "no-custom-derivations: '.checks[\"no-custom-derivations\"].forbid' is empty, so this check would forbid nothing and pass unconditionally. Remove the key to use dlint's own vocabulary, or name the builders to forbid."
  fi
  [ "${#forbid[@]}" -gt 0 ] || read -r -a forbid <<<"${DLINT_DERIVATION_BUILDERS}"

  local path="" builder="" inspected=0 rg_status=0
  local hits="${WORK_DIR}/nocustom-hits.txt"
  for path in "${paths[@]}"; do
    # A declared path that is gone is absent, not clean. Deleting the file a rule
    # is about must never be the way to satisfy the rule.
    [ -e "${path}" ] ||
      config_absent "no-custom-derivations: '${path}', declared in '${CFG_FILE}', does not exist, so the resolver-shape rule has no subject there"

    for builder in "${forbid[@]}"; do
      rg_status=0
      rg -n --no-heading --fixed-strings -- "${builder}" "${path}" >"${hits}" || rg_status=$?
      [ "${rg_status}" -le 1 ] ||
        tool_failure "no-custom-derivations: could not scan '${path}' for '${builder}' (rg exit ${rg_status})"
      if [ "${rg_status}" -eq 0 ]; then
        printf '❌ %s\n' "no-custom-derivations: '${path}' uses '${builder}', so it is a custom build rather than a plain declarative list. Templates compose through the cyanprint nix resolver, which merges simple attribute lists; a custom derivation is not resolver-mergeable. Hoist it to the registry." >&2
        sed 's/^/       | /' "${hits}" >&2
        exit "${EXIT_VIOLATION}"
      fi
    done
    inspected=$((inspected + 1))
  done

  # The vocabulary is PRINTED, not merely applied: an absence claim is only as
  # wide as its word list, so the green states what it was wide enough to see.
  log_info "files inspected: ${inspected} (${paths[*]})"
  log_info "vocabulary enumerated (${#forbid[@]}): ${forbid[*]}"
  if [ "${inspected}" -eq 0 ] && [ "${require_subjects}" = "true" ]; then
    refuse "no declared path was inspected; the resolver-shape check would pass vacuously. If this repository legitimately has none, declare it: '.checks[\"no-custom-derivations\"].requireSubjects': false"
  fi
  ok "Template nix stays plain declarative lists"
}

# --------------------------------------------------------------------------- #
# entrypoint
# --------------------------------------------------------------------------- #

main() {
  local command="${1:-}"
  case "${command}" in
  --help | -h | help)
    usage
    exit 0
    ;;
  --version | -V)
    printf 'dlint %s\n' "${DLINT_VERSION}"
    exit 0
    ;;
  "")
    usage_error "dlint needs a check to run"
    ;;
  esac
  shift

  WORK_DIR="$(mktemp -d)" ||
    tool_failure "dlint could not create a working directory"
  # shellcheck disable=SC2064 # expand WORK_DIR now: the trap must not depend on later state
  trap "rm -rf '${WORK_DIR}'" EXIT

  case "${command}" in
  action-pins) check_action_pins "$@" ;;
  exec-bits) check_exec_bits "$@" ;;
  ci-wiring) check_ci_wiring "$@" ;;
  skills-fresh) check_skills_fresh "$@" ;;
  toolchain-smoke) check_toolchain_smoke "$@" ;;
  no-custom-derivations) check_no_custom_derivations "$@" ;;
  *)
    usage_error "unknown check '${command}'; dlint has exactly six: action-pins, exec-bits, ci-wiring, skills-fresh, toolchain-smoke, no-custom-derivations"
    ;;
  esac
}

main "$@"
