# obs: observability checks with no running stack

`obs` checks a repo's alerts and dashboards without a running Grafana, VictoriaMetrics
or VictoriaLogs. It embeds the engines' own parsers and storage in one static Go binary:
no network, no containers.

**Licence: AGPL-3.0-only** (`LICENSE`). Each registry release attaches the Corresponding Source
(this tool's source plus every vendored Go module) as `obs-<version>-source.tar.gz`; `obs version`
says where. Everything in the binary is AGPL-3.0, Apache-2.0, MIT, BSD or MPL-2.0; the notice files of every
linked module ship in `share/licenses/obs`. Design: `concepts/observability.md` §11 and
`design/registry-tools/obs.md` in the diene corpus (the concept wins where this file and it differ).

## Release 0.1.0: what is in it

| Command              | What it does                                                                                                                                                                   |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `obs query --static` | Parses every MetricsQL and LogsQL query in the alerts and dashboards with the real parsers and applies the rules below.                                                        |
| `obs query --live`   | Makes the seed, loads it into the embedded VictoriaMetrics and VictoriaLogs, runs every alert and dashboard query on it. Each must return rows. Also applies the static rules. |
| `obs version`        | The obs version and every engine pin.                                                                                                                                          |

Not in 0.1.0 (they ship in later releases, see the corpus goal): `obs gen`, `obs check`
(the contract), `obs alerts` (evaluating alerts over incidents), `obs links`, `obs assert`,
`obs profile`, TraceQL (trace queries are skipped with one note), `obs engines --check-chart`,
`obs sandbox`, `obs look`, `obs scrape`, `obs store move`.

```text
 1. decide signals        what the service must say: metrics, log events, spans
        │
        ▼
 2. contract file         observability/contract.yaml, written by hand
        │                 (concept §3: bounded label types, budget per metric,
        │                  metric kinds, log events = name + level)
        ▼
 3. obs gen               types and constants only, per language (concept §3.4),
        │                 plus contract/events.json and the aggregation file (§4.1)
        ▼
 4. code                  calls the one hand-written call surface in *-interfaces,
        │                 which takes only generated types; per-language lint rules
        │                 close the holes the compiler cannot (concept §3.4)
        ▼
 5. dashboards + alerts   observability/dashboards/**, observability/alerts/**
        │                 (MetricsQL, hand-written LogsQL, TraceQL)
        ▼
 6. PR CI                 obs gen --check, obs check, obs query --static,
        │                 obs query --live, obs alerts, obs links   (no containers)
        ▼
 7. e2e                   obs assert, obs profile compare
```

This release ships step 6's `obs query --static` and `obs query --live`.

## Usage

```text
obs query --static [--dir observability] [--names] [--event-rule auto|on|off]
obs query --live   [--dir observability] [--seed seed.yaml] [--base unix] [--names]
obs sandbox up|down|status [--dir observability] [--project name] [--port-base n]
obs look   [--base origin/main] [--all] [--json]
obs scrape --target <url> [--label name=value] [--interval 15s] [--once]
obs version
```

Exit 0 when clean, 1 when a check failed, 2 on a usage or input error. Every failure line names
the file, the rule or panel, the rule id in `[brackets]` and the offending name or value.
`warn` lines never fail; `note` lines are information.

## Inputs

`--dir` (default `observability`) holds:

- **`alerts/<slug>/<severity>.yaml`**: one `GrafanaAlertRuleGroup` CR per file (the golden
  shape in the Primordial observability standard, `alerts.md`). obs reads each rule's `data[]`:
  `datasourceUid`, `relativeTimeRange.from`, `model.expr`, `model.queryType`, and
  `model.datasource.type` when present. Grafana expression nodes (`datasourceUid: __expr__`)
  are not queries and are skipped. A flat authoring file (`title/datasource/expr`) is refused
  with a pointer to the CR. More than one YAML document in a file is refused.
- **`dashboards/**/\*.json`**: dashboard JSON. Panels (also inside collapsed rows), `targets[].expr`,
`templating.list`, `time.from/to` are read. Annotation queries are not checked in 0.1.0.
- **`seed.yaml`** (`--live` only; `--seed` overrides the path): see below.
- **`contract.yaml`**: only its presence matters in 0.1.0 (the event rule, below). Its schema is
  read from obs 0.2.

**Which datasource speaks what.** The plugin type decides (`prometheus`,
`victoriametrics-metrics-datasource` = MetricsQL; `victoriametrics-logs-datasource` = LogsQL;
`loki` = refused; `tempo` = skipped). Without a type the datasource uid decides by name:
`loki` anywhere in it is Loki, `logs` is LogsQL, `prom`, `mimir`, `metrics` or `victoriametrics`
is MetricsQL. A uid that is none of these fails and says to set `model.datasource.type`.
A panel datasource chosen by a variable (`${ds}`) takes the type from that datasource variable.

## `obs query --static`: the rules

| Rule id                          | Kind | What                                                                                                                                                                                                                                           |
| -------------------------------- | ---- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `metricsql-syntax`               | fail | The query does not parse with VictoriaMetrics' `metricsql` (v0.87.6, from VictoriaMetrics v1.156.0).                                                                                                                                           |
| `metricsql-unsupported-function` | fail | A function VictoriaMetrics does not implement (Prometheus-only, e.g. `double_exponential_smoothing`).                                                                                                                                          |
| `rollup-no-window`               | warn | A rollup function on a selector with no `[window]` (the window then comes from the query step).                                                                                                                                                |
| `logsql-syntax`                  | fail | The query does not parse with VictoriaLogs v1.54.0 `lib/logstorage`.                                                                                                                                                                           |
| `logsql-alert-needs-stats`       | fail | A LogsQL **alert** query has no `stats` pipe. This is VictoriaLogs' own rule for `/select/logsql/stats_query`, which the Grafana datasource calls: the last `stats` pipe must exist, and a trailing `\| filter` threshold after it is allowed. |
| `logsql-namespace-wide`          | warn | A `stats` query with no field or stream filter besides `_time` (concept §6.4).                                                                                                                                                                 |
| `loki-datasource`                | fail | A dashboard or alert query on a Loki-type datasource: logs are LogsQL.                                                                                                                                                                         |
| `datasource`                     | fail | The datasource is not one obs knows.                                                                                                                                                                                                           |
| `logsql-alert-needs-event`       | fail | A LogsQL alert has no filter on `"durian.cloud/event"`. Runs only when `contract.yaml` exists (below).                                                                                                                                         |
| `file`                           | fail | A file that is not what it claims: not YAML/JSON, not a `GrafanaAlertRuleGroup`, several documents.                                                                                                                                            |

Dashboard variables are filled before parsing, as Grafana's Prometheus backend does:
`$__interval`, `$__interval_ms`, `$__rate_interval`, `$__rate_interval_ms`, `$__range`,
`$__range_s`, `$__range_ms`, the `${...}` and `[[...]]` spellings, and a template variable's
current value (else a placeholder). `--names` prints, for every query, the metrics, labels and
log fields it reads (filters, stream tags `_stream:<tag>`, and the fields the pipes need).

**The event rule (decided 16 Nov 2026).** The concept (§11.1) says a log alert must name a contract
event. Contract events arrive with obs 0.2. So: when `<dir>/contract.yaml` is absent, a log alert
gets parse, `stats` and live-rows checks only, and obs prints one note per log alert, `event
rule skipped: no contract.yaml`. When it is present, the alert must filter on the event field
(`logsql-alert-needs-event`); that the event name is in the contract is checked from obs 0.2.
`--event-rule on|off` forces it either way (`auto` is the default).

## `obs query --live`

1. Reads the seed, makes baseline data on a time line that ends in the past (stores drop samples
   dated ahead; `--base unix` pins the start for a repeatable run, and must end in the past).
2. Loads it into in-process VictoriaMetrics v1.156.0 (storage and evaluator) and VictoriaLogs v1.54.0
   (`lib/logstorage`), in a temp dir (`OBS_TMPDIR` overrides). No socket is opened.
3. Runs every query:
   - **Alert, metrics:** an instant query at the end of the seed (VictoriaMetrics' default 5 minute
     step for a rollup with no window, as Grafana alerting sends it). A threshold written into the
     expression (`rate(x[5m]) > 10`) is run without its comparison, with a note: a comparison
     filters rows out on a quiet seed even when every name is right.
   - **Alert, LogsQL:** as the Grafana VictoriaLogs datasource v0.34.0 does (FROM SOURCE: datasource
     `pkg/plugin/query.go:179-205`, VictoriaLogs `app/vlselect/logsql/logsql.go:1509-1570`): `start`
     and `end` from the rule's `relativeTimeRange`, the query parsed at `end`, `_time:[start, end)`
     added, the last `stats` pipe's `by (...)` fields as labels. A trailing `| filter` threshold is
     dropped, as above. A `stats count()` with no `by` always answers one row, 0 when nothing matched;
     that counts as no rows.
   - **Dashboard panel:** a range query (metrics) or a stats or log query (LogsQL) over the dashboard's
     `time` range, ending at the seed's end, once with every template variable at all its values and
     once per single value (up to 8 per variable). `label_values(metric, label)` values come from the
     seed; `custom`, `interval` and `textbox` variables from their definition.
4. A query with no rows fails with `[no-rows]` and, when it can tell, the names it reads that the
   seed does not hold: `not in the seed: metric x_total, label y, log field severity`.

### The seed

```yaml
length: 4h # how much time to make
random_seed: 42 # the same file gives the same data
resource: # resource attributes, dots kept; become metric labels (dots -> _) and log fields
  durian.cloud.landscape: pichu
  durian.cloud.platform: payments
  durian.cloud.service: checkout
  durian.cloud.module: api
stream_fields: [cluster] # optional: resource attributes that are also log stream fields here
traffic: { requests: 3/s, shape: daily_wave } # shape: flat | daily_wave
metrics:
  checkout.charges: # counter -> checkout_charges_total
    type: counter
    series:
      - { where: { payment.provider: stripe, outcome: error }, share: 0.01 }
  http.server.request.duration: # seconds histogram -> ..._seconds_bucket / _sum / _count with le
    type: histogram
    unit: s
    buckets: [0.025, 0.05, 0.1, 0.25, 0.5, 1]
    latency: { median: 0.08, sigma: 0.5 }
    series: [{ where: { http.route: /charge }, share: 1 }]
logs: # which log event each metric event writes
  - {
      event: checkout.charge.failed,
      level: error,
      line: charge failed,
      from: { metric: checkout.charges, where: { outcome: error } },
    }
```

`incidents:` and `traces:` are accepted and ignored until the release that uses them.

Seed data is made to look like stored data (concept §11.2). Metrics: counters get `_total`, seconds
histograms `_seconds_bucket`/`_sum`/`_count` with `le`, dots become `_`. Logs, as VictoriaLogs stores an OTLP
log record (FROM SOURCE, v1.54.0; **not yet compared with a real VictoriaLogs through Alloy**): attribute
keys unchanged (`durian.cloud/event`, `payment.provider`), the body as `_msg`, `severity_text` (upper case,
e.g. `ERROR`) and `severity_number`, `trace_id`, `span_id`. Stream fields are the bounded where-from set
(`service.name`, `service.namespace`, `deployment.environment.name`, `k8s.namespace.name` and the four
`durian.cloud.*` keys, when set), plus `stream_fields`. `service.name`, `service.namespace` and
`deployment.environment.name` are filled from the `durian.cloud.*` keys, as Alloy does.

## `obs sandbox`: the local stack, for people

```text
obs sandbox up     [--dir observability] [--seed seed.yaml] [--project name] [--port-base n] [--runtime docker|podman] [--reseed]
obs sandbox down   [--dir observability] [--project name] [--runtime docker|podman]
obs sandbox status [--dir observability] [--project name]
```

`up` starts five containers (docker, or podman when docker is not there; `--runtime` or
`$OBS_CONTAINER_RUNTIME` chooses) and loads them: the repo's dashboards into Grafana, the seed into
VictoriaMetrics and VictoriaLogs. No check depends on it; it is for looking at the data.

| Container    | Image                                      | Host port (default)   |
| ------------ | ------------------------------------------ | --------------------- |
| Grafana      | `grafana/grafana:13.3.0` + VictoriaLogs datasource 0.34.0 | 3000     |
| VictoriaMetrics | `victoriametrics/victoria-metrics:v1.156.0` | 8428               |
| VictoriaLogs | `victoriametrics/victoria-logs:v1.54.0`    | 9428                  |
| Tempo        | `grafana/tempo:3.1.1`                      | 3200                  |
| Pyroscope    | `grafana/pyroscope:2.4.3`                  | 4040                  |

- **Pins.** The tags are the concept §1 versions, written into the binary (`obs version` prints them);
  nothing reads them from the environment. `up` refuses, before it changes anything, when a container of
  the same sandbox runs another tag; after the start it asks VictoriaMetrics, VictoriaLogs, Tempo, Grafana
  and the datasource plugin which version they run and refuses on any difference. Pyroscope has no version
  call obs relies on: its image tag is the pin.
- **Safe removal.** Every container, volume and network carries the label `obs.sandbox=<project>` and a name
  that starts with `obs-sandbox-<project>`. `down` lists by label, double-checks the name, removes only
  those, and says what it skipped. obs never runs a prune: other teams' images and containers live on the
  same machine. `--project` defaults to the repo folder's name. Every VOLUME an image declares is mounted
  from a labelled named volume (Pyroscope 2.4.3 declares three: `/data`, `/data-compactor`,
  `/data-metastore`; the list is in `pins.go` next to the pins, and `up` refuses an image that declares one
  the list does not know), and `down` removes containers with `rm -f -v`: no anonymous volume is left behind.
- **Single-instance settings** (concept §10.4): VictoriaMetrics `-retentionPeriod=30d`,
  `-maxLabelsPerTimeseries=30`, `-search.maxConcurrentRequests=4`, `-memory.allowedPercent=60`;
  VictoriaLogs `-retentionPeriod=30d`, `-retention.maxDiskSpaceUsageBytes=17179869184` (the 16 GiB laptop cap),
  `-search.maxConcurrentRequests=4`, `-search.maxQueueDuration=10s`, `-search.maxQueryDuration=30s`,
  `-memory.allowedPercent=60`; Tempo `distributor.max_attribute_bytes: 16384` and
  `live_store.max_block_duration: 5m`. Left out on purpose: the cluster-only and per-pod settings (vmstorage
  `-storage.maxDailySeries`/`-maxHourlySeries` have no value in §10.4, Pyroscope's retention and raft names
  are for the cluster, S3 backends and resource requests do not exist on a laptop).
- **Ports** are bound to `127.0.0.1` only. `--port-base 18300` moves Grafana to 18300 and the others to
  18301 to 18304 when the defaults are taken.
- **Data.** The seed's time line ends now, so a "last hours" view has data. Grafana is open to an anonymous
  viewer (admin/admin for the API). The datasource uids are the ones the dashboards name (`primordial-metrics`
  and so on); a kind no dashboard names gets `obs-metrics`, `obs-logs`, `obs-traces`, `obs-profiles`.
  A second `up` reuses the running stack, loads the dashboards again and keeps the seed (`--reseed` loads it
  again). Traces are not seeded yet: the seed file's `traces` are read by a later command.
- `up` needs the network the first time (image pulls; Grafana downloads the datasource plugin).

## `obs look`: dashboard screenshots, reviewed by the calling agent

```text
obs look [--base origin/main] [--all] [--file dashboards/x.json] [--checklist docs/standards/grafana-dashboards/index.md]
         [--out obs-look] [--width 1600] [--height 1000] [--json] [sandbox flags]
```

`obs look` is the setup and a prompt; it calls no LLM, reads no key, spends nothing and posts nothing
(owner ruling, 17 Nov 2026). It

1. finds the changed dashboards: `observability/dashboards/*.json` that differ from the merge base with
   `--base` (committed, edited or new), or all of them (`--all`), or the ones named with `--file`;
2. brings up the sandbox, or reuses the running one (it stays up for the browser; the prompt prints the exact
   `obs sandbox down --project <project> --runtime <runtime>` that ends it, with the values this run used);
3. prints, for each dashboard, the Grafana URL (time range fixed to the seed window, template variables
   filled: a datasource variable is resolved against the sandbox's datasources first (a saved uid is kept only
   when the sandbox has it); the others take the dashboard's own current value, the first option, the first
   value on the seed, or All), the viewport and the path to save the screenshot, plus one URL per panel.
   Panels whose datasources are all Tempo or Pyroscope are listed as "expected empty in the sandbox; mark C1 n/a"
   (the seed has no traces or profiles yet), so the reviewer does not fail the No-data item for them;
