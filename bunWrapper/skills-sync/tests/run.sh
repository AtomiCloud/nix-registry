#!/usr/bin/env bash
#
# skills-sync acceptance battery.
#
# It runs against WHATEVER $SKILLS_SYNC names, so the same battery can be aimed
# at the working tree (`bun index.ts`) and at the packaged binary from the nix
# store. A green run against the source proves nothing about the artifact that
# ships; the artifact is stated in the header of every run.
#
#   ./tests/run.sh                                  # working tree
#   SKILLS_SYNC="$(nix build --print-out-paths .#skills-sync)/bin/skills-sync" ./tests/run.sh
#
# Two rules the battery follows, both learned from checks that could not fail:
#
#   * A POSITIVE CONTROL runs FIRST in every group. A mutation that turns a
#     check red proves nothing if the check was already red — and an errored
#     check reads exactly like a clean one.
#   * Every mutation asserts the SPECIFIC exit code and a substring of the
#     specific reason, never merely "non-zero". A check that refuses for the
#     wrong reason is a check that will pass for the wrong reason later.
#
# `set -e` is deliberately NOT used: this script's whole purpose is to run
# commands that are expected to fail and inspect their status.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PKG_DIR="$(dirname "${HERE}")"
SKILLS_SYNC="${SKILLS_SYNC:-bun ${PKG_DIR}/index.ts}"
# The override may name a bare binary or a `bun <script>` pair, so it is split
# into words — deliberately, ONCE, and here rather than at seventy call sites,
# where a stray disable comment would read as "ignore the warning" instead of
# "the splitting is the point".
# shellcheck disable=SC2206
SKILLS_SYNC_CMD=(${SKILLS_SYNC})

TMPROOT="$(mktemp -d "${TMPDIR:-/tmp}/skills-sync-tests.XXXXXX")"
export HOME="${TMPROOT}/home"
mkdir -p "${HOME}"
trap 'chmod -R u+w "${TMPROOT}" 2>/dev/null; rm -rf "${TMPROOT}"' EXIT

PASS=0
FAIL=0
FAILED_NAMES=()

RUN_OUT=""
RUN_RC=0

# Command substitution, never a pipe: `$?` after a pipe is the LAST command's
# status, which is how a failing step gets read as a passing one.
run_in() {
  local dir="$1"
  shift
  RUN_OUT="$(cd "${dir}" && env "$@" 2>&1)"
  RUN_RC=$?
}

# The negative companion to `expect`. Some claims are about what the output must
# NOT say — "this refusal does not leak a stack trace" cannot be asserted by
# looking for something.
expect_absent() {
  local name="$1" want_rc="$2" forbidden="$3"
  local ok=1 why=""
  if [ "${RUN_RC}" -ne "${want_rc}" ]; then
    ok=0
    why="exit ${RUN_RC}, wanted ${want_rc}"
  elif printf '%s' "${RUN_OUT}" | grep -qF -- "${forbidden}"; then
    ok=0
    why="output contained '${forbidden}', which it must not"
  fi
  if [ "${ok}" -eq 1 ]; then
    PASS=$((PASS + 1))
    printf '  ✅ %s\n' "${name}"
  else
    FAIL=$((FAIL + 1))
    FAILED_NAMES+=("${name}")
    printf '  ❌ %s — %s\n' "${name}" "${why}"
    printf '%s\n' "${RUN_OUT}" | sed 's/^/       | /'
  fi
}

expect() {
  local name="$1" want_rc="$2" want_text="$3"
  local ok=1 why=""
  if [ "${RUN_RC}" -ne "${want_rc}" ]; then
    ok=0
    why="exit ${RUN_RC}, wanted ${want_rc}"
  elif [ -n "${want_text}" ] && ! printf '%s' "${RUN_OUT}" | grep -qF -- "${want_text}"; then
    ok=0
    why="output did not contain '${want_text}'"
  fi
  if [ "${ok}" -eq 1 ]; then
    PASS=$((PASS + 1))
    printf '  ✅ %s\n' "${name}"
  else
    FAIL=$((FAIL + 1))
    FAILED_NAMES+=("${name}")
    printf '  ❌ %s — %s\n' "${name}" "${why}"
    printf '%s\n' "${RUN_OUT}" | sed 's/^/       | /'
  fi
}

group() { printf '\n== %s\n' "$1"; }

git_q() {
  local dir="$1"
  shift
  git -C "${dir}" -c user.email=tests@example.invalid -c user.name=tests \
    -c commit.gpgsign=false -c core.hooksPath="${TMPROOT}/no-hooks" "$@" >/dev/null 2>&1
}

# --------------------------------------------------------------------------- #
# fixtures
# --------------------------------------------------------------------------- #

# A repository that declares two node packages and has both installed. The
# vendor tree starts EMPTY; the writer fills it and the result is committed, so
# every later assertion is about a repository that was once genuinely fresh.
make_node_repo() {
  local dir="$1"
  mkdir -p "${dir}/.claude/skills/vendor"
  git init -q "${dir}"
  git -C "${dir}" config core.hooksPath "${TMPROOT}/no-hooks"

  cat >"${dir}/skills-sync.yaml" <<'YAML'
schemaVersion: 1
runtime: bun
YAML
  cat >"${dir}/package.json" <<'JSON'
{
  "name": "fixture",
  "dependencies": { "@atomicloud/diene.alpha": "1.0.0" },
  "devDependencies": { "@atomicloud/diene.beta": "2.0.0", "left-pad": "1.0.0" }
}
JSON
  mkdir -p "${dir}/node_modules/@atomicloud/diene.alpha/skills/alpha"
  mkdir -p "${dir}/node_modules/@atomicloud/diene.beta/skills/beta/nested"
  printf 'alpha skill\n' >"${dir}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
  printf 'beta skill\n' >"${dir}/node_modules/@atomicloud/diene.beta/skills/beta/SKILL.md"
  printf 'beta nested\n' >"${dir}/node_modules/@atomicloud/diene.beta/skills/beta/nested/NOTES.md"
  printf 'node_modules/\n' >"${dir}/.gitignore"
  touch "${dir}/.claude/skills/vendor/.gitkeep"
  git_q "${dir}" add -A
  git_q "${dir}" commit -m fixture --no-verify
}

# Runs the writer and commits, leaving the repository in the state every
# downstream node is supposed to be in.
sync_and_commit() {
  local dir="$1"
  run_in "${dir}" "${SKILLS_SYNC_CMD[@]}" sync
  if [ "${RUN_RC}" -ne 0 ]; then
    printf '  ‼️ fixture setup failed: skills-sync sync exited %s in %s\n' "${RUN_RC}" "${dir}"
    printf '%s\n' "${RUN_OUT}" | sed 's/^/       | /'
    return 1
  fi
  git_q "${dir}" add -A
  git_q "${dir}" commit -m sync --no-verify
}

sha_of_tree() {
  # A stable fingerprint of a directory's contents, used to prove that a
  # read-only subcommand really wrote nothing.
  local dir="$1"
  if [ ! -d "${dir}" ]; then
    printf 'ABSENT'
    return
  fi
  (cd "${dir}" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 -r sha256sum) | sha256sum | cut -d' ' -f1
}

# --------------------------------------------------------------------------- #

printf '=== skills-sync acceptance battery\n'
printf '=== artifact under test: %s\n' "${SKILLS_SYNC}"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" --version
printf '=== reported version: %s (exit %s)\n' "${RUN_OUT}" "${RUN_RC}"
printf '=== fixtures under: %s\n' "${TMPROOT}"

# --------------------------------------------------------------------------- #
group 'A. CLI surface — a subcommand exists only if the ROOT lists it or a bare invocation refuses it'
# --------------------------------------------------------------------------- #

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}"
expect 'A1 bare invocation refuses and lists the commands' 2 'skills-sync needs a command'

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" --help
expect 'A2 root help lists check' 0 '  check'
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" --help
expect 'A3 root help lists sync' 0 '  sync'
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" --help
expect 'A4 root help lists runtimes' 0 '  runtimes'

