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
#     entrypoints in `automation/`, and its per-shell tools in `tools/`. Its
#     trusted-actions regex is `^acme/`. If dlint had a diene-shaped or
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
ARM_DLINT_CONFIG=""

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
arm() { arm_run present "$@"; }

# arm_absent <name> <mutator> <expected-exit> <forbidden-text> -- <dlint args...>
#
# Same contract, inverted assertion: the text must appear NOWHERE, on either
# stream. A warning is only proved to be scoped by looking for its ABSENCE on a
# run that must not raise it — "it fires when it should" and "it stays quiet when
# it should" are two different claims, and only the second one catches a warning
# that fires unconditionally.
arm_absent() { arm_run absent "$@"; }

arm_run() {
  local expect="$1" name="$2" mutator="$3" want_rc="$4" want_text="$5"
  shift 5
  [ "${1:-}" = "--" ] && shift

  ARMS_RUN=$((ARMS_RUN + 1))
  local repo="${TMPROOT}/repo-${ARMS_RUN}"
  local out="${TMPROOT}/out-${ARMS_RUN}.txt"
  local err="${TMPROOT}/err-${ARMS_RUN}.txt"
  local rc=0 stream="" stream_name=""

  materialize "${repo}"
  (cd "${repo}" && "${mutator}") || die "arm '${name}': mutator '${mutator}' failed"

  # A single arm may need DLINT_CONFIG set. It is consumed and cleared here rather
  # than exported globally, so it cannot leak into the arms that follow and quietly
  # redirect them at a configuration they were not written for.
  if [ -n "${ARM_DLINT_CONFIG:-}" ]; then
    (cd "${repo}" && DLINT_CONFIG="${ARM_DLINT_CONFIG}" "${DLINT}" "$@") >"${out}" 2>"${err}" || rc=$?
    ARM_DLINT_CONFIG=""
  else
    (cd "${repo}" && "${DLINT}" "$@") >"${out}" 2>"${err}" || rc=$?
  fi

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

  if [ "${expect}" = "present" ]; then
    if ! grep -qF -- "${want_text}" "${stream}"; then
      FAILURES=$((FAILURES + 1))
      printf '  ❌ %-58s exit %s, but %s never said: %s\n' \
        "${name}" "${rc}" "${stream_name}" "${want_text}"
      sed 's/^/       stdout| /' "${out}"
      sed 's/^/       stderr| /' "${err}"
      return 0
    fi
  else
    if grep -qF -- "${want_text}" "${out}" "${err}"; then
      FAILURES=$((FAILURES + 1))
      printf '  ❌ %-58s exit %s, but it said what it must not: %s\n' \
        "${name}" "${rc}" "${want_text}"
      sed 's/^/       stdout| /' "${out}"
      sed 's/^/       stderr| /' "${err}"
      return 0
    fi
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

m_unmatched_action_tag_pinned() {
  insert_before ci/workflows/reusable-build.yaml \
    '      - name: Build' \
    '      - uses: stranger/unlisted-action@v1'
}

m_trusted_pattern_empty() {
  edit_config '.checks["action-pins"].trustedPattern = ""'
}

m_trusted_pattern_invalid_regex() {
  edit_config '.checks["action-pins"].trustedPattern = "*broken["'
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
m_section_removed_toolchain_smoke() { edit_config 'del(.checks["toolchain-smoke"])'; }
m_section_removed_no_custom_derivations() { edit_config 'del(.checks["no-custom-derivations"])'; }

m_section_disabled_exec_bits() { edit_config '.checks["exec-bits"] = false'; }
m_section_true_exec_bits() { edit_config '.checks["exec-bits"] = true'; }

m_trust_map_key_missing() { edit_config 'del(.checks["action-pins"].trustedPattern)'; }
m_trust_map_key_wrong_type() { edit_config '.checks["action-pins"].trustedPattern = 7'; }
m_orchestrators_empty() { edit_config '.checks["ci-wiring"].orchestrators = []'; }

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

# Resolution alone is not the verdict any more: a binary that resolves and
# cannot RUN (the broken-pin shape) must refuse, with the probe's exit named.
m_toolchain_binary_run_fails() {
  require_file tools/bin-full/fixture-tool
  printf '#!/usr/bin/env bash\nexit 7\n' >tools/bin-full/fixture-tool
  chmod +x tools/bin-full/fixture-tool
}

m_toolchain_probe_args_entry() {
  edit_config '.checks["toolchain-smoke"].shells.lean = ["bash", "git version"]'
}

m_toolchain_probe_args_bad_token() {
  edit_config '.checks["toolchain-smoke"].shells.lean = ["git version; rm"]'
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

# -- YAML configuration ----------------------------------------------------- #
#
# The migration arms. Converting the fixture's own .dlint.json to dlint.yaml and
# then re-running the SAME assertions is what shows the two forms are one
# configuration rather than two code paths that happen to agree today.

# Converts .dlint.json to dlint.yaml and REMOVES the JSON, which is the migration
# a template performs. `yq -P` reads JSON and writes YAML.
m_config_as_yaml() {
  require_file .dlint.json
  yq -P . .dlint.json >dlint.yaml || die "could not convert .dlint.json to dlint.yaml"
  rm -f .dlint.json
  git add -A
}

m_yaml_exec_bit_broken() {
  m_config_as_yaml
  chmod -x automation/build.sh
}

m_yaml_section_removed() {
  m_config_as_yaml
  local tmp=""
  tmp="$(mktemp)"
  yq -P 'del(.checks["exec-bits"])' dlint.yaml >"${tmp}" || die "could not edit dlint.yaml"
  cat "${tmp}" >dlint.yaml
  rm -f "${tmp}"
}

m_yaml_disabled_section() {
  m_config_as_yaml
  local tmp=""
  tmp="$(mktemp)"
  yq -P '.checks["exec-bits"] = false' dlint.yaml >"${tmp}" || die "could not edit dlint.yaml"
  cat "${tmp}" >dlint.yaml
  rm -f "${tmp}"
}

# Both names present: dlint must refuse rather than silently pick one, because the
# loser would sit in the tree looking enforced.
m_both_config_names() {
  require_file .dlint.json
  yq -P . .dlint.json >dlint.yaml || die "could not convert"
  git add -A
}

m_yaml_not_parseable() {
  m_config_as_yaml
  printf 'schemaVersion: 1\nchecks:\n  exec-bits:\n   globs: [\n' >dlint.yaml
}

# An empty YAML document converts to the four bytes `null`, which a reader could
# walk into as if it were a configuration.
m_yaml_empty_document() {
  m_config_as_yaml
  : >dlint.yaml
}

m_yaml_top_level_list() {
  m_config_as_yaml
  printf -- '- schemaVersion: 1\n' >dlint.yaml
}

m_yaml_wrong_schema_version() {
  m_config_as_yaml
  local tmp=""
  tmp="$(mktemp)"
  yq -P '.schemaVersion = 2' dlint.yaml >"${tmp}" || die "could not edit dlint.yaml"
  cat "${tmp}" >dlint.yaml
  rm -f "${tmp}"
}

# DLINT_CONFIG must still win, and its format must follow its extension.
m_config_at_custom_yaml_path() {
  m_config_as_yaml
  mkdir -p config
  mv dlint.yaml config/linters.yaml
  git add -A
}

# -- no-custom-derivations -------------------------------------------------- #
#
# ONE ARM PER VOCABULARY MEMBER. The point of this check is an ABSENCE claim, and
# an absence claim is only as wide as its word list — so no member is merely
# asserted to be covered, each is planted and caught. Redundancy across spellings
# of one word would prove nothing about whether the list spans the concept.

# Plants a custom build written with the named builder.
plant_builder() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    "  custom = pkgs.$1 { name = \"thing\"; };"
}

m_nocustom_override_attrs() { plant_builder 'overrideAttrs'; }
m_nocustom_override_derivation() { plant_builder 'overrideDerivation'; }
m_nocustom_mk_derivation() { plant_builder 'mkDerivation'; }
m_nocustom_run_command() { plant_builder 'runCommand'; }
m_nocustom_build_env() { plant_builder 'buildEnv'; }
m_nocustom_symlink_join() { plant_builder 'symlinkJoin'; }
m_nocustom_write_shell_application() { plant_builder 'writeShellApplication'; }
m_nocustom_write_shell_script_bin() { plant_builder 'writeShellScriptBin'; }
m_nocustom_write_script_bin() { plant_builder 'writeScriptBin'; }
m_nocustom_write_text_file() { plant_builder 'writeTextFile'; }
m_nocustom_bare_derivation() { plant_builder 'derivation'; }

# A substring form: 'stdenv.mkDerivation' is deliberately NOT its own vocabulary
# member because 'mkDerivation' already catches it. This arm proves that.
m_nocustom_stdenv_mk_derivation() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  custom = pkgs.stdenv.mkDerivation { name = "thing"; };'
}

# Planted in the SECOND declared path, so the check is shown to inspect every
# path it declares rather than only the first.
m_nocustom_in_second_path() {
  insert_before nix/env.nix '  env = with pkgs; [' \
    '  custom = pkgs.runCommand "thing" { } "true";'
}

# -- no-custom-derivations: a comment is PROSE, not code -------------------- #
#
# The measured inversion this fix is about. On a live tree the check REFUSED a
# file whose only occurrence of a builder was inside a COMMENT — a sentence
# explaining that the practice had been avoided — while a file carrying three real
# derivations PASSED, because it was not in the declared paths. Blind in the
# direction that matters, loud in the direction that does not.

m_nocustom_comment_only() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  # This list carries no pkgs.buildEnv: bundles are hoisted to the registry.'
}

