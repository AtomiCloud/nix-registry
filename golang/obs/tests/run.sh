#!/usr/bin/env bash
# shellcheck disable=SC2015 # `cmd && ok || bad`: ok/bad only echo and count, so the C-branch-after-A case cannot happen
# End-to-end test of the obs binary, run inside a nix build (no network, no containers).
#   run.sh --obs <bin> --src <tool source dir> [--out <package output>]
#
# The good fixtures run green; every planted failure (tests/fixtures/bad-*) runs red and the
# output names the file, the rule or panel and the offending token. Each fixture's `expect`
# file lists what must be in the output.
set -euo pipefail
while [ $# -gt 0 ]; do
  case "$1" in
  --obs)
    OBS="$2"
    shift 2
    ;;
  --src)
    SRC="$2"
    shift 2
    ;;
  --out)
    OUT="$2"
    shift 2
    ;;
  *)
    echo "unknown arg $1"
    exit 2
    ;;
  esac
done
here="$(cd "$(dirname "$0")" && pwd)"
fx="$here/fixtures"
work="$(mktemp -d)"
pass=0
fail=0
ok() {
  echo "PASS $*"
  pass=$((pass + 1))
}
bad() {
  echo "FAIL $*"
  fail=$((fail + 1))
}

# The seed's time line must end in the past; a fixed base makes two runs comparable.
base=$(($(date +%s) - 86400))

# run <name> <args...>: run obs, keep the output in $work/out.txt, set rc.
run() {
  set +e
  "$OBS" "$@" >"$work/out.txt" 2>&1
  rc=$?
  set -e
}

# ---- version -------------------------------------------------------------------------
run version
[ "$rc" -eq 0 ] && grep -q '^obs [0-9]' "$work/out.txt" && ok "version" || bad "version: $(cat "$work/out.txt")"
grep -q 'VictoriaMetrics v1.156.0' "$work/out.txt" && grep -q 'VictoriaLogs v1.54.0 (tag commit 6433dae5' "$work/out.txt" &&
  ok "version prints every engine pin" || bad "version engine pins"
run
[ "$rc" -eq 2 ] && ok "no command is a usage error (exit 2)" || bad "no command exit $rc"
run query
[ "$rc" -eq 2 ] && ok "query without --static/--live is a usage error" || bad "query without mode exit $rc"

# ---- good fixtures: green ------------------------------------------------------------
for f in good charlotte; do
  for mode in static live; do
    run query "--$mode" --dir "$fx/$f/observability" --base "$base"
    [ "$rc" -eq 0 ] && grep -q "obs query --$mode: ok:" "$work/out.txt" && ok "$f $mode exit 0" || bad "$f $mode exit $rc: $(cat "$work/out.txt")"
  done
done

# Seed data and results are deterministic: the same seed and base give the same output.
run query --live --dir "$fx/good/observability" --base "$base" --names
cp "$work/out.txt" "$work/live1.txt"
run query --live --dir "$fx/good/observability" --base "$base" --names
cmp -s "$work/live1.txt" "$work/out.txt" && ok "live output is identical on a second run" || bad "live output differs between runs"

# Charlotte has no contract.yaml: the event rule is skipped with one note per log alert.
run query --static --dir "$fx/charlotte/observability"
n=$(grep -c 'event rule skipped: no contract.yaml' "$work/out.txt" || true)
[ "$n" -eq 1 ] && ok "charlotte: one 'event rule skipped' note for its one log alert" || bad "charlotte event notes: $n"
# ... and the rule is switchable: forced on, the same alert (no event filter) fails.
run query --static --dir "$fx/charlotte/observability" --event-rule on
[ "$rc" -eq 1 ] && grep -q '\[logsql-alert-needs-event\]' "$work/out.txt" && ok "--event-rule on fails a log alert with no event filter" || bad "--event-rule on: rc=$rc"
run query --static --dir "$fx/bad-event-missing/observability" --event-rule off
[ "$rc" -eq 0 ] && ok "--event-rule off passes the contract-present fixture" || bad "--event-rule off: rc=$rc"

# The charlotte live run must really read the seed: the crash alert's rows come from the seeded panic line.
run query --live --dir "$fx/charlotte/observability" --base "$base"
grep -q "pod-crashloop.*threshold-removed" "$work/out.txt" && ok "charlotte: the threshold in the metrics alert is run without its comparison" || bad "threshold note missing"

