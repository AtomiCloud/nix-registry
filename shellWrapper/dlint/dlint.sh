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
# The canonical name (D6 ONE-CONFIG-NAME: dlint.yaml and release.yaml are the only
# names). The JSON name is still read so a repository can migrate on its own clock.
DLINT_CONFIG_YAML="dlint.yaml"
DLINT_CONFIG_JSON=".dlint.json"

# 0 conforms, or the check is explicitly disabled
readonly EXIT_VIOLATION=1     # the repository breaks the rule
readonly EXIT_USAGE=2         # wrong invocation
readonly EXIT_CONFIG_ABSENT=3 # nothing declared the subject; never a pass
readonly EXIT_CONFIG_INVALID=4
readonly EXIT_TOOL=5 # dlint could not complete the inspection

CHECK=""
# CFG_FILE is what the AUTHOR wrote and is what every message names. CFG_JSON is
# what jq reads: for a YAML configuration it is a converted copy. Keeping them
# separate is what stops a refusal from pointing at a temporary file the author
# has never seen.
CFG_FILE=""
CFG_JSON=""
WORK_DIR=""

# The checks dlint ships, in the order --all-configured runs them. The
# dispatcher, the --all-configured enumerator and the usage text all read this
# one list, so they cannot drift into disagreeing about what dlint has.
readonly DLINT_CHECKS="action-pins exec-bits ci-wiring toolchain-smoke no-custom-derivations"

# --------------------------------------------------------------------------- #
# reporting
# --------------------------------------------------------------------------- #

log_info() { printf 'ℹ️ %s\n' "$1"; }
ok() { printf '✅ %s\n' "$1"; }
skipped() { printf '⏭️ %s\n' "$1"; }

# A WARNING is not a refusal and never changes the exit code. It exists for the
# one thing a refusal cannot express: that the check's own SCOPE may be narrower
# than the reader assumes. It goes to stdout with the other findings of a run
# rather than to stderr, because a green run is exactly when it must be read.
warn() { printf '⚠️ %s\n' "$1"; }

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
  dlint --check <check> [--check <check>]...
  dlint lint          (alias: --all-configured)
  dlint --help
  dlint --version

Running several checks in ONE invocation:

  --check <check>     May be repeated. Each value is one check and its arguments,
                      e.g. --check 'action-pins trusted' --check exec-bits.
  --all-configured    Runs every check DLINT SHIPS, in the order listed below,
                      reading each one's subject from '.checks'. The population is
                      dlint's own list and not the keys the file happens to carry,
                      so deleting a section refuses (exit 3) instead of quietly
                      going unenforced; '"<check>": false' stays the one way to opt
                      out. 'action-pins' runs BOTH trust classes, because enforcing
                      one and reporting green leaves the other unasserted.

  Every requested check RUNS, even after one refuses, so one invocation reports
  every fault rather than only the first. The invocation's exit code is the
  HIGHEST of theirs — the codes below are ordered by severity, so a check that
  could not be trusted (3, 4, 5) outranks a known violation (1). An invocation
  that would inspect nothing refuses; it is never a pass.

Checks (there are exactly five; there is no 'all' CHECK — the flag is --all-configured):
  action-pins <trusted|non-trusted>
      Every GitHub Action reference carries the pin its trust class demands: a
      major tag for trusted actions, an exact 40-hex SHA plus a trailing tag
      comment for non-trusted ones. An action is trusted iff it matches the
      configured 'trustedPattern' regex; everything else is non-trusted, so an
      unlisted action gets the strictest pin by default.
  exec-bits
      Every tracked shell script is executable.
  ci-wiring
      Every orchestrator job calls a repository-local reusable workflow, and
      every reusable workflow calls a CI entrypoint that exists and is
      executable.
  toolchain-smoke
      Enter each declared shell and verify that every binary declared FOR THAT
      SHELL resolves inside it AND runs: each entry is 'binary [probe args]'
      (default probe '--version'), and the probe must exit 0. Resolution proves
      the PATH delivers a file; the run proves the build behind it works — the
      difference a broken nix pin upgrade hides. The legacy single-'shell' form
      still works, but it inspects the invocation environment and says so rather
      than naming a shell it never entered.
  no-custom-derivations
      Every declared nix file stays a plain declarative list: no custom derivation
      builder appears in it. Nix comments are STRIPPED before matching, so prose
      that documents a builder cannot trip the gate. EVERY declared file is read to
      the end and EVERY match is reported in one run. The vocabulary of builders and
      the paths scanned are printed with the result, and a nix file that exists here
      but is not declared is named in a WARNING — a declared-paths check is only as
      wide as its list.

