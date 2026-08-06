#!/usr/bin/env bash

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PKG_DIR="$(dirname "${HERE}")"
SKILLS_SYNC="${SKILLS_SYNC:-bun ${PKG_DIR}/index.ts}"
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

run_in() {
  local dir="$1"
  shift
  RUN_OUT="$(cd "${dir}" && env "$@" 2>&1)"
  RUN_RC=$?
}

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
  local dir="$1"
  if [ ! -d "${dir}" ]; then
    printf 'ABSENT'
    return
  fi
  (cd "${dir}" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 -r sha256sum) | sha256sum | cut -d' ' -f1
}

printf '=== skills-sync acceptance battery\n'
printf '=== artifact under test: %s\n' "${SKILLS_SYNC}"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" --version
printf '=== reported version: %s (exit %s)\n' "${RUN_OUT}" "${RUN_RC}"
printf '=== fixtures under: %s\n' "${TMPROOT}"

group 'A. CLI surface — check and tiers are retired; sync has one boolean mode'

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}"
expect 'A1 bare invocation refuses and lists the commands' 2 'skills-sync needs a command'

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" --help
expect 'A2 root help lists sync' 0 '  sync'
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" --help
expect 'A3 root help lists runtimes' 0 '  runtimes'
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" --help
expect_absent 'A4 root help does not list the retired check command' 0 '  check'

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" nosuchcommand
expect 'A5 unknown command refuses' 2 "unknown command 'nosuchcommand'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" nosuchcommand --help
expect 'A6 unknown command + --help still refuses (no root-help fallthrough)' 2 "unknown command 'nosuchcommand'"

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" check --help
expect 'A7 retired check --help is an unknown command' 2 "unknown command 'check'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" check
expect 'A8 retired bare check is an unknown command' 2 "unknown command 'check'"

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" sync --help
expect 'A9 sync --help prints the frozen synopsis' 0 'Usage: skills-sync sync [--frozen]'
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" runtimes --help
expect 'A10 runtimes --help prints runtimes own usage line' 0 'Usage: skills-sync runtimes'

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" sync --tier ci
expect 'A11 retired --tier refuses on sync' 2 "has no option '--tier'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" sync --bogus
expect 'A12 unknown option on sync refuses' 2 "has no option '--bogus'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" runtimes --frozen
expect 'A13 --frozen belongs only to sync' 2 "has no option '--frozen'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" sync --frozen --bogus
expect 'A14 --frozen does not hide a following option' 2 "has no option '--bogus'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" sync --frozen=value
expect 'A15 --frozen=value is not accepted as --frozen' 2 "has no option '--frozen=value'"
run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" sync --frozen positional
expect 'A16 --frozen does not consume a following positional argument' 2 'takes no positional arguments'

run_in "${TMPROOT}" "${SKILLS_SYNC_CMD[@]}" runtimes
expect 'A17 runtimes lists the dart preset' 0 'dart'

group 'B. OFF — the workspace and shared shape: generic wiring, no runtime named'

OFF_REPO="${TMPROOT}/off"
mkdir -p "${OFF_REPO}/.claude/skills/vendor"
git init -q "${OFF_REPO}"
git -C "${OFF_REPO}" config core.hooksPath "${TMPROOT}/no-hooks"
touch "${OFF_REPO}/.claude/skills/vendor/.gitkeep"
git_q "${OFF_REPO}" add -A
git_q "${OFF_REPO}" commit -m off --no-verify

run_in "${OFF_REPO}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'B1 no config: the writer is inert' 0 'names no runtime'
run_in "${OFF_REPO}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'B2 no config: frozen mode is inert too' 0 'names no runtime'

printf 'schemaVersion: 1\nruntime: none\n' >"${OFF_REPO}/skills-sync.yaml"
run_in "${OFF_REPO}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'B3 runtime: none is inert' 0 'names no runtime'