# The A-B pair. IDENTICAL trees, ONE variable: the same sentence reworded to avoid
# the vocabulary. Before this fix the two exits differed on the same code, which is
# the whole defect stated as an experiment.
m_nocustom_comment_reworded() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  # This list carries no bundle builder: bundles are hoisted to the registry.'
}

# The purest instance: COMPLIANCE DOCUMENTATION. A comment saying the node takes go
# from the registry INSTEAD of carrying its own builder is a file explaining why it
# CONFORMS — and the gate refused it for saying so. A gate that punishes
# documenting why you complied with it is the class in one sentence.
m_nocustom_compliance_comment() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  # go comes from the registry INSTEAD of carrying its own overrideAttrs here.'
}

m_nocustom_block_comment() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  /* symlinkJoin and buildEnv are deliberately absent here; see docs/. */'
}

# A block comment that SPANS lines. The scanner is a state machine rather than a
# regex because the context of a word cannot be decided one line at a time.
m_nocustom_multiline_block_comment() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  /* the registry owns every'
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '     mkDerivation and symlinkJoin this template would otherwise write */'
}

# THE MUST-DIFFER CONTROLS. A stripper that removed too much would turn this whole
# check off, so each of these plants a REAL builder in a place a careless stripper
# would drop.

m_nocustom_code_with_trailing_comment() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  custom = pkgs.symlinkJoin { name = "thing"; }; # to be hoisted, honestly'
}