# The trap this exists to close: on a framework CLI, root help is served before
# the unknown-command handler, so a nonexistent subcommand answers `--help` with
# the root usage and exits 0 — reading as a real subcommand.
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" nosuchcommand
expect 'A5 unknown command refuses' 2 "unknown command 'nosuchcommand'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" nosuchcommand --help
expect 'A6 unknown command + --help still refuses (no root-help fallthrough)' 2 "unknown command 'nosuchcommand'"

# A per-subcommand option claim has to show THAT SUBCOMMAND'S OWN usage line.
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" check --help
expect 'A7 check --help prints check own usage line' 0 'Usage: skills-sync check --tier'
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" sync --help
expect 'A8 sync --help prints sync own usage line' 0 'Usage: skills-sync sync'
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" runtimes --help
expect 'A9 runtimes --help prints runtimes own usage line' 0 'Usage: skills-sync runtimes'

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" check
expect 'A10 check without --tier refuses (the tier is never defaulted)' 2 'requires --tier'
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" check --tier bogus
expect 'A11 unknown tier refuses' 2 "unknown tier 'bogus'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" check --tier ci --bogus
expect 'A12 unknown option on check refuses' 2 "has no option '--bogus'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" runtimes --tier ci
expect 'A13 an option that belongs to another subcommand refuses' 2 "has no option '--tier'"

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" runtimes
expect 'A14 runtimes lists the dart preset' 0 'dart'

# --------------------------------------------------------------------------- #
group 'B. OFF — the workspace and shared shape: generic wiring, no runtime named'
# --------------------------------------------------------------------------- #

OFF_REPO="${TMPROOT}/off"
mkdir -p "${OFF_REPO}/.claude/skills/vendor"
git init -q "${OFF_REPO}"
git -C "${OFF_REPO}" config core.hooksPath "${TMPROOT}/no-hooks"
touch "${OFF_REPO}/.claude/skills/vendor/.gitkeep"
git_q "${OFF_REPO}" add -A
git_q "${OFF_REPO}" commit -m off --no-verify

for tier in setup pre-commit ci; do
  run_in "${OFF_REPO}" "${SKILLS_SYNC_CMD[@]}" check --tier "${tier}"
  expect "B1 no config: --tier ${tier} is inert" 0 'names no runtime'
done
run_in "${OFF_REPO}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'B2 no config: the writer is inert too' 0 'names no runtime'

# `runtime: none` is the explicit form of the same state.
printf 'schemaVersion: 1\nruntime: none\n' >"${OFF_REPO}/skills-sync.yaml"
run_in "${OFF_REPO}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'B3 runtime: none is inert' 0 'names no runtime'

# Deleting the configuration must not be a way to switch the guarantee off while
# the vendored tree stays in the repository.
rm -f "${OFF_REPO}/skills-sync.yaml"
mkdir -p "${OFF_REPO}/.claude/skills/vendor/leftover"
printf 'orphan\n' >"${OFF_REPO}/.claude/skills/vendor/leftover/SKILL.md"
run_in "${OFF_REPO}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'B4 off + a non-empty vendor tree REFUSES (deleting the config is not an off switch)' 1 'holds 1 vendored file'

# --------------------------------------------------------------------------- #
group 'C. Positive control FIRST, then the mutation battery (node fixture, deps restored)'
# --------------------------------------------------------------------------- #

BASE="${TMPROOT}/node-base"
make_node_repo "${BASE}"
sync_and_commit "${BASE}" || printf '  ‼️ the whole C group is unusable\n'

for tier in setup pre-commit ci; do
  run_in "${BASE}" "${SKILLS_SYNC_CMD[@]}" check --tier "${tier}"
  expect "C0 POSITIVE CONTROL: --tier ${tier} is green on a synchronised tree" 0 'are fresh'
done

# Every mutation starts from a fresh copy of the green fixture, so a mutation can
# never be measured against the leftovers of the previous one.
mutate() {
  local name="$1"
  local dir="${TMPROOT}/mut-${name}"
  rm -rf "${dir}"
  cp -a "${BASE}" "${dir}"
  printf '%s' "${dir}"
}

D1="$(mutate content)"
printf 'tampered\n' >"${D1}/.claude/skills/vendor/diene.alpha/alpha/SKILL.md"
for tier in setup pre-commit ci; do
  run_in "${D1}" "${SKILLS_SYNC_CMD[@]}" check --tier "${tier}"
  expect "C1 edited vendored file is RED at --tier ${tier}" 1 'content differs from the package that ships it'
done

D2="$(mutate missing)"
rm -f "${D2}/.claude/skills/vendor/diene.beta/beta/nested/NOTES.md"
run_in "${D2}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'C2 deleted vendored file is RED' 1 'missing from the vendored tree'

D3="$(mutate extra-tracked)"
mkdir -p "${D3}/.claude/skills/vendor/diene.ghost"
printf 'ghost\n' >"${D3}/.claude/skills/vendor/diene.ghost/SKILL.md"
git_q "${D3}" add -A
git_q "${D3}" commit -m ghost --no-verify
run_in "${D3}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'C3 vendored file shipped by no package is RED' 1 'shipped by no resolved package'

D4="$(mutate extra-untracked)"
mkdir -p "${D4}/.claude/skills/vendor/diene.untracked"
printf 'untracked\n' >"${D4}/.claude/skills/vendor/diene.untracked/SKILL.md"
run_in "${D4}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'C4 UNTRACKED regenerated file is RED (a tracked-only diff would miss it)' 1 'shipped by no resolved package'

D5="$(mutate manifest)"
printf '{"schemaVersion":1,"generator":"skills-sync","runtime":"node","packages":[],"entries":[]}\n' \
  >"${D5}/.claude/skills/vendor/manifest.json"
run_in "${D5}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'C5 tampered manifest is RED' 1 'does not describe the vendored tree'

D6="$(mutate upstream-new-skill)"
mkdir -p "${D6}/node_modules/@atomicloud/diene.alpha/skills/brandnew"
printf 'new upstream skill\n' >"${D6}/node_modules/@atomicloud/diene.alpha/skills/brandnew/SKILL.md"
run_in "${D6}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'C6 a package that gained a skill leaves the tree stale' 1 'missing from the vendored tree'

D7="$(mutate uncommitted)"
git_q "${D7}" rm -r --cached .claude/skills/vendor
git_q "${D7}" commit -m untrack --no-verify
run_in "${D7}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'C7 a tree that is right on disk but not committed is RED' 1 'vendored but not tracked by git'

# `check` is read-only BY CONSTRUCTION. Proven on the loudest case: a red run.
D8="$(mutate readonly-proof)"
printf 'tampered again\n' >"${D8}/.claude/skills/vendor/diene.alpha/alpha/SKILL.md"
BEFORE_TREE="$(sha_of_tree "${D8}/.claude/skills/vendor")"
run_in "${D8}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
AFTER_TREE="$(sha_of_tree "${D8}/.claude/skills/vendor")"
RUN_RC=$([ "${BEFORE_TREE}" = "${AFTER_TREE}" ] && echo 0 || echo 1)
RUN_OUT="before=${BEFORE_TREE} after=${AFTER_TREE}"
expect 'C8 a RED check wrote nothing into the vendor tree' 0 ''

# --------------------------------------------------------------------------- #
group 'D. THE TIERS — one condition, three demonstrated behaviours'
# --------------------------------------------------------------------------- #

# Same repository, same defect-free content, one condition: dependencies are not
# restored. If two tiers answered this the same way, they would be one tier
# wearing two names.
COLD="$(mutate cold-checkout)"
rm -rf "${COLD}/node_modules"

run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" check --tier pre-commit
expect 'D1 pre-commit SKIPS on absent deps (exit 0)' 0 '⏭️'
run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" check --tier pre-commit
expect 'D2 pre-commit says it is the WARNING TIER, not the guarantee' 0 'WARNING TIER, not the guarantee'
run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" check --tier pre-commit
expect 'D3 pre-commit names CI as the guarantee' 0 '--tier ci'

