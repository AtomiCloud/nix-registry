#!/usr/bin/env bash
#
# dlint failure-injection harness.
#
# Every check dlint ships is proved by INJECTION, not by being green. Each arm
# either runs on a conforming fixture (a baseline) or on a deliberately broken
# copy of it (a mutation), and every arm asserts the REFUSAL TEXT rather than
# only the exit code: a gate that is merely non-zero has not been shown to say
# why, and a refusal that exits non-zero with no message at all is a real defect
# this shape of harness has caught before.
#
# Three properties are structural here, and each exists because its absence has
# drawn blood somewhere:
#
#   * Every mutator asserts that its target is present before changing it, so an
#     arm can never decay into a no-op that "proves" a refusal dlint never had to
#     produce.
#   * Every arm gets a FRESH fixture repository, so arms cannot contaminate each
#     other, and the four baselines are re-asserted after all mutations.
#   * The fixture repository is deliberately laid out like NEITHER of the two
#     repositories in play: its workflows live in `ci/workflows`, its CI
#     entrypoints in `automation/`, its trust map in `trust/actions.json` and its
#     vendored tree in `third_party/skills`. If dlint had a diene-shaped or
#     registry-shaped constant baked into it, these arms would fail.
#
# Usage:
#   ./inject.sh                  # builds .#dlint from a CLEAN tree and tests that
#   ./inject.sh --dlint <path>   # tests the dlint you name

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURE_DIR="${SCRIPT_DIR}/fixtures/conforming"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

DLINT=""

die() {
  printf '❌ harness: %s\n' "$1" >&2
  exit 1
}

while [ "$#" -gt 0 ]; do
  case "$1" in
  --dlint)
    [ "$#" -ge 2 ] || die "--dlint needs a path"
    DLINT="$2"
    shift 2
    ;;
  -h | --help)
    printf 'Usage: %s [--dlint <path-to-dlint>]\n' "$0"
    exit 0
    ;;
  *) die "unknown argument '$1'" ;;
  esac
done

if [ -z "${DLINT}" ]; then
  # `nix build` on a dirty tree builds uncommitted content, so "which dlint did
  # you test" stops having an answer. Refuse, and say how to answer it explicitly.
  dirty=""
  dirty="$(git -C "${REPO_ROOT}" status --porcelain)" ||
    die "could not read the state of '${REPO_ROOT}'"
  [ -z "${dirty}" ] ||
    die "'${REPO_ROOT}' is dirty, so a built dlint could not be identified. Commit first, or pass --dlint <path>."
  DLINT="$(nix build --no-link --print-out-paths "${REPO_ROOT}#dlint")/bin/dlint" ||
    die "could not build ${REPO_ROOT}#dlint"
fi

[ -x "${DLINT}" ] || die "'${DLINT}' is not an executable"

# Every arm runs after `cd` into a fixture repository, so a relative --dlint would
# pass the check above and then fail every arm with exit 127 and no explanation.
# Resolved in steps: in `x="$(cd … && pwd)/$(basename …)"` the assignment carries
# the status of the LAST substitution, so a failed cd would be masked by a
# successful basename and produce a plausible-looking '/dlint'.
dlint_dir=""
dlint_dir="$(dirname "${DLINT}")" ||
  die "could not take the directory of '${DLINT}'"
dlint_dir="$(cd "${dlint_dir}" && pwd)" ||
  die "could not resolve '${dlint_dir}' to an absolute path"
DLINT="${dlint_dir}/$(basename "${DLINT}")"
[ -x "${DLINT}" ] || die "'${DLINT}' is not an executable"

printf 'dlint under test: %s\n' "${DLINT}"
"${DLINT}" --version || die "'${DLINT} --version' failed"
printf 'fixture layout:   %s (workflows in ci/workflows, entrypoints in automation/)\n\n' "${FIXTURE_DIR}"

# The fixture repositories must not inherit the operator's git configuration:
# a global core.hooksPath or template would leak into every arm.
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME="dlint tests"
export GIT_AUTHOR_EMAIL="dlint@example.invalid"
export GIT_COMMITTER_NAME="dlint tests"
export GIT_COMMITTER_EMAIL="dlint@example.invalid"

TMPROOT="$(mktemp -d)" || die "could not create a working directory"
trap 'rm -rf "${TMPROOT}"' EXIT

ARMS_RUN=0
ARMS_PASSED=0
FAILURES=0

