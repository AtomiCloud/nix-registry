#!/usr/bin/env bash
set -euo pipefail
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
state=${OBS_PARITY_STATE:?set OBS_PARITY_STATE to a new absolute scratch directory}
command=${1:?up, env, logs or down}
namefile="$state/name"
if [[ $command == up ]]; then
  bash "$here/check-pins.sh"
  mkdir -p "$state"
  [[ ! -e $namefile ]] || {
    echo "parity state already in use: $state" >&2
    exit 1
  }
  # A unique run label prevents cleanup from touching another builder's containers.
  name="obs-parity-$(date +%s)-$$"
  echo "$name" >"$namefile"
else
  name=$(cat "$namefile")
fi
image() { jq -r --arg name "$1" '.[$name] | .image + "@" + .digest' "$here/images.json"; }
owned() { docker ps -aq --filter "label=obs.parity=1" --filter "label=obs.parity.run=$name"; }
case "$command" in
up)
  # Resolve each moving tag before starting anything, and compare it with the
  # recorded digest. Running by digest then prevents a race with a tag re-push.
  for engine in VictoriaMetrics VictoriaLogs Tempo Grafana Pyroscope; do
    tag=$(jq -r --arg engine "$engine" '.[$engine].image' "$here/images.json")
    digest=$(jq -r --arg engine "$engine" '.[$engine].digest' "$here/images.json")
    docker pull "$tag" >/dev/null
    repository=${tag%:*}
    docker image inspect "$tag" --format '{{json .RepoDigests}}' |
      jq -e --arg pinned "$repository@$digest" 'index($pinned) != null' >/dev/null || {
      echo "images.json: $engine tag $tag no longer resolves to pinned digest $digest" >&2
      exit 1
    }
  done
  # Everything, including the network, belongs to this run. Never prune.
  trap 'bash "$here/stack.sh" logs; bash "$here/stack.sh" down' ERR
  docker network create --label obs.parity=1 --label "obs.parity.run=$name" "$name" >/dev/null
  mkdir -p "$state/provisioning/datasources"
  chmod 755 "$state" "$state/provisioning" "$state/provisioning/datasources"
  cat >"$state/provisioning/datasources/parity.yaml" <<'YAML'
apiVersion: 1
datasources:
  - name: Parity metrics
    uid: parity-metrics
    type: prometheus
    url: http://vm:8428
    access: proxy
    jsonData:
      timeInterval: 15s
  - name: Parity logs
    uid: parity-logs
    type: victoriametrics-logs-datasource
    url: http://vl:9428
    access: proxy
YAML
  cat >"$state/tempo.yaml" <<'YAML'
target: all
server:
  http_listen_port: 3200
query_frontend:
  query_end_cutoff: 0s
  search:
    query_backend_after: 1h
  metrics:
    query_backend_after: 1h
live_store:
  wal:
    path: /tmp/tempo/live-store
    ingestion_time_range_slack: 1h
  max_trace_idle: 1s
  max_trace_live: 2s
  max_block_duration: 1h
distributor:
  receivers:
    otlp:
      protocols:
        http:
          endpoint: 0.0.0.0:4318
storage:
  trace:
    backend: local
    local:
      path: /tmp/tempo/blocks
    wal:
      path: /tmp/tempo/wal
      ingestion_time_range_slack: 1h