rm -f "${OFF_REPO}/skills-sync.yaml"
mkdir -p "${OFF_REPO}/.claude/skills/vendor/leftover"
printf 'orphan\n' >"${OFF_REPO}/.claude/skills/vendor/leftover/SKILL.md"
run_in "${OFF_REPO}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'B4 off + a non-empty vendor tree REFUSES (deleting the config is not an off switch)' 1 'holds 1 vendored file'
run_in "${OFF_REPO}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'B5 the same off contradiction refuses in writer mode' 1 'holds 1 vendored file'

group 'C. Positive control FIRST, then the mutation battery (node fixture, deps restored)'

BASE="${TMPROOT}/node-base"
make_node_repo "${BASE}"
sync_and_commit "${BASE}" || printf '  ‼️ the whole C group is unusable\n'

run_in "${BASE}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'C0a POSITIVE CONTROL: writer is green on a synchronised tree' 0 'writer mutated 0 file(s)'
run_in "${BASE}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'C0b POSITIVE CONTROL: frozen is green on a synchronised and staged tree' 0 'git index match'

mutate() {
  local name="$1"
  local dir="${TMPROOT}/mut-${name}"
  rm -rf "${dir}"
  cp -a "${BASE}" "${dir}"
  printf '%s' "${dir}"
}

D1F="$(mutate content-frozen)"
printf 'tampered\n' >"${D1F}/.claude/skills/vendor/diene.alpha/alpha/SKILL.md"
run_in "${D1F}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'C1a planted drift is RED in frozen mode' 1 'regenerated 1 file(s)'
RUN_OUT="$(cat "${D1F}/.claude/skills/vendor/diene.alpha/alpha/SKILL.md")"
RUN_RC=$([ "${RUN_OUT}" = 'alpha skill' ] && echo 0 || echo 1)
expect 'C1b frozen mode leaves regenerated package content in the worktree' 0 ''

D1W="$(mutate content-writer)"
printf 'tampered\n' >"${D1W}/.claude/skills/vendor/diene.alpha/alpha/SKILL.md"
run_in "${D1W}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'C1c MUST-DIFFER: bare sync repairs the same drift and succeeds' 0 'vendored skills synchronised'
RUN_OUT="$(cat "${D1W}/.claude/skills/vendor/diene.alpha/alpha/SKILL.md")"
RUN_RC=$([ "${RUN_OUT}" = 'alpha skill' ] && echo 0 || echo 1)
expect 'C1d bare sync wrote the package content' 0 ''

D2="$(mutate missing)"
rm -f "${D2}/.claude/skills/vendor/diene.beta/beta/nested/NOTES.md"
run_in "${D2}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'C2 deleted vendored file is regenerated and RED' 1 'regenerated'

D3="$(mutate extra-tracked)"
mkdir -p "${D3}/.claude/skills/vendor/diene.ghost"
printf 'ghost\n' >"${D3}/.claude/skills/vendor/diene.ghost/SKILL.md"
git_q "${D3}" add -A
git_q "${D3}" commit -m ghost --no-verify
run_in "${D3}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'C3 tracked extra vendored file is removed and RED' 1 'diene.ghost/SKILL.md'

D4="$(mutate extra-untracked)"
mkdir -p "${D4}/.claude/skills/vendor/diene.untracked"
printf 'untracked\n' >"${D4}/.claude/skills/vendor/diene.untracked/SKILL.md"
run_in "${D4}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'C4 untracked extra vendored file is removed and RED' 1 'diene.untracked/SKILL.md'

D5="$(mutate manifest)"
printf '{"schemaVersion":1,"generator":"skills-sync","runtime":"node","packages":[],"entries":[]}\n' \
  >"${D5}/.claude/skills/vendor/manifest.json"
run_in "${D5}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'C5 tampered manifest is regenerated and RED' 1 'manifest.json'

D6="$(mutate upstream-new-skill)"
mkdir -p "${D6}/node_modules/@atomicloud/diene.alpha/skills/brandnew"
printf 'new upstream skill\n' >"${D6}/node_modules/@atomicloud/diene.alpha/skills/brandnew/SKILL.md"
run_in "${D6}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'C6 a package that gained a skill is regenerated and RED' 1 'brandnew/SKILL.md'