# A '#' inside a STRING does not start a comment. Under a stripper that scanned for
# the character without knowing where it sat, everything after it would vanish —
# and the real builder on this line with it.
m_nocustom_hash_inside_string() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  src = "https://example.invalid/x#frag"; custom = pkgs.buildEnv { };'
}

# The same control for the indented-string form.
m_nocustom_hash_inside_indented_string() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    "  note = ''# not a comment''; custom = pkgs.writeTextFile { };"
}

# -- no-custom-derivations: every offender, in one run ---------------------- #
#
# Stopping at the first offender makes removing one uncover the next, so a
# repository learns the size of its problem one run at a time.

m_nocustom_three_in_one_file() {
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  one = pkgs.buildEnv { name = "one"; };'
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  two = pkgs.buildEnv { name = "two"; };'
  insert_before nix/packages.nix '  packages = with pkgs; [' \
    '  three = pkgs.buildEnv { name = "three"; };'
}

# Two DIFFERENT builders in one file: the report is grouped by builder, and both
# groups must survive one run.
m_nocustom_two_builders_one_file() {
  plant_builder 'symlinkJoin'
  plant_builder 'writeScriptBin'
}

# One offender in EACH declared file. Several arms share this mutator and each
# asserts a DIFFERENT file's message, so together they prove the run did not stop
# at the first file.
m_nocustom_in_both_paths() {
  plant_builder 'symlinkJoin'
  insert_before nix/env.nix '  env = with pkgs; [' \
    '  custom = pkgs.runCommand "thing" { } "true";'
}

# -- no-custom-derivations: coverage honesty -------------------------------- #
#
# The other half of the inversion: the file carrying three real derivations was
# never opened, because nothing declared it — and nothing said so.

m_nocustom_undeclared_nix_file() {
  printf '{ pkgs }:\n{\n  extra = pkgs.buildEnv { name = "sneaky"; };\n}\n' >nix/overlays.nix
}