YAML
  # Binding to loopback with dynamic ports avoids collisions on a shared box.
  docker run -d --name "$name-vm" --network "$name" --network-alias vm \
    --label obs.parity=1 --label "obs.parity.run=$name" -p 127.0.0.1::8428 \
    "$(image VictoriaMetrics)" -retentionPeriod=10y -search.latencyOffset=0s -search.disableCache -memory.allowedBytes=256MiB >/dev/null
  docker run -d --name "$name-vl" --network "$name" --network-alias vl \
    --label obs.parity=1 --label "obs.parity.run=$name" -p 127.0.0.1::9428 \
    "$(image VictoriaLogs)" -retentionPeriod=10y -memory.allowedBytes=256MiB \
    -search.maxQueryDuration=30s -search.maxQueryLen=16KiB -search.maxQueryTimeRange=7d >/dev/null
  docker run -d --name "$name-tempo" --network "$name" --network-alias tempo \
    --label obs.parity=1 --label "obs.parity.run=$name" -p 127.0.0.1::3200 -p 127.0.0.1::4318 \
    -v "$state/tempo.yaml:/etc/tempo.yaml:ro" "$(image Tempo)" -config.file=/etc/tempo.yaml >/dev/null
  docker run -d --name "$name-pyroscope" --network "$name" --network-alias pyroscope \
    --label obs.parity=1 --label "obs.parity.run=$name" -p 127.0.0.1::4040 \
    "$(image Pyroscope)" -target=all -server.http-listen-port=4040 >/dev/null
  docker run -d --name "$name-grafana" --network "$name" --network-alias grafana \
    --label obs.parity=1 --label "obs.parity.run=$name" -p 127.0.0.1::3000 \
    -e GF_AUTH_ANONYMOUS_ENABLED=true -e GF_AUTH_ANONYMOUS_ORG_ROLE=Admin -e GF_AUTH_DISABLE_LOGIN_FORM=true \
    -e GF_ANALYTICS_REPORTING_ENABLED=false -e GF_ANALYTICS_CHECK_FOR_UPDATES=false \
    -e "GF_PLUGINS_PREINSTALL_SYNC=$(jq -r '.VictoriaLogsDatasource.plugin' "$here/images.json")" \
    -e GF_UNIFIED_ALERTING_STATE_HISTORY_ENABLED=true -e GF_UNIFIED_ALERTING_STATE_HISTORY_BACKEND=annotations \
    -v "$state/provisioning:/etc/grafana/provisioning:ro" "$(image Grafana)" >/dev/null
  bash "$here/stack.sh" env >"$state/env"
  # shellcheck source=/dev/null
  source "$state/env"
  for endpoint in "$OBS_PARITY_VM/health" "$OBS_PARITY_VL/health" "$OBS_PARITY_TEMPO/ready" "$OBS_PARITY_PYROSCOPE/ready" "$OBS_PARITY_GRAFANA/api/health"; do
    ready=false
    for ((i = 0; i < 120; i++)); do
      if curl --silent --fail --max-time 2 "$endpoint" >/dev/null; then
        ready=true
        break
      fi
      sleep 1
    done
    "$ready" || {
      echo "Not ready: $endpoint" >&2
      exit 1
    }
  done
  curl --fail --silent "$OBS_PARITY_GRAFANA/api/plugins/victoriametrics-logs-datasource/settings" >"$state/plugin.json"
  jq -e --arg v "$(jq -r '.VictoriaLogsDatasource.version | ltrimstr("v")' "$here/images.json")" '.info.version == $v' "$state/plugin.json" >/dev/null
  curl --fail --silent "$OBS_PARITY_GRAFANA/api/health" >"$state/grafana-health.json"
  jq -e --arg version "$(jq -r '.Grafana.version' "$here/images.json")" \
    --arg commit "$(jq -r '.Grafana.imageBuildCommit' "$here/images.json")" \
    '.version == $version and .commit == $commit' "$state/grafana-health.json" >/dev/null
  # Record the real image digests and effective VL flags, not just declared tags.
  docker inspect "$(owned | head -1)" >/dev/null
  mapfile -t containers < <(owned)
  docker inspect "${containers[@]}" >"$state/containers.json"
  curl --fail --silent "$OBS_PARITY_VL/flags" >"$state/victorialogs-flags.txt"
  echo "Parity stores ready. Environment: $state/env"
  ;;
env)
  port() { docker port "$name-$1" "$2/tcp" | head -1; }
  printf 'export OBS_PARITY_VM=http://%s\n' "$(port vm 8428)"
  printf 'export OBS_PARITY_VL=http://%s\n' "$(port vl 9428)"
  printf 'export OBS_PARITY_TEMPO=http://%s\n' "$(port tempo 3200)"
  printf 'export OBS_PARITY_OTLP=http://%s\n' "$(port tempo 4318)"
  printf 'export OBS_PARITY_PYROSCOPE=http://%s\n' "$(port pyroscope 4040)"
  printf 'export OBS_PARITY_GRAFANA=http://%s\n' "$(port grafana 3000)"
  ;;
logs)
  for container in $(owned); do docker logs "$container" >"$state/$container.log" 2>&1 || true; done
  ;;
down)
  mapfile -t containers < <(owned)
  if ((${#containers[@]})); then docker rm -f "${containers[@]}" >/dev/null; fi
  if [[ $(docker network inspect --format '{{index .Labels "obs.parity.run"}}' "$name" 2>/dev/null || true) == "$name" ]]; then
    docker network rm "$name" >/dev/null
  fi
  ;;
*)
  echo "unknown stack command $command" >&2
  exit 2
  ;;
esac