Configuration:
  All checks read ONE file. dlint looks for, in order:
    \$DLINT_CONFIG          (any path; YAML if it ends .yaml/.yml, else JSON)
    ./${DLINT_CONFIG_YAML}            the canonical name
    ./${DLINT_CONFIG_JSON}          still read, so a repository can migrate on its own clock
  Finding BOTH ./${DLINT_CONFIG_YAML} and ./${DLINT_CONFIG_JSON} is an ERROR: whichever
  dlint did not read would look enforced while configuring nothing.
  dlint is run from the repository root and reads every path relative to it.

  The YAML form is the same shape, so this:

    schemaVersion: 1
    checks:
      exec-bits:
        globs: ["*.sh"]
      toolchain-smoke:
        shell: default
        binaries: [bash, git]

  and the JSON below are the same configuration.

    {
      "schemaVersion": 1,
      "checks": {
        "action-pins":  { "trustedPattern": "^AtomiCloud/" },
        "exec-bits":    {},
        "ci-wiring":    {
          "entrypointPattern": "scripts/ci/[A-Za-z0-9._-]+[.]sh",
          "orchestrators": [".github/workflows/ci.yaml"]
        },
        "toolchain-smoke": {
          "enter": "nix develop .#{shell} --command bash -c",
          "shells": {
            "default": ["bash", "git"],
            "cd": ["skopeo", "helm"]
          }
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

# Picks the configuration file and, for YAML, converts it once into JSON that the
# jq readers below can use. Sets CFG_FILE (for messages) and CFG_JSON (for jq).
resolve_config_file() {
  if [ -n "${DLINT_CONFIG:-}" ]; then
    CFG_FILE="${DLINT_CONFIG}"
    [ -f "${CFG_FILE}" ] ||
      config_absent "dlint configuration '${CFG_FILE}', named by DLINT_CONFIG, does not exist. dlint never guesses, and never passes a check it could not configure."
  else
    local have_yaml=0 have_json=0
    [ ! -f "${DLINT_CONFIG_YAML}" ] || have_yaml=1
    [ ! -f "${DLINT_CONFIG_JSON}" ] || have_json=1

    # Two configurations mean the reader cannot know which one is authoritative,
    # and the losing file would sit there looking enforced while nothing read it.
    # That is the same silent-staleness this tool refuses everywhere else, so it
    # is an error rather than a precedence rule.
    [ "$((have_yaml + have_json))" -ne 2 ] ||
      config_invalid "dlint found BOTH '${DLINT_CONFIG_YAML}' and '${DLINT_CONFIG_JSON}'. Keep one: whichever dlint did not read would look enforced while configuring nothing. '${DLINT_CONFIG_YAML}' is the canonical name."

    if [ "${have_yaml}" -eq 1 ]; then
      CFG_FILE="${DLINT_CONFIG_YAML}"
    elif [ "${have_json}" -eq 1 ]; then
      CFG_FILE="${DLINT_CONFIG_JSON}"
    else
      config_absent "dlint configuration is missing, so dlint cannot know this repository's layout. Create '${DLINT_CONFIG_YAML}' (see 'dlint --help') or point DLINT_CONFIG at it. dlint never guesses, and never passes a check it could not configure."
    fi
  fi

  case "${CFG_FILE}" in
  *.yaml | *.yml)
    CFG_JSON="${WORK_DIR}/config-from-yaml.json"
    # yq writes to a file and its own status is checked, rather than being piped
    # into jq: a pipeline takes its status from the LAST command, so a yq that
    # failed here would leave jq reading an empty file and could read as success.
    local yq_status=0
    yq -o=json '.' "${CFG_FILE}" >"${CFG_JSON}" 2>"${WORK_DIR}/yq.err" || yq_status=$?
    if [ "${yq_status}" -ne 0 ]; then
      printf '❌ %s\n' "dlint configuration '${CFG_FILE}' is not valid YAML (yq exit ${yq_status})" >&2
      [ ! -s "${WORK_DIR}/yq.err" ] || sed 's/^/       | /' "${WORK_DIR}/yq.err" >&2
      exit "${EXIT_CONFIG_INVALID}"
    fi
    # An empty YAML document converts to the four bytes `null`, which every reader
    # below would then walk into as if it were a configuration.
    [ "$(jq -r 'type' "${CFG_JSON}")" = "object" ] ||
      config_invalid "dlint configuration '${CFG_FILE}' must be a YAML mapping at the top level"
    ;;
  *)
    CFG_JSON="${CFG_FILE}"
    jq empty "${CFG_JSON}" >/dev/null 2>&1 ||
      config_invalid "dlint configuration '${CFG_FILE}' is not valid JSON"
    ;;
  esac
}

# Loads the configuration FILE and validates everything that is not specific to
# one check, so that selecting a section and enumerating every section apply the
# same rules to the same file. File selection and any YAML conversion are
# delegated to resolve_config_file, so there is one place that decides WHICH file
# is authoritative and one place that validates it.
load_config_file() {
  resolve_config_file

  local version=""
  version="$(jq -r '.schemaVersion // empty' "${CFG_JSON}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.schemaVersion'"
  [ "${version}" = "1" ] ||
    config_invalid "dlint configuration '${CFG_FILE}': '.schemaVersion' must be 1, found '${version:-<absent>}'"

  local checks_type=""
  checks_type="$(jq -r '.checks | type' "${CFG_JSON}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks'"
  [ "${checks_type}" = "object" ] ||
    config_invalid "dlint configuration '${CFG_FILE}': '.checks' must be an object, found ${checks_type}"
}

# Loads the configuration and selects this check's section. Exits 3 when the
# file or the section is absent, 4 when either is malformed, and 0 when the
# section is an explicit `false`.
load_check_config() {
  CHECK="$1"
  load_config_file

  local section_type=""
  section_type="$(jq -r --arg c "${CHECK}" '.checks[$c] | type' "${CFG_JSON}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"]'"

  case "${section_type}" in
  object) ;;
  null)
    config_absent "dlint configuration '${CFG_FILE}' declares no '.checks[\"${CHECK}\"]' section, so the ${CHECK} check has no subject. Declare it, or disable the check on purpose with '\"${CHECK}\": false'. An absent section is never a pass."
    ;;
  boolean)
    local enabled=""
    enabled="$(jq -r --arg c "${CHECK}" '.checks[$c]' "${CFG_JSON}")" ||
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
  type="$(jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k] | type' "${CFG_JSON}")" ||
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
  value="$(jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k]' "${CFG_JSON}")" ||
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
  type="$(jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k] | type' "${CFG_JSON}")" ||
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
    "${CFG_JSON}" >/dev/null 2>&1 ||
    config_invalid "dlint configuration '${CFG_FILE}': every entry of '.checks[\"${CHECK}\"].${key}' must be a non-empty string"
  jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k][]' "${CFG_JSON}" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"].${key}'"
}

