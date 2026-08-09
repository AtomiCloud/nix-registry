#!/usr/bin/env bash
#
# resolver-smoke failure-injection harness.
#
# resolver-smoke exists because the published atomi/nix@2 packages merger, given
# a multi-set `//` packages.nix, silently merged it to an EMPTY 42-byte skeleton
# and exited 0. A gate against that defect is worthless unless the gate itself is
# proven to fire, so every arm here is an INJECTION: a fixture in the canonical
# shape passes, a deliberately broken copy of it must REFUSE, and every refusal
# arm asserts the refusal TEXT and not merely a non-zero exit. A gate that is
# only non-zero has not been shown to say why.
#
# Four properties are structural, and each exists because its absence has drawn
# blood on this defect before:
#
#   * Every arm gets a FRESH materialized repository. Arms never share a tree and
#     therefore cannot contaminate one another, and the canonical baseline is
#     re-asserted after the whole mutation battery (arm 7).
#   * Every mutator asserts its target is PRESENT before changing it. Without the
#     guard a mutation can decay into a no-op and "prove" a refusal that
#     resolver-smoke never had to produce. The collapse fixture additionally
#     re-asserts its byte length and sha256 on every use.
#   * Fixture sources are stored with a `.nixsrc` suffix and materialized into
#     `flake.nix` / `nix/*.nix` at run time. `.nix` files in this repository are
#     rewritten by treefmt (nixpkgs-fmt), and the defect under test is a
#     BYTE-LEVEL shape: a formatter that reflows a function-argument header onto
#     several lines converts a passing fixture into a failing one, or silently
#     repairs the collapse fixture. Materializing keeps the bytes exact.
#   * The fixture is deliberately NOT shaped like the workspace it came from: its
#     registry is `acme`, its packages are `acmeutils` / `acmelint`. If
#     resolver-smoke had a diene-shaped constant baked into it, arm 1 would fail.
#
# The one exception to "purpose-built fixture" is the collapse arm. Those are the
# EXACT pre-fix workspace bytes (git show f4b6bdb3^:nix/packages.nix), 344 bytes,
# sha256 38838467be749d2197180eb87baf0caa4efee150651e346c790e4b96cd7be82e, stored
# byte-for-byte. A hand-written approximation of the defect is not the defect.
#
# Usage:
#   ./run.sh                                     # tests `bun <pkg>/index.ts`
#   RESOLVER_SMOKE=/path/to/resolver-smoke ./run.sh
#
# RESOLVER_SMOKE may name a packaged binary; bun is only required when the
# default is in use.
#
# Portability is a property of the harness, not a requirement on the host. Every
# in-place fixture edit and the millisecond clock below are implemented without
# GNU extensions — no `sed -i`, no `date +%s%N`, no `0,/re/` address, no
# one-line `a\text` append — so a BSD/macOS userland runs the same battery and a
# failure here is always resolver-smoke's, never the shell's.

set -uo pipefail

# `cd` consults CDPATH for any operand that is not absolute and does not begin
# with `.` or `..`, and prints the directory it lands in — which would be
# captured by the command substitutions below. Cleared once here rather than
# guarded at every site.
unset CDPATH

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PKG_DIR="$(dirname "${HERE}")"
FIXTURES="${HERE}/fixtures"
CANON_SRC="${FIXTURES}/canonical"
COLLAPSE_SRC="${FIXTURES}/collapse/packages.nixsrc"

# The exact pre-fix workspace bytes. Re-asserted on every use.
COLLAPSE_BYTES=344
COLLAPSE_SHA=38838467be749d2197180eb87baf0caa4efee150651e346c790e4b96cd7be82e

RESOLVER_SMOKE="${RESOLVER_SMOKE:-bun ${PKG_DIR}/index.ts}"
# shellcheck disable=SC2206 # deliberate word split: the override may carry args
SMOKE_CMD=(${RESOLVER_SMOKE})

die() {
  printf '‼️ harness: %s\n' "$1" >&2
  exit 5
}

[ "${#SMOKE_CMD[@]}" -ge 1 ] || die 'RESOLVER_SMOKE is empty'
command -v "${SMOKE_CMD[0]}" >/dev/null 2>&1 ||
  die "'${SMOKE_CMD[0]}' is not on PATH (RESOLVER_SMOKE=${RESOLVER_SMOKE})"
[ -d "${CANON_SRC}" ] || die "canonical fixture sources missing at ${CANON_SRC}"
[ -f "${COLLAPSE_SRC}" ] || die "collapse fixture missing at ${COLLAPSE_SRC}"

