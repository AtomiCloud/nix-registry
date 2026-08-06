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

# `cd` consults CDPATH for any operand that is not absolute and does not start
# with `.` or `..`, and prints the directory it landed in — which would end up
# inside the command substitutions below. Every relative `cd` in this file is
# affected, not just one, so the variable is cleared once here rather than
# guarded at each site. (`cd --` does not help: `--` ends option parsing, it does
# not disable the CDPATH search.)
unset CDPATH

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
# CDPATH is unset at the top of this file, which is what makes this `cd` land in
# the directory it was given rather than in a CDPATH match of the same name.
dlint_dir="$(cd -- "${dlint_dir}" && pwd)" ||
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
  # The per-shell binaries carry no `.sh`, so they need their own chmod: a
  # fixture tool that silently lost its exec bit would make the toolchain-smoke
  # baseline fail for a reason that has nothing to do with dlint.
  chmod +x "${repo}/tools/bin-full/fixture-tool"
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
m_section_removed_toolchain_smoke() { edit_config 'del(.checks["toolchain-smoke"])'; }
m_section_removed_workflow_policy() { edit_config 'del(.checks["workflow-policy"])'; }

m_section_disabled_exec_bits() { edit_config '.checks["exec-bits"] = false'; }
m_section_true_exec_bits() { edit_config '.checks["exec-bits"] = true'; }

m_trust_map_key_missing() { edit_config 'del(.checks["action-pins"].trustMap)'; }
m_trust_map_key_wrong_type() { edit_config '.checks["action-pins"].trustMap = 7'; }
m_orchestrators_empty() { edit_config '.checks["ci-wiring"].orchestrators = []'; }
m_paths_empty() { edit_config '.checks["skills-fresh"].paths = []'; }

# -- toolchain-smoke ------------------------------------------------------- #

m_toolchain_binary_missing() {
  edit_config '.checks["toolchain-smoke"].shells.full = ["definitely-not-a-real-binary"]'
}

m_toolchain_binaries_empty() {
  edit_config '.checks["toolchain-smoke"].shells.full = []'
}

# The ARM THAT PROVES SCOPING. `fixture-tool` exists in the `full` shell and not
# in `lean`. Asking `lean` for it must refuse and must name `lean` — under an
# implementation that resolved binaries in the invocation environment instead of
# entering each shell, the two shells would be indistinguishable.
m_toolchain_binary_missing_in_one_shell_only() {
  edit_config '.checks["toolchain-smoke"].shells.lean += ["fixture-tool"]'
}

m_toolchain_shells_not_object() {
  edit_config '.checks["toolchain-smoke"].shells = ["full", "lean"]'
}

m_toolchain_shells_empty() {
  edit_config '.checks["toolchain-smoke"].shells = {}'
}

m_toolchain_shell_binaries_not_array() {
  edit_config '.checks["toolchain-smoke"].shells.full = "bash"'
}

m_toolchain_enter_without_placeholder() {
  edit_config '.checks["toolchain-smoke"].enter = "bash -c"'
}

m_toolchain_enter_absent() {
  edit_config 'del(.checks["toolchain-smoke"].enter)'
}

# An unenterable shell must be UNKNOWN (5), never a missing binary (1): the
# fixture's enter script exits 9 for a shell it does not know.
m_toolchain_unenterable_shell() {
  edit_config '.checks["toolchain-smoke"].shells = {"nosuch": ["bash"]}'
}

# Both forms declared at once leaves it ambiguous which shell a green is about.
m_toolchain_both_forms() {
  edit_config '.checks["toolchain-smoke"].shell = "fixture"
    | .checks["toolchain-smoke"].binaries = ["bash"]'
}

# The legacy single-shell form, kept working so a repository configured for it
# does not break the moment this lands. Its message must claim the INVOCATION
# ENVIRONMENT and not the shell it names.
m_toolchain_legacy_form() {
  edit_config 'del(.checks["toolchain-smoke"].shells)
    | del(.checks["toolchain-smoke"].enter)
    | .checks["toolchain-smoke"].shell = "fixture"
    | .checks["toolchain-smoke"].binaries = ["bash", "git"]'
}

