#!/usr/bin/env bash
# shellcheck disable=SC2015 # `cmd && ok || bad`: ok/bad only echo and count
# End-to-end test of `obs profile summarize|baseline|compare` (concept section 11.6).
#   profile.sh <obs binary> <fixtures/profile dir>
# Every baseline-green case has a planted red twin; every red names the baseline file, the row
# (function or span) and the values.
set -euo pipefail
OBS="$1"
fx="$2"
work="$(mktemp -d)"
pass=0
fail=0
ok() {
  echo "PASS profile: $*"
  pass=$((pass + 1))
}
bad() {
  echo "FAIL profile: $*"
  fail=$((fail + 1))
}
run() {
  set +e
  "$OBS" "$@" >"$work/out.txt" 2>&1
  rc=$?
  set -e
}
has() { grep -qF -- "$1" "$work/out.txt"; }
# expect <rc> <what> <needle>...: exit code and every needle in the output
expect() {
  local want=$1 what=$2 miss=""
  shift 2
  [ "$rc" -eq "$want" ] || miss="exit $rc, want $want;"
  for n in "$@"; do has "$n" || miss="$miss missing '$n';"; done
  if [ -z "$miss" ]; then ok "$what"; else
    bad "$what: $miss"
    sed 's/^/  | /' "$work/out.txt"
  fi
}
own=(--own '^svc/')

# ---- summarize: three formats ------------------------------------------------------------
run profile summarize --format pprof "${own[@]}" "$fx/base-1.pb.gz"
expect 0 "summarize pprof: functions, spans and OS CPU per request" "svc/render.Page" "40.0%" "GET /browse" "POST /order" "OS CPU per request: 1.000 ms (600 requests)"
run profile summarize --format pprof "${own[@]}" --json "$fx/base-1.pb.gz"
expect 0 "summarize pprof --json" '"cpu_per_request_ns": 1000000' '"name": "GET /browse"'
run profile summarize --format cpuprofile --own '/app/src/' "$fx/base.cpuprofile"
expect 0 "summarize cpuprofile: Bun span__ frame becomes a span, idle is not CPU" "orders_list" "4700 samples" "handleOrders"
run profile summarize --format speedscope --own '^app/' "$fx/base.speedscope.json"
expect 0 "summarize speedscope: Dart UserTag frame becomes a span" "checkout_page" "500 samples" "render"
run profile summarize --format pprof "$fx/base.cpuprofile"
expect 2 "summarize: a .cpuprofile read as pprof is an input error naming the file" "base.cpuprofile" "not a pprof profile"
run profile summarize --format pprof
expect 2 "summarize without a file is a usage error"

# ---- baseline: median of 3, deterministic, small, changed only with --update ------------------
b="$work/baseline"
base_args=(--format pprof --service checkout --scenario browse "${own[@]}" --dir "$b")
run profile baseline "${base_args[@]}" "$fx/base-1.pb.gz" "$fx/base-2.pb.gz" "$fx/base-3.pb.gz"
expect 0 "baseline from 3 runs" "checkout.browse.json" "median of 3 runs" "5 functions, 2 spans"
cp "$b/checkout.browse.json" "$work/first.json"
size=$(wc -c <"$b/checkout.browse.json")
[ "$size" -lt 4096 ] && ok "baseline is a few KB ($size bytes)" || bad "baseline is $size bytes"
run profile baseline "${base_args[@]}" "$fx/base-1.pb.gz" "$fx/base-2.pb.gz" "$fx/base-3.pb.gz"
expect 2 "baseline over an existing file needs --update" "exists" "--update"
run profile baseline "${base_args[@]}" --update "$fx/base-1.pb.gz" "$fx/base-2.pb.gz" "$fx/base-3.pb.gz"
cmp -s "$work/first.json" "$b/checkout.browse.json" && ok "baseline is byte-identical on a second run (--update)" || bad "baseline differs between runs"
run profile baseline "${base_args[@]}" --update "$fx/base-1.pb.gz" "$fx/base-2.pb.gz"
expect 2 "baseline of 2 runs is refused" "exactly 3 runs"
grep -q '"name": "svc/render.Page"' "$b/checkout.browse.json" && ! grep -q 'rare.Thing\|func1\|json.Marshal' "$b/checkout.browse.json" &&
  ok "baseline rows: functions >= 3 % in own code, no closure, no library, no function under 3 %" || bad "baseline rows wrong"
bl="$b/checkout.browse.json"

