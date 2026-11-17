# obs: observability checks with no running stack

`obs` checks a repo's alerts and dashboards without a running Grafana, VictoriaMetrics,
VictoriaLogs or Tempo. It embeds the engines' own parsers and storage in one static Go binary:
no network, no containers.

**Licence: AGPL-3.0-only** (`LICENSE`). Each registry release attaches the Corresponding Source
(this tool's source plus every vendored Go module) as `obs-<version>-source.tar.gz`; `obs version`
says where. Everything in the binary is AGPL-3.0, Apache-2.0, MIT, BSD or MPL-2.0; the notice files of every
linked module ship in `share/licenses/obs`. Design: `concepts/observability.md` §11 and
`design/registry-tools/obs.md` in the diene corpus (the concept wins where this file and it differ).

## Available commands

| Command              | What it does                                                                                                                                                                          |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `obs query --static` | Parses every MetricsQL, LogsQL and TraceQL query in the alerts and dashboards with the real parsers and applies the rules below.                                                      |
| `obs query --live`   | Makes the seed, loads it into the embedded VictoriaMetrics, VictoriaLogs and Tempo, runs every alert and dashboard query on it. Each must return rows. Also applies the static rules. |
| `obs links`          | Checks trace → logs, metric → traces and backend span → profile labels on the seed.                                                                                                   |
| `obs version`        | The obs version and every engine pin.                                                                                                                                                 |

Follow-up commands (see the corpus goal): `obs gen`, `obs check`
(the contract), `obs alerts` (evaluating alerts over incidents), `obs assert`,
`obs profile`, trace alert evaluation, `obs engines --check-chart`,
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

This branch includes `obs check`, `obs gen`, `obs alerts` and `obs links`; the final release version is set when obs-full lands.

## Usage

```text
obs query --static [--dir observability] [--names] [--event-rule auto|on|off]
obs query --live   [--dir observability] [--seed seed.yaml] [--base unix] [--names]
obs alerts [--dir observability] [--vl-settings victorialogs-settings.yaml]

obs sandbox up|down|status [--dir observability] [--project name] [--port-base n]
obs look   [--base origin/main] [--all] [--json]
obs scrape --target <url> [--label name=value] [--interval 15s] [--once]
obs links [--dir observability] [--seed seed.yaml] [--base unix]

obs check [--dir observability] [--pods n] [--landscapes n]
obs gen [--check] [--dir observability] [--lang go,dotnet,dart,bun]
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
- **`contract.yaml`**: the contract (see "The contract" below); its presence switches on the contract name checks and the event rule.

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

## The contract (`observability/contract.yaml`)

Written by hand (concept §3). Unknown keys fail. `obs check` and `obs gen` refuse an invalid contract.

```yaml
service: checkout
modules: [api, worker] # the log stream count uses it
pod_keys: [k8s.pod.name, k8s.pod.uid, k8s.node.name, service.instance.id] # default shown
interval: 30s # aggregation interval (default 30s)
types:
  provider: { kind: enum, values: [stripe, adyen, paypal] }
  shard: { kind: int, min: 0, max: 15 }
  hour: { kind: time, unit: hour_of_day } # or weekday
  billing_day: { kind: date, window_days: 30 } # a metric label only
  user_id: { kind: free } # log fields and span attributes only
metrics:
  checkout.charges:
    kind: counter # counter | histogram | gauge
    budget: 5000
    labels: { provider: provider, shard: shard, hour: hour }
  checkout.queue.depth: { kind: gauge, across_pods: sum } # sum | avg | max | last, or per_pod: true
logs:
  ChargeFailed: { event: checkout.charge.failed, level: error }
spans:
  checkout.charge: { kind: internal, attributes: { user_id: user_id, provider: provider } }
platform_metrics: # metrics the platform makes (OTel auto instrumentation), with their labels
  http.server.request.duration: [http.route, http.response.status_code]
generate: # `obs gen` targets, `out` relative to the folder that holds observability/
  - { lang: go, out: internal/signals/generated, package: signals }
```

Two keys go beyond the concept's examples: `platform_metrics` (so a query on a metric the platform
makes is not "undeclared") and `modules` (the module count of the stream formula). `free` may be
written without declaring it (`attributes: { user_id: free }`, as concept 3.1 does). The VictoriaLogs
stream fields are only the bounded where-from set (concept 3.3): a log event declares none, so a
`stream_labels` entry is refused (`stream-label-declared`; with `free-stream-label` or `date-stream-label`
when its type is free or a date).

## `obs check`

```text
obs check [--dir observability] [--pods n] [--landscapes n]
```

Prints the worst-case series per metric (the product of its label sizes, times buckets + 2 for a
histogram: 14 for the default 12 buckets), the worst case times `--pods` for a `per_pod` metric, the
log stream count (modules x `--landscapes`, times any declared stream label; log events add no
streams) and `events=[...]`. It fails (exit 1, `FAIL <file> <yaml path>: [rule] ...`) on:

- `free-metric-label`: `free` on a metric label;
- `stream-label-declared` (with `free-stream-label`, `date-stream-label`): a log event declares a stream field, or a free or date one;
- `date-no-window`: a calendar date type without `window_days`;
- `over-budget`, `budget-missing`: a metric over its budget, or with labels and no budget;
- `gauge-pods`: a gauge with neither `across_pods` nor `per_pod` (or both);
- `metric-kind`: no kind or an unknown kind; and the schema rules (`label-type`, `enum-empty`, `int-bounds`, `event-level`, ...).

The after-start pod-key check of §4.1 is not here (it needs a running store: `obs assert`).

## `obs gen`

```text
obs gen [--dir observability] [--lang go,dotnet,dart,bun]
obs gen --check [--dir observability]
```

Writes, for a valid contract: one set of **types and constants** per `generate:` target (concept
§3.4: label value types, one labels type per metric and span, a name carrier per signal, the
allowed-keys table; no per-metric functions; shared base types are prefixed `DurianSignal`),
`<dir>/contract/events.json` (event names, read by the log-alert event check) and
`<dir>/contract/aggregation.yaml` (the §4.1 VictoriaMetrics stream-aggregation file: counters and
histogram parts get `total`; gauges get `sum_samples`, `avg`, `max` or `last` with `dedup_interval`;
`without` = `pod_keys`; `per_pod` metrics and unlisted metrics are in no match).
Output is deterministic and formatted. Every file starts with `Code generated by obs gen. DO NOT EDIT.`
and `contract-hash: sha256:<hash of the contract file>`.

`--check` regenerates into a temp dir and compares with the committed files: a file that differs is
`[gen-edited]` when its header hash equals the contract's (a hand edit) and `[gen-stale]` when it does
not (the contract changed and nobody regenerated); a missing file is `[gen-missing]`.
Golden files: `tests/fixtures/gen/good` is a consumer repo with its generated files committed; the
Go unit tests and `obs gen --check` compare against it (`OBS_UPDATE_GOLDEN=1 go test ./internal/gen/...`
rewrites them). The tests find `tests/` in an ancestor of the package (or `OBS_TESTS_DIR`); a build from `src/` alone skips them, and the nix check copies `tests/` next to the source root. The generated code compiles: `tests/typecheck/<lang>.sh` builds a consumer against it and
runs planted bad consumers that must not compile.

## Contract name checks in `obs query --static`

When `<dir>/contract.yaml` exists, every name a query reads must be in the contract or in the platform
label list (concept §2): `contract-undeclared-metric` (stored name, with "did you mean"),
`contract-undeclared-label` (selector and `by`/`on` labels; a label of another metric fails),
`contract-bad-enum-value` (an equality or plain `a|b` value that is not an enum value or is outside a
bounded number; a value that holds a dashboard variable is not judged),
`contract-pod-label` (a pod key on a business metric, §4.1), `contract-undeclared-event` and
`contract-undeclared-stream-field` (a stream tag outside the where-from set). Regex metric names (`{__name__=~"..."}`) are matched against the known names and a regex that matches none is
undeclared; regex label values must match an allowed value; label functions (`label_replace`, ...) must read
labels their input has. Names compare with dots
and underscores equal. Other log fields are free-form (§6): not judged.

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
  marked file (check it still compiles), change `src/internal/pins/pins.json`, update
  `third_party/VictoriaLogs.source.json` (version, tag commit and manifest SHA256),
  the chart and the corpus together. Pin tests tie the source marker to the actual manifest.

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

Use the labeled, digest-pinned stack and cleanup commands in [Nightly parity](#nightly-parity).
The harness creates fresh containers for every run: VictoriaLogs retains duplicate rows.

## obs alerts

`obs alerts --dir observability` evaluates the `GrafanaAlertRuleGroup` CRs in
`alerts/<slug>/<severity>.yaml` on the seed and each named incident. It prints one
line per rule and expectation, including its file and incident window, and exits
1 on a miss. Every rule must stay quiet on the baseline; rules omitted from an
incident's expectations are also checked for quiet. Metric and LogsQL rules run
inside VictoriaMetrics v1.156.0 and VictoriaLogs v1.54.0 with no network or containers.

```text
obs alerts --dir observability
OBS_VERIFY_FAST=1 OBS_VERIFY_SKIP=1 obs alerts --dir observability
OBS_CACHE_DIR=.cache/obs obs alerts --dir observability
obs alerts --dir observability --steps steps.json --recorded recorded.json
```

The seed's `incidents: [spike]` reads `incidents/spike.yaml`. An incident declares
`window: {start: 10m, duration: 10m}`, `change` entries with a metric, matching
`where` labels and `rate`, `latency_x` or `share_x`, and
`expect: {fire: [rule-uid], quiet: [], fire_within: 5m, resolve_within: 5m}`.
Firing must begin inside the incident and before `fire_within`; it must stop by
the end plus `resolve_within` (default zero). Use the recovery allowance for the
query's rollup window and `keepFiringFor`. The time line ends in the past; an
explicit `--base` whose complete time line reaches the future is refused.

The Grafana 13.3.0 expression port supports math, reduce (including non-number
mapping), threshold and recovery thresholds, `for`, `keepFiringFor`, missing
series, `noDataState` and `execErrState`. Source functions are cited alongside
each state transition. Recording rules use the same CR reader with
`record: {metric: fixture_recorded, from: A}`; dependent recording rules execute
first and their samples are appended to the embedded metric store before alerts.
`--recorded` writes `{labels, timestamps, values}` rows; `--steps` writes each
minute's evaluation and state. Both exports bypass the persistent result cache.

LogsQL queries use each rule's `relativeTimeRange.from/to`, parse at the resulting
end time and add the exclusive `[start, end)` range. Final `stats by (...)` fields
are instance labels, alongside `__name__` for each stats output column. A numeric
null keeps that instance's labels and evaluates as NoData. With a contract, `durian.cloud/event` filters are checked
against its event list. Other field-value filters are printed as assumptions and
removed from the parsed AST; event filters in `_stream` or `or`, unknown events
and stats reading field values are refused. Seed events must be declared too.
Without a contract, one event-rule-skipped note is printed for each log query.

The numeric light path is checked against the vendored Grafana dataframe path
with `OBS_VERIFY_FAST=1`; unchanged incident inputs are compared with their
baseline reuse by `OBS_VERIFY_SKIP=1`. Any mismatch fails the command. The result
cache includes the binary, seed, incidents, recording rules, contract and each
alert's normalized CR definition. Time-derived queries (including sample
timestamps) also include the base timestamp and do not reuse shifted baseline
results. Wall-clock/random queries and a configured query guard bypass persistent
caching. Storage uses `OBS_TMPDIR`,
otherwise a writable `/dev/shm` with at least 1 GiB free (else the system temp
directory); VictoriaMetrics caches are capped at 512 MiB.
Unused metric series are omitted and VictoriaLogs starts only when needed.

MEASURED: regression tests cover state transitions, numeric light/full parity,
event-filter refusals, recording outputs, cache invalidation and planted wrong
thresholds. NOT TESTED: parity against the live Grafana scheduler and datasource
in this part PR, notification/persistence behavior, and VictoriaLogs runtime
timeout/concurrency limits. TraceQL alerts use the `DatasourceEngine` hook in a
later part. The Grafana AGPL licence ships in `share/licenses/obs/grafana/LICENSE`.

### Existing query-check limits

- `obs sandbox up` on aarch64 and macOS, and with podman (the box that ran it has docker only); Tempo and Pyroscope take no data from the seed yet.
- `obs look` with a pull request on GitHub (the agent posts the comment; obs only prints the prompt).
- Variable filling for LogsQL panels against the Grafana VictoriaLogs datasource (it reuses the Prometheus macros).
- Seeded log rows against a real VictoriaLogs fed through Alloy (field names are FROM SOURCE).
- aarch64 and macOS builds are in CI; only x86_64 Linux was run by hand.

## Nightly parity

The `Obs parity` workflow runs nightly and on changes to engine pins, and can be
dispatched manually. The main-push release job also requires the reusable parity
workflow: it compares `golang/obs/**` with the latest full `vX.Y.Z` release tag,
skips successfully when unchanged, and runs full parity otherwise (or when no
release tag exists). Branch CI is unchanged. It uses GitHub's standard
`ubuntu-24.04` runner in this public repository.

`src/internal/pins/pins.json` is obs's version source, embedded in `obs version`.
Before any container starts, `parity/check-pins.sh` compares every version and
source commit with `parity/images.json`, including the image tags and datasource
plugin version. The job starts VictoriaMetrics v1.156.0, VictoriaLogs v1.54.0,
Tempo 3.1.1, Pyroscope 2.4.3 and Grafana 13.3.0 with VictoriaLogs datasource
v0.34.0. **Chart pins are NOT TESTED:** their location in the chart registry has
not been specified. Recorded image digests establish which binaries ran; the
manifest's source commit fields describe the embedded source provenance. Every
image is pinned by tag and digest: the current tag must still resolve to that
digest before anything starts, and the containers run by digest. Grafana's
reported image build commit is also checked against its separate pin. The public
v13.3.0 source commit is `752e3462`; the image build commit is
`6f8ce3c693b493043ebcd4787566d2b6b118b0f0`. The source commit cannot be proven
from the released image.

Each comparison is a separate workflow step: fixture and panel queries, historic
per-minute datasource values, LogsQL alert values through Grafana's `/api/ds/query`,
VictoriaLogs query limits, and (when the corresponding command exists) full
Grafana alert evaluation, live scheduler history, TraceQL, links and profiles.
An available command with a missing parity test fails. Container tests carry the
`parity` build tag and never run in the offline Nix checks. Tempo/links are now
part of the base branch, so their adapter compiles directly against those packages.

On 17 November 2026, the final local core run compared **23 fixture query
executions (three intentionally empty), nine metric panel executions through
Grafana, 472 LogsQL alert minutes through Grafana, 462 MetricsQL alert minutes
through Grafana, and six settings-driven VictoriaLogs limit cases (MEASURED)**.
The complete local settings/core/TraceQL session took 155 seconds, including
startup, builds, ordinary Go tests, 30 offline shell checks and the TraceQL/links
suite (three baselines and five planted failures). All seven planted pin drifts,
four planted comparison drifts and six release-gate histories were rejected or
classified correctly. VictoriaMetrics values use a relative tolerance of `1e-11`.

The panel comparison found a real difference: Grafana aligns range endpoints to
the step grid, while obs previously used the unaligned seed end. `obs query
--live` now applies the same alignment before executing metric panels.

Advanced adapters were also run against the parallel part branches: M3
`aa2085a` (27 historic alert evaluations, including numeric LogsQL counts; four
live scheduler transitions and ten recording-rule points), M4 `e462cb4` (seven
TraceQL searches, two range-metric queries and six link checks), and M5 `0257d0b` (12 function CPU shares
and two span CPU shares). These are MEASURED local results. Alerts and TraceQL
are now integrated in this branch; the profile step switches on when its command lands.

VictoriaLogs referee settings found on that run: a recent, disjoint time line per
fixture; `-retentionPeriod=10y`; explicit insert `/internal/force_flush` before
queries; a two-second settling wait; `-memory.allowedBytes=256MiB`;
`-search.maxQueryDuration=30s`; `-search.maxQueryLen=16KiB`;
`-search.maxQueryTimeRange=7d`. Length and range boundaries are compared with
the settings-file-driven embedded engine, including an authored `_time` filter
that narrows a larger API range. Runtime timeout/concurrency equivalence on
different hardware is NOT TESTED. The artifacts include the real server's `/flags`, plugin
metadata, container digests, test output and timings. Tempo uses one hour of
WAL ingestion slack in both storage and live-store, a one-hour recent-query
window, zero query-end cutoff, and short trace-idle timers so a past seed remains
searchable without waiting for the backend block poll.

For a local run, use a unique absolute scratch directory and hold the machine's
heavy-work lock for the whole stack and test session:

```bash
export OBS_PARITY_STATE=/tmp/obs-parity-local
bash golang/obs/parity/stack.sh up
(cd golang/obs/src && go build -o "$OBS_PARITY_STATE/obs" .)
bash golang/obs/parity/step.sh TestParity
bash golang/obs/parity/step.sh TestParityGrafanaLogs
bash golang/obs/parity/stack.sh logs
bash golang/obs/parity/stack.sh down
```

Every container has `obs.parity=1` and a unique run label. Cleanup removes only
that run's containers and network. No Docker prune is used.

### VictoriaLogs chart settings

`obs query --live --vl-settings FILE` and `obs alerts --vl-settings FILE` read the consumer's extracted VictoriaLogs
args. By default it loads `<dir>/victorialogs-settings.yaml` if present; without
that file it uses the pinned v1.54.0 defaults and prints one note. Finding the
chart and extracting its deployed args is the consumer's responsibility. An
explicit missing file, duplicate flag, invalid value or unknown `search.*` flag
is an input error. Accepted YAML forms are a flag map, a list of one-flag maps,
or a list of `-flag=value` strings, for example:

```yaml
-search.maxQueryLen: 16KiB
-search.maxQueryTimeRange: 7d
-search.maxQueryDuration: 30s
-search.maxConcurrentRequests: 4
-search.maxQueueDuration: 10s
```

The embedded engine enforces the original query's byte length before rewriting,
the effective time-filter intersection, execution deadline, concurrency slots
and queue deadline. Refusals name the server flag. `select.disable` is also
honored. Non-query args (retention, storage, HTTP auth, etc.) do not configure the
temporary embedded store; `search.allowPartialResponse` is cluster-only and
`search.logSlowQueryDuration` affects logging, not refusal. Pinned defaults are
16KiB, unlimited time range, 30s execution, 10s queue, and the server's CPU-based
concurrency formula (twice CPUs up to four CPUs, otherwise CPUs, capped at 16).
These defaults and flag parsing follow the pinned
[vlselect frontend](https://github.com/VictoriaMetrics/VictoriaLogs/blob/v1.54.0/app/vlselect/main.go)
and [LogsQL frontend](https://github.com/VictoriaMetrics/VictoriaLogs/blob/v1.54.0/app/vlselect/logsql/logsql.go).
`vl.Limits.Check` is also the alerts integration's query-guard interface. Alerts
check the original query before assumed-filter rewriting; effective settings
participate in result-cache keys, so tightening a limit cannot reuse a cached success.

## obs links

`obs query --static` and `--live` also check Tempo dashboard targets (`query`, `expr`
or `expression`). `queryType: traceId` targets and hex-only `traceql` targets
(including the default query type) use a validated ID lookup; an
unknown ID with no spans fails the live check. Legacy search models are refused
explicitly (`nativeSearch`/`search`). Service maps, upload/clear and other
non-query models print a note and are skipped, as are Jaeger/Zipkin datasources.
TraceQL is parsed and validated by **Tempo 3.1.1**, commit
`9db7df79d2af`; no regular-expression query parser is used. `--names` lists every
non-intrinsic attribute, including those in `select` and metrics grouping. A
`check.SpanAttributeNames` interface connects the contract's name list; a nil
adapter leaves name enforcement off. Live checks reject missing attributes even
in `select(...)` or `by(...)` when the query otherwise returns rows. Trace alerts
print a note and await the alerts integration.

**FROM SOURCE:** Grafana v13.3.0's
[TraceQL editor documentation](https://github.com/grafana/grafana/blob/v13.3.0/docs/sources/shared/datasources/tempo-editor-traceql.md#query-by-traceid)
supports entering a trace ID in the TraceQL editor. The requested
`public/app/plugins/datasource/tempo/datasource.ts` no longer exists at that tag:
[commit 61ec981](https://github.com/grafana/grafana/commit/61ec98144cd42d26d87a2fd477c74be122b99e20)
removed Tempo from core. Its last core implementation at
[datasource.ts:335](https://github.com/grafana/grafana/blob/b92bfd830f9c748ba7d1a2860b23b562fed68d28/public/app/plugins/datasource/tempo/datasource.ts#L335)
matches trimmed hex-only text, and
[datasource.ts:369](https://github.com/grafana/grafana/blob/b92bfd830f9c748ba7d1a2860b23b562fed68d28/public/app/plugins/datasource/tempo/datasource.ts#L369)
routes it to `handleTraceIdQuery`. This routing is covered offline; parity with
the separately shipped plugin remains untested.

```text
obs query --static --dir observability --names
obs query --live --dir observability
obs links --dir observability                  # optional --seed and --base
```

Add trace definitions to the seed. Each source event makes a server span and an
optional client child, with real parent IDs, resource attributes, OTLP scalar
types and matching `trace_id`/`span_id` on its log lines. The HTTP status code is
an integer; explicit `attributes` retain YAML string/int/double/bool types.

```yaml
resource:
  durian.cloud.service: checkout
  k8s.pod.name: checkout-example
traces:
  - from: { metric: requests }
    server: POST /charge
    route: /charge
    client: charge
    error_when: { outcome: error }
    attributes: { http.response.status_code: 200, cached: true }
```

`obs links` checks these paths offline:

- **Trace → logs:** for every distinct server route and attribute set, run
  `trace_id:="<id>" AND service.name:="<service>"` in VictoriaLogs over the span's
  millisecond time range. Compare timestamps and bodies with that trace's seeded
  log lines; a missing or foreign row fails. The range is at least 1 ms.
- **Metric → traces:** for every stored series, keep all shared resource/span
  labels except `__name__` and histogram `le`, preserving numeric/bool types.
  Run the TraceQL search in the last active seeded minute; an unmapped label or
  no matching trace fails. Searches are capped at 20; they prove existence.
- **Span → profile:** check backend `service.name` and `k8s.pod.name`, mapped to
  `service_name` and `pod`, plus the optional bounded where-from labels. This is
  a light identity-label check; it does not read profiles or require CPU samples
  in short spans.

**FROM SOURCE:** the trace-to-logs expression follows Grafana 13.3.0's
[`getQueryForVictoriaLogs`](https://github.com/grafana/grafana/blob/v13.3.0/public/app/features/explore/TraceView/components/logsLink.ts),
with `filterByTraceID: true`, `filterBySpanID: false`, and explicit
`tags: [{key: "service.name"}]`. Set this unmapped tag explicitly: Grafana's
[`getDefaultLogsTags`](https://github.com/grafana/grafana/blob/v13.3.0/public/app/features/explore/TraceView/crossSignalConfig.ts)
otherwise maps `service.name` to `service_name`, while VictoriaLogs retains dots.
Span time rounding follows
[`getTimeRangeFromTimestamps`](https://github.com/grafana/grafana/blob/v13.3.0/public/app/features/explore/TraceView/createTraceLink.ts).
The VictoriaLogs datasource v0.34.0
[`queryInstantURL`](https://github.com/VictoriaMetrics/victorialogs-datasource/blob/v0.34.0/pkg/plugin/query.go)
sends the interpolated expression as `query`, and start/end as Unix nanoseconds;
its [frontend interpolation](https://github.com/VictoriaMetrics/victorialogs-datasource/blob/v0.34.0/src/datasource.ts)
keeps this expression. **NOT TESTED:** this exact query and time range through a
real Grafana/VictoriaLogs UI; the parity job is the referee.

The in-memory storage adapter ports the prototype's Tempo vParquet5 nested-set
methods (AGPL-3.0) and typed-column predicates. The public engine API exposes
`Search(query,start,end,limit)` and `MetricsRange(query,start,end,step)` for the
future alert evaluator. **MEASURED offline:** structural operators, typed
comparisons, metrics counts/rates/quantiles, both dashboard query modes and all
three links. `tests/links.sh` plants syntax, attribute, log-link, trace-link and
profile-label failures; every failure names the file, query/route/series and bad
attribute or value. Real-store parity remains **NOT TESTED** here.

## `obs alerts` speed (concept §11.1.1, re-measured with VictoriaLogs)

### VictoriaLogs speed measurements

The §11.1.1 table was measured in spike 7 with Loki as the log engine. This is the same
table with the VictoriaLogs engine (`obs alerts` of this branch). MEASURED.

| Alerts | 4 h seed | 24 h seed | Peak memory 4 h / 24 h |
|---|---|---|---|
| 1 | 3.55 s | 15.78 s | 2.03 / 7.85 GB |
| 20 | 3.92 s | 18.55 s | 2.16 / 8.70 GB |
| 200 | 11.86 s | 65.46 s | 2.21 / 8.17 GB |
| 1,000 | 15.06 s | 68.75 s | 2.14 / 8.21 GB |

Loki numbers (spike 7, same table) for comparison: 1 alert 0.23 / 1.10 s, 0.19 / 0.78 GB;
20 alerts 1.05 / 5.45 s, 0.76 / 4.0 GB; 200 alerts 1.92 / 10.64 s, 1.07 / 5.3 GB;
1,000 alerts 2.44 / 10.14 s, 1.22 / 5.9 GB.

**What the numbers say**

- Every cell is slower and heavier than the Loki table. A 4 h seed takes 3.5–15 s and
  about 2 GB; a 24 h seed takes 16–69 s and 7.9–8.7 GB.
- The 24 h column is at or over the 8 GB of a standard CI runner. The §11.1.1 budget
  ("1,000 alerts in ~10 s and under 6 GB on a 24 h seed") does **not** hold with the
  VictoriaLogs engine. The 4 h budget (under 2.5 s and 1.2 GB) does not hold either; the
  concept text keeps the old numbers until the owner decides what to do.
- Memory is flat across alert counts (2.0–2.2 GB at 4 h, 7.9–8.7 GB at 24 h): the cost is
  loading the seed, not evaluating alerts. With Loki, 1 alert at 4 h used 0.19 GB. So in
  this build the "load only what rules read" rule is not working for the seed (the
  stores take the whole seed even for one alert). That is the first thing to look at.
- Time does grow with alert count (3.9 s for 20 alerts and 11.9 s for 200 at 4 h), then
  flattens (1,000 alerts: 15 s), as the shared seed and run reuse take over.

**Box and method**

- Box: Intel Xeon Platinum 8488C, 32 vCPU, 247 GB RAM, Linux 7.0 (AWS). The job runs on
  2 CPUs (`taskset -c 0,1`), but the box is shared with other builders, so the other
  30 CPUs, the disk and the memory bus were busy: expect noise. One run per cell
  (`OBS_RUNS=1`); the runner takes several and prints the median time and the largest peak.
- Cold start: a new process per run, an empty `OBS_CACHE_DIR`, the set copied to
  `/dev/shm` first. The OS page cache is not dropped (needs root). Time is the process's
  wall clock; peak memory is the process's `ru_maxrss`.
- Binary: `obs` built from this branch (the `obs-alerts` evaluator at aa2085a merged).
  Every run also checked its own expectations: all runs exited 0 (the table has no failed
  cell; a failed run prints FAILED instead of a time).
- Run it (under the shared heavy lock):

  ```sh
  cd golang/obs/src
  go build -o /tmp/obs . && OBS_BIN=/tmp/obs OBS_FIXTURES=../tests/fixtures/alerts-speed OBS_RUNS=3 \
    go test -tags speedbench -run TestSpeedTable -v -timeout 2h ./internal/alerts/speedfixtures
  ```

### Fixture sets (`tests/fixtures/alerts-speed/`)

- `n1`, `n20`, `n200`, `n1000`: each has `alerts/` (GrafanaAlertRuleGroup CRs),
  `incidents/` (3 incidents), `contract.yaml`, `seed.4h.yaml` and `seed.24h.yaml`.
  The runner copies one seed to `seed.yaml`.
- Generated, not hand-written: `go generate ./internal/alerts/speedfixtures` (run
  `treefmt` after; the repo hook reformats the YAML). `go test ./internal/alerts/speedfixtures`
  checks that every set loads with `internal/files`, has no LogQL, that variants differ,
  and that the seeds load.
- The 20 base alerts are the spike prototype's `proj20` mix: 13 metric-only, 6 log-only,
  1 metric + log (`charge-errors/critical` joins both). The bigger sets repeat them as
  variants `v2/ ... v50/`: a different rollup window (2–15 min, count thresholds scale with the
  window) and a new rule uid, so 1,000 rules are 1,000 different rules.
  Names are made up (landscape `speedland`, service `checkout`); no real landscape.
- Incidents: `stripe-outage`, `checkout-slow`, `adyen-declines` (seed format of M1). Every
  alert uid, variants included, is in `expect.fire` or `expect.quiet`.
  Assertion coverage per set (baseline run + 3 incidents): n1 = 4 (1 positive, 3 negative);
  n20 = 80 (19 positive, 61 negative); n200 = 800 (190 / 610); n1,000 = 4,000 (950 / 3,050).
  The expect lists are what the evaluator produced on the 4 h and 24 h seeds, so they are a
  regression baseline, not an independent oracle.

### Fixture porting rules (prototype LogQL to LogsQL, by hand)

1. `{service_name="checkout"}` becomes the stream filter `{durian.cloud.service="checkout"}`.
2. `| durian_cloud_event="E"` becomes `"durian.cloud/event":="E"` (the contract event rule
   needs this form); `| json | event="E"` has no json step in VictoriaLogs, it is the same filter
   (written as `:in("E")` so the two alerts differ).
3. `| severity_text="ERROR"` becomes `severity_text:="ERROR"`; `| payment_provider="adyen"`
   becomes `"payment.provider":="adyen"`.
4. `sum(count_over_time(S [W]))` becomes `_time:W S | stats count() hits`;
   `sum(rate(S [W]))` becomes `_time:W S | stats rate() per_s`; `sum by (l)` becomes
   `stats by (l)`. The rule's `relativeTimeRange.from` is the window in seconds.
5. The threshold is a Grafana math node (`$A > 100`), as in the prototype, not a LogsQL `filter`.
6. `unwrap duration_ms` (reads a log field value) has no LogsQL equivalent under seeds that
   carry no field values; obs refuses it. The speed set replaces it with a count of
   `declined` events at `WARN` severity (`logs/slow-unwrap`); a refusal test for the real
   unwrap belongs to the alerts e2e tests.
7. Metric queries are unchanged (MetricsQL); every metric node is `instant: true`, every
   log node `queryType: stats`; the datasource types are `prometheus` and
   `victoriametrics-logs-datasource`.

NOT TESTED: 24 h seeds were measured once per cell; no run was on a quiet box; the cache-hit
rows of §11.1.1 (unchanged PR 0.2-0.4 s, one changed alert 0.3-0.5 s) were not re-measured.
