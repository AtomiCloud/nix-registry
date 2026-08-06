#!/usr/bin/env bash
#
# The fixture's stand-in for `nix develop .#<shell> --command bash -c`.
#
# Each named shell gets a DIFFERENT PATH, which is what lets the arms prove that
# toolchain-smoke's verdict is attributable to the shell it names: `fixture-tool`
# exists in `full` and not in `lean`, so the same binary must pass in one and
# refuse in the other. An unknown shell exits 9 rather than 1, so "could not enter
# the shell" stays distinguishable from "the binary is missing".
set -euo pipefail

shell="${1:-}"
shift || true

case "${shell}" in
full) PATH="${PWD}/tools/bin-full:${PATH}" ;;
lean) PATH="${PWD}/tools/bin-lean:${PATH}" ;;
*)
  printf 'enter-shell: no such fixture shell %s\n' "${shell}" >&2
  exit 9
  ;;
esac
export PATH

exec bash -c "$@"