# ---- compare: green and red ------------------------------------------------------------------
cmpr() { run profile compare --format pprof --baseline "$bl" "$@"; }
cmpr "$fx/now-ok.pb.gz"
expect 0 "compare: a run like the baseline is green; table has what / baseline / now / change (limit)" "change (limit)" "svc/render.Page" "none slower than its limit"
cmpr "$fx/now-slow.pb.gz"
expect 1 "compare: render.Page 50 % slower fails, naming file, function and values" "FAIL $bl function \"svc/render.Page\": share 40.0% -> 50" "above the limit of 7.9%" "SLOWER"
has "FAIL $bl span \"GET /browse\"" && ok "compare: the span of the slow function fails too" || bad "span row did not fail"
has "FAIL $bl OS CPU per request: 1.000 ms -> 1.180 ms" && ok "compare: total CPU per request +18 % fails on a quiet runner" || bad "total row did not fail"
cmpr "$fx/now-noise.pb.gz"
expect 0 "compare: a small row +20 % is inside 3 x sampling noise (limit 27.9 %), green" "svc/cache.Get" "(27.9%)"
cmpr "$fx/now-faster.pb.gz"
expect 0 "compare: a faster row is reported, never failed" "faster" "svc/cache.Get"

# total CPU per request: quiet runner fails, noisy and unknown only report
cmpr "$fx/now-ok.pb.gz" --run "$fx/run-total-slow-quiet.json"
expect 1 "total +20 %, quiet runner: fails" "FAIL $bl OS CPU per request" "quiet runner"
cmpr "$fx/now-ok.pb.gz" --run "$fx/run-total-slow-noisy.json"
expect 0 "total +20 %, noisy runner: reported as slower, not failed" "not enforced: noisy runner" "6.25% of core time lost"
cmpr "$fx/now-ok.pb.gz" --run "$fx/run-total-slow-unknown.json"
expect 0 "total +20 %, no runner signal: reported, not failed" "not enforced: no runner signal"

# limits: .NET 30 %, overrides in config only, each with a why
dn="$work/dotnet"
run profile baseline --format pprof --service checkout --scenario browse "${own[@]}" --profiler dotnet-trace --dir "$dn" "$fx/base-1.pb.gz" "$fx/base-2.pb.gz" "$fx/base-3.pb.gz"
run profile compare --format pprof --baseline "$dn/checkout.browse.json" "$fx/now-slow.pb.gz"
expect 0 "dotnet-trace: render.Page +25 % and total +18 % are inside 30 % (the same run fails the default 7.9 % above)" "(30.0%)" "none slower than its limit"
run profile compare --format pprof --baseline "$dn/checkout.browse.json" "$fx/now-slow-x2.pb.gz"
expect 1 "dotnet-trace: render.Page 2.5x slower still fails, naming the 30 % source" "FAIL $dn/checkout.browse.json function \"svc/render.Page\"" "tolerance from profiled with dotnet-trace"
cat >"$work/limits.yaml" <<'YAML'
overrides:
  - service: checkout
    kind: function
    name: svc/render.Page
    limit: 0.60
    why: the page template is being rewritten, a temporary slowdown is agreed
  - service: checkout
    kind: span
    name: GET /browse
    limit: 0.60
    why: same rewrite, the span contains render.Page
  - service: checkout
    kind: total
    limit: 0.30
    why: same rewrite
YAML
cmpr "$fx/now-slow.pb.gz" --limits "$work/limits.yaml"
expect 0 "an override in config (with a why) lifts the limit: render.Page +25 % is inside 60 %" "(60.0%)" "obs profile compare: ok"
cmpr "$fx/now-slow-x2.pb.gz" --limits "$work/limits.yaml"
expect 1 "an override is not a free pass: total CPU +40 % is over its 30 % override and the failure names the override's why" "FAIL $bl OS CPU per request: 1.000 ms -> 1.400 ms" "tolerance from override: same rewrite"
sed '/why: same rewrite$/d' "$work/limits.yaml" >"$work/limits-nowhy.yaml"
cmpr "$fx/now-slow.pb.gz" --limits "$work/limits-nowhy.yaml"
expect 2 "an override without a why is refused, naming the file" "limits-nowhy.yaml" "must say why"
cmpr "$fx/now-slow.pb.gz" --limits "$work/limits.yaml" --format cpuprofile
expect 2 "compare with another --format than the baseline's is an input error" "made from pprof profiles"

# cpuprofile and speedscope baselines and compares
for pair in "cpuprofile:base.cpuprofile:now-slow.cpuprofile:/app/src/:handleOrders" "speedscope:base.speedscope.json:now-slow.speedscope.json:^app/:render"; do
  IFS=: read -r fmt bf nf pat _ <<<"$pair"
  d="$work/b-$fmt"
  run profile baseline --format "$fmt" --service web --scenario main --own "$pat" --dir "$d" "$fx/$bf" "$fx/$bf" "$fx/$bf"
  expect 0 "baseline $fmt" "median of 3 runs"
  run profile compare --format "$fmt" --baseline "$d/web.main.json" "$fx/$bf"
  expect 0 "compare $fmt: the baseline's own profile is green" "none slower than its limit"
  run profile compare --format "$fmt" --baseline "$d/web.main.json" "$fx/$nf"
  expect 1 "compare $fmt: slower fixture fails, naming the file and a row" "FAIL $d/web.main.json" "SLOWER"
done

echo "profile tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