run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'D4 ci REFUSES on the same condition (exit 1)' 1 'never skipped and never conditional'

run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" check --tier setup
expect 'D5 setup REFUSES on the same condition with its own code (exit 3)' 3 'Setup restores dependencies'

# A PARTIALLY warm cache is the case the tiers exist for: some declared packages
# resolve and some do not. Publishing the resolvable half would be a partial tree
# that then reads as a content defect.
PARTIAL="$(mutate partial-install)"
rm -rf "${PARTIAL}/node_modules/@atomicloud/diene.beta"
run_in "${PARTIAL}" "${SKILLS_SYNC_CMD[@]}" check --tier pre-commit
expect 'D6 partial install SKIPS at pre-commit' 0 "declared package '@atomicloud/diene.beta 2.0.0' (from package.json) is not installed"
run_in "${PARTIAL}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'D7 partial install REFUSES in CI' 1 "declared package '@atomicloud/diene.beta 2.0.0' (from package.json) is not installed"
run_in "${PARTIAL}" "${SKILLS_SYNC_CMD[@]}" check --tier setup
expect 'D8 partial install REFUSES at setup' 3 "declared package '@atomicloud/diene.beta 2.0.0' (from package.json) is not installed"

# The distinctness assertion itself, stated as a fact rather than left to a
# reader comparing three blocks by eye.
run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" check --tier pre-commit
PRE_RC=${RUN_RC}
run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
CI_RC=${RUN_RC}
run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" check --tier setup
SETUP_RC=${RUN_RC}
RUN_OUT="setup=${SETUP_RC} pre-commit=${PRE_RC} ci=${CI_RC}"
if [ "${PRE_RC}" != "${CI_RC}" ] && [ "${CI_RC}" != "${SETUP_RC}" ] && [ "${PRE_RC}" != "${SETUP_RC}" ]; then
  RUN_RC=0
else
  RUN_RC=1
fi
expect "D9 the three tiers produced THREE DISTINCT exit codes on one condition (${RUN_OUT})" 0 ''

# --------------------------------------------------------------------------- #
group 'E. THE WRITER — D1 MIGRATED (detection kept, refusal retired) and D11'
# --------------------------------------------------------------------------- #

# The owner revoked D1's never-in-hooks clause and mandated the writer run at
# setup, pre-commit AND CI. The REFUSAL is gone; the DETECTION is not. The six
# markers now check that the DECLARED tier is consistent with the environment,
# which preserves the one mis-wiring the old rule caught that is still worth
# catching: a setup script wired into a hook.
W="$(mutate writer)"
for marker in PRE_COMMIT=1 GIT_INDEX_FILE=/tmp/index HUSKY_GIT_PARAMS=x LEFTHOOK=1 SKILLS_SYNC_HOOK_CONTEXT=1; do
  run_in "${W}" "${marker}" "${SKILLS_SYNC_CMD[@]}" sync --tier setup
  expect "E1 --tier setup in a hook context (${marker%%=*}) is a wiring mistake" 2 'a hook is not setup'
done

# The refusal has to name WHICH marker fired; "this looks like a hook" is not
# something an operator can act on.
run_in "${W}" PRE_COMMIT=1 "${SKILLS_SYNC_CMD[@]}" sync --tier setup
expect 'E2 the refusal names the marker that fired' 2 'PRE_COMMIT says so'

# The tier decides what the writer does on drift, so it is never inferred.
run_in "${W}" PRE_COMMIT=1 "${SKILLS_SYNC_CMD[@]}" sync
expect 'E2b a hook context with no --tier refuses and asks for one' 2 'no --tier was declared'

# MUST-DIFFER: the same markers must NOT refuse the tiers the owner mandated.
run_in "${W}" PRE_COMMIT=1 "${SKILLS_SYNC_CMD[@]}" sync --tier pre-commit
expect 'E2c MUST-DIFFER: --tier pre-commit is ALLOWED in the same hook context' 0 'may proceed'

# The read-only half is exactly what a hook is allowed to run, so it must NOT be
# refused under the same markers.
run_in "${W}" PRE_COMMIT=1 "${SKILLS_SYNC_CMD[@]}" check --tier pre-commit
expect 'E3 the READ-ONLY check is allowed in the same hook context' 0 'are fresh'

WCOLD="$(mutate writer-cold)"
rm -rf "${WCOLD}/node_modules"
BEFORE_TREE="$(sha_of_tree "${WCOLD}/.claude/skills/vendor")"
run_in "${WCOLD}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'E4 the writer REFUSES on an unrestored tree (no degraded mode)' 3 'never publishes a partial vendored tree'
AFTER_TREE="$(sha_of_tree "${WCOLD}/.claude/skills/vendor")"
RUN_RC=$([ "${BEFORE_TREE}" = "${AFTER_TREE}" ] && echo 0 || echo 1)
RUN_OUT="before=${BEFORE_TREE} after=${AFTER_TREE}"
expect 'E5 the refused writer left the committed tree byte-identical' 0 ''

WNEW="$(mutate writer-roundtrip)"
mkdir -p "${WNEW}/node_modules/@atomicloud/diene.alpha/skills/added"
printf 'added later\n' >"${WNEW}/node_modules/@atomicloud/diene.alpha/skills/added/SKILL.md"
run_in "${WNEW}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'E6 before the writer runs, the tree is stale' 1 'missing from the vendored tree'
run_in "${WNEW}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'E7 the writer synchronises' 0 'vendored skills synchronised'
git_q "${WNEW}" add -A
git_q "${WNEW}" commit -m resync --no-verify
run_in "${WNEW}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'E8 and the check goes green again' 0 'are fresh'

# D1 must be REACHABLE, not merely correct once reached.
#
# This pair exists because of a real defect, found by a control that could not
# reach its subject. The writer used to resolve the work-tree root BEFORE
# evaluating the hook guard, so in a NON-git directory both the hook arm and the
# no-hook arm returned exit 5, "not inside a git work tree", and D1 was never
# evaluated at all. Two arms agreeing reads exactly like a controlled result,
# which is how a rule gets certified by a test that never executed it.
#
# E9 is the arm that catches that ordering. Its SUBJECT changed when the owner
# revoked the never-in-hooks clause — the marker check is now a tier-consistency
# check — but the REACHABILITY property it protects is unchanged and still worth
# asserting: the marker logic must be evaluated before the git precondition can
# pre-empt it, or no control aimed at it can ever reach it. E10 is not a second
# arm; it is what makes E9 mean something, by proving the same directory still
# answers 5 when no marker is set. Without E10, a build that refused everything
# with exit 2 would pass E9.
NONGIT="${TMPROOT}/nongit-hook"
mkdir -p "${NONGIT}"

# Both arms below are worthless if this directory is secretly inside a work
# tree, so that is asserted rather than assumed.
run_in "${NONGIT}" git rev-parse --show-toplevel
expect 'E9a POSITIVE CONTROL: the fixture directory is NOT a git work tree' 128 'not a git repository'

run_in "${NONGIT}" PRE_COMMIT=1 "${SKILLS_SYNC_CMD[@]}" sync --tier setup
expect 'E9 the marker check is REACHED where the git precondition would pre-empt it' 2 'a hook is not setup'
run_in "${NONGIT}" PRE_COMMIT=1 "${SKILLS_SYNC_CMD[@]}" sync --tier setup
expect 'E9b and it still names the marker that fired' 2 'PRE_COMMIT says so'

run_in "${NONGIT}" env -u SKILLS_SYNC_HOOK_CONTEXT -u PRE_COMMIT -u PRE_COMMIT_HOME \
  -u GIT_INDEX_FILE -u HUSKY_GIT_PARAMS -u LEFTHOOK "${SKILLS_SYNC_CMD[@]}" sync --tier setup