# --------------------------------------------------------------------------- #
# fixture helpers (mutators run with the fresh repository as their CWD)
# --------------------------------------------------------------------------- #

materialize() {
  local repo="$1"
  mkdir -p "${repo}"
  cp -R "${FIXTURE_DIR}/." "${repo}/"
  chmod +x "${repo}/automation/"*.sh "${repo}/tools/"*.sh
  git -C "${repo}" init -q -b main
  git -C "${repo}" add -A
  git -C "${repo}" commit -q -m "fixture"
}

require_match() {
  grep -qF -- "$2" "$1" ||
    die "mutation target '$2' is not in '$1' — the arm would have proved nothing"
}

require_file() {
  [ -e "$1" ] || die "mutation target '$1' does not exist — the arm would have proved nothing"
}

# Fixed-string replace-in-place. awk avoids sed's regex escaping entirely, and
# writing through `cat` keeps the file's mode (a lost exec bit would silently
# turn one arm's subject into another's).
replace_in() {
  local file="$1" from="$2" to="$3" tmp=""
  # awk works a line at a time, and `grep -F` reads a newline as "either
  # pattern" — so a multi-line target would pass require_match and then replace
  # nothing at all, leaving an arm that proves a refusal dlint never produced.
  case "${from}" in
  *"
"*) die "replace_in target must be a single line: '${from}'" ;;
  esac
  require_match "${file}" "${from}"
  tmp="$(mktemp)"
  awk -v from="${from}" -v to="${to}" '{
    out = ""; rest = $0
    while ((p = index(rest, from)) > 0) {
      out = out substr(rest, 1, p - 1) to
      rest = substr(rest, p + length(from))
    }
    print out rest
  }' "${file}" >"${tmp}"
  cat "${tmp}" >"${file}"
  rm -f "${tmp}"
}

insert_before() {
  local file="$1" anchor="$2" line="$3" tmp=""
  require_match "${file}" "${anchor}"
  tmp="$(mktemp)"
  awk -v anchor="${anchor}" -v line="${line}" 'index($0, anchor) { print line } { print }' \
    "${file}" >"${tmp}"
  cat "${tmp}" >"${file}"
  rm -f "${tmp}"
}

# jq-based config edit, so the config arms cannot be defeated by formatting.
edit_config() {
  local filter="$1" tmp=""
  tmp="$(mktemp)"
  jq "${filter}" .dlint.json >"${tmp}" || die "could not edit .dlint.json with '${filter}'"
  cat "${tmp}" >.dlint.json
  rm -f "${tmp}"
}

# --------------------------------------------------------------------------- #
# the arm runner
# --------------------------------------------------------------------------- #

# arm <name> <mutator> <expected-exit> <expected-text> -- <dlint args...>
#
# The stream is derived from the expected exit rather than declared: a pass must
# report on stdout and every refusal must report on stderr, so a message that
# moves streams fails the arm.
arm() {
  local name="$1" mutator="$2" want_rc="$3" want_text="$4"
  shift 4
  [ "${1:-}" = "--" ] && shift

  ARMS_RUN=$((ARMS_RUN + 1))
  local repo="${TMPROOT}/repo-${ARMS_RUN}"
  local out="${TMPROOT}/out-${ARMS_RUN}.txt"
  local err="${TMPROOT}/err-${ARMS_RUN}.txt"
  local rc=0 stream="" stream_name=""

  materialize "${repo}"
  (cd "${repo}" && "${mutator}") || die "arm '${name}': mutator '${mutator}' failed"

  (cd "${repo}" && "${DLINT}" "$@") >"${out}" 2>"${err}" || rc=$?

  if [ "${want_rc}" -eq 0 ]; then
    stream="${out}"
    stream_name="stdout"
  else
    stream="${err}"
    stream_name="stderr"
  fi

  if [ "${rc}" -ne "${want_rc}" ]; then
    FAILURES=$((FAILURES + 1))
    printf '  ❌ %-58s expected exit %s, got %s\n' "${name}" "${want_rc}" "${rc}"
    sed 's/^/       stdout| /' "${out}"
    sed 's/^/       stderr| /' "${err}"
    return 0
  fi

  if ! grep -qF -- "${want_text}" "${stream}"; then
    FAILURES=$((FAILURES + 1))
    printf '  ❌ %-58s exit %s, but %s never said: %s\n' \
      "${name}" "${rc}" "${stream_name}" "${want_text}"
    sed 's/^/       stdout| /' "${out}"
    sed 's/^/       stderr| /' "${err}"
    return 0
  fi

  ARMS_PASSED=$((ARMS_PASSED + 1))
  printf '  ✅ %-58s exit %s\n' "${name}" "${rc}"
}