# ---- planted failures: each red, naming file + rule/panel + the bad token ------------
for d in "$fx"/bad-* "$fx"/ok-*; do
  name=$(basename "$d")
  mode=$(sed -n 's/^mode: //p' "$d/expect")
  want_rc=$(sed -n 's/^exit: //p' "$d/expect")
  modes="$mode"
  [ "$mode" = both ] && modes="static live"
  for m in $modes; do
    run query "--$m" --dir "$d/observability" --seed "$fx/shared-seed.yaml" --base "$base"
    good=1
    [ "$rc" -eq "$want_rc" ] || good=0
    while IFS= read -r line; do
      case "$line" in
      "contains: "*) grep -qF -- "${line#contains: }" "$work/out.txt" || {
        good=0
        echo "  missing: ${line#contains: }"
      } ;;
      "absent: "*) ! grep -qF -- "${line#absent: }" "$work/out.txt" || {
        good=0
        echo "  must be absent: ${line#absent: }"
      } ;;
      esac
    done <"$d/expect"
    if [ "$good" -eq 1 ]; then ok "$name ($m) exit $rc and names the file, rule and token"; else
      bad "$name ($m): exit $rc, want $want_rc"
      sed 's/^/  | /' "$work/out.txt"
    fi
  done
done

# A planted failure must also fail the OTHER fixtures' good copy: the baseline stays green
# (checked above), so each red result is caused by the planted file alone.

# ---- the vendored VictoriaLogs tree equals the tag, except the one marked file -------
(cd "$SRC/third_party/VictoriaLogs" && grep -v ' lib/logstorage/obs_fieldnames.go$' ../VictoriaLogs.sha256 | sha256sum -c --quiet) &&
  ok "third_party/VictoriaLogs equals tag v1.54.0 (6433dae5) in every listed file" || bad "third_party tree differs from the tag"
extra=$(cd "$SRC/third_party/VictoriaLogs" && find . -type f | sed 's#^\./##' | sort | comm -13 <(sed 's/^[0-9a-f]*  //' ../VictoriaLogs.sha256 | sort) - | grep -v '^lib/logstorage/obs_fieldnames.go$' || true)
[ -z "$extra" ] && ok "no file in third_party/VictoriaLogs besides the tag's and the marked one" || bad "unexpected files in third_party: $extra"
grep -q 'OBS CHANGE' "$SRC/third_party/VictoriaLogs/lib/logstorage/obs_fieldnames.go" && ok "the one added file is marked as an obs change" || bad "added file not marked"
# Planted drift: a copy of the tree with one byte changed must fail the same check.
cp -r "$SRC/third_party/VictoriaLogs" "$work/tp"
chmod -R u+w "$work/tp"
echo "// tamper" >>"$work/tp/lib/logstorage/parser.go"
(cd "$work/tp" && grep -v ' lib/logstorage/obs_fieldnames.go$' "$SRC/third_party/VictoriaLogs.sha256" | sha256sum -c --quiet >/dev/null 2>&1) &&
  bad "a tampered tree passed the drift check" || ok "a tampered third_party tree fails the drift check"

# ---- licences ship with the binary --------------------------------------------------
if [ -n "${OUT:-}" ]; then
  # buildGoModule installs every main package. Development-only generators must
  # stay out of the shipped package: obs is one executable.
  only_obs() { [ "$(ls -1 "$1/bin")" = obs ] && [ -x "$1/bin/obs" ]; }
  only_obs "$OUT" && ok "single-binary: bin/obs is the only executable" || bad "single-binary: unexpected file in $OUT/bin"
  mkdir -p "$work/binary-shape/bin"
  cp "$OUT/bin/obs" "$work/binary-shape/bin/obs"
  alias_dir="$work/binary-shape"
  only_obs "$alias_dir" && ok "single-binary: copied baseline is green" || bad "single-binary: copied baseline failed"
  touch "$alias_dir/bin/gen"
  only_obs "$alias_dir" && bad "single-binary: planted bin/gen passed" || ok "single-binary: planted bin/gen is refused"
  lic="$OUT/share/licenses/obs"
  [ -s "$lic/LICENSE" ] && grep -q 'GNU AFFERO GENERAL PUBLIC LICENSE' "$lic/LICENSE" && ok "AGPL-3.0 text ships in share/licenses/obs" || bad "AGPL LICENSE missing in output"
  [ -s "$lic/modules.txt" ] && ok "vendor/modules.txt ships" || bad "modules.txt missing"
  [ -s "$lic/github.com/VictoriaMetrics/VictoriaLogs/LICENSE" ] && ok "VictoriaLogs LICENSE ships" || bad "VictoriaLogs LICENSE missing"
fi

# ---- obs check, obs gen, generated code (gen.sh)
# shellcheck disable=SC1091 # path is dynamic; gen.sh is checked on its own
. "$here/gen.sh"

# shellcheck source=/dev/null
. "$here/sandbox.sh"

bash "$here/links.sh" "$OBS" "${OUT:-}"
echo "obs tests: $pass passed, $fail failed"
bash "$here/alerts.sh" "$OBS"
[ "$fail" -eq 0 ]