HARNESS_TMP_BASE="$(cd "${TMPDIR:-/tmp}" && pwd -P)" ||
  die "could not resolve temporary-directory base ${TMPDIR:-/tmp}"
TMPROOT="$(mktemp -d "${HARNESS_TMP_BASE}/resolver-smoke-tests.XXXXXX")" ||
  die 'could not create a temporary directory'
# shellcheck disable=SC2329 # invoked indirectly by the EXIT trap below
cleanup() {
  [ -n "${TMPROOT:-}" ] || return 0
  case "${TMPROOT}" in
  "${HARNESS_TMP_BASE}"/resolver-smoke-tests.*) ;;
  *) return 0 ;;
  esac
  chmod -R u+w "${TMPROOT}" 2>/dev/null
  rm -rf -- "${TMPROOT}"
}
trap cleanup EXIT

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

record() {
  local name="$1" ok="$2" why="$3"
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
  record "${name}" "${ok}" "${why}"
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
  record "${name}" "${ok}" "${why}"
}

group() { printf '\n== %s\n' "$1"; }

# ---------------------------------------------------------------------------
# Millisecond clock. `date +%s%N` is a GNU extension: a BSD date prints a literal
# `N`, which would leave CANON_MS non-numeric and turn the budget arm (J3) into a
# shell error instead of an assertion. bash 5's EPOCHREALTIME needs no external
# process at all, GNU nanoseconds are the next choice, and whole seconds are the
# floor — coarse, but a 3000ms budget is still a real check at 1000ms resolution,
# so the mode is reported in the header rather than silently assumed.
# ---------------------------------------------------------------------------

CLOCK_MODE=seconds
CLOCK_RESOLUTION_MS=1000
if [ -n "${EPOCHREALTIME:-}" ]; then
  CLOCK_MODE=epochrealtime
  CLOCK_RESOLUTION_MS=1
else
  case "$(date +%s%N 2>/dev/null)" in
  '' | *[!0-9]*) ;;
  *)
    CLOCK_MODE=nanoseconds
    CLOCK_RESOLUTION_MS=1
    ;;
  esac
fi

now_ms() {
  local stamp int frac
  case "${CLOCK_MODE}" in
  epochrealtime)
    # `seconds.microseconds`, except that the radix character follows the
    # locale, so both `.` and `,` have to be accepted.
    stamp="${EPOCHREALTIME}"
    int="${stamp%%[.,]*}"
    case "${stamp}" in
    *[.,]*) frac="${stamp#*[.,]}" ;;
    *) frac="" ;;
    esac
    frac="${frac}000000"
    printf '%s' "$((int * 1000 + 10#${frac:0:3}))"
    ;;
  nanoseconds)
    stamp="$(date +%s%N)"
    printf '%s' "$((10#${stamp} / 1000000))"
    ;;
  *)
    stamp="$(date +%s)"
    printf '%s' "$((10#${stamp} * 1000))"
    ;;
  esac
}

case "$(now_ms)" in
'' | *[!0-9]*) die "no numeric clock available (mode ${CLOCK_MODE})" ;;
esac

# ---------------------------------------------------------------------------
# Fixture materialization. Sources carry a `.nixsrc` suffix so treefmt cannot
# reach them; they become real `.nix` files only inside a throwaway repo.
# ---------------------------------------------------------------------------

# `sha256sum` is coreutils; a stock macOS has `shasum` instead. Both print the
# digest as the first field, so the assertions downstream are unchanged.
if command -v sha256sum >/dev/null 2>&1; then
  SHA_CMD=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  SHA_CMD=(shasum -a 256)
else
  die 'neither sha256sum nor shasum is on PATH; the fixture digests cannot be checked'
fi

sha_of() { "${SHA_CMD[@]}" "$1" | cut -d' ' -f1; }

# materialize <arm-name> -> prints the fresh repo path
materialize() {
  local dir="${TMPROOT}/$1"
  rm -rf -- "${dir}"
  mkdir -p "${dir}/nix" || die "could not create ${dir}"
  cp "${CANON_SRC}/flake.nixsrc" "${dir}/flake.nix" || die 'flake fixture missing'
  local f
  for f in env fmt packages shells pre-commit; do
    cp "${CANON_SRC}/nix/${f}.nixsrc" "${dir}/nix/${f}.nix" ||
      die "canonical fixture source nix/${f}.nixsrc missing"
  done
  printf '%s' "${dir}"
}