D7="$(mutate uncommitted)"
git_q "${D7}" rm -r --cached .claude/skills/vendor
git_q "${D7}" commit -m untrack --no-verify
run_in "${D7}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'C7 a correct worktree absent from the index is RED' 1 'vendor index disagree'

group 'D. THE TWO MODES — unrestored dependencies must differ'

COLD="$(mutate cold-checkout)"
rm -rf "${COLD}/node_modules"
BEFORE_TREE="$(sha_of_tree "${COLD}/.claude/skills/vendor")"

run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'D1 frozen loudly SKIPS on absent dependencies' 0 'enforcement is skipped'
AFTER_TREE="$(sha_of_tree "${COLD}/.claude/skills/vendor")"
RUN_OUT="before=${BEFORE_TREE} after=${AFTER_TREE}"
RUN_RC=$([ "${BEFORE_TREE}" = "${AFTER_TREE}" ] && echo 0 || echo 1)
expect 'D2 the frozen skip left the vendor tree byte-identical' 0 ''
run_in "${COLD}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'D3 bare writer REFUSES on the same absent dependencies' 3 'never publishes a partial vendored tree'

PARTIAL="$(mutate partial-install)"
rm -rf "${PARTIAL}/node_modules/@atomicloud/diene.beta"
run_in "${PARTIAL}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'D4 partial install SKIPS in frozen mode' 0 "declared package '@atomicloud/diene.beta 2.0.0' (from package.json) is not installed"
FROZEN_RC=${RUN_RC}
run_in "${PARTIAL}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'D5 partial install REFUSES in writer mode' 3 "declared package '@atomicloud/diene.beta 2.0.0' (from package.json) is not installed"
WRITER_RC=${RUN_RC}
RUN_OUT="writer=${WRITER_RC} frozen=${FROZEN_RC}"
RUN_RC=$([ "${FROZEN_RC}" -eq 0 ] && [ "${WRITER_RC}" -eq 3 ] && echo 0 || echo 1)
expect "D6 MUST-DIFFER: partial dependencies produce frozen=0 and writer=3 (${RUN_OUT})" 0 ''

group 'E. REQUIRE SUBJECTS — both modes retain the refusal'

V="$(mutate vacuous)"
printf '{"name":"fixture","dependencies":{"left-pad":"1.0.0"}}\n' >"${V}/package.json"
rm -rf "${V}/node_modules/@atomicloud"
mkdir -p "${V}/node_modules"
rm -rf "${V}/.claude/skills/vendor/diene.alpha" "${V}/.claude/skills/vendor/diene.beta"
rm -f "${V}/.claude/skills/vendor/manifest.json"
git_q "${V}" add -A
git_q "${V}" commit -m vacuous --no-verify
run_in "${V}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'E1 frozen refuses when requireSubjects has no subject' 1 'no vendored skill resolved'
run_in "${V}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'E2 writer refuses when requireSubjects has no subject' 1 'no vendored skill resolved'

printf 'schemaVersion: 1\nruntime: bun\nrequireSubjects: false\n' >"${V}/skills-sync.yaml"
run_in "${V}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'E3 writer accepts the explicitly empty case' 0 'vendored skills synchronised'
git_q "${V}" add -A
git_q "${V}" commit -m empty --no-verify
run_in "${V}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'E4 frozen accepts the staged explicitly empty case' 0 'git index match'

group 'F. MODE IS EXPLICIT — hook markers do not infer behavior'

WCOLD="$(mutate writer-cold)"
rm -rf "${WCOLD}/node_modules"
run_in "${WCOLD}" PRE_COMMIT=1 "${SKILLS_SYNC_CMD[@]}" sync
expect 'F1 a hook marker does not turn bare sync into frozen mode' 3 'never publishes a partial vendored tree'
run_in "${WCOLD}" PRE_COMMIT=1 "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'F2 --frozen explicitly selects skip behavior in the same environment' 0 'enforcement is skipped'