# cfg_bool <key> <default> -> "true" or "false" on stdout.
cfg_bool() {
  local key="$1" fallback="$2" type=""
  type="$(jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k] | type' "${CFG_JSON}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks[\"${CHECK}\"].${key}'"

  if [ "${type}" = "null" ]; then
    printf '%s' "${fallback}"
    return 0
  fi

  [ "${type}" = "boolean" ] ||
    config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${CHECK}\"].${key}' must be true or false, found ${type}"
  jq -r --arg c "${CHECK}" --arg k "${key}" '.checks[$c][$k]' "${CFG_JSON}" ||
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

# The one =~ site, wrapped so callers read a COMMAND status: a match is 0, a
# non-match is 1, and a regex that does not compile is 2 — distinguishable
# without the $?-of-a-condition pattern shellcheck rightly flags (SC2319).
regex_matches() {
  [[ $1 =~ $2 ]]
}

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

  local trusted_pattern="" workflows_dir="" require_subjects=""
  trusted_pattern="$(cfg_string trustedPattern)"
  workflows_dir="$(cfg_string workflowsDir '.github/workflows')"
  require_subjects="$(cfg_bool requireSubjects true)"

  # A pattern that does not compile must refuse loudly, not classify everything
  # non-trusted: =~ exits 2 on a bad regex, and only 0/1 are match verdicts.
  # (cfg_string already refuses an EMPTY pattern, which would trust every action.)
  local probe_rc=0
  regex_matches probe "${trusted_pattern}" || probe_rc=$?
  [ "${probe_rc}" -le 1 ] ||
    config_invalid "action-pins: 'trustedPattern' '${trusted_pattern}' is not a valid regex"

  [ -d "${workflows_dir}" ] ||
    config_absent "action-pins: the workflow directory '${workflows_dir}' declared in '${CFG_FILE}' does not exist"

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

    if regex_matches "${action}" "${trusted_pattern}"; then
      classification="trusted"
    else
      classification="non-trusted"
    fi
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
# toolchain-smoke
# --------------------------------------------------------------------------- #

# An entry is 'binary' or 'binary <probe args>'; the binary must RESOLVE and the
# probe must RUN (exit 0). The default probe is '--version'. Probe args are
# restricted to flag-shaped tokens because the probe is embedded into the enter
# command's script string — a freer charset would be an injection surface.
SMOKE_BINARY=""
SMOKE_PROBE=""
smoke_parse_entry() {
  local -a parts=()
  read -r -a parts <<<"$1"
  [ "${#parts[@]}" -gt 0 ] ||
    config_invalid "toolchain-smoke: an entry is empty; each entry is 'binary [probe args]'"
  SMOKE_BINARY="${parts[0]}"
  [[ ${SMOKE_BINARY} =~ ^[A-Za-z0-9._+-]+$ ]] ||
    config_invalid "toolchain-smoke: binary '${SMOKE_BINARY}' is not a command name"
  if [ "${#parts[@]}" -eq 1 ]; then
    SMOKE_PROBE="--version"
  else
    local tok=""
    for tok in "${parts[@]:1}"; do
      [[ ${tok} =~ ^[A-Za-z0-9._+=-]+$ ]] ||
        config_invalid "toolchain-smoke: probe argument '${tok}' of '${SMOKE_BINARY}' must be a flag-shaped token"
    done
    SMOKE_PROBE="${parts[*]:1}"
  fi
}

check_toolchain_smoke() {
  [ "$#" -eq 0 ] || usage_error "toolchain-smoke takes no arguments, got '$1'"

  load_check_config toolchain-smoke

  local shells_type="" legacy_type=""
  shells_type="$(jq -r '.checks["toolchain-smoke"].shells | type' "${CFG_JSON}")" ||
    config_invalid "toolchain-smoke: could not read '.checks[\"toolchain-smoke\"].shells'"
  legacy_type="$(jq -r '.checks["toolchain-smoke"].shell | type' "${CFG_JSON}")" ||
    config_invalid "toolchain-smoke: could not read '.checks[\"toolchain-smoke\"].shell'"

  [ "${shells_type}" = "null" ] || [ "${legacy_type}" = "null" ] ||
    config_invalid "toolchain-smoke: declare EITHER 'shells' (scoped, one entry per shell) OR the single 'shell', never both; two declarations would leave it ambiguous which shell a green result is about"

  if [ "${shells_type}" != "null" ]; then
    check_toolchain_smoke_scoped "${shells_type}"
    return
  fi
  check_toolchain_smoke_ambient
}

# The SCOPED form. Each declared shell is ENTERED and its binaries are resolved
# inside it, so a green result is attributable to that shell and no other.
#
# This exists because of a real defect: toolchain-smoke was configured
# "shell": "default" and then resolved binaries in whatever environment dlint
# happened to be invoked from. It reported that `skopeo` resolves — and it was
# RIGHT, about a shell the failing probe never used. A control that answers a
# neighbouring question is worse than none, because it retires the doubt without
# answering it. Attribution, not just resolution, is what this form adds.
check_toolchain_smoke_scoped() {
  local shells_type="$1"
  [ "${shells_type}" = "object" ] ||
    config_invalid "toolchain-smoke: '.checks[\"toolchain-smoke\"].shells' must be an object mapping each shell name to its binaries, found ${shells_type}"

  local enter=""
  enter="$(cfg_string enter)"
  # Without a way to enter the shell, every result would come from the invocation
  # environment while the message named a shell — exactly the false attribution
  # this form exists to remove.
  case "${enter}" in
  *'{shell}'*) ;;
  *) config_invalid "toolchain-smoke: '.checks[\"toolchain-smoke\"].enter' must contain the '{shell}' placeholder, so each shell is entered by name; got '${enter}'" ;;
  esac

  local names_file="${WORK_DIR}/toolchain-shells.txt"
  jq -r '.checks["toolchain-smoke"].shells | keys_unsorted[]' "${CFG_JSON}" >"${names_file}" ||
    config_invalid "toolchain-smoke: could not list the shells of '.checks[\"toolchain-smoke\"].shells'"
  local -a names=()
  mapfile -t names <"${names_file}"
  [ "${#names[@]}" -gt 0 ] ||
    config_invalid "toolchain-smoke: '.checks[\"toolchain-smoke\"].shells' must name at least one shell; an empty object would inspect no toolchain"

  local shell="" binaries_type="" binary="" verdict="" rc=0 shell_count=0 total=0
  local -a binaries=() argv=()
  local binaries_file="" errs=""
  for shell in "${names[@]}"; do
    [[ ${shell} =~ ^[A-Za-z0-9._#-]+$ ]] ||
      config_invalid "toolchain-smoke: shell name '${shell}' must be letters, digits, '.', '_', '#' or '-'"

    binaries_type="$(jq -r --arg s "${shell}" '.checks["toolchain-smoke"].shells[$s] | type' "${CFG_JSON}")" ||
      config_invalid "toolchain-smoke: could not read the binaries of shell '${shell}'"
    [ "${binaries_type}" = "array" ] ||
      config_invalid "toolchain-smoke: '.checks[\"toolchain-smoke\"].shells[\"${shell}\"]' must be an array of binaries, found ${binaries_type}"

    binaries_file="${WORK_DIR}/toolchain-binaries-${shell_count}.txt"
    jq -r --arg s "${shell}" '.checks["toolchain-smoke"].shells[$s][]' "${CFG_JSON}" >"${binaries_file}" ||
      config_invalid "toolchain-smoke: could not read the binaries of shell '${shell}'"
    binaries=()
    mapfile -t binaries <"${binaries_file}"
    [ "${#binaries[@]}" -gt 0 ] ||
      config_invalid "toolchain-smoke: shell '${shell}' declares no binary; an empty list would inspect no toolchain"

    # The enter command is split into argv here, with {shell} substituted, and the
    # probe is appended as ONE final argument — so it must end in something that
    # takes a script string, e.g. 'nix develop .#{shell} --command bash -c'.
    argv=()
    read -r -a argv <<<"${enter//\{shell\}/${shell}}"
    [ "${#argv[@]}" -gt 0 ] ||
      config_invalid "toolchain-smoke: 'enter' expanded to nothing for shell '${shell}'"

    local entry="" probe=""
    for entry in "${binaries[@]}"; do
      smoke_parse_entry "${entry}"
      binary="${SMOKE_BINARY}"
      probe="${SMOKE_PROBE}"

      # The probe reports through a MARKER rather than through its exit status.
      # 'not found', 'resolves but does not run' and 'could not enter the shell
      # at all' all exit non-zero, and collapsing them would let a broken entry
      # mechanism read as a missing binary — a wrong reason, loudly stated. A
      # missing marker is UNKNOWN. The run half exists because resolution only
      # proves the PATH delivers a file; executing it is what proves the build
      # behind a nix pin actually works, which is the moment a pin upgrade
      # would otherwise ship a binary that resolves and cannot run.
      errs="${WORK_DIR}/enter-${shell_count}.err"
      verdict=""
      rc=0
      verdict="$("${argv[@]}" "command -v -- '${binary}' >/dev/null 2>&1 || { printf DLINT_MISSING; exit 0; }; if ${binary} ${probe} </dev/null >/dev/null 2>&1; then printf DLINT_RAN; else printf 'DLINT_RUNFAIL:%s' \$?; fi" 2>"${errs}")" || rc=$?

      case "${verdict}" in
      DLINT_RAN) ;;
      DLINT_MISSING)
        refuse "shell '${shell}' is missing binary '${binary}'"
        ;;
      DLINT_RUNFAIL:*)
        refuse "shell '${shell}' resolves binary '${binary}' but '${binary} ${probe}' exited ${verdict#DLINT_RUNFAIL:} — the build does not run"
        ;;
      *)
        printf '❌ %s\n' "toolchain-smoke: could not enter shell '${shell}' to look for '${binary}' (exit ${rc}); the toolchain of that shell is UNKNOWN, which is not a pass. The entry command was: ${argv[*]}" >&2
        [ ! -s "${errs}" ] || sed 's/^/       | /' "${errs}" >&2
        exit "${EXIT_TOOL}"
        ;;
      esac
      total=$((total + 1))
    done

    log_info "shell '${shell}': ${#binaries[@]} binary/binaries resolved AND ran INSIDE it"
    shell_count=$((shell_count + 1))
  done

  log_info "toolchain binaries inspected: ${total} across ${shell_count} shell(s): ${names[*]}"
  ok "Every declared shell resolves and runs its required binaries (${names[*]})"
}