# materialize_empty <arm-name> -> a repo with NONE of the dispatch-table files
materialize_empty() {
  local dir="${TMPROOT}/$1"
  rm -rf -- "${dir}"
  mkdir -p "${dir}" || die "could not create ${dir}"
  printf 'placeholder\n' >"${dir}/README.md"
  printf '%s' "${dir}"
}

# collapse_into <repo> — drop the exact pre-fix bytes onto nix/packages.nix
collapse_into() {
  local dir="$1" got_bytes got_sha
  got_bytes="$(wc -c <"${COLLAPSE_SRC}" | tr -d ' ')"
  got_sha="$(sha_of "${COLLAPSE_SRC}")"
  [ "${got_bytes}" = "${COLLAPSE_BYTES}" ] ||
    die "collapse fixture is ${got_bytes} bytes, expected ${COLLAPSE_BYTES} — a formatter has rewritten it"
  [ "${got_sha}" = "${COLLAPSE_SHA}" ] ||
    die "collapse fixture sha256 is ${got_sha}, expected ${COLLAPSE_SHA} — these are no longer the pre-fix bytes"
  mkdir -p "${dir}/nix"
  cp "${COLLAPSE_SRC}" "${dir}/nix/packages.nix" || die 'could not place the collapse fixture'
}

# guard_contains <file> <literal> — a mutation may never decay into a no-op
guard_contains() {
  grep -qF -- "$2" "$1" || die "injection guard: '$2' is not present in $1"
}

# guard_absent <file> <literal>
guard_absent() {
  grep -qF -- "$2" "$1" && die "injection guard: '$2' unexpectedly already present in $1"
  return 0
}

# rewrite_header <file> <old-first-line> <new-header-block>
rewrite_header() {
  local file="$1" old="$2" new="$3" tmp="${TMPROOT}/.hdr"
  guard_contains "${file}" "${old}"
  {
    printf '%s\n' "${new}"
    tail -n +2 "${file}"
  } >"${tmp}" || die "could not rewrite the header of ${file}"
  mv "${tmp}" "${file}"
  guard_absent "${file}" "${old}"
}

# ---------------------------------------------------------------------------
# Portable in-place editors. `sed -i` is not portable — GNU takes a bare `-i`,
# BSD requires an argument — and neither are the extensions this battery needs: a
# `\n` in a replacement, the `0,/re/` address, and a one-line `a\text` append.
# These helpers perform the same edits with bash string handling and a whole-file
# rewrite, so the injected bytes are identical on every userland. Each one dies
# unless it matched something: the injection guards around the call sites make
# the same demand, and a mutation that decays into a no-op would "prove" a
# refusal resolver-smoke never had to produce.
# ---------------------------------------------------------------------------

# edit_write <file> <content> — stage, then replace, so a failed write cannot
# leave a half-mutated fixture behind.
edit_write() {
  local file="$1" tmp="${TMPROOT}/.edit"
  printf '%s' "$2" >"${tmp}" || die "could not stage an edit of ${file}"
  mv "${tmp}" "${file}" || die "could not write ${file}"
}

# replace_first <file> <literal> <replacement> — replace the first occurrence of
# a literal and require the fixture to contain exactly one matching line.
replace_first() {
  local file="$1" old="$2" new="$3"
  local line out="" hits=0 pre post
  while IFS= read -r line || [ -n "${line}" ]; do
    case "${line}" in
    *"${old}"*)
      pre="${line%%"${old}"*}"
      post="${line#*"${old}"}"
      line="${pre}${new}${post}"
      hits=$((hits + 1))
      ;;
    esac
    out="${out}${line}"$'\n'
  done <"${file}"
  [ "${hits}" -eq 1 ] || die "injection guard: '${old}' matched ${hits} lines in ${file}, expected 1"
  edit_write "${file}" "${out}"
}

# insert_after <file> <exact-line> <new-line>... — insert below the only line
# that is exactly <exact-line>.
insert_after() {
  local file="$1" anchor="$2"
  shift 2
  local line out="" hits=0 extra
  while IFS= read -r line || [ -n "${line}" ]; do
    out="${out}${line}"$'\n'
    if [ "${line}" = "${anchor}" ]; then
      hits=$((hits + 1))
      for extra in "$@"; do out="${out}${extra}"$'\n'; done
    fi
  done <"${file}"
  [ "${hits}" -eq 1 ] || die "injection guard: ${hits} lines equal '${anchor}' in ${file}, expected 1"
  edit_write "${file}" "${out}"
}