expect 'E10 MUST-DIFFER: same directory, no marker, still the git precondition' 5 'is not inside a git work tree'

# --------------------------------------------------------------------------- #
group 'F. VACUOUS PASSES — a check with no subject is not a passing check'
# --------------------------------------------------------------------------- #

V="$(mutate vacuous)"
printf '{"name":"fixture","dependencies":{"left-pad":"1.0.0"}}\n' >"${V}/package.json"
rm -rf "${V}/node_modules/@atomicloud"
rm -rf "${V}/.claude/skills/vendor"
mkdir -p "${V}/.claude/skills/vendor"
touch "${V}/.claude/skills/vendor/.gitkeep"
git_q "${V}" add -A
git_q "${V}" commit -m vacuous --no-verify
run_in "${V}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'F1 a runtime with no declared package REFUSES rather than pass vacuously' 1 'would pass vacuously'

printf 'schemaVersion: 1\nruntime: bun\nrequireSubjects: false\n' >"${V}/skills-sync.yaml"
run_in "${V}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'F2 declaring the empty case explicitly makes it a pass' 0 ''
run_in "${V}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'F3 and the writer accepts the declared empty case' 0 ''

# --------------------------------------------------------------------------- #
group 'G. THE LIFECYCLE TEST (D10) — adding a language changes ONE place'
# --------------------------------------------------------------------------- #

PKG_FINGERPRINT_BEFORE="$(sha_of_tree "${PKG_DIR}/src")"

# A runtime skills-sync has never heard of, added entirely from the consuming
# repository's own configuration: no preset, no edit to this package, nothing at
# workspace or shared.
ZIG="${TMPROOT}/zig"
mkdir -p "${ZIG}/.claude/skills/vendor" "${ZIG}/zig-cache/diene_zig/skills/zed"
git init -q "${ZIG}"
git -C "${ZIG}" config core.hooksPath "${TMPROOT}/no-hooks"
printf 'zig skill\n' >"${ZIG}/zig-cache/diene_zig/skills/zed/SKILL.md"
cat >"${ZIG}/build.zig.zon" <<'ZON'
{ "dependencies": { "diene_zig": "1.0.0", "other": "2.0.0" } }
ZON
cat >"${ZIG}/zig-packages.json" <<'JSON'
{ "installed": [ { "id": "diene_zig", "path": "zig-cache/diene_zig" } ] }
JSON
cat >"${ZIG}/skills-sync.yaml" <<'YAML'
schemaVersion: 1
resolver:
  name: zig
  declare:
    - file: build.zig.zon
      format: json
      maps: [dependencies]
      match: "^diene_"
  deps:
    requirePath: [zig-cache]
  resolve:
    strategy: json-file
    file: zig-packages.json
    listPath: installed
    nameKey: id
    dirKey: path
    subdir: skills
    vendorName: full
YAML
printf 'zig-cache/\n' >"${ZIG}/.gitignore"
touch "${ZIG}/.claude/skills/vendor/.gitkeep"
git_q "${ZIG}" add -A
git_q "${ZIG}" commit -m zig --no-verify

run_in "${ZIG}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'G1 a brand-new language syncs from an inline resolver alone' 0 'vendored skills synchronised'
git_q "${ZIG}" add -A
git_q "${ZIG}" commit -m zigsync --no-verify
run_in "${ZIG}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'G2 and its CI check is green' 0 'are fresh'

printf 'tampered\n' >"${ZIG}/.claude/skills/vendor/diene_zig/zed/SKILL.md"
run_in "${ZIG}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'G3 and it can go RED — the new language got a real gate, not a stub' 1 'content differs'

printf 'tampered\n' >"${ZIG}/.claude/skills/vendor/diene_zig/zed/SKILL.md"
rm -rf "${ZIG}/zig-cache"
run_in "${ZIG}" "${SKILLS_SYNC_CMD[@]}" check --tier pre-commit
expect 'G4 and its pre-commit tier degrades exactly like every other runtime' 0 'WARNING TIER'

PKG_FINGERPRINT_AFTER="$(sha_of_tree "${PKG_DIR}/src")"
RUN_RC=$([ "${PKG_FINGERPRINT_BEFORE}" = "${PKG_FINGERPRINT_AFTER}" ] && echo 0 || echo 1)
RUN_OUT="skills-sync/src before=${PKG_FINGERPRINT_BEFORE} after=${PKG_FINGERPRINT_AFTER}"
expect 'G5 the places changed to add that language = 1 (skills-sync itself is untouched)' 0 ''

# --------------------------------------------------------------------------- #
group 'H. CONFIGURATION — an unusable config is loud, never an "off"'
# --------------------------------------------------------------------------- #

H="$(mutate config)"
printf 'schemaVersion: 1\nruntime: kotlin\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'H1 an unknown runtime is INVALID, not silently off' 4 'is not a built-in preset'

printf 'schemaVersion: 7\nruntime: bun\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'H2 an unknown schema version is invalid' 4 "'schemaVersion' must be 1"

printf 'schemaVersion: 1\nruntime: bun\nvendorDir: /etc\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'H3 a vendorDir outside the repository is invalid' 4 'must be a path inside the repository'

printf 'schemaVersion: 1\nruntime: bun\n' >"${H}/skills-sync.yaml"
run_in "${H}" SKILLS_SYNC_CONFIG=nowhere.yaml "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'H4 a config pointer aimed at nothing is invalid, not off' 4 'does not exist'

printf 'schemaVersion: 1\nresolver:\n  name: broken\n  declare: []\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'H5 an empty inline resolver is invalid' 4 "'resolver.declare' must be a non-empty array"

printf 'schemaVersion: 1\nresolver:\n  name: broken\n  declare:\n    - file: x\n      format: text\n      pattern: "[unclosed"\n  resolve:\n    strategy: path-template\n    template: x\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'H6 an inline resolver with a broken regex is invalid' 4 'not a valid regular expression'

# --------------------------------------------------------------------------- #
group 'I. OTHER RUNTIME PRESETS — declared, and exercised where the toolchain exists'
# --------------------------------------------------------------------------- #

# The dart/pub preset uses a different resolution strategy (json-file with a
# file:// rootUri) and a glob over every pubspec.yaml in the tree, which is what
# a pub workspace needs. It requires no toolchain, so it always runs.
DART="${TMPROOT}/dart"
mkdir -p "${DART}/.claude/skills/vendor" "${DART}/.dart_tool" \
  "${DART}/packages/app" "${DART}/pubcache/diene_core/skills/core"
git init -q "${DART}"
git -C "${DART}" config core.hooksPath "${TMPROOT}/no-hooks"
printf 'schemaVersion: 1\nruntime: dart\n' >"${DART}/skills-sync.yaml"
# The ROOT pubspec carries only a member list; the real dependency is one level
# down, which a root-only parse would miss entirely.
printf 'name: root\nworkspace:\n  - packages/app\n' >"${DART}/pubspec.yaml"
printf 'name: app\ndependencies:\n  diene_core: ^1.0.0\n  http: ^1.0.0\n' >"${DART}/packages/app/pubspec.yaml"
printf 'core skill\n' >"${DART}/pubcache/diene_core/skills/core/SKILL.md"
cat >"${DART}/.dart_tool/package_config.json" <<JSON
{ "configVersion": 2, "packages": [ { "name": "diene_core", "rootUri": "file://${DART}/pubcache/diene_core" }, { "name": "http", "rootUri": "file://${DART}/pubcache/http" } ] }
JSON
printf 'pubcache/\n' >"${DART}/.gitignore"
touch "${DART}/.claude/skills/vendor/.gitkeep"
git_q "${DART}" add -A
git_q "${DART}" commit -m dart --no-verify