# The AMBIENT form, kept so a repository configured for the single 'shell' key
# keeps working. It resolves binaries in the environment dlint was INVOKED from,
# which is not necessarily the shell it names, so it says exactly that and claims
# nothing about the named shell.
check_toolchain_smoke_ambient() {
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

  local entry="" binary="" resolved="" run_rc=0 count=0
  for entry in "${binaries[@]}"; do
    smoke_parse_entry "${entry}"
    binary="${SMOKE_BINARY}"
    resolved="$(command -v -- "${binary}" 2>/dev/null || true)"
    [ -n "${resolved}" ] ||
      refuse "the invocation environment is missing binary '${binary}' (declared for shell '${shell}')"
    run_rc=0
    # SMOKE_PROBE is validated flag-shaped tokens; the split is intentional.
    # shellcheck disable=SC2086
    "${binary}" ${SMOKE_PROBE} </dev/null >/dev/null 2>&1 || run_rc=$?
    [ "${run_rc}" -eq 0 ] ||
      refuse "the invocation environment resolves binary '${binary}' but '${binary} ${SMOKE_PROBE}' exited ${run_rc} — the build does not run"
    count=$((count + 1))
  done

  log_info "toolchain binaries inspected: ${count} in the INVOCATION ENVIRONMENT (declared shell: ${shell}, not entered)"
  log_info "to attribute this result to a shell, declare 'shells' with an 'enter' command instead of 'shell'"
  ok "The invocation environment resolves and runs every binary declared for '${shell}'"
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

# The globs this check uses to ask "is my own scope complete?". It is a layout
# convention rather than a language one, so it is declarable ('discover'); it has
# a default at all only because a repository that never declares it would get the
# silence this warning exists to break.
readonly DLINT_NIX_DISCOVER_GLOB="nix/*.nix"

# Writes a comment-FREE copy of a Nix file, one output line per input line so the
# line numbers of a match still address the file as the author wrote it.
#
# This exists because of a measured inversion: the check refused a file whose only
# occurrence of a builder was inside a COMMENT — a sentence explaining that the
# practice had been AVOIDED — while a file carrying three real derivations passed,
# because it was not in the declared paths. A gate that reads prose as code and
# code as nothing is blind in the direction that matters.
#
# The scanner is a state machine rather than a regex because the three contexts a
# '#' can sit in are not distinguishable line by line: block comments and both
# string forms span lines. STRING CONTENTS ARE KEPT, never stripped — a builder
# written inside a string or an antiquotation is still a builder written in the
# file, and dropping it would be the silent direction again. Only comments go.
#
# Outside a string and a comment, '#' in Nix ALWAYS starts a comment: neither the
# URI literal nor the path literal admits '#' in its character set, so there is no
# bare token this can mistake for one.
# The apostrophe is passed IN as `q` rather than written into the program: the
# program is single-quoted for the shell, and a literal apostrophe inside it would
# have to be spelled with the '\'' dance at five separate sites — a scanner whose
# quoting is unreadable is a scanner nobody can review.
nocustom_strip_comments() {
  local src="$1" dst="$2"
  awk -v q="'" '
    BEGIN { state = "code"; ident = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_" q }
    {
      line = $0; out = ""; i = 1; n = length(line)
      while (i <= n) {
        c = substr(line, i, 1)
        two = substr(line, i, 2)
        if (state == "code") {
          if (c == "#") { break }
          if (two == "/*") { state = "block"; i += 2; continue }
          # An identifier may END in an apostrophe, so a doubled one opens an
          # indented string only where an identifier cannot be continuing.
          if (two == q q && (out == "" || index(ident, substr(out, length(out), 1)) == 0)) {
            state = "indented"; out = out two; i += 2; continue
          }
          if (c == "\"") { state = "double"; out = out c; i += 1; continue }
          out = out c; i += 1; continue
        }
        if (state == "double") {
          if (c == "\\") { out = out substr(line, i, 2); i += 2; continue }
          if (c == "\"") { state = "code" }
          out = out c; i += 1; continue
        }
        if (state == "indented") {
          if (two == q q) {
            tail = substr(line, i + 2, 1)
            # A THIRD apostrophe, a $ or a backslash after the pair is an ESCAPE
            # inside an indented string, not the end of one.
            if (tail == q || tail == "$" || tail == "\\") { out = out substr(line, i, 3); i += 3; continue }
            state = "code"; out = out two; i += 2; continue
          }
          out = out c; i += 1; continue
        }
        # state == "block"
        if (two == "*/") { state = "code"; i += 2; continue }
        i += 1
      }
      print out
    }
  ' "${src}" >"${dst}" ||
    tool_failure "no-custom-derivations: could not strip the comments of '${src}'"
}

# Is <file> covered by one of the declared paths? A declared FILE covers itself; a
# declared DIRECTORY covers everything beneath it. Used only by the coverage
# warning, so a false "covered" here would silence a warning and never hide a
# match.
nocustom_is_declared() {
  local file="${1#./}" declared=""
  shift
  for declared in "$@"; do
    declared="${declared#./}"
    declared="${declared%/}"
    [ "${file}" != "${declared}" ] || return 0
    [ "${file#"${declared}"/}" = "${file}" ] || return 0
  done
  return 1
}

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
  if [ "$(jq -r '.checks["no-custom-derivations"].forbid | type' "${CFG_JSON}")" = "array" ]; then
    [ "${#forbid[@]}" -gt 0 ] ||
      config_invalid "no-custom-derivations: '.checks[\"no-custom-derivations\"].forbid' is empty, so this check would forbid nothing and pass unconditionally. Remove the key to use dlint's own vocabulary, or name the builders to forbid."
  fi
  [ "${#forbid[@]}" -gt 0 ] || read -r -a forbid <<<"${DLINT_DERIVATION_BUILDERS}"

  # The globs the COVERAGE WARNING sweeps. Declarable for the same reason
  # 'exec-bits.globs' is: 'nix/*.nix' is a layout convention, and a tool that bakes
  # one in has stopped being repo-agnostic.
  local discover_file="${WORK_DIR}/nocustom-discover.txt"
  cfg_array discover optional >"${discover_file}"
  local -a discover=()
  mapfile -t discover <"${discover_file}"
  if [ "$(jq -r '.checks["no-custom-derivations"].discover | type' "${CFG_JSON}")" = "array" ]; then
    [ "${#discover[@]}" -gt 0 ] ||
      config_invalid "no-custom-derivations: '.checks[\"no-custom-derivations\"].discover' is empty, so this check could never notice a nix file it does not read. Remove the key to use dlint's own glob, or name the globs to sweep."
  fi
  [ "${#discover[@]}" -gt 0 ] || discover=("${DLINT_NIX_DISCOVER_GLOB}")

  # EVERY declared file is read to the end and EVERY match is collected before
  # anything is reported. Stopping at the first offender makes removing one uncover
  # the next, so a repository learns the size of its problem one run at a time.
  local path="" file="" builder="" lineno="" original=""
  local inspected=0 rg_status=0 offenders=0 offending_files=0
  local -A claimed=()
  local files="${WORK_DIR}/nocustom-files.txt"
  local stripped="${WORK_DIR}/nocustom-stripped.txt"
  local hits="${WORK_DIR}/nocustom-hits.txt"
  local found="${WORK_DIR}/nocustom-found.txt"
  local selected="${WORK_DIR}/nocustom-selected.txt"
  local report="${WORK_DIR}/nocustom-report.txt"
  : >"${report}"

  for path in "${paths[@]}"; do
    # A declared path that is gone is absent, not clean. Deleting the file a rule
    # is about must never be the way to satisfy the rule.
    [ -e "${path}" ] ||
      config_absent "no-custom-derivations: '${path}', declared in '${CFG_FILE}', does not exist, so the resolver-shape rule has no subject there"

    # A declared path may be a directory, which stands for every file beneath it.
    if [ -d "${path}" ]; then
      find "${path}" -type f -print | sort >"${files}" ||
        tool_failure "no-custom-derivations: could not list the files under '${path}'"
    else
      printf '%s\n' "${path}" >"${files}"
    fi

    while IFS= read -r file; do
      [ -n "${file}" ] || continue
      inspected=$((inspected + 1))
      nocustom_strip_comments "${file}" "${stripped}"

      : >"${found}"
      for builder in "${forbid[@]}"; do
        rg_status=0
        rg -n --no-filename --no-heading --fixed-strings -- "${builder}" "${stripped}" >"${hits}" || rg_status=$?
        [ "${rg_status}" -le 1 ] ||
          tool_failure "no-custom-derivations: could not scan '${file}' for '${builder}' (rg exit ${rg_status})"
        [ "${rg_status}" -eq 0 ] || continue

        # One LINE is reported under ONE builder: the vocabulary is ordered most
        # specific first, so the first member to claim a line is the builder
        # actually written rather than a shorter token inside it.
        while IFS=: read -r lineno _; do
          [ -n "${lineno}" ] || continue
          [ -z "${claimed["${file}:${lineno}"]:-}" ] || continue
          claimed["${file}:${lineno}"]="${builder}"
          printf '%s\t%s\n' "${builder}" "${lineno}" >>"${found}"
        done <"${hits}"
      done

      [ -s "${found}" ] || continue
      offending_files=$((offending_files + 1))
      for builder in "${forbid[@]}"; do
        awk -F'\t' -v b="${builder}" '$1 == b { print $2 }' "${found}" >"${selected}" ||
          tool_failure "no-custom-derivations: could not group the matches of '${file}'"
        [ -s "${selected}" ] || continue
        printf '❌ %s\n' "no-custom-derivations: '${file}' uses '${builder}', so it is a custom build rather than a plain declarative list. Templates compose through the cyanprint nix resolver, which merges simple attribute lists; a custom derivation is not resolver-mergeable. Hoist it to the registry." >>"${report}"
        while IFS= read -r lineno; do
          [ -n "${lineno}" ] || continue
          # The line is quoted AS WRITTEN, comments and all: the match was made
          # against the stripped copy, but the author has to find the line.
          original="$(sed -n "${lineno}p" -- "${file}")" ||
            tool_failure "no-custom-derivations: could not read line ${lineno} of '${file}'"
          printf '       | %s:%s\n' "${lineno}" "${original}" >>"${report}"
          offenders=$((offenders + 1))
        done <"${selected}"
      done
    done <"${files}"
  done

  # The vocabulary is PRINTED, not merely applied: an absence claim is only as
  # wide as its word list, so the green states what it was wide enough to see. The
  # PATHS are printed for the same reason — an absence claim is also only as wide
  # as the files it opened.
  log_info "files inspected: ${inspected} (${paths[*]})"
  log_info "vocabulary enumerated (${#forbid[@]}): ${forbid[*]}"

  # COVERAGE HONESTY. A declared-paths check that silently ignores an undeclared
  # file full of derivations is how a green gets inverted: the file that broke the
  # rule was never opened, and nothing said so. This is a WARNING and not a refusal
  # because dlint cannot know that an undeclared file is in scope — but it can
  # refuse to be quiet about it.
  local -a undeclared=() matches=()
  local -A discovered=()
  local pattern="" candidate="" saved_ifs="${IFS}" nullglob_was_off=0
  # A glob that matches nothing must expand to NOTHING, not to itself: the literal
  # pattern would then be probed as a filename and quietly found absent.
  shopt -q nullglob || nullglob_was_off=1
  shopt -s nullglob
  for pattern in "${discover[@]}"; do
    # Pathname expansion with IFS emptied. The glob still yields one field per
    # match, so a path containing spaces stays ONE candidate, and no word
    # splitting happens on the pattern itself. (`compgen -G` says this more
    # directly, but the bash a packaged dlint runs under is built without
    # programmable completion and does not have it — found by running this
    # harness against the built derivation rather than against the script.)
    IFS=''
    # shellcheck disable=SC2206 # deliberate: this IS the glob expansion
    matches=(${pattern})
    IFS="${saved_ifs}"
    for candidate in "${matches[@]}"; do
      [ -f "${candidate}" ] || continue
      [ -z "${discovered["${candidate}"]:-}" ] || continue
      discovered["${candidate}"]=1
      nocustom_is_declared "${candidate}" "${paths[@]}" || undeclared+=("${candidate}")
    done
  done
  [ "${nullglob_was_off}" -eq 0 ] || shopt -u nullglob
  if [ "${#undeclared[@]}" -gt 0 ]; then
    warn "no-custom-derivations: ${#undeclared[@]} nix file(s) matching '${discover[*]}' exist here and are NOT covered by the declared paths, so this check never read them: ${undeclared[*]}. Add them to '.checks[\"no-custom-derivations\"].paths', or narrow 'discover' if they are deliberately out of scope."
  fi

  if [ "${offenders}" -gt 0 ]; then
    cat "${report}" >&2 ||
      tool_failure "no-custom-derivations: could not report the matches it found"
    printf '❌ %s\n' "no-custom-derivations: ${offenders} offending line(s) across ${offending_files} of ${inspected} inspected file(s). Every declared file was read to the end, so this is the whole list and not the first fault." >&2
    exit "${EXIT_VIOLATION}"
  fi

  if [ "${inspected}" -eq 0 ] && [ "${require_subjects}" = "true" ]; then
    refuse "no declared path was inspected; the resolver-shape check would pass vacuously. If this repository legitimately has none, declare it: '.checks[\"no-custom-derivations\"].requireSubjects': false"
  fi
  ok "Template nix stays plain declarative lists"
}

# --------------------------------------------------------------------------- #
# dispatch
# --------------------------------------------------------------------------- #

# Runs exactly one check. Called directly for a single-check invocation, and in a
# SUBSHELL for each check of a multi-check one: every check reports by exiting, so
# without that subshell the first one to finish would end the whole invocation and
# the checks after it would never run at all.
dispatch_check() {
  local check="$1"
  shift
  case "${check}" in
  action-pins) check_action_pins "$@" ;;
  exec-bits) check_exec_bits "$@" ;;
  ci-wiring) check_ci_wiring "$@" ;;
  toolchain-smoke) check_toolchain_smoke "$@" ;;
  no-custom-derivations) check_no_custom_derivations "$@" ;;
  *)
    usage_error "unknown check '${check}'; dlint has these: ${DLINT_CHECKS}"
    ;;
  esac
}