# insert_before <file> <exact-line> <new-line>... — insert above the only line
# that is exactly <exact-line>.
insert_before() {
  local file="$1" anchor="$2"
  shift 2
  local line out="" hits=0 extra
  while IFS= read -r line || [ -n "${line}" ]; do
    if [ "${line}" = "${anchor}" ]; then
      hits=$((hits + 1))
      for extra in "$@"; do out="${out}${extra}"$'\n'; done
    fi
    out="${out}${line}"$'\n'
  done <"${file}"
  [ "${hits}" -eq 1 ] || die "injection guard: ${hits} lines equal '${anchor}' in ${file}, expected 1"
  edit_write "${file}" "${out}"
}

# append_to_lines_ending <file> <suffix> <appended> — append to every line that
# ends with <suffix> (`sed 's/;$/;   /'`).
append_to_lines_ending() {
  local file="$1" suffix="$2" appended="$3"
  local line out="" hits=0
  while IFS= read -r line || [ -n "${line}" ]; do
    case "${line}" in
    *"${suffix}")
      line="${line}${appended}"
      hits=$((hits + 1))
      ;;
    esac
    out="${out}${line}"$'\n'
  done <"${file}"
  [ "${hits}" -ge 1 ] || die "injection guard: no line ends with '${suffix}' in ${file}"
  edit_write "${file}" "${out}"
}

printf '=== resolver-smoke acceptance battery\n'
printf '=== artifact under test: %s\n' "${RESOLVER_SMOKE}"
printf '=== fixtures under: %s\n' "${TMPROOT}"
printf '=== clock: %s (%sms resolution)\n' "${CLOCK_MODE}" "${CLOCK_RESOLUTION_MS}"
run_in "${TMPROOT}" "${SMOKE_CMD[@]}" --version
printf '=== reported version: %s (exit %s)\n' "${RUN_OUT}" "${RUN_RC}"

# ---------------------------------------------------------------------------
group 'A. CLI SURFACE — help, version, and refusal to guess'
# ---------------------------------------------------------------------------

A="$(materialize cli)"

run_in "${A}" "${SMOKE_CMD[@]}" --help
expect 'A1 --help exits 0' 0 'resolver-smoke'
run_in "${A}" "${SMOKE_CMD[@]}" --version
expect 'A2 --version exits 0' 0 ''
run_in "${A}" "${SMOKE_CMD[@]}" --bogus
expect 'A3 an unknown argument exits 2, it is not ignored' 2 ''
run_in "${A}" "${SMOKE_CMD[@]}" --bogus
expect_absent 'A4 and an unknown argument does not fall through to a run' 2 'resolver probe passed'
run_in "${A}" "${SMOKE_CMD[@]}" nosuchpositional
expect 'A5 an unknown positional exits 2' 2 ''

# ---------------------------------------------------------------------------
group 'B. POSITIVE CONTROL — the canonical six, one proven probe per file'
# ---------------------------------------------------------------------------

B="$(materialize canonical)"

run_in "${B}" "${SMOKE_CMD[@]}"
expect 'B0 the canonical fixture passes from cwd' 0 'resolver-managed file(s) passed'

for subject in flake.nix nix/env.nix nix/fmt.nix nix/packages.nix nix/shells.nix nix/pre-commit.nix; do
  expect "B1 ${subject} reports its own passing probe" 0 "${subject}: resolver probe passed"
done

expect 'B2 all six dispatch-table files were counted' 0 '6 resolver-managed file(s) passed'
expect_absent 'B3 a passing run names no refusal' 0 'non-degenerate'

CANON_MS_START="$(now_ms)"
run_in "${B}" "${SMOKE_CMD[@]}"
CANON_MS_END="$(now_ms)"
CANON_MS=$((CANON_MS_END - CANON_MS_START))

# ---------------------------------------------------------------------------
group 'C. THE DEFECT — the exact pre-fix multi-set packages.nix must refuse'
# ---------------------------------------------------------------------------

C="$(materialize collapse)"
collapse_into "${C}"
guard_contains "${C}/nix/packages.nix" '// (with pkgs-2605; {'
guard_absent "${C}/nix/packages.nix" 'all = rec {'

run_in "${C}" "${SMOKE_CMD[@]}"
expect 'C1 the collapse shape is a violation' 1 ''
expect 'C2 the refusal names the file that was lost' 1 'nix/packages.nix'
expect 'C3 the refusal names the non-degenerate probe' 1 'non-degenerate'
expect 'C4 the refusal says what the output collapsed to' 1 'empty skeleton'
expect_absent 'C5 and it does not claim the file passed' 1 'nix/packages.nix: resolver probe passed'
expect_absent 'C6 a refusal is stated, not leaked as a stack trace' 1 'at <anonymous>'