m_toolchain_legacy_binary_missing() {
  m_toolchain_legacy_form
  edit_config '.checks["toolchain-smoke"].binaries = ["definitely-not-a-real-binary"]'
}

m_toolchain_legacy_shell_invalid() {
  m_toolchain_legacy_form
  edit_config '.checks["toolchain-smoke"].shell = "not a shell"'
}

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

# -- multiple checks in one invocation -------------------------------------- #

m_all_checks_disabled() {
  edit_config '.checks |= with_entries(.value = false)'
}

m_unknown_check_configured() {
  edit_config '.checks["not-a-check"] = {}'
}

# Two faults in two DIFFERENT checks, both of which refuse with exit 1. Used to
# prove that a multi-check run does not stop at the first refusal.
m_two_violations() {
  m_entrypoint_not_executable
  m_nontrusted_pinned_to_tag
}

# A violation (1) in one check and an absent section (3) in another. The
# aggregate must report 3: an unrunnable check outranks a known violation.
m_violation_and_absent_section() {
  m_entrypoint_not_executable
  edit_config 'del(.checks["ci-wiring"])'
}

# -- workflow-policy -------------------------------------------------------- #
#
# The first five mirror, one for one, the sabotages the validator this check
# replaces catches: its four release-trigger assertions and its one
# release-concurrency assertion. The sixth mirrors dotnet's workflow-names mode.

m_release_trigger_not_upstream() {
  replace_in ci/workflows/release.yaml 'workflows: [Main]' 'workflows: [Something-Else]'
}

m_release_trigger_any_branch() {
  replace_in ci/workflows/release.yaml 'branches: [trunk]' 'branches: [any]'
}

m_release_trigger_wrong_type() {
  replace_in ci/workflows/release.yaml 'types: [completed]' 'types: [requested]'
}

m_release_job_without_success_gate() {
  replace_in ci/workflows/release.yaml \
    "if: github.event.workflow_run.conclusion == 'success'" \
    "if: always()"
}

m_release_concurrency_group_dropped() {
  replace_in ci/workflows/release.yaml '  group: release' '  group: deploy'
}

# The whole concurrency block removed, so the PATH is absent rather than wrong.
# An absent path gets its own refusal: comparing a missing field would otherwise
# be indistinguishable from a field that is correctly null.
m_release_concurrency_absent() {
  require_match ci/workflows/release.yaml 'concurrency:'
  local tmp=""
  tmp="$(mktemp)"
  awk '/^concurrency:/ { skip = 2; next } skip > 0 { skip--; next } { print }' \
    ci/workflows/release.yaml >"${tmp}"
  cat "${tmp}" >ci/workflows/release.yaml
  rm -f "${tmp}"
}

m_workflow_name_drifted() {
  replace_in ci/workflows/main.yaml 'name: Main' 'name: Continuous'
}

m_release_workflow_deleted() {
  require_file ci/workflows/release.yaml
  rm -f ci/workflows/release.yaml
}

# A workflow file that is not parseable YAML. The value this arm protects is that
# an unreadable subject is UNKNOWN (exit 5), never a pass: the validator this
# check replaces piped yq into jq, took its status from jq, and could exit 0 with
# yq's failure only on stderr.
m_release_workflow_unparseable() {
  require_file ci/workflows/release.yaml
  printf 'name: Release\n  bad: [indentation\n    worse:\n' >ci/workflows/release.yaml
}

m_policy_assertions_empty() {
  edit_config '.checks["workflow-policy"].assertions = []'
}

m_policy_assertions_not_array() {
  edit_config '.checks["workflow-policy"].assertions = "release-trigger"'
}

m_policy_assertion_without_reason() {
  edit_config 'del(.checks["workflow-policy"].assertions[0].reason)'
}

