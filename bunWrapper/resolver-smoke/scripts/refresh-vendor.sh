#!/usr/bin/env bash

set -euo pipefail
unset CDPATH

if [ "$#" -ne 1 ]; then
  printf 'Usage: %s <published-nix-bundle-path-or-url>\n' "$0" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PACKAGE_DIR="$(dirname "${SCRIPT_DIR}")"
SOURCE="$1"
TMPFILE="$(mktemp "${TMPDIR:-/tmp}/resolver-smoke-vendor.XXXXXX")"
trap 'rm -f "${TMPFILE}"' EXIT

case "${SOURCE}" in
https://*)
  curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' "${SOURCE}" --output "${TMPFILE}"
  ;;
http://*)
  printf "resolver-smoke refresh: '%s' uses http; only https URLs are accepted\n" "${SOURCE}" >&2
  exit 1
  ;;
*)
  [ -f "${SOURCE}" ] || {
    printf "resolver-smoke refresh: '%s' is not a file\n" "${SOURCE}" >&2
    exit 1
  }
  cp "${SOURCE}" "${TMPFILE}"
  ;;
esac

[ -s "${TMPFILE}" ] || {
  printf 'resolver-smoke refresh: downloaded bundle is empty\n' >&2
  exit 1
}
grep -q 'resolver' "${TMPFILE}" || {
  printf 'resolver-smoke refresh: bundle does not appear to export a resolver\n' >&2
  exit 1
}

DIGEST="$(sha256sum "${TMPFILE}")"
DIGEST="${DIGEST%% *}"
install -m 0644 "${TMPFILE}" "${PACKAGE_DIR}/vendor/nix.mjs"
printf '%s\n' "${DIGEST}" >"${PACKAGE_DIR}/vendor/SHA256"

printf 'refreshed vendor/nix.mjs\n'
printf 'sha256 %s\n' "${DIGEST}"
printf 'Run the injection harness and review the published resolver behavior before committing.\n'