# --------------------------------------------------------------------------- #
# mutators
# --------------------------------------------------------------------------- #

m_none() { :; }

# -- action-pins ------------------------------------------------------------ #

m_trusted_pinned_to_sha() {
  replace_in ci/workflows/reusable-build.yaml \
    'acme/setup-toolchain@v3' \
    'acme/setup-toolchain@3333333333333333333333333333333333333333'
}

m_nontrusted_pinned_to_tag() {
  replace_in ci/workflows/reusable-build.yaml \
    'third-party/gatekeeper@1111111111111111111111111111111111111111 # v2.1.0' \
    'third-party/gatekeeper@v2'
}

m_nontrusted_sha_without_comment() {
  replace_in ci/workflows/reusable-build.yaml \
    'third-party/gatekeeper@1111111111111111111111111111111111111111 # v2.1.0' \
    'third-party/gatekeeper@1111111111111111111111111111111111111111'
}

m_nontrusted_comment_without_tag() {
  replace_in ci/workflows/reusable-build.yaml \
    '1111111111111111111111111111111111111111 # v2.1.0' \
    '1111111111111111111111111111111111111111 # pinned by hand'
}

m_unclassified_action() {
  insert_before ci/workflows/reusable-build.yaml \
    '      - name: Build' \
    '      - uses: stranger/unclassified-action@v1'
}

m_trust_map_deleted() {
  require_file trust/actions.json
  rm -f trust/actions.json
}

m_trust_map_wrong_schema() {
  local tmp=""
  tmp="$(mktemp)"
  jq '.schemaVersion = 2' trust/actions.json >"${tmp}"
  cat "${tmp}" >trust/actions.json
  rm -f "${tmp}"
}

m_trust_map_bad_classification() {
  local tmp=""
  tmp="$(mktemp)"
  jq '.actions["acme/setup-toolchain"] = "probably-fine"' trust/actions.json >"${tmp}"
  cat "${tmp}" >trust/actions.json
  rm -f "${tmp}"
}

m_workflows_dir_missing() {
  edit_config '.checks["action-pins"].workflowsDir = "no/such/dir"'
}

# -- configuration ---------------------------------------------------------- #

m_config_deleted() {
  require_file .dlint.json
  rm -f .dlint.json
}

m_config_not_json() {
  require_file .dlint.json
  printf 'this is not json\n' >.dlint.json
}

m_config_wrong_schema_version() { edit_config '.schemaVersion = 2'; }
m_config_checks_not_object() { edit_config '.checks = []'; }

m_section_removed_action_pins() { edit_config 'del(.checks["action-pins"])'; }
m_section_removed_exec_bits() { edit_config 'del(.checks["exec-bits"])'; }
m_section_removed_ci_wiring() { edit_config 'del(.checks["ci-wiring"])'; }
m_section_removed_skills_fresh() { edit_config 'del(.checks["skills-fresh"])'; }

m_section_disabled_exec_bits() { edit_config '.checks["exec-bits"] = false'; }
m_section_true_exec_bits() { edit_config '.checks["exec-bits"] = true'; }

m_trust_map_key_missing() { edit_config 'del(.checks["action-pins"].trustMap)'; }
m_trust_map_key_wrong_type() { edit_config '.checks["action-pins"].trustMap = 7'; }
m_orchestrators_empty() { edit_config '.checks["ci-wiring"].orchestrators = []'; }
m_paths_empty() { edit_config '.checks["skills-fresh"].paths = []'; }

# -- exec-bits -------------------------------------------------------------- #

m_entrypoint_not_executable() {
  require_file automation/build.sh
  chmod -x automation/build.sh
}

m_tracked_script_deleted_from_worktree() {
  require_file automation/deploy.sh
  rm -f automation/deploy.sh
}

m_no_tracked_shell_script() {
  edit_config '.checks["exec-bits"].globs = ["*.no-such-extension"]'
}

m_no_tracked_shell_script_declared() {
  edit_config '.checks["exec-bits"].globs = ["*.no-such-extension"]
    | .checks["exec-bits"].requireSubjects = false'
}