m_policy_assertion_without_file() {
  edit_config 'del(.checks["workflow-policy"].assertions[0].file)'
}

m_policy_assertion_null_expectation() {
  edit_config '.checks["workflow-policy"].assertions[0].equals = null'
}

m_policy_assertion_bad_path() {
  edit_config '.checks["workflow-policy"].assertions[0].path = "on.workflow_run"'
}

m_policy_declared_file_missing() {
  edit_config '.checks["workflow-policy"].assertions[0].file = "ci/workflows/no-such.yaml"'
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
arm "toolchain-smoke baseline (2 shells entered)" m_none 0 "✅ Every declared shell resolves its required binaries (full lean)" -- toolchain-smoke

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

printf '\ntoolchain-smoke mutations (per-shell scoping)\n'
arm "declared binary missing in a named shell" m_toolchain_binary_missing 1 \
  "shell 'full' is missing binary" -- toolchain-smoke
# THE SCOPING ARM. Same binary, two shells, two verdicts. This is green under an
# implementation that ignores the shell and only inspects where it was invoked.
arm "a binary present in one shell is missing in another" m_toolchain_binary_missing_in_one_shell_only 1 \
  "shell 'lean' is missing binary 'fixture-tool'" -- toolchain-smoke
arm "the refusal names the shell, not the environment" m_toolchain_binary_missing_in_one_shell_only 1 \
  "'lean'" -- toolchain-smoke
arm "an unenterable shell is UNKNOWN, not a missing binary" m_toolchain_unenterable_shell 5 \
  "could not enter shell 'nosuch'" -- toolchain-smoke
arm "an unenterable shell says which command it ran" m_toolchain_unenterable_shell 5 \
  "The entry command was" -- toolchain-smoke
arm "binary list empty" m_toolchain_binaries_empty 4 \
  "declares no binary" -- toolchain-smoke
arm "shells is not an object" m_toolchain_shells_not_object 4 \
  "must be an object mapping each shell name" -- toolchain-smoke
arm "shells is empty" m_toolchain_shells_empty 4 \
  "must name at least one shell" -- toolchain-smoke
arm "a shell's binaries are not an array" m_toolchain_shell_binaries_not_array 4 \
  "must be an array of binaries" -- toolchain-smoke
arm "enter without the {shell} placeholder" m_toolchain_enter_without_placeholder 4 \
  "must contain the '{shell}' placeholder" -- toolchain-smoke
arm "scoped form without an enter command" m_toolchain_enter_absent 4 \
  "is required" -- toolchain-smoke
arm "both shell and shells declared" m_toolchain_both_forms 4 \
  "never both" -- toolchain-smoke

printf '\ntoolchain-smoke: the legacy single-shell form still works\n'
# It keeps working, but its green may not be attributed to the shell it names.
arm "legacy form passes" m_toolchain_legacy_form 0 \
  "✅ The invocation environment resolves every binary declared for 'fixture'" -- toolchain-smoke
arm "legacy form says it did NOT enter the shell" m_toolchain_legacy_form 0 \
  "not entered" -- toolchain-smoke
arm "legacy form refusal blames the environment" m_toolchain_legacy_binary_missing 1 \
  "the invocation environment is missing binary" -- toolchain-smoke
arm "legacy shell name invalid" m_toolchain_legacy_shell_invalid 4 \
  "must name one shell" -- toolchain-smoke

printf '\nworkflow-policy mutations\n'
arm "workflow-policy baseline" m_none 0 \
  "✅ Workflow policy conforms" -- workflow-policy
arm "release does not trigger from upstream" m_release_trigger_not_upstream 1 \
  "release must trigger from the Main workflow" -- workflow-policy
arm "release is not limited to one branch" m_release_trigger_any_branch 1 \
  "release must be limited to trunk" -- workflow-policy
arm "release workflow_run type drifted" m_release_trigger_wrong_type 1 \
  "release workflow_run type must be completed" -- workflow-policy
arm "release job drops its success gate" m_release_job_without_success_gate 1 \
  "release job must require upstream success" -- workflow-policy
arm "release concurrency group drifted" m_release_concurrency_group_dropped 1 \
  "release concurrency group must be release" -- workflow-policy
arm "release concurrency block absent" m_release_concurrency_absent 1 \
  "is absent" -- workflow-policy
arm "workflow name drifted" m_workflow_name_drifted 1 \
  "workflow name must be exactly Main" -- workflow-policy
# The refusal names the observed value as well as the expected one, so the
# message is actionable without re-reading the file.
arm "a refusal names what it found" m_release_concurrency_group_dropped 1 \
  'expected "release"' -- workflow-policy
arm "declared workflow file deleted" m_release_workflow_deleted 3 \
  "does not exist" -- workflow-policy
arm "declared workflow file unparseable is UNKNOWN" m_release_workflow_unparseable 5 \
  "which is not a pass" -- workflow-policy
arm "assertion list empty" m_policy_assertions_empty 4 \
  "must declare at least one assertion" -- workflow-policy
arm "assertion list is not an array" m_policy_assertions_not_array 4 \
  "must be an array of assertions" -- workflow-policy
arm "assertion without a reason" m_policy_assertion_without_reason 4 \
  "needs a non-empty 'reason'" -- workflow-policy
arm "assertion without a file" m_policy_assertion_without_file 4 \
  "needs a non-empty 'file'" -- workflow-policy
arm "assertion expecting null" m_policy_assertion_null_expectation 4 \
  "null is reserved for reporting an absent path" -- workflow-policy
arm "assertion path not rooted at '.'" m_policy_assertion_bad_path 4 \
  "must start with '.'" -- workflow-policy
arm "assertion names a missing file" m_policy_declared_file_missing 3 \
  "does not exist" -- workflow-policy
arm "workflow-policy rejects arguments" m_none 2 \
  "workflow-policy takes no arguments" -- workflow-policy release-trigger

printf '\nconfiguration arms (an absent subject is never a pass)\n'
arm "config file absent / action-pins" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- action-pins trusted
arm "config file absent / exec-bits" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- exec-bits
arm "config file absent / ci-wiring" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- ci-wiring
arm "config file absent / skills-fresh" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- skills-fresh
arm "config file absent / toolchain-smoke" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- toolchain-smoke
arm "config file absent / workflow-policy" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- workflow-policy
arm "config section absent / action-pins" m_section_removed_action_pins 3 \
  "An absent section is never a pass" -- action-pins trusted
arm "config section absent / exec-bits" m_section_removed_exec_bits 3 \
  "An absent section is never a pass" -- exec-bits
arm "config section absent / ci-wiring" m_section_removed_ci_wiring 3 \
  "An absent section is never a pass" -- ci-wiring
arm "config section absent / skills-fresh" m_section_removed_skills_fresh 3 \
  "An absent section is never a pass" -- skills-fresh
arm "config section absent / toolchain-smoke" m_section_removed_toolchain_smoke 3 \
  "An absent section is never a pass" -- toolchain-smoke
arm "config section absent / workflow-policy" m_section_removed_workflow_policy 3 \
  "An absent section is never a pass" -- workflow-policy
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
  "unknown check 'not-a-check'" -- not-a-check
arm "there is still no 'all' CHECK" m_none 2 \
  "unknown check 'all'" -- all
arm "no check at all" m_none 2 \
  "dlint needs a check to run" --
arm "exec-bits rejects arguments" m_none 2 \
  "exec-bits takes no arguments" -- exec-bits --fix

printf '\nmultiple checks in one invocation\n'
arm "--all-configured baseline (7 specs from 6 sections)" m_none 0 \
  "✅ every requested check passed (7)" -- --all-configured
arm "--all-configured counts what it ran" m_none 0 \
  "checks run: 7 (did not pass: 0)" -- --all-configured
# --all-configured reads dlint's OWN check list, so a check added to dlint is
# enforced by it without anything else being edited. This arm is what makes that
# claim checkable rather than asserted: it plants a workflow-policy fault and
# requires the aggregate run to catch it.
arm "--all-configured enforces workflow-policy too" m_release_concurrency_group_dropped 1 \
  "release concurrency group must be release" -- --all-configured

# The expansion arm that matters: only the NON-TRUSTED pin is broken. An
# --all-configured that ran just 'action-pins trusted' would report GREEN here,
# so this is the control that separates "both classes ran" from "one did".
arm "--all-configured runs BOTH action-pins classes" m_nontrusted_pinned_to_tag 1 \
  "must use an exact SHA" -- --all-configured
arm "--all-configured reports the trusted class too" m_trusted_pinned_to_sha 1 \
  "must use a major pin" -- --all-configured

# Two arms, one mutator: each asserts a DIFFERENT check's reason, so both
# together prove the run continued past the first refusal instead of stopping.
arm "two faults: the exec-bits reason is reported" m_two_violations 1 \
  "is tracked but not executable" -- --all-configured
arm "two faults: the action-pins reason is reported too" m_two_violations 1 \
  "must use an exact SHA" -- --all-configured
arm "two faults are counted, not just the first" m_two_violations 1 \
  "did not pass" -- --all-configured

# The regression guard for a hole this harness found during development: when
# --all-configured derived its work list from the CONFIG's keys, deleting a
# section made that check silently stop being enforced and the run still reported
# green. The population is dlint's own check list, so a deleted section refuses.
arm "a deleted section refuses, it is not skipped" m_section_removed_ci_wiring 3 \
  "An absent section is never a pass" -- --all-configured
arm "a deleted section still names the check" m_section_removed_ci_wiring 3 \
  '.checks["ci-wiring"]' -- --all-configured
arm "an unrunnable check outranks a violation" m_violation_and_absent_section 3 \
  "An absent section is never a pass" -- --all-configured
arm "config file absent / --all-configured" m_config_deleted 3 \
  "configuration '.dlint.json' is missing" -- --all-configured
arm "every check disabled asserts nothing" m_all_checks_disabled 3 \
  "no check left enabled" -- --all-configured
arm "a configured key that is not a check" m_unknown_check_configured 4 \
  "is not a dlint check" -- --all-configured
arm "--all-configured takes no arguments" m_none 2 \
  "--all-configured takes no arguments" -- --all-configured exec-bits

arm "--check runs one check" m_none 0 \
  "✅ every requested check passed (1)" -- --check exec-bits
# This is the exact invocation that replaces the `dlints` bash-wrapper hook.
arm "--check replaces the two-class wrapper hook" m_none 0 \
  "✅ every requested check passed (2)" -- --check "action-pins trusted" --check "action-pins non-trusted"
arm "--check reports a refusal" m_entrypoint_not_executable 1 \
  "is tracked but not executable" -- --check exec-bits
arm "--check with no value" m_none 2 \
  "'--check' needs a check to run" -- --check
arm "--check with an empty value" m_none 2 \
  "needs a check to run, got an empty value" -- --check ""
arm "--check with an unknown check" m_none 2 \
  "unknown check 'not-a-check'" -- --check not-a-check
arm "a bare argument is not a check list" m_none 2 \
  "combine checks with a repeated" -- --check exec-bits ci-wiring

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
arm "toolchain-smoke rebaseline" m_none 0 "✅ Every declared shell resolves its required binaries (full lean)" -- toolchain-smoke
arm "workflow-policy rebaseline" m_none 0 "✅ Workflow policy conforms" -- workflow-policy

printf '\narms: %s/%s passed\n' "${ARMS_PASSED}" "${ARMS_RUN}"
if [ "${FAILURES}" -ne 0 ]; then
  printf '❌ %s arm(s) failed\n' "${FAILURES}" >&2
  exit 1
fi
printf '✅ every arm passed\n'