CONLY="$(materialize_empty collapse-only)"
collapse_into "${CONLY}"
run_in "${CONLY}" "${SMOKE_CMD[@]}"
expect 'C7 the collapse shape refuses even as the only subject in the repo' 1 'empty skeleton'

# ---------------------------------------------------------------------------
group 'D. VENDOR INTEGRITY — a corrupted bundle is exit 4, never a pass'
# ---------------------------------------------------------------------------

VENDOR_DIR="${PKG_DIR}/vendor"
VENDOR_SHA_FILE="${VENDOR_DIR}/SHA256"
VENDOR_BUNDLE="${VENDOR_DIR}/nix-v2.mjs"

if [ ! -f "${VENDOR_BUNDLE}" ] || [ ! -f "${VENDOR_SHA_FILE}" ]; then
  printf '  ⏭️ D1-D4 NOT RUN: %s does not yet hold a bundle and a SHA256 file.\n' "${VENDOR_DIR}"
  printf '     Vendor integrity is UNEXERCISED by this run.\n'
else
  EXPECT_SHA="$(awk 'NR==1{print $1}' "${VENDOR_SHA_FILE}")"
  REAL_SHA="$(sha_of "${VENDOR_BUNDLE}")"

  RUN_OUT="vendor/SHA256=${EXPECT_SHA} actual=${REAL_SHA}"
  RUN_RC=$([ "${EXPECT_SHA}" = "${REAL_SHA}" ] && echo 0 || echo 1)
  expect 'D0 POSITIVE CONTROL: the shipped bundle already matches vendor/SHA256' 0 ''

  GOOD_COPY="${TMPROOT}/vendor-good.mjs"
  BAD_COPY="${TMPROOT}/vendor-bad.mjs"
  cp "${VENDOR_BUNDLE}" "${GOOD_COPY}" || die 'could not copy the vendored bundle'
  cp "${VENDOR_BUNDLE}" "${BAD_COPY}" || die 'could not copy the vendored bundle'
  chmod u+w "${GOOD_COPY}" "${BAD_COPY}"
  # One byte, in the bundle's leading banner, well inside every plausible file.
  # `status=none` is a coreutils operand; silencing stderr works everywhere.
  printf 'X' | dd of="${BAD_COPY}" bs=1 seek=8 conv=notrunc 2>/dev/null ||
    die 'could not corrupt the bundle copy'
  BAD_SHA="$(sha_of "${BAD_COPY}")"
  [ "${BAD_SHA}" != "${REAL_SHA}" ] ||
    die 'injection guard: the one-byte corruption did not change the sha256'
  [ "$(wc -c <"${BAD_COPY}")" = "$(wc -c <"${GOOD_COPY}")" ] ||
    die 'injection guard: the corruption changed the file length, not one byte'

  D="$(materialize vendor)"

  run_in "${D}" RESOLVER_SMOKE_VENDOR="${GOOD_COPY}" "${SMOKE_CMD[@]}"
  expect 'D1 POSITIVE CONTROL: an uncorrupted copy via RESOLVER_SMOKE_VENDOR still passes' 0 '6 resolver-managed file(s) passed'

  run_in "${D}" RESOLVER_SMOKE_VENDOR="${BAD_COPY}" "${SMOKE_CMD[@]}"
  expect 'D2 a one-byte-corrupted bundle is an integrity failure, not a violation' 4 ''
  expect 'D3 the refusal names the expected sha256' 4 "${EXPECT_SHA}"
  expect 'D4 the refusal names the actual sha256' 4 "${BAD_SHA}"
  expect_absent 'D5 a corrupted bundle never reports a passing probe' 4 'resolver probe passed'
fi

# ---------------------------------------------------------------------------
group 'E. SUBJECTS — vacuity is a refusal, emptiness must be declared'
# ---------------------------------------------------------------------------

E1D="$(materialize_empty none-default)"
run_in "${E1D}" "${SMOKE_CMD[@]}"
expect 'E1 a repo with none of the dispatch-table files refuses' 1 'no resolver-managed files'
expect_absent 'E2 and a vacuous run never reports a pass count' 1 'resolver-managed file(s) passed'