m_no_action_reference() {
  # A workflow directory whose only reference is a local one, which action-pins
  # skips by design: zero classifiable subjects.
  printf 'name: Local Only\non:\n  push:\njobs:\n  build:\n    uses: ./ci/workflows/reusable-build.yaml\n' \
    >ci/workflows/only.yaml
  rm -f ci/workflows/reusable-build.yaml ci/workflows/reusable-deploy.yaml ci/workflows/main.yaml
  git add -A
}

m_no_action_reference_declared() {
  m_no_action_reference
  edit_config '.checks["action-pins"].requireSubjects = false'
}

m_require_subjects_wrong_type() {
  edit_config '.checks["exec-bits"].requireSubjects = "yes"'
}

m_bash_glob_not_executable() {
  printf '#!/usr/bin/env bash\necho hi\n' >automation/extra.bash
  chmod -x automation/extra.bash
  git add automation/extra.bash
  edit_config '.checks["exec-bits"].globs = ["*.bash"]'
}

# -- ci-wiring -------------------------------------------------------------- #

m_entrypoint_missing() {
  require_file automation/build.sh
  git rm -q --cached automation/build.sh
  rm -f automation/build.sh
}

m_job_without_uses() {
  replace_in ci/workflows/main.yaml \
    '    uses: ./ci/workflows/reusable-deploy.yaml' \
    '    runs-on: ubuntu-latest'
}

m_job_uses_external_workflow() {
  replace_in ci/workflows/main.yaml \
    'uses: ./ci/workflows/reusable-deploy.yaml' \
    'uses: another-org/shared/.github/workflows/deploy.yaml@v1'
}

m_reusable_workflow_missing() {
  replace_in ci/workflows/main.yaml \
    'uses: ./ci/workflows/reusable-deploy.yaml' \
    'uses: ./ci/workflows/reusable-absent.yaml'
}

m_reusable_calls_no_entrypoint() {
  replace_in ci/workflows/reusable-deploy.yaml \
    'run: ./automation/deploy.sh' \
    'run: echo "no entrypoint here"'
}

m_orchestrator_without_jobs() {
  printf 'name: Main\non:\n  push:\n' >ci/workflows/main.yaml
}

m_orchestrator_deleted() {
  require_file ci/workflows/main.yaml
  git rm -q --cached ci/workflows/main.yaml
  rm -f ci/workflows/main.yaml
}

# -- skills-fresh ----------------------------------------------------------- #

m_vendored_source_changed() {
  replace_in source/skills/alpha/SKILL.md \
    'Vendored fixture skill.' \
    'Vendored fixture skill, revised upstream.'
}

# The index is the proposed tree, so a STAGED regeneration is the canonical
# commit-in-progress and must pass. This arm is positive on purpose: it proves
# the check refuses worktree drift rather than "any git status output".
m_vendored_regeneration_staged() {
  replace_in source/skills/alpha/SKILL.md \
    'Vendored fixture skill.' \
    'Vendored fixture skill, revised upstream.'
  bash tools/regen.sh
  git add -A third_party/skills
}

m_vendored_subjects_untracked() {
  require_file third_party/skills/alpha/SKILL.md
  git rm -q --cached third_party/skills/alpha/SKILL.md
}

m_regeneration_fails() {
  require_file tools/regen.sh
  printf '#!/usr/bin/env bash\nexit 3\n' >tools/regen.sh
  chmod +x tools/regen.sh
}

# -- repo-agnosticism ------------------------------------------------------- #

# Move the whole GitHub-shaped layout to the documented DEFAULT workflow
# directory and drop the explicit key, so the default is exercised too.
m_default_workflows_dir() {
  mkdir -p .github/workflows
  git mv ci/workflows/main.yaml .github/workflows/main.yaml
  git mv ci/workflows/reusable-build.yaml .github/workflows/reusable-build.yaml
  git mv ci/workflows/reusable-deploy.yaml .github/workflows/reusable-deploy.yaml
  replace_in .github/workflows/main.yaml './ci/workflows/' './.github/workflows/'
  edit_config 'del(.checks["action-pins"].workflowsDir)
    | del(.checks["ci-wiring"].workflowsDir)
    | .checks["ci-wiring"].orchestrators = [".github/workflows/main.yaml"]'
  git add -A
}

# --------------------------------------------------------------------------- #
# arms
# --------------------------------------------------------------------------- #

