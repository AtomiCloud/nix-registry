#!/usr/bin/env bash
# obs live: the read-only commands against a running store. This script needs no network and
# no container: it checks what happens before and instead of a store answering (usage errors,
# targets: in obs.yaml, headers read from the environment, an unreachable target). The store
# answers themselves are checked by the Go tests (internal/live) against fake stores that
# record every request, and by one run against the local sandbox (see the README).
set -euo pipefail
OBS=$1
here="$(cd "$(dirname "$0")" && pwd)"
fx="$here/fixtures"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
run() {
  set +e
  "$OBS" "$@" >"$work/out" 2>&1
  rc=$?
  set -e
}
expect() {
  local want=$1 what=$2
  shift 2
  local miss=""
  [ "$rc" -eq "$want" ] || miss="exit $rc, want $want;"
  for token in "$@"; do grep -qF -- "$token" "$work/out" || miss="$miss missing '$token';"; done
  if [ -n "$miss" ]; then
    sed 's/^/  | /' "$work/out"
    echo "FAIL live: $what: $miss"
    exit 1
  fi
  echo "PASS live: $what"
}
# fresh <name>: a writable copy of the good fixture; extra obs.yaml text is appended.
fresh() {
  rm -rf "${work:?}/$1"
  mkdir -p "$work/$1"
  cp -R "$fx/good/observability" "$work/$1/observability"
  chmod -R u+w "$work/$1"
  printf '%s\n' "${2:-}" >>"$work/$1/observability/obs.yaml"
}

run live
expect 2 "no command prints the usage and exits 2" "obs live usage" "only reads"
run live frobnicate
expect 2 "an unknown command names it" 'unknown command "frobnicate"'
run live help
expect 0 "help exits 0" "obs live look"

g="$fx/good/observability"
run live usage --dir "$g"
expect 2 "usage without --target is an input error" "give --target"
run live usage --dir "$g" --target prod
expect 2 "a target that is not written anywhere names where to write it" "no targets: block in obs.yaml"
run live usage --dir "$g" --target local --window banana
expect 2 "a bad --window names the value" "--window" "banana"
run live usage --dir "$g" --target local --unknown maybe
expect 2 "a bad --unknown names the value" '--unknown "maybe"'
run live shape --dir "$g" --target local --window 1h --step 30s
expect 2 "a step under 1m is refused" "--step"
run live incidents --dir "$g" --target local --window "2026-11-10"
expect 2 "a window without .. names the input" "--window" "2026-11-10"

# targets: in obs.yaml
t='targets:
  prod:
    metrics: https://vm.prod.example
    grafana: https://grafana.prod.example
    headers:
      Authorization: OBS_LIVE_TEST_TOKEN
  dead:
    metrics: http://127.0.0.1:1
    logs: http://127.0.0.1:1
    traces: http://127.0.0.1:1
    grafana: http://127.0.0.1:1'
fresh targets "$t"
d="$work/targets/observability"
run check --dir "$d"
expect 0 "a file with targets: still loads for obs check" "obs check: ok"
run query --static --dir "$d"
expect 0 "a file with targets: still loads for obs query" "versions match"
unset OBS_LIVE_TEST_TOKEN
run live usage --dir "$d" --target prod
expect 2 "an unset header variable names file, line and header, never the variable name" "obs.yaml:" "targets.prod.headers.Authorization" "not set"
if grep -qF OBS_LIVE_TEST_TOKEN "$work/out"; then
  echo "FAIL live: the unset-variable error printed the supplied value"
  exit 1
fi
run live usage --dir "$d" --target stage
expect 2 "an unknown target lists the known ones" '"stage"' "dead, prod"
run live usage --dir "$d" --target dead
expect 2 "an unreachable store is an input error that names the address" "127.0.0.1:1"
run live shape --dir "$d" --target dead --window 1h
expect 2 "shape on an unreachable store is an input error" "127.0.0.1:1"
run live incidents --dir "$d" --target dead
expect 2 "incidents on an unreachable store is an input error" "127.0.0.1:1"
run live look --dir "$d" --target dead
expect 0 "look never fails, even when Grafana is down" "exits 0 on purpose" "127.0.0.1:1"
run live look --dir "$d" --target prod
expect 0 "look never fails on an unset header variable" "not set"
run live look --target
expect 0 "look never fails on a usage error" "usage error"

# a secret written in the file is refused, naming the key
fresh secret 'targets:
  prod:
    metrics: https://vm.prod.example
    headers:
      Authorization: Bearer abc.def.ghi'
run live usage --dir "$work/secret/observability" --target prod
expect 2 "a token written in obs.yaml is refused" "targets.prod.headers.Authorization" "environment variable"
if grep -qF 'abc.def.ghi' "$work/out"; then
  echo "FAIL live: the refusal printed the secret"
  exit 1
fi
echo "PASS live: the refusal does not print the value"

# a token that is shaped like an environment variable name is refused and never printed (the token is built
# here so that no literal in this file looks like a credential)
tok="gl""sa_$(printf 'Ab1%.0s' 1 2 3 4 5 6 7 8 9 10)"
fresh tokenname "targets:
  prod:
    metrics: https://vm.prod.example
    headers:
      Authorization: $tok"
run live usage --dir "$work/tokenname/observability" --target prod
expect 2 "a token shaped like a variable name is refused, naming the key" "targets.prod.headers.Authorization" "access token"
if grep -qF "$tok" "$work/out"; then
  echo "FAIL live: the refusal printed the token"
  exit 1
fi
echo "PASS live: a token shaped like a variable name is not printed"

# a store URL with credentials, a query string or a fragment is refused, naming the key, not the URL
for bad in "https://admin:hunter2secret@vm.prod.example" "https://vm.prod.example/?token=hunter2secret" "https://vm.prod.example/#hunter2secret"; do
  fresh badurl "targets:
  prod:
    metrics: $bad"
  run live usage --dir "$work/badurl/observability" --target prod
  expect 2 "a store URL with credentials, a query or a fragment is refused" "targets.prod.metrics"
  if grep -qF 'hunter2secret' "$work/out"; then
    echo "FAIL live: the refusal printed the URL"
    exit 1
  fi
done
echo "PASS live: a refused URL is not printed"

# an unknown key under a target names its dotted path
fresh badkey 'targets:
  prod:
    metricz: https://vm.prod.example'
run live usage --dir "$work/badkey/observability" --target prod
expect 2 "an unknown targets key names its path" "targets.prod.metricz"

# the old commands keep their stdout; the note goes to stderr only
set +e
"$OBS" assert --dir "$fx/assert/observability" --series "$fx/assert/series/series-ok.json" >"$work/o1" 2>"$work/e1"
set -e
if grep -q 'obs live usage' "$work/e1"; then
  echo "PASS live: obs assert names obs live usage on stderr"
else
  echo "FAIL live: no note on stderr"
  exit 1
fi
if grep -q 'obs live' "$work/o1"; then
  echo "FAIL live: obs assert printed the note on stdout"
  exit 1
fi
echo "PASS live: obs assert prints nothing new on stdout"
echo "obs live: all checks passed"
