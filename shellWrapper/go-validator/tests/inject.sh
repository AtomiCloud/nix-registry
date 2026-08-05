#!/usr/bin/env bash
set -euo pipefail
unset CDPATH

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "${here}/../../.." && pwd)"
validator="${1:-}"

if [ -z "${validator}" ]; then
  dirty="$(git -C "${repo}" status --porcelain)"
  [ -z "${dirty}" ] || {
    printf '❌ harness: registry is dirty; pass the tested binary explicitly\n' >&2
    exit 1
  }
  validator="$(nix build --no-link --print-out-paths "${repo}#go-validator")/bin/go-validator"
fi

[ -x "${validator}" ] || {
  printf "❌ harness: '%s' is not executable\n" "${validator}" >&2
  exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
proxy="${tmp}/proxy"
mkdir -p "${proxy}"
arms=0
passed=0

expect() {
  local name="$1" want_rc="$2" want_text="$3"
  shift 3
  local out="${tmp}/${name}.out" err="${tmp}/${name}.err" rc=0 stream=""
  stream="${out}"
  arms=$((arms + 1))
  "$@" >"${out}" 2>"${err}" || rc=$?
  [ "${want_rc}" -ne 0 ] && stream="${err}"
  if [ "${rc}" -ne "${want_rc}" ] || ! grep -qF -- "${want_text}" "${stream}"; then
    printf "❌ %s: expected exit %s and text '%s', got exit %s\n" \
      "${name}" "${want_rc}" "${want_text}" "${rc}" >&2
    sed 's/^/  stdout| /' "${out}" >&2
    sed 's/^/  stderr| /' "${err}" >&2
    return 1
  fi
  passed=$((passed + 1))
  printf '✅ %s\n' "${name}"
}

expect positive-offline-environment 0 "file://${proxy}" \
  "${validator}" run --proxy "${proxy}" -- go env GOPROXY
expect goprivate-cannot-bypass-proxy 0 "none" \
  env GOPRIVATE=example.invalid "${validator}" run --proxy "${proxy}" -- go env GONOPROXY
expect unknown-command 1 "unknown command 'lint'" "${validator}" lint
expect missing-proxy-flag 1 "requires '--proxy" "${validator}" run
expect absent-proxy 1 "does not exist" \
  "${validator}" run --proxy "${tmp}/absent" -- go env GOPROXY
expect missing-separator 1 "needs '--'" \
  "${validator}" run --proxy "${proxy}" go env GOPROXY
expect missing-command 1 "needs a validator command" \
  "${validator}" run --proxy "${proxy}" --

if expect control-harness-can-go-red 1 "this diagnostic must not exist" "${validator}" lint; then
  printf '❌ control unexpectedly passed\n' >&2
  exit 1
fi

printf '✅ go-validator injections: %s expected arms passed; control went red as intended\n' "${passed}"
