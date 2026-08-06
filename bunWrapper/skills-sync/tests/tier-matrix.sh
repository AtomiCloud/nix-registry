#!/usr/bin/env bash
#
# tier-matrix.sh — reproduce the MARKER/TIER-CONSISTENCY result on any skills-sync
# binaries.
#
#   ./tests/tier-matrix.sh <binary-A> [binary-B] ...
#
# Example (comparing a candidate against the current build):
#   ./tests/tier-matrix.sh \
#     "$(nix build --no-link --print-out-paths .#skills-sync)/bin/skills-sync"
#
# WHY THIS IS A FILE AND NOT A SNIPPET IN A REPORT.
#
# The matrix was originally handed over as an ad-hoc shell snippet. Re-running it
# defeated its reader's harness TWICE in one sitting:
#
#   * a human-readable label containing an `=` was passed through `env`, which
#     swallowed it as a bogus assignment on two arms and turned it into a command
#     on the third (exit 127);
#   * the flags were then moved into a variable and used unquoted — under ZSH,
#     which does NOT word-split unquoted parameter expansions, so the whole
#     string became one nonexistent command and that arm returned 127 again.
#
# Both times the surrounding verdict logic printed CONFIDENT CONCLUSIONS derived
# from an arm that never ran ("defect not reproduced", "B unmoved 127 to 127").
# That is an errored check rendering as a verdict, and it was caught only because
# 127 is implausible for a tool that exists — implausibility, not a control.
#
# So this script does three things a snippet cannot:
#   1. it has a bash shebang, so it does not inherit the caller's shell semantics;
#   2. it passes no labels through `env` and expands nothing unquoted;
#   3. it REFUSES TO PRINT A VERDICT when any arm returns an implausible exit
#      code. A missing measurement is reported as missing, never folded into a
#      conclusion.
#
# `set -e` is deliberately absent: this script exists to run commands that are
# expected to fail and then inspect their status.

set -uo pipefail

if [ "$#" -lt 1 ]; then
  printf 'usage: %s <skills-sync-binary> [more binaries...]\n' "$0" >&2
  exit 2
fi

# The only exit codes skills-sync itself defines. Anything else means the
# measurement did not happen: 127 command-not-found, 126 not-executable, a
# signal, and so on.
is_plausible() {
  case "$1" in
  0 | 1 | 2 | 3 | 4 | 5) return 0 ;;
  *) return 1 ;;
  esac
}

WORK="$(mktemp -d "${TMPDIR:-/tmp}/d1-matrix.XXXXXX")"
trap 'rm -rf "${WORK}"' EXIT

NONGIT="${WORK}/nongit"
GITREPO="${WORK}/gitrepo"
mkdir -p "${NONGIT}" "${GITREPO}"
git init -q "${GITREPO}"
git -C "${GITREPO}" config core.hooksPath "${WORK}/no-hooks"

# ---------------------------------------------------------------------------
# POSITIVE CONTROLS, PRINTED FIRST.
#
# Every arm below is worthless unless these two fixtures genuinely differ. This
# is the check whose absence let a non-git directory silently pre-empt the marker
# first place.
# ---------------------------------------------------------------------------
printf '=== POSITIVE CONTROLS (these two MUST differ)\n'
NONGIT_ROOT="$(cd "${NONGIT}" && git rev-parse --show-toplevel 2>&1)"
NONGIT_RC=$?
GITREPO_ROOT="$(cd "${GITREPO}" && git rev-parse --show-toplevel 2>&1)"
GITREPO_RC=$?
printf '  non-git fixture : rc=%s  %s\n' "${NONGIT_RC}" "${NONGIT_ROOT}"
printf '  git fixture     : rc=%s  %s\n' "${GITREPO_RC}" "${GITREPO_ROOT}"

if [ "${NONGIT_RC}" -eq 0 ] || [ "${GITREPO_RC}" -ne 0 ]; then
  printf '\n❌ ABORT: the fixtures did not come out as intended, so no arm below would mean anything.\n' >&2
  exit 5
fi
printf '  ✅ fixtures differ: one is outside a work tree, one is inside\n'

# ---------------------------------------------------------------------------
# The three arms, per binary.
# ---------------------------------------------------------------------------
STATUS=0