# Declaring the file dlint warned about turns the silent pass into the refusal it
# should always have been.
m_nocustom_undeclared_then_declared() {
  m_nocustom_undeclared_nix_file
  edit_config '.checks["no-custom-derivations"].paths += ["nix/overlays.nix"]'
}

# A declared DIRECTORY stands for every file beneath it, so it covers a file that
# did not exist when the configuration was written.
m_nocustom_directory_path() {
  m_nocustom_undeclared_nix_file
  edit_config '.checks["no-custom-derivations"].paths = ["nix"]'
}

# The escape hatch: a repository whose nix files legitimately live elsewhere
# narrows the sweep, and the warning goes quiet because it was told to.
m_nocustom_discover_narrowed() {
  m_nocustom_undeclared_nix_file
  edit_config '.checks["no-custom-derivations"].discover = ["no/such/dir/*.nix"]'
}

m_nocustom_discover_empty() {
  edit_config '.checks["no-custom-derivations"].discover = []'
}

m_nocustom_paths_empty() { edit_config '.checks["no-custom-derivations"].paths = []'; }
m_nocustom_forbid_empty() { edit_config '.checks["no-custom-derivations"].forbid = []'; }

# A narrowed vocabulary must still catch what it names, and must PRINT what it
# enumerated so its green states how wide it was.
m_nocustom_narrowed_vocabulary() {
  edit_config '.checks["no-custom-derivations"].forbid = ["symlinkJoin"]'
  plant_builder 'symlinkJoin'
}

# The hazard a narrowed vocabulary carries: it goes green on a builder it does
# not name. That is not a defect in dlint, it is the reason the vocabulary is
# printed, and this arm records the behaviour explicitly.
m_nocustom_narrow_vocabulary_misses() {
  edit_config '.checks["no-custom-derivations"].forbid = ["symlinkJoin"]'
  plant_builder 'overrideAttrs'
}