WNEW="$(mutate writer-roundtrip)"
mkdir -p "${WNEW}/node_modules/@atomicloud/diene.alpha/skills/added"
printf 'added later\n' >"${WNEW}/node_modules/@atomicloud/diene.alpha/skills/added/SKILL.md"
run_in "${WNEW}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'F3 the writer synchronises a newly shipped skill' 0 'vendored skills synchronised'
git_q "${WNEW}" add -A
git_q "${WNEW}" commit -m resync --no-verify
run_in "${WNEW}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'F4 frozen is green after the result is staged' 0 'git index match'

NONGIT="${TMPROOT}/nongit"
mkdir -p "${NONGIT}"
run_in "${NONGIT}" git rev-parse --show-toplevel
expect 'F5 POSITIVE CONTROL: the fixture directory is not a git worktree' 128 'not a git repository'
run_in "${NONGIT}" PRE_COMMIT=1 "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'F6 hook markers do not replace the git-worktree precondition' 5 'is not inside a git work tree'

group 'G. INLINE RESOLVER — adding a language changes one place'

PKG_FINGERPRINT_BEFORE="$(sha_of_tree "${PKG_DIR}/src")"

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
run_in "${ZIG}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'G2 and frozen mode is green after staging' 0 'git index match'

printf 'tampered\n' >"${ZIG}/.claude/skills/vendor/diene_zig/zed/SKILL.md"
run_in "${ZIG}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'G3 and it can go RED — the new language got a real gate, not a stub' 1 'regenerated'

printf 'tampered\n' >"${ZIG}/.claude/skills/vendor/diene_zig/zed/SKILL.md"
rm -rf "${ZIG}/zig-cache"
run_in "${ZIG}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'G4 and its unrestored dependencies skip like every other runtime' 0 'enforcement is skipped'

PKG_FINGERPRINT_AFTER="$(sha_of_tree "${PKG_DIR}/src")"
RUN_RC=$([ "${PKG_FINGERPRINT_BEFORE}" = "${PKG_FINGERPRINT_AFTER}" ] && echo 0 || echo 1)
RUN_OUT="skills-sync/src before=${PKG_FINGERPRINT_BEFORE} after=${PKG_FINGERPRINT_AFTER}"
expect 'G5 the places changed to add that language = 1 (skills-sync itself is untouched)' 0 ''

group 'H. CONFIGURATION — an unusable config is loud, never an "off"'

H="$(mutate config)"
printf 'schemaVersion: 1\nruntime: kotlin\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'H1 an unknown runtime is INVALID, not silently off' 4 'is not a built-in preset'

printf 'schemaVersion: 7\nruntime: bun\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'H2 an unknown schema version is invalid' 4 "'schemaVersion' must be 1"

printf 'schemaVersion: 1\nruntime: bun\nvendorDir: /etc\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'H3 a vendorDir outside the repository is invalid' 4 'must be a path inside the repository'

printf 'schemaVersion: 1\nruntime: bun\n' >"${H}/skills-sync.yaml"
run_in "${H}" SKILLS_SYNC_CONFIG=nowhere.yaml "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'H4 a config pointer aimed at nothing is invalid, not off' 4 'does not exist'

printf 'schemaVersion: 1\nresolver:\n  name: broken\n  declare: []\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'H5 an empty inline resolver is invalid' 4 "'resolver.declare' must be a non-empty array"

printf 'schemaVersion: 1\nresolver:\n  name: broken\n  declare:\n    - file: x\n      format: text\n      pattern: "[unclosed"\n  resolve:\n    strategy: path-template\n    template: x\n' >"${H}/skills-sync.yaml"
run_in "${H}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'H6 an inline resolver with a broken regex is invalid' 4 'not a valid regular expression'

group 'I. OTHER RUNTIME PRESETS — declared, and exercised where the toolchain exists'