for BIN in "$@"; do
  printf '\n=== BINARY: %s\n' "${BIN}"

  if [ ! -x "${BIN}" ]; then
    printf '❌ not an executable file — SKIPPED, and no verdict is printed for it\n' >&2
    STATUS=1
    continue
  fi

  # Arm A: hook marker set, in a directory that is NOT a work tree.
  A_OUT="$(cd "${NONGIT}" && PRE_COMMIT=1 "${BIN}" sync --tier setup 2>&1)"
  A_RC=$?
  # Arm B: same directory, every known marker unset.
  B_OUT="$(cd "${NONGIT}" && env -u SKILLS_SYNC_HOOK_CONTEXT -u PRE_COMMIT -u PRE_COMMIT_HOME \
    -u GIT_INDEX_FILE -u HUSKY_GIT_PARAMS -u LEFTHOOK "${BIN}" sync --tier setup 2>&1)"
  B_RC=$?
  # Arm C: hook marker set, inside a real work tree.
  C_OUT="$(cd "${GITREPO}" && PRE_COMMIT=1 "${BIN}" sync --tier pre-commit 2>&1)"
  C_RC=$?

  printf '  ARM A  non-git + marker, --tier setup      -> exit %s | %s\n' "${A_RC}" "$(printf '%s' "${A_OUT}" | head -1 | cut -c1-90)"
  printf '  ARM B  non-git + NO marker, --tier setup   -> exit %s | %s\n' "${B_RC}" "$(printf '%s' "${B_OUT}" | head -1 | cut -c1-90)"
  printf '  ARM C  git repo + marker, --tier pre-commit-> exit %s | %s\n' "${C_RC}" "$(printf '%s' "${C_OUT}" | head -1 | cut -c1-90)"

  # An implausible code means the arm did not run. It is reported as a missing
  # measurement and NO verdict is derived from it — which is the whole point of
  # this file.
  BAD=""
  is_plausible "${A_RC}" || BAD="${BAD} A(${A_RC})"
  is_plausible "${B_RC}" || BAD="${BAD} B(${B_RC})"
  is_plausible "${C_RC}" || BAD="${BAD} C(${C_RC})"
  if [ -n "${BAD}" ]; then
    printf '  ❌ NO VERDICT: implausible exit code(s) —%s. skills-sync only ever exits 0-5,\n' "${BAD}" >&2
    printf '     so that arm did not run. A conclusion drawn here would describe the harness,\n' >&2
    printf '     not the binary.\n' >&2
    STATUS=1
    continue
  fi

  # B is the arm that decides it. A==B means both arms died at the same earlier
  # precondition and the marker check was never evaluated; B moving would mean it
  # swallowed cases it has no business refusing.
  if [ "${A_RC}" -eq "${B_RC}" ]; then
    printf '  VERDICT: A==B at %s -> THE MARKER CHECK IS NEVER EVALUATED here (pre-empted)\n' "${A_RC}"
    # A printed defect with a zero exit is a weak failure: whoever runs this in a
    # battery reads the status, not the prose.
    STATUS=1
  else
    printf '  VERDICT: A(%s) != B(%s) -> the marker check IS REACHED, and B still stops at the earlier precondition\n' "${A_RC}" "${B_RC}"
  fi

  # Unmoved in REASON, not merely in exit code: two different refusals can share
  # a code, so the message is asserted too.
  if printf '%s' "${B_OUT}" | grep -qF 'is not inside a git work tree'; then
    printf '  ✅ ARM B is unmoved in REASON: still the git-work-tree refusal, not the hook refusal\n'
  else
    printf '  ⚠️ ARM B exit %s but NOT the git-work-tree refusal — read its output before concluding\n' "${B_RC}"
    STATUS=1
  fi

  # ARM C is the half that proves the REFUSAL was RETIRED rather than merely
  # reordered. The owner mandated the writer run at pre-commit and CI, so those
  # tiers must NOT be refused as a wiring mistake. Without this, a build that
  # refused every hook context would still satisfy A != B.
  if printf '%s' "${C_OUT}" | grep -qF 'a hook is not setup'; then
    printf '  ⚠️ ARM C was refused as a wiring mistake — a MANDATED tier is being blocked\n'
    STATUS=1
  else
    printf '  ✅ ARM C: --tier pre-commit is ALLOWED in a hook context, as the owner mandated\n'
  fi
done

printf '\n=== done (exit %s)\n' "${STATUS}"
exit "${STATUS}"