E2D="$(materialize_empty none-declared)"
printf 'schemaVersion: 1\nrequireSubjects: false\n' >"${E2D}/resolver-smoke.yaml"
run_in "${E2D}" "${SMOKE_CMD[@]}"
expect 'E3 MUST-DIFFER: the same repo passes once the emptiness is declared' 0 ''

E3D="$(materialize_empty malformed-config)"
printf 'schemaVersion: 1\nrequireSubjects: [unclosed\n  : : :\n' >"${E3D}/resolver-smoke.yaml"
run_in "${E3D}" "${SMOKE_CMD[@]}"
expect 'E4 an unparseable config is exit 4, never a silent off switch' 4 ''

E4D="$(materialize_empty wrong-schema)"
printf 'schemaVersion: 7\nrequireSubjects: false\n' >"${E4D}/resolver-smoke.yaml"
run_in "${E4D}" "${SMOKE_CMD[@]}"
expect 'E5 an unknown schemaVersion is exit 4' 4 ''

E5D="$(materialize_empty missing-config-pointer)"
run_in "${E5D}" RESOLVER_SMOKE_CONFIG=nowhere.yaml "${SMOKE_CMD[@]}"
expect 'E6 a config pointer aimed at nothing is exit 4, not an off switch' 4 ''

# ---------------------------------------------------------------------------
group 'F. CONFIG DISCOVERY — each documented default name is actually read'
# ---------------------------------------------------------------------------

# `requireSubjects: false` in an otherwise-empty repo flips exit 1 to exit 0, so
# it is a clean observable for "was this file read at all?".
config_body_for() {
  case "$1" in
  *.json) printf '{ "schemaVersion": 1, "requireSubjects": false }\n' ;;
  *) printf 'schemaVersion: 1\nrequireSubjects: false\n' ;;
  esac
}

for name in resolver-smoke.yaml resolver-smoke.yml resolver-smoke.json .resolver-smoke.json; do
  FD="$(materialize_empty "cfg-$(printf '%s' "${name}" | tr './' '--')")"
  config_body_for "${name}" >"${FD}/${name}"
  run_in "${FD}" "${SMOKE_CMD[@]}"
  expect "F1 default config name ${name} is discovered" 0 ''
done

FCTL="$(materialize_empty cfg-control)"
config_body_for other.yaml >"${FCTL}/not-a-resolver-smoke-config.yaml"
run_in "${FCTL}" "${SMOKE_CMD[@]}"
expect 'F2 MUST-DIFFER: an undocumented filename is NOT read (still refuses)' 1 'no resolver-managed files'

FEXP="$(materialize_empty cfg-explicit)"
config_body_for elsewhere.yaml >"${TMPROOT}/elsewhere.yaml"
run_in "${FEXP}" "${SMOKE_CMD[@]}" --config "${TMPROOT}/elsewhere.yaml"
expect 'F3 --config <path> reads a config outside the repo' 0 ''

FENV="$(materialize_empty cfg-env)"
config_body_for env.yaml >"${TMPROOT}/env-pointed.yaml"
run_in "${FENV}" RESOLVER_SMOKE_CONFIG="${TMPROOT}/env-pointed.yaml" "${SMOKE_CMD[@]}"
expect 'F4 the RESOLVER_SMOKE_CONFIG variable reads a config outside the repo' 0 ''

# ---------------------------------------------------------------------------
group 'G. HEADER-SHAPE BATTERY — the silent losses byte ratio cannot see'
# ---------------------------------------------------------------------------
#
# Four of the six mergers parse the function-argument header on line 1 ONLY, via
# /^\s*\{([^}]+)\}\s*:\s*$/. A multi-line header, a leading comment, or a leading
# blank line makes that match fail and the arguments — and the `with packages;` /
# `with env;` prelude, and every `inherit shellHook;` — vanish with NO throw.
# On nix/env.nix that catastrophe costs 33 bytes: 0.91x. A byte-ratio oracle
# cannot see it, and healthy nix/fmt.nix output legitimately shrinks to 0.60x, so
# no ratio threshold exists that separates the two. These arms exist to keep the
# oracle honest. Their refusal wording is not fixed by the interface contract, so
# they assert the exit code and the named file only.

G1="$(materialize hdr-packages)"
rewrite_header "${G1}/nix/packages.nix" \
  '{ atomi, pkgs-2605, pkgs-unstable }:' \
  '{
  atomi,
  pkgs-2605,
  pkgs-unstable,
}:'
run_in "${G1}" "${SMOKE_CMD[@]}"
expect 'G1 a multi-line header on packages.nix refuses (function args are lost)' 1 'nix/packages.nix'
expect_absent 'G1b and packages.nix is not reported as passing' 1 'nix/packages.nix: resolver probe passed'