printf 'baselines (a conforming, non-diene, non-registry repository)\n'
arm "action-pins/trusted baseline" m_none 0 "✅ trusted action pins conform" -- action-pins trusted
arm "action-pins/non-trusted baseline" m_none 0 "✅ non-trusted action pins conform" -- action-pins non-trusted
arm "exec-bits baseline" m_none 0 "✅ Tracked shell scripts are executable" -- exec-bits
arm "ci-wiring baseline" m_none 0 "✅ Workflow jobs resolve to existing CI scripts" -- ci-wiring
arm "skills-fresh baseline" m_none 0 "✅ Vendored tree is fresh" -- skills-fresh

printf '\naction-pins mutations\n'
arm "trusted action pinned to a SHA" m_trusted_pinned_to_sha 1 \
  "must use a major pin" -- action-pins trusted
arm "non-trusted action pinned to a tag" m_nontrusted_pinned_to_tag 1 \
  "must use an exact SHA" -- action-pins non-trusted
arm "non-trusted SHA without a tag comment" m_nontrusted_sha_without_comment 1 \
  "needs its tag as a trailing comment" -- action-pins non-trusted
arm "trailing comment names no tag" m_nontrusted_comment_without_tag 1 \
  "trailing comment must name the source tag" -- action-pins non-trusted
arm "unclassified action (trusted pass)" m_unclassified_action 1 \
  "has no authored trust classification" -- action-pins trusted
arm "unclassified action (non-trusted pass)" m_unclassified_action 1 \
  "has no authored trust classification" -- action-pins non-trusted
arm "trust map deleted" m_trust_map_deleted 3 \
  "does not exist" -- action-pins trusted
arm "trust map schemaVersion 2" m_trust_map_wrong_schema 4 \
  "invalid schema or classification" -- action-pins trusted
arm "trust map bad classification value" m_trust_map_bad_classification 4 \
  "invalid schema or classification" -- action-pins trusted
arm "declared workflow dir absent" m_workflows_dir_missing 3 \
  "does not exist" -- action-pins trusted
arm "no action reference refuses (not a pass)" m_no_action_reference 1 \
  "would pass vacuously" -- action-pins trusted
arm "declared action-free repo passes" m_no_action_reference_declared 0 \
  "✅ trusted action pins conform" -- action-pins trusted
arm "action-pins with no mode" m_none 2 \
  "needs a mode" -- action-pins
arm "action-pins with a bogus mode" m_none 2 \
  "must be 'trusted' or 'non-trusted'" -- action-pins sort-of-trusted

printf '\nexec-bits mutations\n'
arm "tracked script not executable" m_entrypoint_not_executable 1 \
  "is tracked but not executable" -- exec-bits
arm "tracked script gone from worktree" m_tracked_script_deleted_from_worktree 1 \
  "is tracked but missing from the worktree" -- exec-bits
arm "configured glob (*.bash) not executable" m_bash_glob_not_executable 1 \
  "automation/extra.bash' is tracked but not executable" -- exec-bits
arm "no tracked subject refuses (not a pass)" m_no_tracked_shell_script 1 \
  "would pass vacuously" -- exec-bits
arm "declared zero-subject repo passes" m_no_tracked_shell_script_declared 0 \
  "✅ Tracked shell scripts are executable" -- exec-bits
arm "requireSubjects wrong type" m_require_subjects_wrong_type 4 \
  "must be true or false, found string" -- exec-bits

printf '\nci-wiring mutations\n'
arm "referenced entrypoint missing" m_entrypoint_missing 1 \
  "workflow references missing script" -- ci-wiring
arm "referenced entrypoint not executable" m_entrypoint_not_executable 1 \
  "is not executable" -- ci-wiring
arm "orchestrator job with no uses" m_job_without_uses 1 \
  "must call a reusable workflow" -- ci-wiring
arm "orchestrator job calls an external workflow" m_job_uses_external_workflow 1 \
  "must call a repository-local reusable workflow" -- ci-wiring
arm "reusable workflow missing" m_reusable_workflow_missing 1 \
  "references missing reusable workflow" -- ci-wiring
arm "reusable workflow calls no entrypoint" m_reusable_calls_no_entrypoint 1 \
  "does not call a CI entrypoint" -- ci-wiring
arm "orchestrator declares no jobs" m_orchestrator_without_jobs 1 \
  "declares no jobs" -- ci-wiring