DART="${TMPROOT}/dart"
mkdir -p "${DART}/.claude/skills/vendor" "${DART}/.dart_tool" \
  "${DART}/packages/app" "${DART}/pubcache/diene_core/skills/core"
git init -q "${DART}"
git -C "${DART}" config core.hooksPath "${TMPROOT}/no-hooks"
printf 'schemaVersion: 1\nruntime: dart\n' >"${DART}/skills-sync.yaml"
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
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'I2 dart: frozen mode is green' 0 'git index match'
printf 'tampered\n' >"${DART}/.claude/skills/vendor/diene_core/core/SKILL.md"
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'I3 dart: and it can go RED' 1 'regenerated'
cp "${DART}/.dart_tool/package_config.json" "${TMPROOT}/pc.json"
rm -rf "${DART}/.dart_tool"
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'I4 dart: an absent package_config loudly skips frozen enforcement' 0 'package_config.json'"'"' does not exist'

mkdir -p "${DART}/.dart_tool"
printf '{ "configVersion": 2, "packages": [ { "name": "http", "rootUri": "file://%s/pubcache/http" } ] }\n' "${DART}" \
  >"${DART}/.dart_tool/package_config.json"
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'I4b dart: a package_config missing the package skips frozen enforcement' 0 "declared package 'diene_core ^1.0.0' (from packages/app/pubspec.yaml) is not installed"
run_in "${DART}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'I4c dart: the same partial state refuses in writer mode' 3 "declared package 'diene_core ^1.0.0' (from packages/app/pubspec.yaml) is not installed"
cp "${TMPROOT}/pc.json" "${DART}/.dart_tool/package_config.json"

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
run_in "${NUGET}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'I6 nuget: frozen mode is green' 0 'git index match'
printf 'tampered\n' >"${NUGET}/.claude/skills/vendor/AtomiCloud.Diene.Core/core/SKILL.md"
run_in "${NUGET}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'I7 nuget: and it can go RED' 1 'regenerated'

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
  run_in "${GO}" GOFLAGS=-mod=mod GOPATH="${TMPROOT}/gopath" GOCACHE="${TMPROOT}/gocache" "${SKILLS_SYNC_CMD[@]}" sync --frozen
  expect 'I9 go: frozen mode is green' 0 'git index match'
  printf 'tampered\n' >"${GO}/.claude/skills/vendor/diene.go/gopher/SKILL.md"
  run_in "${GO}" GOFLAGS=-mod=mod GOPATH="${TMPROOT}/gopath" GOCACHE="${TMPROOT}/gocache" "${SKILLS_SYNC_CMD[@]}" sync --frozen
  expect 'I10 go: and it can go RED' 1 'regenerated'
else
  printf '  ⏭️ I8-I10 go arm NOT RUN: no go toolchain on PATH. The go preset is UNEXERCISED by this run.\n'
fi

group 'J. OFF IS NOT A HIDING PLACE — the other operand'

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
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'J1 declares diene packages + empty tree + NO config REFUSES' 1 '@atomicloud/diene.alpha'
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'J1b and it says why rather than only that' 1 'declares diene package'

printf 'schemaVersion: 1\n' >"${DECL}/skills-sync.yaml"
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'J2 a config naming no runtime is an absence, not a declaration' 1 'declares diene package'

printf 'schemaVersion: 1\nruntime: none\n' >"${DECL}/skills-sync.yaml"
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'J3 MUST-DIFFER: runtime: none is an explicit opt-out and PASSES' 0 ''
run_in "${DECL}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'J3b and the opt-out still names what it is ignoring' 0 '@atomicloud/diene.alpha'