G2="$(materialize hdr-shells)"
rewrite_header "${G2}/nix/shells.nix" \
  '{ pkgs, packages, env, shellHook }:' \
  '{
  pkgs,
  packages,
  env,
  shellHook,
}:'
run_in "${G2}" "${SMOKE_CMD[@]}"
expect 'G2 a multi-line header on shells.nix refuses (args, the with-env prelude and shellHook are lost)' 1 'nix/shells.nix'
expect_absent 'G2b and shells.nix is not reported as passing' 1 'nix/shells.nix: resolver probe passed'

G3="$(materialize hdr-env-comment)"
guard_contains "${G3}/nix/env.nix" '{ pkgs, packages }:'
printf '# managed by the acme resolver\n%s' "$(cat "${G3}/nix/env.nix")" >"${TMPROOT}/.env-c" &&
  mv "${TMPROOT}/.env-c" "${G3}/nix/env.nix"
head -1 "${G3}/nix/env.nix" | grep -qF '# managed by' ||
  die 'injection guard: the leading comment was not prepended to nix/env.nix'
run_in "${G3}" "${SMOKE_CMD[@]}"
expect 'G3 a leading comment above the header on env.nix refuses' 1 'nix/env.nix'
expect_absent 'G3b and env.nix is not reported as passing' 1 'nix/env.nix: resolver probe passed'

G4="$(materialize hdr-env-ellipsis)"
rewrite_header "${G4}/nix/env.nix" \
  '{ pkgs, packages }:' \
  '{ pkgs, packages, ... }:'
run_in "${G4}" "${SMOKE_CMD[@]}"
expect 'G4 MUST-DIFFER: an ellipsis header is legitimate and stays green' 0 '6 resolver-managed file(s) passed'

# ---------------------------------------------------------------------------
group 'H. GRAMMAR BATTERY — shapes outside the merger that still exit 0 today'
# ---------------------------------------------------------------------------

H1="$(materialize grammar-nonrec)"
guard_contains "${H1}/nix/packages.nix" 'all = rec {'
replace_first "${H1}/nix/packages.nix" 'all = rec {' 'all = {'
guard_absent "${H1}/nix/packages.nix" 'all = rec {'
run_in "${H1}" "${SMOKE_CMD[@]}"
expect 'H1 a non-rec all block refuses — every package is dropped' 1 'nix/packages.nix'

H2="$(materialize grammar-let-preamble)"
guard_contains "${H2}/nix/packages.nix" 'let'
insert_after "${H2}/nix/packages.nix" 'let' '  acmeHelper = "dropped-by-the-merger";'
guard_contains "${H2}/nix/packages.nix" 'acmeHelper'
run_in "${H2}" "${SMOKE_CMD[@]}"
expect 'H2 a let-binding outside the all block refuses — the merger keeps only all' 1 'nix/packages.nix'

H3="$(materialize grammar-resolver-throw)"
guard_contains "${H3}/nix/fmt.nix" '    projectRootFile = "flake.nix";'
insert_after "${H3}/nix/fmt.nix" \
  '    projectRootFile = "flake.nix";' \
  '    resolverSmokeUnsupported = true;'
guard_contains "${H3}/nix/fmt.nix" '    resolverSmokeUnsupported = true;'
run_in "${H3}" "${SMOKE_CMD[@]}"
expect 'H3 a published-merger refusal on well-formed input is a compatibility violation' 1 'nix/fmt.nix'
expect 'H3b the refusal preserves the published merger reason' 1 'unknown top-level key "resolverSmokeUnsupported"'
expect_absent 'H3c and the refusing file is not reported as passing' 1 'nix/fmt.nix: resolver probe passed'

H4="$(materialize grammar-dropped-option)"
guard_contains "${H4}/nix/fmt.nix" '      prettier.enable = true;'
insert_after "${H4}/nix/fmt.nix" \
  '      prettier.enable = true;' \
  '      prettier.package = pkgs.prettier;'
guard_contains "${H4}/nix/fmt.nix" '      prettier.package = pkgs.prettier;'
run_in "${H4}" "${SMOKE_CMD[@]}"
expect 'H4 a formatter option the published merger silently drops is a violation' 1 'nix/fmt.nix'
expect 'H4b normalising dotted paths still detects the genuinely lost binding' 1 "binding 'package'"