run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'I1 dart: a dependency declared in a WORKSPACE MEMBER pubspec is found' 0 'vendored skills synchronised'
git_q "${DART}" add -A
git_q "${DART}" commit -m dartsync --no-verify
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'I2 dart: CI check green' 0 'are fresh'
printf 'tampered\n' >"${DART}/.claude/skills/vendor/diene_core/core/SKILL.md"
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'I3 dart: and it can go RED' 1 'content differs'
# Two DIFFERENT unrestored shapes, because they are found by different guards
# and only one of them would be caught by the other.
cp "${DART}/.dart_tool/package_config.json" "${TMPROOT}/pc.json"
rm -rf "${DART}/.dart_tool"
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'I4 dart: an absent package_config is a CI refusal' 1 'package_config.json'"'"' does not exist'

# The subtler shape: the resolution input EXISTS but no longer lists the declared
# package. A guard that only checked for the file would pass this and publish an
# emptied vendor tree.
mkdir -p "${DART}/.dart_tool"
printf '{ "configVersion": 2, "packages": [ { "name": "http", "rootUri": "file://%s/pubcache/http" } ] }\n' "${DART}" \
  >"${DART}/.dart_tool/package_config.json"
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'I4b dart: a package_config that no longer lists the package is a CI refusal' 1 "declared package 'diene_core ^1.0.0' (from packages/app/pubspec.yaml) is not installed"
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" check --tier pre-commit
expect 'I4c dart: the same shape SKIPS at pre-commit' 0 'WARNING TIER'
cp "${TMPROOT}/pc.json" "${DART}/.dart_tool/package_config.json"

# nuget resolves out of $HOME/.nuget; the fixture builds that cache directly.
NUGET="${TMPROOT}/nuget"
mkdir -p "${NUGET}/.claude/skills/vendor" "${HOME}/.nuget/packages/atomicloud.diene.core/1.2.3/skills/core"
git init -q "${NUGET}"
git -C "${NUGET}" config core.hooksPath "${TMPROOT}/no-hooks"
printf 'schemaVersion: 1\nruntime: dotnet\n' >"${NUGET}/skills-sync.yaml"
cat >"${NUGET}/Directory.Packages.props" <<'XML'
<Project>
  <ItemGroup>
    <PackageVersion Include="AtomiCloud.Diene.Core" Version="1.2.3" />
    <PackageVersion Include="Serilog" Version="3.0.0" />
  </ItemGroup>
</Project>
XML
printf 'core skill\n' >"${HOME}/.nuget/packages/atomicloud.diene.core/1.2.3/skills/core/SKILL.md"
touch "${NUGET}/.claude/skills/vendor/.gitkeep"
git_q "${NUGET}" add -A
git_q "${NUGET}" commit -m nuget --no-verify

run_in "${NUGET}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'I5 nuget: resolves a case-folded cache path from name+version' 0 'vendored skills synchronised'
git_q "${NUGET}" add -A
git_q "${NUGET}" commit -m nugetsync --no-verify
run_in "${NUGET}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'I6 nuget: CI check green' 0 'are fresh'
printf 'tampered\n' >"${NUGET}/.claude/skills/vendor/AtomiCloud.Diene.Core/core/SKILL.md"
run_in "${NUGET}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'I7 nuget: and it can go RED' 1 'content differs'

# go needs the real toolchain. Where it is absent the arm is NOT run, and the
# skip is REPORTED — a battery that quietly drops an arm overstates its width.
if command -v go >/dev/null 2>&1; then
  GO="${TMPROOT}/go"
  mkdir -p "${GO}/.claude/skills/vendor" "${GO}/vendored/diene.go/skills/gopher"
  git init -q "${GO}"
  git -C "${GO}" config core.hooksPath "${TMPROOT}/no-hooks"
  printf 'schemaVersion: 1\nruntime: go\n' >"${GO}/skills-sync.yaml"
  printf 'go skill\n' >"${GO}/vendored/diene.go/skills/gopher/SKILL.md"
  printf 'module example.com/fixture\n\ngo 1.21\n\nrequire example.com/diene.go v0.0.0\n\nreplace example.com/diene.go => ./vendored/diene.go\n' >"${GO}/go.mod"
  printf 'module example.com/diene.go\n\ngo 1.21\n' >"${GO}/vendored/diene.go/go.mod"
  printf 'vendored/\n' >"${GO}/.gitignore"
  touch "${GO}/.claude/skills/vendor/.gitkeep"
  git_q "${GO}" add -A
  git_q "${GO}" commit -m go --no-verify

  run_in "${GO}" GOFLAGS=-mod=mod GOPATH="${TMPROOT}/gopath" GOCACHE="${TMPROOT}/gocache" "${SKILLS_SYNC_CMD[@]}" sync
  expect 'I8 go: resolves module directories from go list -m -json all' 0 'vendored skills synchronised'
  git_q "${GO}" add -A
  git_q "${GO}" commit -m gosync --no-verify
  run_in "${GO}" GOFLAGS=-mod=mod GOPATH="${TMPROOT}/gopath" GOCACHE="${TMPROOT}/gocache" "${SKILLS_SYNC_CMD[@]}" check --tier ci
  expect 'I9 go: CI check green' 0 'are fresh'
  printf 'tampered\n' >"${GO}/.claude/skills/vendor/diene.go/gopher/SKILL.md"
  run_in "${GO}" GOFLAGS=-mod=mod GOPATH="${TMPROOT}/gopath" GOCACHE="${TMPROOT}/gocache" "${SKILLS_SYNC_CMD[@]}" check --tier ci
  expect 'I10 go: and it can go RED' 1 'content differs'
else
  printf '  ⏭️ I8-I10 go arm NOT RUN: no go toolchain on PATH. The go preset is UNEXERCISED by this run.\n'
fi

# --------------------------------------------------------------------------- #
group 'J. OFF IS NOT A HIDING PLACE — the other operand'
# --------------------------------------------------------------------------- #

# A contradiction guard written in one direction leaves its mirror open.
#
# The existing guard refuses when nothing names a runtime WHILE THE VENDOR TREE
# HOLDS FILES — the subject witnessed by the OUTPUT. It said nothing about the
# subject witnessed by the INPUT: a repository whose manifests DECLARE diene
# packages that ship real skills, whose vendor tree happens to be empty, and
# which carries no configuration. That node exited 0 in silence, where the
# mechanism this tool replaced (dlint skills-fresh) exited 3 — a regression
# against the very thing being retired.
#
# The rulings authorise absent-equals-off BECAUSE a node naming no runtime has no
# subject (ledger :98, and the stamp at :149-153). A node declaring diene
# packages HAS one, so the authorisation does not reach it. Ratification does not
# confer scope.

# A repository that declares a package shipping real skills, with an EMPTY vendor
# tree and NO configuration at all.
make_declaring_repo() {
  local dir="$1"
  rm -rf "${dir}"
  mkdir -p "${dir}/.claude/skills/vendor" "${dir}/node_modules/@atomicloud/diene.alpha/skills/alpha"
  git init -q "${dir}"
  git -C "${dir}" config core.hooksPath "${TMPROOT}/no-hooks"
  printf '{"name":"f","dependencies":{"@atomicloud/diene.alpha":"1.0.0"}}\n' >"${dir}/package.json"
  printf 'real skill\n' >"${dir}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
  printf 'node_modules/\n' >"${dir}/.gitignore"
  touch "${dir}/.claude/skills/vendor/.gitkeep"
  git_q "${dir}" add -A
  git_q "${dir}" commit -m declaring --no-verify
}

DECL="${TMPROOT}/declaring"
make_declaring_repo "${DECL}"
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'J1 declares diene packages + empty tree + NO config REFUSES' 1 '@atomicloud/diene.alpha'
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'J1b and it says why rather than only that' 1 'declares diene package'

# A config that EXISTS but names no runtime is still an absence of declaration.
printf 'schemaVersion: 1\n' >"${DECL}/skills-sync.yaml"
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'J2 a config naming no runtime is an absence, not a declaration' 1 'declares diene package'