m_nocustom_declared_path_missing() {
  require_file nix/env.nix
  rm -f nix/env.nix
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
arm "toolchain-smoke baseline (2 shells entered)" m_none 0 "✅ Every declared shell resolves and runs its required binaries (full lean)" -- toolchain-smoke

printf '\naction-pins mutations\n'
arm "trusted action pinned to a SHA" m_trusted_pinned_to_sha 1 \
  "must use a major pin" -- action-pins trusted
arm "non-trusted action pinned to a tag" m_nontrusted_pinned_to_tag 1 \
  "must use an exact SHA" -- action-pins non-trusted
arm "non-trusted SHA without a tag comment" m_nontrusted_sha_without_comment 1 \
  "needs its tag as a trailing comment" -- action-pins non-trusted
arm "trailing comment names no tag" m_nontrusted_comment_without_tag 1 \
  "trailing comment must name the source tag" -- action-pins non-trusted
arm "unmatched action defaults to non-trusted (tag refused)" m_unmatched_action_tag_pinned 1 \
  "must use an exact SHA" -- action-pins non-trusted
arm "unmatched action is no business of the trusted pass" m_unmatched_action_tag_pinned 0 \
  "✅ trusted action pins conform" -- action-pins trusted
arm "empty trustedPattern refuses" m_trusted_pattern_empty 4 \
  "must not be empty" -- action-pins trusted
arm "invalid trustedPattern regex refuses" m_trusted_pattern_invalid_regex 4 \
  "is not a valid regex" -- action-pins trusted
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

printf '\ntoolchain-smoke mutations (per-shell scoping)\n'
arm "declared binary missing in a named shell" m_toolchain_binary_missing 1 \
  "shell 'full' is missing binary" -- toolchain-smoke
# THE SCOPING ARM. Same binary, two shells, two verdicts. This is green under an
# implementation that ignores the shell and only inspects where it was invoked.
arm "a binary present in one shell is missing in another" m_toolchain_binary_missing_in_one_shell_only 1 \
  "shell 'lean' is missing binary 'fixture-tool'" -- toolchain-smoke
arm "the refusal names the shell, not the environment" m_toolchain_binary_missing_in_one_shell_only 1 \
  "'lean'" -- toolchain-smoke
arm "a binary that resolves but cannot run refuses" m_toolchain_binary_run_fails 1 \
  "resolves binary 'fixture-tool' but 'fixture-tool --version' exited 7 — the build does not run" -- toolchain-smoke
arm "a probe-args entry runs the stated probe" m_toolchain_probe_args_entry 0 \
  "✅ Every declared shell resolves and runs its required binaries (full lean)" -- toolchain-smoke
arm "a probe argument with shell metacharacters refuses" m_toolchain_probe_args_bad_token 4 \
  "must be a flag-shaped token" -- toolchain-smoke
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
  "✅ The invocation environment resolves and runs every binary declared for 'fixture'" -- toolchain-smoke
arm "legacy form says it did NOT enter the shell" m_toolchain_legacy_form 0 \
  "not entered" -- toolchain-smoke
arm "legacy form refusal blames the environment" m_toolchain_legacy_binary_missing 1 \
  "the invocation environment is missing binary" -- toolchain-smoke
arm "legacy shell name invalid" m_toolchain_legacy_shell_invalid 4 \
  "must name one shell" -- toolchain-smoke

printf '\nno-custom-derivations mutations (one arm per vocabulary member)\n'
arm "no-custom-derivations baseline" m_none 0 \
  "✅ Template nix stays plain declarative lists" -- no-custom-derivations
# The clause that survives if everything else is dropped: a green states how wide
# it was, so it cannot retire doubt it never earned.
arm "the green PRINTS the vocabulary it enumerated" m_none 0 \
  "vocabulary enumerated (11):" -- no-custom-derivations
arm "overrideAttrs" m_nocustom_override_attrs 1 \
  "uses 'overrideAttrs'" -- no-custom-derivations
arm "overrideDerivation" m_nocustom_override_derivation 1 \
  "uses 'overrideDerivation'" -- no-custom-derivations
arm "mkDerivation" m_nocustom_mk_derivation 1 \
  "uses 'mkDerivation'" -- no-custom-derivations
arm "runCommand" m_nocustom_run_command 1 \
  "uses 'runCommand'" -- no-custom-derivations
arm "buildEnv" m_nocustom_build_env 1 \
  "uses 'buildEnv'" -- no-custom-derivations
arm "symlinkJoin" m_nocustom_symlink_join 1 \
  "uses 'symlinkJoin'" -- no-custom-derivations
arm "writeShellApplication" m_nocustom_write_shell_application 1 \
  "uses 'writeShellApplication'" -- no-custom-derivations
arm "writeShellScriptBin" m_nocustom_write_shell_script_bin 1 \
  "uses 'writeShellScriptBin'" -- no-custom-derivations
arm "writeScriptBin" m_nocustom_write_script_bin 1 \
  "uses 'writeScriptBin'" -- no-custom-derivations
arm "writeTextFile" m_nocustom_write_text_file 1 \
  "uses 'writeTextFile'" -- no-custom-derivations
arm "bare derivation" m_nocustom_bare_derivation 1 \
  "uses 'derivation'" -- no-custom-derivations
# 'stdenv.mkDerivation' is not a separate vocabulary member because 'mkDerivation'
# already contains it. This arm is the evidence for that claim rather than an
# assertion of it.
arm "stdenv.mkDerivation is caught by mkDerivation" m_nocustom_stdenv_mk_derivation 1 \
  "uses 'mkDerivation'" -- no-custom-derivations
arm "the refusal names the resolver reason" m_nocustom_override_attrs 1 \
  "not resolver-mergeable" -- no-custom-derivations
arm "the refusal quotes the offending line" m_nocustom_override_attrs 1 \
  "custom = pkgs.overrideAttrs" -- no-custom-derivations
# Proves every declared path is inspected, not just the first.
arm "a violation in the SECOND declared path" m_nocustom_in_second_path 1 \
  "'nix/env.nix' uses 'runCommand'" -- no-custom-derivations
arm "a narrowed vocabulary still catches what it names" m_nocustom_narrowed_vocabulary 1 \
  "uses 'symlinkJoin'" -- no-custom-derivations
arm "a narrowed vocabulary states its width" m_nocustom_narrow_vocabulary_misses 0 \
  "vocabulary enumerated (1): symlinkJoin" -- no-custom-derivations
printf '\nno-custom-derivations: a comment is prose, not code\n'
# THE ARM THAT NAMES THE DEFECT. The only occurrence of the vocabulary is a
# sentence saying the practice was AVOIDED. This exits 1 under the implementation
# that shipped, and must exit 0 now.
arm "a builder named only in a comment PASSES" m_nocustom_comment_only 0 \
  "✅ Template nix stays plain declarative lists" -- no-custom-derivations
# The A-B pair: identical trees, one variable — the same sentence reworded to
# avoid the vocabulary. The two exits must now agree, because the code does.
arm "the same comment reworded PASSES too (the A-B control)" m_nocustom_comment_reworded 0 \
  "✅ Template nix stays plain declarative lists" -- no-custom-derivations
# The purest instance: a file documenting WHY IT COMPLIES was refused for saying so.
arm "compliance documentation is not a violation" m_nocustom_compliance_comment 0 \
  "✅ Template nix stays plain declarative lists" -- no-custom-derivations
arm "a block comment is prose too" m_nocustom_block_comment 0 \
  "✅ Template nix stays plain declarative lists" -- no-custom-derivations
arm "a block comment spanning lines is prose too" m_nocustom_multiline_block_comment 0 \
  "✅ Template nix stays plain declarative lists" -- no-custom-derivations
# The must-differ controls. A stripper that removed too much would turn this check
# off entirely, so each of these plants a REAL builder where a careless one would
# drop it.
arm "a real builder with a trailing comment still refuses" m_nocustom_code_with_trailing_comment 1 \
  "uses 'symlinkJoin'" -- no-custom-derivations
arm "a '#' inside a string does not hide the builder after it" m_nocustom_hash_inside_string 1 \
  "uses 'buildEnv'" -- no-custom-derivations
arm "a '#' inside an indented string does not either" m_nocustom_hash_inside_indented_string 1 \
  "uses 'writeTextFile'" -- no-custom-derivations

printf '\nno-custom-derivations: every offender, in one run\n'
# Three arms, one mutator: each asserts a DIFFERENT planted line, so together they
# prove the run did not stop at the first match. Removing one offender must not be
# how a repository discovers the next.
arm "three offenders: the first is reported" m_nocustom_three_in_one_file 1 \
  'one = pkgs.buildEnv' -- no-custom-derivations
arm "three offenders: the second is reported" m_nocustom_three_in_one_file 1 \
  'two = pkgs.buildEnv' -- no-custom-derivations
arm "three offenders: the third is reported" m_nocustom_three_in_one_file 1 \
  'three = pkgs.buildEnv' -- no-custom-derivations
arm "three offenders are counted, not just the first" m_nocustom_three_in_one_file 1 \
  "3 offending line(s) across 1 of 2 inspected file(s)" -- no-custom-derivations
arm "two builders in one file: the first group is reported" m_nocustom_two_builders_one_file 1 \
  "uses 'symlinkJoin'" -- no-custom-derivations
arm "two builders in one file: the second group is too" m_nocustom_two_builders_one_file 1 \
  "uses 'writeScriptBin'" -- no-custom-derivations
arm "offenders in both files: the first file is reported" m_nocustom_in_both_paths 1 \
  "'nix/packages.nix' uses 'symlinkJoin'" -- no-custom-derivations
arm "offenders in both files: the second file is too" m_nocustom_in_both_paths 1 \
  "'nix/env.nix' uses 'runCommand'" -- no-custom-derivations
arm "the summary states the whole list, not the first fault" m_nocustom_in_both_paths 1 \
  "2 offending line(s) across 2 of 2 inspected file(s)" -- no-custom-derivations

printf '\nno-custom-derivations: coverage honesty\n'
# The green already states its vocabulary; it must state its SUBJECTS too. An
# absence claim is only as wide as the files it opened.
arm "the green PRINTS the paths it scanned" m_none 0 \
  "files inspected: 2 (nix/packages.nix nix/env.nix)" -- no-custom-derivations
# The other half of the measured inversion: a file full of derivations that nothing
# declared. dlint cannot know it is in scope, so this is a WARNING — but it can
# refuse to be quiet about it, and the warning NAMES the file.
arm "an undeclared nix file is named in a warning" m_nocustom_undeclared_nix_file 0 \
  "never read them: nix/overlays.nix" -- no-custom-derivations
arm "the warning is a warning, not a refusal" m_nocustom_undeclared_nix_file 0 \
  "⚠️ no-custom-derivations:" -- no-custom-derivations
# The warning is about SCOPE, not a scan it did behind the configuration's back:
# the undeclared file's builder is not reported, because it was never read.
arm_absent "the warning does not pretend to have read the file" m_nocustom_undeclared_nix_file 0 \
  "uses 'buildEnv'" -- no-custom-derivations
# It must stay quiet when there is nothing to say — a warning that fires
# unconditionally is noise, and noise is how a real one gets ignored.
arm_absent "no undeclared nix file, no warning" m_none 0 \
  "are NOT covered by the declared paths" -- no-custom-derivations
# Declaring the file dlint named turns the silent pass into the refusal it should
# always have been. This is the arm that closes the loop on the inversion.
arm "declaring the warned-about file makes it refuse" m_nocustom_undeclared_then_declared 1 \
  "'nix/overlays.nix' uses 'buildEnv'" -- no-custom-derivations
arm_absent "and the warning goes quiet once it is declared" m_nocustom_undeclared_then_declared 1 \
  "are NOT covered by the declared paths" -- no-custom-derivations
# A declared DIRECTORY stands for every file beneath it, so it covers a file that
# did not exist when the configuration was written.
arm "a declared directory covers the files beneath it" m_nocustom_directory_path 1 \
  "'nix/overlays.nix' uses 'buildEnv'" -- no-custom-derivations
arm_absent "a declared directory raises no coverage warning" m_nocustom_directory_path 1 \
  "are NOT covered by the declared paths" -- no-custom-derivations
# The escape hatch, declared rather than assumed.
arm_absent "a narrowed 'discover' silences the sweep it was told to skip" m_nocustom_discover_narrowed 0 \
  "are NOT covered by the declared paths" -- no-custom-derivations
arm "an empty 'discover' could never notice anything" m_nocustom_discover_empty 4 \
  "could never notice a nix file it does not read" -- no-custom-derivations

arm "no-custom paths list empty" m_nocustom_paths_empty 4 \
  "must name at least one path" -- no-custom-derivations
arm "forbid list explicitly empty forbids nothing" m_nocustom_forbid_empty 4 \
  "would forbid nothing and pass unconditionally" -- no-custom-derivations
arm "a declared path that is gone is absent, not clean" m_nocustom_declared_path_missing 3 \
  "does not exist, so the resolver-shape rule has no subject" -- no-custom-derivations
arm "no-custom-derivations rejects arguments" m_none 2 \
  "no-custom-derivations takes no arguments" -- no-custom-derivations --fix

printf '\nconfiguration arms (an absent subject is never a pass)\n'
arm "config file absent / action-pins" m_config_deleted 3 \
  "dlint configuration is missing" -- action-pins trusted
arm "config file absent / exec-bits" m_config_deleted 3 \
  "dlint configuration is missing" -- exec-bits
arm "config file absent / ci-wiring" m_config_deleted 3 \
  "dlint configuration is missing" -- ci-wiring
arm "config file absent / toolchain-smoke" m_config_deleted 3 \
  "dlint configuration is missing" -- toolchain-smoke
arm "config file absent / no-custom-derivations" m_config_deleted 3 \
  "dlint configuration is missing" -- no-custom-derivations
arm "config section absent / action-pins" m_section_removed_action_pins 3 \
  "An absent section is never a pass" -- action-pins trusted
arm "config section absent / exec-bits" m_section_removed_exec_bits 3 \
  "An absent section is never a pass" -- exec-bits
arm "config section absent / ci-wiring" m_section_removed_ci_wiring 3 \
  "An absent section is never a pass" -- ci-wiring
arm "config section absent / toolchain-smoke" m_section_removed_toolchain_smoke 3 \
  "An absent section is never a pass" -- toolchain-smoke
arm "config section absent / no-custom-derivations" m_section_removed_no_custom_derivations 3 \
  "An absent section is never a pass" -- no-custom-derivations
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

printf '\nYAML configuration (dlint.yaml is the canonical name)\n'
# The same five checks, same fixture, config converted to YAML and the JSON
# removed. These are the arms that show the two forms are ONE configuration.
arm "yaml config / action-pins trusted" m_config_as_yaml 0 \
  "✅ trusted action pins conform" -- action-pins trusted
arm "yaml config / action-pins non-trusted" m_config_as_yaml 0 \
  "✅ non-trusted action pins conform" -- action-pins non-trusted
arm "yaml config / exec-bits" m_config_as_yaml 0 \
  "✅ Tracked shell scripts are executable" -- exec-bits
arm "yaml config / ci-wiring" m_config_as_yaml 0 \
  "✅ Workflow jobs resolve to existing CI scripts" -- ci-wiring
arm "yaml config / toolchain-smoke" m_config_as_yaml 0 \
  "✅ Every declared shell resolves and runs its required binaries (full lean)" -- toolchain-smoke
arm "yaml config / --all-configured (every reader)" m_config_as_yaml 0 \
  "✅ every requested check passed (6)" -- --all-configured
# Added by the #68 rebase, and predicted by the stale-scope law before it was found:
# no-custom-derivations reads its own `forbid` type directly, a reader this refactor
# never saw. Under YAML that read would have been handed the YAML file. This arm is
# the must-differ control for it — it fails if that one line is left on CFG_FILE
# while every other arm in the suite still passes.
arm "yaml config / no-custom-derivations" m_config_as_yaml 0 \
  "✅ Template nix stays plain declarative lists" -- no-custom-derivations

# A green under YAML proves nothing on its own — the check has to still be able to
# FAIL when read from YAML.
arm "yaml config still refuses a real violation" m_yaml_exec_bit_broken 1 \
  "is tracked but not executable" -- exec-bits
arm "yaml config: an absent section is still not a pass" m_yaml_section_removed 3 \
  "An absent section is never a pass" -- exec-bits
arm "yaml config: an explicit opt-out is still honoured" m_yaml_disabled_section 0 \
  "⏭️ dlint exec-bits is disabled" -- exec-bits
# The message must name the file the AUTHOR wrote, never the converted temporary.
arm "yaml refusal names dlint.yaml, not a temp file" m_yaml_section_removed 3 \
  "'dlint.yaml'" -- exec-bits
arm "yaml config: schemaVersion is still enforced" m_yaml_wrong_schema_version 4 \
  "'.schemaVersion' must be 1" -- exec-bits

arm "both config names present is an error" m_both_config_names 4 \
  "Keep one" -- exec-bits
arm "unparseable yaml is invalid, not empty" m_yaml_not_parseable 4 \
  "is not valid YAML" -- exec-bits
arm "an empty yaml document configures nothing" m_yaml_empty_document 4 \
  "must be a YAML mapping at the top level" -- exec-bits
arm "a top-level yaml list is not a mapping" m_yaml_top_level_list 4 \
  "must be a YAML mapping at the top level" -- exec-bits

ARM_DLINT_CONFIG="config/linters.yaml"
arm "DLINT_CONFIG wins and follows its extension" m_config_at_custom_yaml_path 0 \
  "✅ Tracked shell scripts are executable" -- exec-bits
ARM_DLINT_CONFIG="config/nowhere.yaml"
arm "DLINT_CONFIG naming a missing file is absent" m_none 3 \
  "named by DLINT_CONFIG, does not exist" -- exec-bits

printf '\nmultiple checks in one invocation\n'
arm "--all-configured baseline (6 specs from 5 sections)" m_none 0 \
  "✅ every requested check passed (6)" -- --all-configured
arm "--all-configured counts what it ran" m_none 0 \
  "checks run: 6 (did not pass: 0)" -- --all-configured
# --all-configured reads dlint's OWN check list, so a check added to dlint is
# enforced by it without anything else being edited: plant a fault in one check
# and require the aggregate run to catch it.
arm "--all-configured enforces exec-bits too" m_entrypoint_not_executable 1 \
  "is not executable" -- --all-configured
# The same control for the check this PR adds. --all-configured reads dlint's OWN list, so a
# check added to dlint must be enforced by the aggregate run without anything else being edited.
# Under a purely TEXTUAL merge onto the multi-check work, DLINT_CHECKS would not have gained
# no-custom-derivations and THIS ARM WOULD BE GREEN while the check went unenforced.
arm "--all-configured enforces no-custom-derivations too" m_nocustom_override_attrs 1 \
  "uses 'overrideAttrs'" -- --all-configured

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
  "dlint configuration is missing" -- --all-configured
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
arm "toolchain-smoke rebaseline" m_none 0 "✅ Every declared shell resolves and runs its required binaries (full lean)" -- toolchain-smoke
arm "no-custom-derivations rebaseline" m_none 0 "✅ Template nix stays plain declarative lists" -- no-custom-derivations

printf '\narms: %s/%s passed\n' "${ARMS_PASSED}" "${ARMS_RUN}"
if [ "${FAILURES}" -ne 0 ]; then
  printf '❌ %s arm(s) failed\n' "${FAILURES}" >&2
  exit 1
fi
printf '✅ every arm passed\n'