4. prints the items of the visual checklist (the bullets under the "Visual checklist" heading of the
   `--checklist` file) and the layout of the one pull request comment the agent writes: the images and a
   verdict (pass, fail, unclear) with one line of evidence per item.

The agent opens each URL in a browser, takes the screenshots, judges them and posts the comment. `--json`
prints the same content (`ok`, `error`, `warnings`, `sandbox`, `dashboards`, `checklist`, `prompt`).

**Always exit 0**: a failed setup (no runtime, a pin mismatch, git cannot find the base), a bad flag and
`--help` print what failed (or the usage) and `ok: false` in JSON, and never fail the CI job that calls it.

## `obs scrape`: an app's own metrics into the sandbox

```text
obs scrape --target <url> [--target <url> ...] [--label name=value ...] [--job name] [--interval 15s] [--once]
           [--vm-url url] [--dir observability] [--project name]
```

Concept §11.8 names `obs scrape` next to the sandbox but does not define it; this is the minimal reading.
It scrapes Prometheus text endpoints (an app's `/metrics`) on an interval and writes them into the running
sandbox's VictoriaMetrics (`/api/v1/import/prometheus`), with `job`, `instance` and the `--label`s on every
sample, so the app's metrics can be looked at next to the seed. Scraped metrics carry their where-from
labels as `durian_cloud_*` (concept §2): pass `--label durian_cloud_landscape=local` and so on. `--once`
scrapes once and exits 1 when it failed; otherwise it runs until Ctrl-C and keeps going after a failed
scrape.

## Engines and pins

| Signal  | Engine                                               | Version                        |
| ------- | ---------------------------------------------------- | ------------------------------ |
| metrics | VictoriaMetrics (`metricsql`, storage and evaluator) | v1.156.0                       |
| logs    | VictoriaLogs `lib/logstorage`                        | v1.54.0, tag commit `6433dae5` |

Each equals the version in the charts (concept §1). `obs version` prints them.
No Loki module is in `go.mod`.

### `src/third_party/VictoriaLogs`

VictoriaLogs v1.54.0 is taken from the tag's commit, never through the Go module proxy (the proxy holds a
2021 VictoriaMetrics commit under that version). The tree holds the tag's `go.mod`, `go.sum`, `LICENSE`,
`lib/logstorage` and `lib/prefixfilter` (no tests; 1.9 MB) and `go.mod` carries a `replace` to it. `go mod vendor`
copies it into `vendor/`, so the vendor hash covers it.