# `runtime: none` IS a declaration, and is the accepted opt-out. It must still be
# audible: the packages being deliberately ignored are named.
printf 'schemaVersion: 1\nruntime: none\n' >"${DECL}/skills-sync.yaml"
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'J3 MUST-DIFFER: runtime: none is an explicit opt-out and PASSES' 0 ''
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'J3b and the opt-out still names what it is ignoring' 0 '@atomicloud/diene.alpha'

# The workspace/shared shape: no manifest of any kind, empty tree, no config.
# Measured at the landed refs b0bbadbd and cd25b877 — no package.json, no
# Directory.Packages.props, no go.mod, no pubspec.yaml. The guard must be INERT
# here or it breaks the two nodes that already landed.
rm -f "${DECL}/skills-sync.yaml"
WSSHAPE="${TMPROOT}/workspace-shape"
mkdir -p "${WSSHAPE}/.claude/skills/vendor"
git init -q "${WSSHAPE}"
git -C "${WSSHAPE}" config core.hooksPath "${TMPROOT}/no-hooks"
touch "${WSSHAPE}/.claude/skills/vendor/.gitkeep"
printf '[]\n' >"${WSSHAPE}/.claude/skills/vendor/manifest.json"
git_q "${WSSHAPE}" add -A
git_q "${WSSHAPE}" commit -m wsshape --no-verify
for tier in setup pre-commit ci; do
  run_in "${WSSHAPE}" "${SKILLS_SYNC_CMD[@]}" check --tier "${tier}"
  expect "J4 the workspace/shared shape stays inert at --tier ${tier}" 0 'names no runtime'
done

# An off-path probe that prints nothing is indistinguishable from a probe that
# never ran. It has to account for itself even when it finds nothing.
run_in "${WSSHAPE}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'J5 the probe reports that it ran even when it finds nothing' 0 'mechanism(s) probed'

# PER-MECHANISM LIVENESS ON THE PROBE ITSELF.
#
# The probe sweeps four mechanisms and reports one number. "0 declared" is
# exactly the shape a DEAD mechanism produces, so each one is shown able to fire
# on its own manifest. A mechanism that can never match would silently narrow the
# guard to the mechanisms that still work, and the summary line would look
# identical.
probe_fires() {
  local id="$1" file="$2" body="$3"
  local dir="${TMPROOT}/probe-${id}"
  rm -rf "${dir}"
  mkdir -p "${dir}/.claude/skills/vendor" "$(dirname "${dir}/${file}")"
  git init -q "${dir}"
  git -C "${dir}" config core.hooksPath "${TMPROOT}/no-hooks"
  printf '%s' "${body}" >"${dir}/${file}"
  touch "${dir}/.claude/skills/vendor/.gitkeep"
  git_q "${dir}" add -A
  git_q "${dir}" commit -m probe --no-verify
  run_in "${dir}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
}

probe_fires node package.json '{"name":"n","dependencies":{"@atomicloud/diene.alpha":"1.0.0"}}'
expect 'J6a probe mechanism node FIRES on package.json' 1 '@atomicloud/diene.alpha'

probe_fires nuget Directory.Packages.props \
  '<Project><ItemGroup><PackageVersion Include="AtomiCloud.Diene.Core" Version="1.2.3" /></ItemGroup></Project>'
expect 'J6b probe mechanism nuget FIRES on Directory.Packages.props' 1 'AtomiCloud.Diene.Core'

probe_fires go go.mod \
  'module example.com/app

go 1.21

require example.com/diene.go v1.0.0
'
expect 'J6c probe mechanism go FIRES on a go.mod require' 1 'diene.go'

probe_fires pub pubspec.yaml 'name: app
dependencies:
  diene_core: ^1.0.0
'
expect 'J6d probe mechanism pub FIRES on a pubspec dependency' 1 'diene_core'

# MUST-DIFFER for the go mechanism, and it is not academic: go-base's own module
# line is `module github.com/AtomiCloud/diene.go-base`. A pattern that matched a
# module NAME rather than a dependency would refuse on every go node in the
# corpus. A main module whose name contains diene is not a dependency obligation.
probe_fires go-selfname go.mod 'module github.com/AtomiCloud/diene.go-base

go 1.26.0

require github.com/redis/go-redis/v9 v9.21.0
'
expect 'J6e MUST-DIFFER: a main module NAMED diene.* is not a declaration' 0 '0 diene package(s) declared'

# --------------------------------------------------------------------------- #
group 'K. THE WRITER REFUSES CLEANLY — a tool whose refusals are its product must not leak a stack'
# --------------------------------------------------------------------------- #

# The upstream package must MOVE first, or there is nothing for the writer to
# write and an unwritable tree never gets touched. That is not a flaw in the
# fixture — it is the content-idempotent writer behaving correctly, and K0 asserts
# it, because "the writer succeeded on a read-only tree" is only acceptable when
# the writer genuinely had nothing to do.
UNW="$(mutate unwritable)"
chmod -R a-w "${UNW}/.claude"
run_in "${UNW}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'K0 an idempotent writer needs no write permission when nothing changed' 0 'writer mutated 0 file(s)'
chmod -R u+w "${UNW}/.claude" 2>/dev/null

UNW2="$(mutate unwritable-drift)"
printf 'moved upstream\n' >"${UNW2}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
chmod -R a-w "${UNW2}/.claude"
run_in "${UNW2}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'K1 a write it CANNOT perform is a stated refusal' 5 'could not write the vendored tree at'
run_in "${UNW2}" "${SKILLS_SYNC_CMD[@]}" sync
expect_absent 'K2 and it does NOT leak an unhandled exception' 5 'failed unexpectedly'
run_in "${UNW2}" "${SKILLS_SYNC_CMD[@]}" sync
expect_absent 'K3 and it does NOT leak a stack trace' 5 'at <anonymous>'
# The read-only half is unaffected by an unwritable tree.
run_in "${UNW2}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'K4 MUST-DIFFER: check still reports the drift, unaffected by permissions' 1 'content differs'
chmod -R u+w "${UNW2}/.claude" 2>/dev/null

# --------------------------------------------------------------------------- #
group 'L. SELECTION CONTROLS — drawn from the EXCLUDED and NON-MATCHING sets'
# --------------------------------------------------------------------------- #

# Every other group in this file controls the ASSERTION: does the check fire when
# it should? These control the SELECTION — which files the sweep could ever see,
# and which names the pattern could ever match. A control that only exercises the
# matching set can never detect that the set is WRONG, and that fault reports as
# a smaller number or a clean zero, both indistinguishable from a true negative.
#
# Two selections live in this tool and neither is reachable from group J:
#
#   GLOB_PRUNE          fails toward SILENTLY OFF — a declaration under a pruned
#                       path leaves the repository looking like it declares
#                       nothing.
#   the declare regexes  yield "0 declared" for a BROKEN pattern and for a
#                       genuinely absent one identically. That is the shape that
#                       nearly produced a false finding against this tool: a
#                       fixture named `@atomicloud/diene-skills-demo` (HYPHEN)
#                       could not match `diene\.`, and its null read as "the
#                       guard is inert".
#
# Each row below is a PAIR: one case from the included/matching set, one from the
# excluded/non-matching set.

# Builds a repo with NO config and an EMPTY vendor tree, then drops one manifest
# at a caller-chosen path. Exit 1 means the off-probe SAW the declaration.
selection_probe() {
  local id="$1" path="$2" body="$3"
  local dir="${TMPROOT}/sel-${id}"
  rm -rf "${dir}"
  mkdir -p "${dir}/.claude/skills/vendor" "$(dirname "${dir}/${path}")"
  git init -q "${dir}"
  git -C "${dir}" config core.hooksPath "${TMPROOT}/no-hooks"
  printf '%s' "${body}" >"${dir}/${path}"
  touch "${dir}/.claude/skills/vendor/.gitkeep"
  git_q "${dir}" add -A
  git_q "${dir}" commit -m sel --no-verify
  run_in "${dir}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
}

PUBSPEC_DIENE='name: app
dependencies:
  diene_core: ^1.0.0