# ---------------------------------------------------------------------------
group 'I. NO FALSE ALARMS — a gate that fires on comments blocks every commit'
# ---------------------------------------------------------------------------

I1="$(materialize benign-comments)"
guard_contains "${I1}/nix/env.nix" '  lint = ['
insert_before "${I1}/nix/env.nix" '  lint = [' '  # the lint category'
guard_contains "${I1}/nix/env.nix" '# the lint category'
run_in "${I1}" "${SMOKE_CMD[@]}"
expect 'I1 a comment above a binding is benign' 0 '6 resolver-managed file(s) passed'

I2="$(materialize benign-inherit-comment)"
guard_contains "${I2}/nix/packages.nix" '          git'
insert_before "${I2}/nix/packages.nix" '          git' '          # pinned deliberately'
guard_contains "${I2}/nix/packages.nix" '# pinned deliberately'
run_in "${I2}" "${SMOKE_CMD[@]}"
expect 'I2 a comment inside an inherit list is benign' 0 '6 resolver-managed file(s) passed'

I3="$(materialize benign-blank-lines)"
guard_contains "${I3}/nix/env.nix" '  main = ['
I3_BEFORE="$(wc -l <"${I3}/nix/env.nix")"
insert_before "${I3}/nix/env.nix" '  main = [' '' ''
[ "$(wc -l <"${I3}/nix/env.nix")" -eq $((I3_BEFORE + 2)) ] ||
  die 'injection guard: the two extra blank lines were not inserted into nix/env.nix'
run_in "${I3}" "${SMOKE_CMD[@]}"
expect 'I3 extra blank lines inside the body are benign' 0 '6 resolver-managed file(s) passed'

I4="$(materialize benign-trailing-ws)"
guard_contains "${I4}/nix/env.nix" 'with packages;'
append_to_lines_ending "${I4}/nix/env.nix" ';' '   '
grep -qF 'with packages;   ' "${I4}/nix/env.nix" ||
  die 'injection guard: trailing whitespace was not appended in nix/env.nix'
run_in "${I4}" "${SMOKE_CMD[@]}"
expect 'I4 trailing whitespace is benign' 0 '6 resolver-managed file(s) passed'

I5="$(materialize benign-partial-subject-set)"
rm -f "${I5}/nix/fmt.nix" "${I5}/nix/pre-commit.nix"
[ ! -e "${I5}/nix/fmt.nix" ] || die 'injection guard: nix/fmt.nix was not removed'
run_in "${I5}" "${SMOKE_CMD[@]}"
expect 'I5 a repo with only some of the table files checks only those' 0 '4 resolver-managed file(s) passed'
expect_absent 'I5b and it does not invent a probe for the absent files' 0 'nix/fmt.nix'

I6="$(materialize benign-nested-single-key)"
guard_contains "${I6}/nix/pre-commit.nix" '    shellcheck = {'
guard_contains "${I6}/nix/pre-commit.nix" '      enable = false;'
run_in "${I6}" "${SMOKE_CMD[@]}"
expect 'I6 a single-key nested hook may be re-rendered as a dotted binding' 0 '6 resolver-managed file(s) passed'

# ---------------------------------------------------------------------------
group 'J. RE-ASSERTED BASELINE AND BUDGET'
# ---------------------------------------------------------------------------

J="$(materialize canonical-rerun)"
run_in "${J}" "${SMOKE_CMD[@]}"
expect 'J1 the canonical baseline is still green after the whole battery' 0 '6 resolver-managed file(s) passed'
for subject in flake.nix nix/env.nix nix/fmt.nix nix/packages.nix nix/shells.nix nix/pre-commit.nix; do
  expect "J2 ${subject} still reports its probe after all mutations" 0 "${subject}: resolver probe passed"
done

RUN_OUT="canonical run measured ${CANON_MS}ms (budget 3000ms)"
RUN_RC=$([ "${CANON_MS}" -lt 3000 ] && echo 0 || echo 1)
expect "J3 the whole canonical run is under 3s (${CANON_MS}ms)" 0 ''

printf '\n=== artifact under test: %s\n' "${RESOLVER_SMOKE}"
printf '=== canonical run: %sms\n' "${CANON_MS}"
printf '=== %s passed, %s failed\n' "${PASS}" "${FAIL}"
if [ "${FAIL}" -ne 0 ]; then
  printf '=== failed:\n'
  printf '    %s\n' "${FAILED_NAMES[@]}"
  exit 1
fi
exit 0