- It equals the tag **except one added file**, `lib/logstorage/obs_fieldnames.go`, which exports the field-name
  walker (the parsed query keeps its filters unexported) and is marked as an obs change (Apache-2.0 §4).
- `third_party/VictoriaLogs.sha256` lists the sha256 of every file of the tag's tree. A `go test`
  (`internal/treecheck`) and `tests/run.sh` fail when any file differs, is missing or is not listed, other than
  the marked one. Planted drift (edit, add, delete) is tested red.
- To bump: clone the new tag, copy the same file set (without `*_test.go`), regenerate the manifest, re-add the
  marked file (check it still compiles), change the pin in `src/main.go`, the chart and the corpus together.

The engines run in-process; obs marks the global flag set parsed before the first engine call
(VictoriaMetrics' `lib/memory` panics otherwise) and caps VictoriaMetrics' cache at 512 MiB
(it takes 60 % of host memory by default).

## Toolchain

obs needs Go ≥ 1.27.4 (VictoriaLogs v1.54.0's `go.mod`). The registry's exported `go` is 1.26.10 and is
not touched. obs builds with `golang/obs/go.nix`: Go 1.27.4 from the narrow `nixpkgs-go127` flake input
(exact commit, like `nixpkgs-dotnet`). Remove both when the main nixpkgs pin carries Go ≥ 1.27.4.

## Tests

- `go test ./...` runs in the nix build (`doCheck`): the field walker, the stats window, the tree-drift check
  (with planted drift), query rules, file readers, seed shape, variable filling and `obs` end to end on
  temp projects.
- `tests/run.sh` runs the built binary in the sandbox (`nix flake check` runs it as `obs-tests`, `nix build .#obs-tests`):
  - green: `tests/fixtures/good` (a checkout service, with `contract.yaml`) and `tests/fixtures/charlotte`
    (Primordial's `crash-logs` and `pod-crashloop` alerts, copied and written as CRs, and a dashboard);
  - red, each naming file + rule or panel + the bad token: `bad-logsql-syntax`, `bad-metricsql-syntax`,
    `bad-logsql-no-stats`, `bad-prom-only-function`, `bad-loki-datasource`, `bad-flat-alert`,
    `bad-event-missing`, `bad-no-rows` (a metric the seed does not hold), `bad-drifted-field`
    (`severity` for `severity_text`), `bad-dashboard-label`; `ok-rollup-no-window` is green with a warning.
    Each fixture's `expect` file lists what its output must contain.

## Parity with the real stores (local run)

`src/internal/check/parity_test.go` (build tag `parity`, so it never runs in the nix build) loads every fixture's seed into
**real** VictoriaMetrics v1.156.0 and VictoriaLogs v1.54.0 containers and runs every query a live run executes on both
the embedded engines and the containers (instant and range queries through `/api/v1/query` and `/query_range`, stats
queries through `/select/logsql/stats_query` with `start`, `end` and `time` as the datasource sends them, log queries
through `/select/logsql/query`). Results must be equal: same series, same values, same row counts.

```text
docker run -d --name obs-parity-vm -p 127.0.0.1::8428 victoriametrics/victoria-metrics:v1.156.0 \
    -retentionPeriod=10y -search.latencyOffset=0s -search.disableCache
docker run -d --name obs-parity-vl -p 127.0.0.1::9428 victoriametrics/victoria-logs:v1.54.0 -retentionPeriod=10y
OBS_PARITY_VM=http://127.0.0.1:<port> OBS_PARITY_VL=http://127.0.0.1:<port> \
    go test -tags parity -run Parity -v ./internal/check      # in golang/obs/src
```

Use fresh containers for every run (VictoriaLogs keeps duplicate rows). The nightly parity job (a later release)
replaces this by hand run.

## Not tested yet

- `obs sandbox up` on aarch64 and macOS, and with podman (the box that ran it has docker only); Tempo and Pyroscope take no data from the seed yet.
- `obs look` with a pull request on GitHub (the agent posts the comment; obs only prints the prompt).
- Variable filling for LogsQL panels against the Grafana VictoriaLogs datasource (it reuses the Prometheus macros).
- Seeded log rows against a real VictoriaLogs fed through Alloy (field names are FROM SOURCE).
- LogsQL `_time:[start, end)` handling against real Grafana (the nightly parity job, a later release).
- aarch64 and macOS builds are in CI; only x86_64 Linux was run by hand.