# The specs one configured check expands into. A configured `action-pins` section
# means BOTH trust classes: the check enforces one class per invocation, so
# running only `trusted` would leave every non-trusted pin unasserted while the
# invocation reported green.
specs_for_check() {
  case "$1" in
  action-pins)
    printf 'action-pins trusted\n'
    printf 'action-pins non-trusted\n'
    ;;
  *) printf '%s\n' "$1" ;;
  esac
}

# Runs every spec it is given and exits with the HIGHEST code any of them
# produced. The exit codes are ordered by severity, so this reports a check that
# could not be trusted (3, 4, 5) ahead of a known violation (1) — the opposite
# would hide an unrunnable check behind a lesser, more reassuring answer.
run_specs() {
  # An invocation that inspects nothing is the vacuous pass this tool exists to
  # refuse, so it is a tool failure rather than a silent 0.
  [ "$#" -gt 0 ] ||
    tool_failure "dlint was asked to run no check at all, so it would report success without inspecting anything"

  local spec="" rc=0 worst=0 ran=0 failed=0
  local -a argv=()
  for spec in "$@"; do
    # Splitting on whitespace is what turns 'action-pins trusted' into a check
    # and its argument; an empty spec would split to nothing and silently run no
    # check, so it is refused rather than skipped.
    read -r -a argv <<<"${spec}"
    [ "${#argv[@]}" -gt 0 ] ||
      usage_error "a requested check is empty; every '--check' needs a check name"

    printf '\n── dlint %s\n' "${spec}"
    rc=0
    (dispatch_check "${argv[@]}") || rc=$?
    # A code dlint does not define (a signal, a missing interpreter) is a tool
    # failure, not a violation: clamping keeps an unknown fault from reading as
    # the milder answer.
    [ "${rc}" -le 5 ] || rc=5
    ran=$((ran + 1))
    [ "${rc}" -eq 0 ] || failed=$((failed + 1))
    [ "${rc}" -le "${worst}" ] || worst="${rc}"
  done

  printf '\nℹ️ checks run: %s (did not pass: %s)\n' "${ran}" "${failed}"
  if [ "${worst}" -ne 0 ]; then
    printf '❌ %s\n' "dlint: ${failed} of ${ran} check(s) did not pass; exiting with the highest code, ${worst}" >&2
    exit "${worst}"
  fi
  ok "every requested check passed (${ran})"
}