rm -f "${DECL}/skills-sync.yaml"
WSSHAPE="${TMPROOT}/workspace-shape"
mkdir -p "${WSSHAPE}/.claude/skills/vendor"
git init -q "${WSSHAPE}"
git -C "${WSSHAPE}" config core.hooksPath "${TMPROOT}/no-hooks"
touch "${WSSHAPE}/.claude/skills/vendor/.gitkeep"
printf '[]\n' >"${WSSHAPE}/.claude/skills/vendor/manifest.json"
git_q "${WSSHAPE}" add -A
git_q "${WSSHAPE}" commit -m wsshape --no-verify
run_in "${WSSHAPE}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'J4a the workspace/shared shape stays inert in writer mode' 0 'names no runtime'
run_in "${WSSHAPE}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'J4b the workspace/shared shape stays inert in frozen mode' 0 'names no runtime'

run_in "${WSSHAPE}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'J5 the probe reports that it ran even when it finds nothing' 0 'mechanism(s) probed'

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
  run_in "${dir}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
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

probe_fires go-selfname go.mod 'module github.com/AtomiCloud/diene.go-base

go 1.26.0

require github.com/redis/go-redis/v9 v9.21.0
'
expect 'J6e MUST-DIFFER: a main module NAMED diene.* is not a declaration' 0 '0 diene package(s) declared'

group 'K. THE WRITER REFUSES CLEANLY — a tool whose refusals are its product must not leak a stack'

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
chmod -R u+w "${UNW2}/.claude" 2>/dev/null

group 'L. SELECTION CONTROLS — drawn from the EXCLUDED and NON-MATCHING sets'

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
  run_in "${dir}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
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

selection_probe boundary buildx/gen/pubspec.yaml "${PUBSPEC_DIENE}"
expect 'L3b MUST-DIFFER: buildx/gen/pubspec.yaml IS counted — the prune name is the cause' 1 'diene_core'

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

run_in "${SELF}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'L9a POSITIVE CONTROL: one pubspec swept, one package declared' 0 "declare: '**/pubspec.yaml (1 file(s))'"

printf 'name: shipped\ndependencies:\n  diene_ghost: ^9.9.9\n' \
  >"${SELF}/.claude/skills/vendor/diene_core/core/pubspec.yaml"
git_q "${SELF}" add -A
git_q "${SELF}" commit -m ghost --no-verify
run_in "${SELF}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'L9b a manifest INSIDE the vendor tree is not swept (still 1 file)' 1 "declare: '**/pubspec.yaml (1 file(s))'"
run_in "${SELF}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect_absent 'L9c and its package is NOT declared — no self-reference' 1 'diene_ghost'

group 'M. FROZEN ENFORCEMENT — mutation, index, and no staging'

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
  # shellcheck disable=SC2016
  printf '#!/bin/sh\ncd "$(git rev-parse --show-toplevel)"\nexec %s sync --frozen\n' \
    "${SKILLS_SYNC_CMD[*]}" >"${dir}/.git/hooks/pre-commit"
  chmod +x "${dir}/.git/hooks/pre-commit"
}

commit_in() {
  RUN_OUT="$(cd "$1" && git -c user.email=tests@example.invalid -c user.name=tests \
    -c commit.gpgsign=false commit -m "$2" 2>&1)"
  RUN_RC=$?
}

VENDORED=.claude/skills/vendor/diene.alpha/alpha/SKILL.md
VENDOR_ROOT=.claude/skills/vendor

PC="${TMPROOT}/precommit-control"
make_precommit_repo "${PC}"
printf '#!/bin/sh\nexit 7\n' >"${PC}/.git/hooks/pre-commit"
chmod +x "${PC}/.git/hooks/pre-commit"
printf 'z\n' >"${PC}/z.txt"
git_q "${PC}" add z.txt
commit_in "${PC}" 'control'
expect 'M0 POSITIVE CONTROL: a nonzero hook aborts the commit' 1 ''

IDEM="${TMPROOT}/idempotent"
make_precommit_repo "${IDEM}"
run_in "${IDEM}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'M1 a clean frozen run mutates zero files and passes' 0 'writer mutated 0 file(s)'