'

selection_probe inc-root pubspec.yaml "${PUBSPEC_DIENE}"
expect 'L1 INCLUDED  pubspec.yaml at the repo root is seen' 1 'diene_core'
selection_probe inc-nested packages/app/pubspec.yaml "${PUBSPEC_DIENE}"
expect 'L2 INCLUDED  packages/app/pubspec.yaml is seen' 1 'diene_core'

for pruned in build/gen .dart_tool/x .direnv/x node_modules/x; do
  selection_probe "exc-$(printf '%s' "${pruned}" | tr '/.' '--')" "${pruned}/pubspec.yaml" "${PUBSPEC_DIENE}"
  expect "L3 EXCLUDED  ${pruned}/pubspec.yaml is NOT counted" 0 '0 diene package(s) declared'
done

# Excluded BY THE PRUNE, not by depth or by nesting. `buildx/` differs from
# `build/` only in the prune list, so if this were also unseen the rows above
# would be measuring something else entirely — a two-level path, say — and would
# still have read as a clean pass.
selection_probe boundary buildx/gen/pubspec.yaml "${PUBSPEC_DIENE}"
expect 'L3b MUST-DIFFER: buildx/gen/pubspec.yaml IS counted — the prune name is the cause' 1 'diene_core'

# Non-matching names. The hyphen row is the exact shape that nearly produced a
# false finding: it is UNSEEN because it is not a diene package under the
# `diene.` convention, NOT because the pattern is dead — L4 is what proves that.
selection_probe pat-dot package.json '{"name":"n","dependencies":{"@atomicloud/diene.alpha":"1.0.0"}}'
expect 'L4 MATCHING     @atomicloud/diene.alpha (dot) is seen' 1 '@atomicloud/diene.alpha'
selection_probe pat-hyphen package.json '{"name":"n","dependencies":{"@atomicloud/diene-skills-demo":"1.0.0"}}'
expect 'L5 NON-MATCHING @atomicloud/diene-skills-demo (HYPHEN) is not' 0 '0 diene package(s) declared'
selection_probe pat-other package.json '{"name":"n","dependencies":{"@atomicloud/other.alpha":"1.0.0"}}'
expect 'L6 NON-MATCHING @atomicloud/other.alpha is not' 0 '0 diene package(s) declared'
selection_probe nuget-n Directory.Packages.props \
  '<Project><ItemGroup><PackageVersion Include="AtomiCloud.Other.Core" Version="1.0.0" /></ItemGroup></Project>'
expect 'L7 NON-MATCHING AtomiCloud.Other.Core is not' 0 '0 diene package(s) declared'
selection_probe pub-n pubspec.yaml 'name: app
dependencies:
  notdiene_core: ^1.0.0
'
expect 'L8 NON-MATCHING notdiene_core is not' 0 '0 diene package(s) declared'

# THE SELF-REFERENCE HAZARD — the tool must not read its OWN OUTPUT as input.
#
# This is the one prune that cannot be reached the way the others are: any file
# placed under the vendor tree of a repo that names NO runtime trips the
# contradiction guard first, so the prune is never observable. Reaching it needs
# a repo that legitimately HAS a populated vendor tree, i.e. one that names a
# runtime — and the witness is the DECLARE FILE COUNT, not the exit code, because
# the content check refuses the planted file for its own separate and correct
# reason.
SELF="${TMPROOT}/selfref"
mkdir -p "${SELF}/.claude/skills/vendor" "${SELF}/.dart_tool" "${SELF}/pubcache/diene_core/skills/core"
git init -q "${SELF}"
git -C "${SELF}" config core.hooksPath "${TMPROOT}/no-hooks"
printf 'schemaVersion: 1\nruntime: dart\n' >"${SELF}/skills-sync.yaml"
printf 'name: root\ndependencies:\n  diene_core: ^1.0.0\n' >"${SELF}/pubspec.yaml"
printf 'core skill\n' >"${SELF}/pubcache/diene_core/skills/core/SKILL.md"
cat >"${SELF}/.dart_tool/package_config.json" <<JSON
{ "configVersion": 2, "packages": [ { "name": "diene_core", "rootUri": "file://${SELF}/pubcache/diene_core" } ] }
JSON
printf 'pubcache/\n' >"${SELF}/.gitignore"
touch "${SELF}/.claude/skills/vendor/.gitkeep"
git_q "${SELF}" add -A
git_q "${SELF}" commit -m selfref --no-verify
sync_and_commit "${SELF}" || printf '  ‼️ the L9 pair is unusable\n'

run_in "${SELF}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'L9a POSITIVE CONTROL: one pubspec swept, one package declared' 0 "declare: '**/pubspec.yaml (1 file(s))'"

printf 'name: shipped\ndependencies:\n  diene_ghost: ^9.9.9\n' \
  >"${SELF}/.claude/skills/vendor/diene_core/core/pubspec.yaml"
git_q "${SELF}" add -A
git_q "${SELF}" commit -m ghost --no-verify
run_in "${SELF}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect 'L9b a manifest INSIDE the vendor tree is not swept (still 1 file)' 1 "declare: '**/pubspec.yaml (1 file(s))'"
run_in "${SELF}" "${SKILLS_SYNC_CMD[@]}" check --tier ci
expect_absent 'L9c and its package is NOT declared — no self-reference' 1 'diene_ghost'

# --------------------------------------------------------------------------- #
group 'M. THE PRE-COMMIT WRITER — treefmt shape, plus the INDEX condition'
# --------------------------------------------------------------------------- #

# The owner mandated the writer run at pre-commit. norah ruled the shape: any
# tree mutation FAILS the commit loudly, regenerated files left for the user to
# stage, and the tool NEVER stages anything.
#
# That shape is necessary but not sufficient, and M3 is why: git commits the
# INDEX. A user who regenerates by hand and does not stage leaves NOTHING to
# mutate, so a mutation-only rule passes and the commit still ships the stale
# tree. So the rule is: refuse if MUTATED **or** INDEX != EXPECTED.

# A repo wired the way the owner mandated: a real pre-commit hook running the
# WRITER. `exec` so the hook's exit status IS the writer's — a trailing `echo`
# would return its own status and the commit would survive a refusal, which is
# how this harness lied to me once already.
make_precommit_repo() {
  local dir="$1"
  rm -rf "${dir}"
  mkdir -p "${dir}/.claude/skills/vendor" "${dir}/node_modules/@atomicloud/diene.alpha/skills/alpha"
  git init -q "${dir}"
  git -C "${dir}" config core.hooksPath "${dir}/.git/hooks"
  printf 'schemaVersion: 1\nruntime: bun\n' >"${dir}/skills-sync.yaml"
  printf '{"name":"f","dependencies":{"@atomicloud/diene.alpha":"1.0.0"}}\n' >"${dir}/package.json"
  printf 'v1\n' >"${dir}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
  printf 'node_modules/\n' >"${dir}/.gitignore"
  printf 'orig\n' >"${dir}/other.txt"
  touch "${dir}/.claude/skills/vendor/.gitkeep"
  git_q "${dir}" add -A
  git_q "${dir}" commit -m init --no-verify
  (cd "${dir}" && "${SKILLS_SYNC_CMD[@]}" sync) >/dev/null 2>&1
  git_q "${dir}" add -A
  git_q "${dir}" commit -m sync --no-verify
  # shellcheck disable=SC2016  # the hook body must expand at COMMIT time, not now
  printf '#!/bin/sh\ncd "$(git rev-parse --show-toplevel)"\nexec %s sync --tier pre-commit\n' \
    "${SKILLS_SYNC_CMD[*]}" >"${dir}/.git/hooks/pre-commit"
  chmod +x "${dir}/.git/hooks/pre-commit"
}

commit_in() {
  RUN_OUT="$(cd "$1" && git -c user.email=tests@example.invalid -c user.name=tests \
    -c commit.gpgsign=false commit -m "$2" 2>&1)"
  RUN_RC=$?
}

