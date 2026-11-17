#!/usr/bin/env bash
set -euo pipefail
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
state=${OBS_PARITY_STATE:?set OBS_PARITY_STATE}
test=${1:?exact Go test name}
# shellcheck source=/dev/null
source "$state/env"
export OBS_PARITY_OBS="$state/obs"
cd "$here/../src"
# A newly available command must not silently turn a missing adapter green.
tags=parity
case "$test" in
TestParityTraceQL | TestParityLinks)
  tags=parity,parity_tempo
  # go mod vendor follows every build tag. Keep this optional adapter outside
  # src until Tempo/links actually exist, so the M1-only package still vendors.
  cp "$here/tempo_test.go.txt" internal/check/parity_tempo_test.go
  ;;
esac
listed=$("${GO:-go}" test -tags "$tags" -list "^$test\$" ./internal/check)
command grep -qx "$test" <<<"$listed" || {
  echo "parity adapter $test is missing for an available obs command" >&2
  exit 1
}
started=$SECONDS
"${GO:-go}" test -tags "$tags" -count=1 -timeout=12m -run "^$test\$" -v ./internal/check 2>&1 | tee "$state/$test.log"
echo "$test: $((SECONDS - started)) s" | tee -a "$state/timings.txt"