BEFORE_MTIME="$(stat -c %Y "${IDEM}/${VENDORED}")"
BEFORE_INODE="$(stat -c %i "${IDEM}/${VENDORED}")"
sleep 1.1
run_in "${IDEM}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
AFTER_MTIME="$(stat -c %Y "${IDEM}/${VENDORED}")"
AFTER_INODE="$(stat -c %i "${IDEM}/${VENDORED}")"
RUN_OUT="mtime ${BEFORE_MTIME}->${AFTER_MTIME} inode ${BEFORE_INODE}->${AFTER_INODE}"
RUN_RC=$([ "${BEFORE_MTIME}" = "${AFTER_MTIME}" ] && [ "${BEFORE_INODE}" = "${AFTER_INODE}" ] && echo 0 || echo 1)
expect "M1b content-idempotence preserves mtime and inode (${RUN_OUT})" 0 ''

OVER="${TMPROOT}/overwrite"
make_precommit_repo "${OVER}"
printf 'HAND-EDITED\n' >"${OVER}/${VENDORED}"
run_in "${OVER}" "${SKILLS_SYNC_CMD[@]}" sync
RUN_OUT="content now: $(cat "${OVER}/${VENDORED}")"
RUN_RC=$([ "$(cat "${OVER}/${VENDORED}")" = 'v1' ] && echo 0 || echo 1)
expect "M2 writer overwrites an existing destination (${RUN_OUT})" 0 ''

STAGED="${TMPROOT}/staged-regeneration"
make_precommit_repo "${STAGED}"
printf 'v2\n' >"${STAGED}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
run_in "${STAGED}" "${SKILLS_SYNC_CMD[@]}" sync
git_q "${STAGED}" add "${VENDOR_ROOT}"
commit_in "${STAGED}" 'staged regeneration'
expect 'M3 an already-staged regeneration passes the frozen hook' 0 ''
RUN_OUT="$(git -C "${STAGED}" show "HEAD:${VENDORED}" 2>&1)"
RUN_RC=$([ "${RUN_OUT}" = 'v2' ] && echo 0 || echo 1)
expect "M3b the commit records the regenerated content (${RUN_OUT})" 0 ''

GAP="${TMPROOT}/gap"
make_precommit_repo "${GAP}"
printf 'v2\n' >"${GAP}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
run_in "${GAP}" "${SKILLS_SYNC_CMD[@]}" sync
printf 'changed\n' >"${GAP}/other.txt"
git_q "${GAP}" add other.txt
commit_in "${GAP}" 'gap'
expect 'M4 correct worktree but stale expected entries in the index are refused' 1 'vendor index disagree'
expect 'M4b the index-only refusal occurs with zero writer mutations' 1 'writer mutated 0 file(s)'
RUN_OUT="$(git -C "${GAP}" log --oneline -1)"
RUN_RC=$(git -C "${GAP}" log --oneline -1 | grep -q 'gap' && echo 1 || echo 0)
expect 'M4c the refused commit was not created' 0 ''

DRIFT="${TMPROOT}/drift"
make_precommit_repo "${DRIFT}"
printf 'v2\n' >"${DRIFT}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
printf 'changed\n' >"${DRIFT}/other.txt"
git_q "${DRIFT}" add other.txt
commit_in "${DRIFT}" 'drift'
expect 'M5 frozen repairs drift and refuses the commit' 1 'regenerated'
expect 'M5b the refusal states that nothing was staged' 1 'NOTHING was added to your commit'
RUN_OUT="$(cat "${DRIFT}/${VENDORED}")"
RUN_RC=$([ "${RUN_OUT}" = 'v2' ] && echo 0 || echo 1)
expect "M5c regenerated content remains in the worktree (${RUN_OUT})" 0 ''
RUN_OUT="$(git -C "${DRIFT}" diff --cached --name-only -- "${VENDOR_ROOT}" | tr '\n' ' ')"
RUN_RC=$([ -z "${RUN_OUT}" ] && echo 0 || echo 1)
expect "M5d frozen staged no vendor paths (${RUN_OUT})" 0 ''