VENDORED=.claude/skills/vendor/diene.alpha/alpha/SKILL.md

# POSITIVE CONTROL: the harness can see a hook abort a commit at all. Without it
# every "LOUD" row below could be a harness that swallows exit codes.
PC="${TMPROOT}/precommit-control"
make_precommit_repo "${PC}"
printf '#!/bin/sh\nexit 7\n' >"${PC}/.git/hooks/pre-commit"
chmod +x "${PC}/.git/hooks/pre-commit"
printf 'z\n' >"${PC}/z.txt"
git_q "${PC}" add z.txt
commit_in "${PC}" 'control'
expect 'M0 POSITIVE CONTROL: a hook exiting non-zero DOES abort the commit' 1 ''

# ---- CONDITION 2: content-idempotence, demonstrated not assumed -------------
IDEM="${TMPROOT}/idempotent"
make_precommit_repo "${IDEM}"
run_in "${IDEM}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'M1 a no-change sync mutates ZERO files (content-idempotent)' 0 'writer mutated 0 file(s)'

# The justification, in the battery rather than only in a report: a
# content-identical sync CHURNS inode and mtime while git stays silent. So an
# mtime-keyed or directory-replacement-keyed detector would report a mutation
# here and reject A2; only a CONTENT-keyed detector reports zero.
BEFORE_MTIME="$(stat -c %Y "${IDEM}/${VENDORED}")"
BEFORE_INODE="$(stat -c %i "${IDEM}/${VENDORED}")"
sleep 1.1
run_in "${IDEM}" "${SKILLS_SYNC_CMD[@]}" sync
AFTER_MTIME="$(stat -c %Y "${IDEM}/${VENDORED}")"
AFTER_INODE="$(stat -c %i "${IDEM}/${VENDORED}")"
RUN_OUT="mtime ${BEFORE_MTIME}->${AFTER_MTIME} inode ${BEFORE_INODE}->${AFTER_INODE}"
RUN_RC=$([ "${BEFORE_MTIME}" = "${AFTER_MTIME}" ] && [ "${BEFORE_INODE}" = "${AFTER_INODE}" ] && echo 0 || echo 1)
expect "M1b an idempotent write does not even touch mtime/inode (${RUN_OUT})" 0 ''

# ---- CONDITION 3: cpSync silently not overwriting is a false green ----------
# MUST-DIFFER on the CONTENT, not on the call returning: a writer that silently
# does not write is exactly the failure this asserts against.
OVER="${TMPROOT}/overwrite"
make_precommit_repo "${OVER}"
printf 'HAND-EDITED\n' >"${OVER}/${VENDORED}"
run_in "${OVER}" "${SKILLS_SYNC_CMD[@]}" sync
RUN_OUT="content now: $(cat "${OVER}/${VENDORED}")"
RUN_RC=$([ "$(cat "${OVER}/${VENDORED}")" = "v1" ] && echo 0 || echo 1)
expect "M2 the writer OVERWROTE an existing file — content changed, not just a return code (${RUN_OUT})" 0 ''

# ---- A2: the row that must not be rejected ---------------------------------
A2="${TMPROOT}/a2"
make_precommit_repo "${A2}"
printf 'v2\n' >"${A2}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
(cd "${A2}" && "${SKILLS_SYNC_CMD[@]}" sync) >/dev/null 2>&1
git_q "${A2}" add -A
commit_in "${A2}" 'A2 staged regeneration'
expect 'M3 A2: an already-staged regeneration PASSES — the commit that fixes the tree' 0 ''
RUN_OUT="$(git -C "${A2}" show "HEAD:${VENDORED}" 2>&1)"
RUN_RC=$([ "${RUN_OUT}" = "v2" ] && echo 0 || echo 1)
expect "M3b and the commit records the CORRECT tree (${RUN_OUT})" 0 ''

# ---- CONDITION 1: THE GAP ROW, permanent -----------------------------------
# mutated = 0, index != expected, exit 1. Without this arm the index condition
# could be inert and nothing would say so.
GAP="${TMPROOT}/gap"
make_precommit_repo "${GAP}"
printf 'v2\n' >"${GAP}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
(cd "${GAP}" && "${SKILLS_SYNC_CMD[@]}" sync) >/dev/null 2>&1 # worktree correct, NOT staged
printf 'changed\n' >"${GAP}/other.txt"
git_q "${GAP}" add other.txt
commit_in "${GAP}" 'gap'
expect 'M4 THE GAP ROW: correct on disk but unstaged is REFUSED' 1 'not staged as the packages ship them'
expect 'M4b and it is refused with mutated=0 — a mutation-only rule would PASS this' 1 'writer mutated 0 file(s)'
RUN_OUT="$(git -C "${GAP}" log --oneline -1)"
RUN_RC=$(git -C "${GAP}" log --oneline -1 | grep -q 'gap' && echo 1 || echo 0)
expect 'M4c and the commit was NOT created' 0 ''

# ---- drift: loud, files left, nothing staged -------------------------------
DRIFT="${TMPROOT}/drift"
make_precommit_repo "${DRIFT}"
printf 'v2\n' >"${DRIFT}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
printf 'changed\n' >"${DRIFT}/other.txt"
git_q "${DRIFT}" add other.txt
commit_in "${DRIFT}" 'drift'
expect 'M5 drift REFUSES the commit loudly' 1 'regenerated'
expect 'M5b and states that nothing was added to the commit' 1 'NOTHING was added to your commit'
RUN_OUT="$(cat "${DRIFT}/${VENDORED}")"
RUN_RC=$([ "${RUN_OUT}" = "v2" ] && echo 0 || echo 1)
expect "M5c and the regenerated file is LEFT IN THE WORKING TREE to stage (${RUN_OUT})" 0 ''
# The tool never stages: the vendor tree must still be unstaged after the refusal.
RUN_OUT="$(git -C "${DRIFT}" diff --cached --name-only -- .claude/skills/vendor | tr '\n' ' ')"
RUN_RC=$([ -z "${RUN_OUT}" ] && echo 0 || echo 1)
expect "M5d and the tool staged NOTHING itself (staged vendor paths: [${RUN_OUT}])" 0 ''

# ---- deps absent per tier --------------------------------------------------
COLDW="$(mutate writer-precommit-cold)"
rm -rf "${COLDW}/node_modules"
run_in "${COLDW}" "${SKILLS_SYNC_CMD[@]}" sync --tier pre-commit
expect 'M6 deps absent at pre-commit SKIPS (0) — a commit must not need a restored tree' 0 'this tier skips'
run_in "${COLDW}" "${SKILLS_SYNC_CMD[@]}" sync --tier ci
expect 'M6b deps absent in CI REFUSES (1)' 1 'not restored'
run_in "${COLDW}" "${SKILLS_SYNC_CMD[@]}" sync --tier setup
expect 'M6c deps absent at setup REFUSES (3)' 3 'not restored'

# ---- CI writes then refuses, never repair-and-pass --------------------------
CIR="${TMPROOT}/ci-drift"
make_precommit_repo "${CIR}"
printf 'v2\n' >"${CIR}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
run_in "${CIR}" "${SKILLS_SYNC_CMD[@]}" sync --tier ci
expect 'M7 CI on drift REFUSES rather than repair-and-pass' 1 'is not what the packages ship'
run_in "${CIR}" "${SKILLS_SYNC_CMD[@]}" sync --tier ci
expect 'M7b MUST-DIFFER: a second CI run still refuses (no self-healing loop)' 1 'not staged as the packages ship them'

# --------------------------------------------------------------------------- #
printf '\n=== artifact under test: %s\n' "${SKILLS_SYNC}"
printf '=== %s passed, %s failed\n' "${PASS}" "${FAIL}"
if [ "${FAIL}" -ne 0 ]; then
  printf '=== failed:\n'
  printf '    %s\n' "${FAILED_NAMES[@]}"
  exit 1
fi
exit 0