# dlint --check <check> [--check <check>]...
run_selected() {
  local -a specs=()
  while [ "$#" -gt 0 ]; do
    case "$1" in
    --check)
      [ "$#" -ge 2 ] || usage_error "'--check' needs a check to run"
      [ -n "$2" ] || usage_error "'--check' needs a check to run, got an empty value"
      specs+=("$2")
      shift 2
      ;;
    *)
      usage_error "unexpected argument '$1'; combine checks with a repeated '--check <check>'"
      ;;
    esac
  done
  run_specs "${specs[@]}"
}

# dlint --all-configured
run_all_configured() {
  [ "$#" -eq 0 ] || usage_error "--all-configured takes no arguments, got '$1'"

  load_config_file

  # Every key is validated against the checks dlint actually has. A key dlint
  # does not know is a configuration error, never something to pass over: a
  # misspelled check would otherwise be silently unenforced while
  # --all-configured reported green.
  local enabled=""
  enabled="$(jq -r '[.checks | to_entries[] | select(.value != false)] | length' "${CFG_JSON}")" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not read '.checks'"
  [ "${enabled}" -gt 0 ] ||
    config_absent "dlint configuration '${CFG_FILE}' has no check left enabled under '.checks', so --all-configured would assert nothing at all. That is never a pass; enable a check, or do not run dlint."

  # A configured key dlint does not know is a configuration error, never
  # something to pass over: a misspelled check would otherwise sit in the file
  # looking enforced while nothing ran it.
  local keys_file="${WORK_DIR}/configured-checks.txt"
  jq -r '.checks | keys_unsorted[]' "${CFG_JSON}" >"${keys_file}" ||
    config_invalid "dlint configuration '${CFG_FILE}': could not list the keys of '.checks'"

  local key="" known="" candidate=""
  while IFS= read -r key; do
    [ -n "${key}" ] || continue
    known=0
    for candidate in ${DLINT_CHECKS}; do
      [ "${key}" != "${candidate}" ] || known=1
    done
    [ "${known}" -eq 1 ] ||
      config_invalid "dlint configuration '${CFG_FILE}': '.checks[\"${key}\"]' is not a dlint check. dlint has these: ${DLINT_CHECKS}"
  done <"${keys_file}"

  # The population is the checks DLINT SHIPS, never the keys the configuration
  # happens to carry. Deriving it from the file would mean a deleted section
  # quietly stopped being enforced while this invocation still reported green —
  # the vacuous pass every other refusal in this tool exists to prevent. Read
  # from dlint's own list, an absent section reaches its normal exit-3 refusal
  # and `false` stays the one way to opt a check out.
  local specs_file="${WORK_DIR}/requested-specs.txt"
  : >"${specs_file}"
  for candidate in ${DLINT_CHECKS}; do
    specs_for_check "${candidate}" >>"${specs_file}"
  done

  local -a specs=()
  mapfile -t specs <"${specs_file}"
  [ "${#specs[@]}" -gt 0 ] ||
    tool_failure "dlint lists no check to run, so --all-configured would inspect nothing"

  log_info "running every check dlint ships (${#specs[@]}) against '${CFG_FILE}'"
  run_specs "${specs[@]}"
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
  lint | --all-configured) run_all_configured "$@" ;;
  --check) run_selected --check "$@" ;;
  *) dispatch_check "${command}" "$@" ;;
  esac
}

main "$@"