arm "declared orchestrator deleted" m_orchestrator_deleted 1 \
  "does not exist" -- ci-wiring
arm "orchestrators list empty" m_orchestrators_empty 4 \
  "must name at least one workflow" -- ci-wiring

printf '\nskills-fresh mutations\n'
arm "vendored tree stale after regeneration" m_vendored_source_changed 1 \
  "is stale; re-run" -- skills-fresh
arm "staged regeneration passes (index-only)" m_vendored_regeneration_staged 0 \
  "✅ Vendored tree is fresh" -- skills-fresh
arm "no tracked subject left" m_vendored_subjects_untracked 1 \
  "would pass vacuously" -- skills-fresh
arm "regeneration command fails" m_regeneration_fails 1 \
  "failed (exit 3)" -- skills-fresh
arm "paths list empty" m_paths_empty 4 \
  "must name at least one tracked path" -- skills-fresh

printf '\nconfiguration arms (an absent subject is never a pass)\n'
arm "config file absent / action-pins" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- action-pins trusted
arm "config file absent / exec-bits" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- exec-bits
arm "config file absent / ci-wiring" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- ci-wiring
arm "config file absent / skills-fresh" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- skills-fresh
arm "config section absent / action-pins" m_section_removed_action_pins 3 \
  "An absent section is never a pass" -- action-pins trusted
arm "config section absent / exec-bits" m_section_removed_exec_bits 3 \
  "An absent section is never a pass" -- exec-bits
arm "config section absent / ci-wiring" m_section_removed_ci_wiring 3 \
  "An absent section is never a pass" -- ci-wiring
arm "config section absent / skills-fresh" m_section_removed_skills_fresh 3 \
  "An absent section is never a pass" -- skills-fresh
arm "explicit opt-out is honoured" m_section_disabled_exec_bits 0 \
  "⏭️ dlint exec-bits is disabled" -- exec-bits
arm "section true configures nothing" m_section_true_exec_bits 4 \
  "is true, which configures nothing" -- exec-bits
arm "config is not JSON" m_config_not_json 4 \
  "is not valid JSON" -- exec-bits
arm "config schemaVersion 2" m_config_wrong_schema_version 4 \
  "'.schemaVersion' must be 1" -- exec-bits
arm "config .checks is not an object" m_config_checks_not_object 4 \
  "'.checks' must be an object" -- exec-bits
arm "required key absent" m_trust_map_key_missing 4 \
  "is required. It is repository-specific" -- action-pins trusted
arm "required key wrong type" m_trust_map_key_wrong_type 4 \
  "must be a string, found number" -- action-pins trusted

printf '\nentrypoint arms\n'
arm "unknown check" m_none 2 \
  "dlint has exactly four" -- not-a-check
arm "there is no 'all'" m_none 2 \
  "dlint has exactly four" -- all
arm "no check at all" m_none 2 \
  "dlint needs a check to run" --
arm "exec-bits rejects arguments" m_none 2 \
  "exec-bits takes no arguments" -- exec-bits --fix

printf '\nrepo-agnosticism\n'
arm "default workflow dir / action-pins" m_default_workflows_dir 0 \
  "✅ trusted action pins conform" -- action-pins trusted
arm "default workflow dir / ci-wiring" m_default_workflows_dir 0 \
  "✅ Workflow jobs resolve to existing CI scripts" -- ci-wiring

printf '\nbaselines re-asserted after every mutation\n'
arm "action-pins/trusted rebaseline" m_none 0 "✅ trusted action pins conform" -- action-pins trusted
arm "action-pins/non-trusted rebaseline" m_none 0 "✅ non-trusted action pins conform" -- action-pins non-trusted
arm "exec-bits rebaseline" m_none 0 "✅ Tracked shell scripts are executable" -- exec-bits
arm "ci-wiring rebaseline" m_none 0 "✅ Workflow jobs resolve to existing CI scripts" -- ci-wiring
arm "skills-fresh rebaseline" m_none 0 "✅ Vendored tree is fresh" -- skills-fresh

printf '\narms: %s/%s passed\n' "${ARMS_PASSED}" "${ARMS_RUN}"
if [ "${FAILURES}" -ne 0 ]; then
  printf '❌ %s arm(s) failed\n' "${FAILURES}" >&2
  exit 1
fi
printf '✅ every arm passed\n'
