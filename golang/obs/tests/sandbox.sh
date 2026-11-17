#!/usr/bin/env bash
# shellcheck disable=SC2015,SC2154 # sourced by run.sh: ok/bad/run/work/fx/OBS/rc come from there
# `obs sandbox`, `obs look` and `obs scrape` with no container runtime and no network.
# A stub `docker` records every call and lists a stack plus resources that are NOT ours; the
# real stack (five containers) is a manual run, recorded in the pull request.

stub="$work/sandbox-stub"
mkdir -p "$stub"
cat >"$stub/docker" <<'STUB'
#!/bin/sh
echo "$*" >>"$STUB_LOG"
case "$1" in
ps) printf '%s\n' obs-sandbox-demo-vm obs-sandbox-demo-grafana reels-worker ;;
network) [ "$2" = ls ] && printf '%s\n' obs-sandbox-demo other-net ;;
volume) [ "$2" = ls ] && printf '%s\n' obs-sandbox-demo-vm team-volume ;;
esac
exit 0
STUB
chmod +x "$stub/docker"
export STUB_LOG="$work/sandbox-stub.log"

# ---- down: only this sandbox's labelled, prefixed resources go; never a prune --------
: >"$STUB_LOG"
set +e
PATH="$stub:$PATH" OBS_CONTAINER_RUNTIME=docker "$OBS" sandbox down --project demo >"$work/out.txt" 2>&1
rc=$?
set -e
[ "$rc" -eq 0 ] && ok "sandbox down exit 0 (stub runtime)" || bad "sandbox down rc=$rc: $(cat "$work/out.txt")"
for want in "rm -f -v obs-sandbox-demo-vm" "rm -f -v obs-sandbox-demo-grafana" "network rm obs-sandbox-demo" "volume rm obs-sandbox-demo-vm"; do
  grep -qxF -- "$want" "$STUB_LOG" && ok "down ran: $want" || bad "down did not run: $want"
done
# Planted: the runtime also lists a container, a network and a volume that are not ours.
! grep -qE 'reels-worker|other-net|team-volume' <(grep -E '^(rm|network rm|volume rm) ' "$STUB_LOG") &&
  ok "down removed nothing it does not own (reels-worker, other-net, team-volume stay)" || bad "down touched a foreign resource: $(cat "$STUB_LOG")"
! grep -q prune "$STUB_LOG" && ok "down never prunes" || bad "a prune ran"
[ "$(grep -cE '^(ps|network ls|volume ls) ' "$STUB_LOG")" -eq 3 ] && ! grep -E '^(ps|network ls|volume ls) ' "$STUB_LOG" | grep -qv 'label=obs.sandbox=demo' &&
  ok "every listing is filtered by label obs.sandbox=demo" || bad "a listing without the label filter"
grep -q 'skipping container reels-worker' "$work/out.txt" && ok "down names what it skipped" || bad "skip note missing"

# ---- up refuses a container with another image tag, before it starts anything --------
mkdir -p "$stub/old"
cat >"$stub/old/docker" <<'STUB'
#!/bin/sh
echo "$*" >>"$STUB_LOG"
[ "$1" = ps ] && echo 'obs-sandbox-demo-vm|victoriametrics/victoria-metrics:v1.111.0|running'
exit 0
STUB
chmod +x "$stub/old/docker"
: >"$STUB_LOG"
set +e
PATH="$stub/old:$PATH" OBS_CONTAINER_RUNTIME=docker "$OBS" sandbox up --dir "$fx/good/observability" --project demo >"$work/out.txt" 2>&1
rc=$?
set -e
[ "$rc" -eq 1 ] && grep -q 'refusing: image tags differ from the pins' "$work/out.txt" && grep -q 'v1.111.0' "$work/out.txt" && grep -q 'v1.156.0' "$work/out.txt" &&
  ok "up refuses a container on another tag and names both tags" || bad "up refusal rc=$rc: $(cat "$work/out.txt")"
[ "$(wc -l <"$STUB_LOG")" -eq 1 ] && ok "up changed nothing before refusing (only the listing ran)" || bad "up ran more than the listing: $(cat "$STUB_LOG")"

# ---- look: a setup failure is exit 0 with a clear message ------------------------------
set +e
OBS_CONTAINER_RUNTIME=nerdctl "$OBS" look --dir "$fx/good/observability" --checklist "$fx/look/checklist.md" --file dashboards/overview.json >"$work/out.txt" 2>&1
rc=$?
set -e
[ "$rc" -eq 0 ] && grep -q 'setup failed, nothing to review' "$work/out.txt" && grep -q 'container runtime "nerdctl"' "$work/out.txt" &&
  ok "look: sandbox setup failure exits 0 and says what failed" || bad "look failure rc=$rc: $(cat "$work/out.txt")"
set +e
OBS_CONTAINER_RUNTIME=nerdctl "$OBS" look --json --dir "$fx/good/observability" --checklist "$fx/look/checklist.md" --file dashboards/overview.json >"$work/out.txt" 2>&1
rc=$?
set -e
[ "$rc" -eq 0 ] && grep -q '"ok": false' "$work/out.txt" && grep -q '"prompt":' "$work/out.txt" && ok "look --json: same failure, as JSON, exit 0" || bad "look --json rc=$rc"
run look --nope
[ "$rc" -eq 0 ] && grep -q 'usage error' "$work/out.txt" && grep -q 'flag provided but not defined: -nope' "$work/out.txt" &&
  ok "look: a bad flag exits 0 and prints the usage error" || bad "look bad flag rc=$rc: $(cat "$work/out.txt")"
run look --help
[ "$rc" -eq 0 ] && grep -q -- '-checklist' "$work/out.txt" && ok "look --help exits 0 and prints the usage" || bad "look --help rc=$rc"
run look --json --nope
[ "$rc" -eq 0 ] && grep -q '"ok": false' "$work/out.txt" && grep -q 'flag provided but not defined' "$work/out.txt" &&
  ok "look --json with a bad flag: ok:false JSON, exit 0" || bad "look --json bad flag rc=$rc"

# ---- scrape: usage ------------------------------------------------------------------
run scrape
[ "$rc" -eq 2 ] && ok "scrape without a target is a usage error" || bad "scrape no target rc=$rc"
run scrape --target http://127.0.0.1:1/m --label nope
[ "$rc" -eq 2 ] && grep -q 'name=value' "$work/out.txt" && ok "scrape rejects a label without =" || bad "scrape bad label rc=$rc"
run scrape --once --target http://127.0.0.1:1/m --vm-url http://127.0.0.1:1
[ "$rc" -eq 1 ] && grep -q 'scrape http://127.0.0.1:1/m' "$work/out.txt" && ok "scrape --once on an unreachable target exits 1 and names it" || bad "scrape unreachable rc=$rc: $(cat "$work/out.txt")"

# ---- version prints the sandbox pins ---------------------------------------------------
run version
for pin in grafana/grafana:13.3.0 victoriametrics/victoria-metrics:v1.156.0 victoriametrics/victoria-logs:v1.54.0 grafana/tempo:3.1.1 grafana/pyroscope:2.4.3; do
  grep -qF -- "sandbox image $pin" "$work/out.txt" && ok "version prints sandbox image $pin" || bad "version lacks sandbox image $pin"
done
grep -qF -- "sandbox Grafana plugin victoriametrics-logs-datasource v0.34.0" "$work/out.txt" && ok "version prints the datasource plugin pin" || bad "version lacks the plugin pin"