COLDW="$(mutate writer-frozen-cold)"
rm -rf "${COLDW}/node_modules"
run_in "${COLDW}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'M6 frozen skips when dependencies are absent' 0 'enforcement is skipped'
run_in "${COLDW}" "${SKILLS_SYNC_CMD[@]}" sync
expect 'M6b writer refuses when dependencies are absent' 3 'not restored'

TWOSTEP="${TMPROOT}/two-step"
make_precommit_repo "${TWOSTEP}"
printf 'v2\n' >"${TWOSTEP}/node_modules/@atomicloud/diene.alpha/skills/alpha/SKILL.md"
run_in "${TWOSTEP}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'M7 first frozen run writes and refuses' 1 'writer mutated 2 file(s)'
run_in "${TWOSTEP}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'M7b second frozen run still refuses the repaired but unstaged tree' 1 'writer mutated 0 file(s)'
expect 'M7c second frozen run names the index disagreement' 1 'vendor index disagree'
git_q "${TWOSTEP}" add "${VENDOR_ROOT}"
run_in "${TWOSTEP}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'M7d frozen passes after the repaired tree is staged' 0 'git index match'

KEEP="${TMPROOT}/gitkeep"
make_precommit_repo "${KEEP}"
rm -f "${KEEP}/${VENDOR_ROOT}/.gitkeep"
git_q "${KEEP}" add -u -- "${VENDOR_ROOT}/.gitkeep"
git_q "${KEEP}" commit -m remove-gitkeep --no-verify
run_in "${KEEP}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'M8 creating .gitkeep counts as a frozen mutation' 1 'writer mutated 1 file(s): .gitkeep'
RUN_OUT="$(git -C "${KEEP}" diff --cached --name-only -- "${VENDOR_ROOT}" | tr '\n' ' ')"
RUN_RC=$([ -z "${RUN_OUT}" ] && echo 0 || echo 1)
expect "M8b creating .gitkeep staged nothing (${RUN_OUT})" 0 ''
git_q "${KEEP}" add "${VENDOR_ROOT}/.gitkeep"
run_in "${KEEP}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'M8c staged .gitkeep passes frozen enforcement' 0 'git index match'

MANIFEST_INDEX="${TMPROOT}/manifest-index"
make_precommit_repo "${MANIFEST_INDEX}"
printf '{"tampered":true}\n' >"${MANIFEST_INDEX}/${VENDOR_ROOT}/manifest.json"
git_q "${MANIFEST_INDEX}" add "${VENDOR_ROOT}/manifest.json"
git -C "${MANIFEST_INDEX}" show "HEAD:${VENDOR_ROOT}/manifest.json" >"${MANIFEST_INDEX}/${VENDOR_ROOT}/manifest.json"
run_in "${MANIFEST_INDEX}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'M9 manifest-only index disagreement is refused' 1 'writer mutated 0 file(s)'
expect 'M9b manifest.json participates in the index comparison' 1 "${VENDOR_ROOT}/manifest.json"

INDEX_EXTRA="${TMPROOT}/index-extra"
make_precommit_repo "${INDEX_EXTRA}"
printf 'ghost\n' >"${INDEX_EXTRA}/${VENDOR_ROOT}/index-only.txt"
git_q "${INDEX_EXTRA}" add "${VENDOR_ROOT}/index-only.txt"
rm -f "${INDEX_EXTRA}/${VENDOR_ROOT}/index-only.txt"
run_in "${INDEX_EXTRA}" "${SKILLS_SYNC_CMD[@]}" sync --frozen
expect 'M10 an index-only extra path is refused with zero writer mutations' 1 'writer mutated 0 file(s)'
expect 'M10b the index-only extra path is named' 1 "${VENDOR_ROOT}/index-only.txt"

printf '\n=== artifact under test: %s\n' "${SKILLS_SYNC}"
printf '=== %s passed, %s failed\n' "${PASS}" "${FAIL}"
if [ "${FAIL}" -ne 0 ]; then
  printf '=== failed:\n'
  printf '    %s\n' "${FAILED_NAMES[@]}"
  exit 1
fi
exit 0
