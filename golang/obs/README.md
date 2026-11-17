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
| `obs check`          | Checks the contract, worst-case metric series and log stream count. |
| `obs gen`            | Generates bounded types, event constants and aggregation rules; `--check` detects drift. |
| `obs assert`         | Checks real metric series counts, labels and values against the contract. |
| `obs profile`        | Summarizes CPU profiles, builds a median baseline and compares function/span shares. |
| `obs sandbox`        | Starts, stops or inspects the local stack for people. |
| `obs look`           | Sets up dashboard views and prints the screenshot review prompt. |
| `obs scrape`         | Scrapes an app's metrics into the sandbox. |
| `obs version`        | The obs version and every engine pin.                                                                                                                                                 |

Follow-up commands (see the corpus goal): `obs alerts` (evaluating alerts over incidents),
trace alert evaluation, `obs engines --check-chart`, `obs store move`.

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

This branch also ships step 6's `obs links`; the final release version is set when obs-full lands.

## Usage

```text
obs query --static [--dir observability] [--names] [--event-rule auto|on|off]
obs query --live   [--dir observability] [--seed seed.yaml] [--base unix] [--names]
obs check [--dir observability] [--pods n] [--landscapes n]
obs gen [--check] [--dir observability] [--lang go,dotnet,dart,bun]
obs sandbox up|down|status [--dir observability] [--project name] [--port-base n]
obs look   [--base origin/main] [--all] [--json]
obs scrape --target <url> [--label name=value] [--interval 15s] [--once]
obs links [--dir observability] [--seed seed.yaml] [--base unix]
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
  marked file (check it still compiles), change the pin in `src/main.go`, the chart and the corpus together.

The engines run in-process; obs marks the global flag set parsed before the first engine call
(VictoriaMetrics' `lib/memory` panics otherwise) and caps VictoriaMetrics' cache at 512 MiB
(it takes 60 % of host memory by default).

## `obs assert`

Step 7 of the workflow (concept §11.4). After e2e, take a series export from the store the e2e run wrote to
and give it to obs as a file; obs counts the real series per metric and fails when a count is above the
contract's worst case (a hole: a cast, reflection, a raw OTel call). No network.

```text
obs assert --series FILE [--dir observability] [--pods n] [--unknown warn|fail|ignore]
```

- **Input** (`--series`): `/api/v1/series` JSON (`{"status":"success","data":[{"__name__":...}]}`) or
  `/api/v1/export` lines (`{"metric":{...},"values":[...],"timestamps":[...]}`), for example
  `curl -g "$VM/api/v1/series?match[]={durian_cloud_service=\"checkout\"}&start=<e2e start>"`. Scope the export to the
  service under test: obs counts every series it is given. A series repeated in an export counts once.
  Label names compare with dots and underscores equal (`payment.provider` = `payment_provider`).
- **Count.** A series belongs to the contract metric whose stored names it carries (`checkout_charges_total`,
  `checkout_charge_duration_seconds_bucket|_sum|_count`: a histogram counts all three, like the worst case of
  `obs check`, buckets + 2 per label set). The series are counted **per landscape** (label
  `durian_cloud_landscape`; none = one group), since the worst case is per landscape.
- **Limit.** `contract.WorstCase(metric)`; for a `per_pod` metric worst case × `--pods` (the pods per landscape,
  from the landscape config). Fails with `series-over-worst-case`, naming the series file, the metric, the count,
  the stored names and the worst case. Platform metrics (`platform_metrics:`) are counted and printed, with no
  limit.
- **Label checks run on every known series, even below the worst case.** Platform and `per_pod`
  metrics may retain pod keys. Findings name the hole:
  `label-not-in-contract` (a label the contract
  does not give the metric, with example values), `pod-label-on-business-metric` (a pod key on a metric without
  `per_pod`), `label-value-outside-type` (a value outside an enum, int or time type).
  An undeclared label with one value for the metric is a warning: constant OTel SDK resource
  labels (`telemetry.sdk.language`, `.name`, `.version`) do not split series. More than one value
  fails `label-not-in-contract`, even below capacity. Pod-key and declared-type violations still fail
  when constant.
- **Metrics outside the contract** (neither a contract metric nor in `platform_metrics`; runtime and process metrics
  of a pod land here) are a `warn` (`metric-not-in-contract`); `--unknown fail` makes them failures, `ignore` hides them.
- Exit 0 clean, 1 a count over its limit, 2 input error (no contract, unreadable or wrong export, a failed export
  response). _NOT TESTED: against a real VictoriaMetrics export; the two forms follow the documented API._

## `obs profile summarize|baseline|compare`

Step 7 of the workflow (concept §11.6). An e2e run sends a fixed number of requests, takes a CPU
profile of the load window and compares each function's and each span's **share** of CPU with a
stored baseline. Files only: no network.

```text
obs profile summarize --format pprof|cpuprofile|speedscope [--own REGEX]... [--run run.json] [--top N] [--json] FILE
obs profile baseline  --format F --service S --scenario N --own REGEX [--own REGEX]... [--profiler P] [--dir e2e/profile-baseline] [--update] RUN1 RUN2 RUN3
obs profile compare   --format F (--baseline FILE | --service S --scenario N [--dir DIR]) [--limits e2e/profile-limits.yaml] [--run run.json] FILE
```

Exit 0 when no row is slower than its limit, 1 when one is, 2 on a usage or input error.

**Formats.** `pprof` (Go, gzip or raw; value types `samples` for the count and `cpu` for the weight),
Chrome `.cpuprofile` (Bun, Flutter web; `samples[]` or `hitCount`; `(idle)` is not CPU) and `speedscope`
(`sampled` and `evented` profiles). Evented O/C stacks are weighted by elapsed open time per
thread, with time units normalized before combining threads. _FROM SOURCE:
[TraceEvent's SpeedScopeStackSourceWriter](https://github.com/microsoft/perfview/blob/main/src/TraceEvent/Stacks/SpeedScopeStackSourceWriter.cs)
writes dotnet-trace's export as evented O/C profiles per thread._
_FROM SOURCE: dotnet-trace's
[TraceFileFormatConverter](https://github.com/dotnet/diagnostics/blob/main/src/Tools/dotnet-trace/TraceFileFormatConverter.cs)
calls that writer after constructing thread-time stacks._
obs drops stacks containing `(idle)`, `BLOCKED_TIME`, `AWAIT_TIME` or `UNMANAGED_CODE_TIME`.
_FROM SOURCE:
[SampleProfilerThreadTimeComputer](https://github.com/microsoft/perfview/blob/main/src/TraceEvent/Computers/SampleProfilerThreadTimeComputer.cs)
uses the latter two pseudo-frames for await/native thread time._ Native time can be blocked or
running; dropping it is conservative, and does not identify every possible wait in arbitrary
speedscope inputs. For evented input the sampling count is positive CPU stack time divided by
the collection's sampling period, so coalescing intervals cannot loosen the gate.
Every profile subcommand accepts `--sample-period DURATION` (for example `2ms`); the default
assumes dotnet-trace's **1 ms** period. _FROM SOURCE:
[EventPipe's sample profiler](https://github.com/dotnet/runtime/blob/main/src/native/eventpipe/ep-sample-profiler.c)
initializes `_sampling_rate_in_ns` to `NUM_NANOSECONDS_IN_1_MS`; collection settings can override it._
Pass the actual collection period for other settings or exporters. Evented baselines store the
period in `sample_period_ns`, all three runs must agree, and compare refuses a different period
(exit 2). These are CPU-time-derived sample counts; no original per-sample timestamps are recovered.
_MEASURED: a
dotnet-trace-shaped fixture and planted regression; real dotnet-trace collection NOT TESTED._

**Spans.** A sample is in a span when it has the pprof label `span_name` (Go, `otel-profiling-go`), or
when its stack holds a frame `span__<name>` (Bun) or `UserTag:<name>` (Dart). Spans count
children (a sample in two nested spans is in both). A span with no sample has no row (concept §8.2:
short spans are often empty). _NOT TESTED: `UserTag:<name>` is obs's own converter convention,
not a native Dart frame spelling. The dart template's CI step must write the converter from
`vm_service.getCpuSamples`, inserting these frames for UserTag spans. That converter does not
exist here; this has only been tested on synthetic frames._

**OS CPU per request.** The profile cannot know it, so the load run writes `<profile>.run.json` next to
it (or pass `--run`): `{"requests": 600, "os_cpu_ns": 6e8, "wall_ns": 20e9, "cores": 2, "steal_ns": 1e6,
"throttled_ns": 0}`. `os_cpu_ns` is the process's user + system CPU over the load window (cgroup `cpu.stat`
`usage_usec` delta × 1000, or `/proc/<pid>/stat` utime + stime); `steal_ns` is the `steal` column of
`/proc/stat` over the window, summed over the pinned cores; `throttled_ns` is the cgroup `cpu.stat`
`throttled_usec` delta × 1000. Unknown fields are refused.

**Function rows.** `summarize` prints, per function, `self`, `inclusive` and `own`. `own` is the CPU
attributed to the function: its own samples plus the samples of callees that are not the service's own
code (a library or runtime frame called from it). Each sample goes to the nearest frame, going from the
leaf, that is own code, not a closure and not a span frame; so a closure's time belongs to the function that
contains it, and the `own` shares add up to at most 100 %. **Own code** is what `--own` says (a regular
expression matched against the function name and file; repeatable). `summarize` defaults to all
code for inspection. **`baseline` requires at least one `--own`** (exit 2 when absent), stores
the patterns, and `compare` uses the baseline's. Function identity is **name + normalized file**.
File URI prefixes and checkout roots are removed: known source directories (`src`, `internal`,
`cmd`, `pkg`, `lib`, `app`) are retained as relative paths; otherwise the common directory of the
selected own-code sources is used. A line shift above a function cannot turn it into a new row.
`--source-root DIR` overrides inference for all three subcommands. The root used is stored in
`source_root` and printed in the report header; explicit roots are marked as such. Compare
inherits an explicit baseline root when the flag is omitted and refuses a different explicit
root (exit 2). A single unanchored source file cannot establish the checkout boundary: obs prints
a note naming the inferred path and suggesting `--source-root`, without failing the run.
Declaration line and column are retained only to disambiguate repeated name+file declarations;
matching uses the nearest available location once per declaration. Chrome's zero-based
`lineNumber` and `columnNumber` become one-based; speedscope `line` and `col` are kept, and pprof
uses `Function.StartLine` rather than instruction lines. This includes separate same-line functions
in a minified bundle. Baseline format is version 3: regenerate earlier preview baselines from
three profiles with `--own` and `--update`. **Closures** (never rows): Go `.funcN`,
`.gowrap`, `-fm`; JS `(anonymous)` and empty names; Dart `<anonymous closure>`; .NET `<>c__`, `>b__`,
`lambda_method`.

**Baseline** = median of exactly 3 runs → `e2e/profile-baseline/<service>.<scenario>.json` (about 1 KB;
no times, sorted rows, shares rounded to 6 places, so two runs give byte-identical files). Rows:
functions whose median own share is ≥ 3 %, every span with samples in at least 2 of the 3 runs, the median
samples and the median OS CPU per request. An existing baseline is replaced only with `--update`
(baselines change only through a PR). `--profiler dotnet-trace` is stored and gives every row 30 %.

**Compare** prints `what / baseline / now / change (limit)` for every baseline row and exits 1 when a row is
slower than its limit, or a hot baseline function has no matching samples. The latter is an
`ERROR` naming the function and suggesting `--update` after inspecting an intentional removal.
For a function or span row the change is `now share / baseline share − 1`.

- **limit = max(tolerance, 3 × noise)**, noise = sqrt(1/n_base + 1/n_now). _FROM SOURCE: the concept does not say
  what n counts; obs uses the **row's own** sample count (own samples of the function, samples inside
  the span) in the baseline (median) and in this run, so small rows get wider limits. For the total-CPU row
  n is the total sample count of each run._
- **Tolerances:** 5 % for a function and a span, 15 % for total OS CPU per request, 30 % for every row of
  a baseline made with `--profiler dotnet-trace`.
- **Total CPU per request fails only on a quiet runner.** Quiet = during the load window the pinned cores
  lost at most 1 % of their time: `(steal_ns + throttled_ns) / (wall_ns × cores) ≤ 1 %`. A noisy runner, or
  a run meta without those four fields, still prints the row (`slower`, with the reason) but does not fail it.
  A run with no `os_cpu_ns` and `requests` has no total row; a note says so.
- **Overrides only in config**, `e2e/profile-limits.yaml` (`--limits`; none when the file is absent); each one
  needs a `why`, otherwise obs exits 2 naming the file:

  ```yaml
  overrides:
    - service: checkout # required
      scenario: browse # optional: every scenario when absent
      kind: function # optional: function | span | total
      name: svc/render.Page # optional: one row
      file: src/render.go # optional: normalized function file from the baseline
      line: 12 # optional: one-based declaration line; requires kind: function
      column: 11 # optional: one-based declaration column; requires kind: function
      limit: 0.10 # a fraction: 0.10 = 10 %
      why: the page template is being rewritten # required
  ```

  The most specific matching override wins. A name alone intentionally covers all functions
  with that name; use file, line and column to select a single declaration. Location selectors refer
  to the baseline's location, so a new line shift does not disable an agreed override. A failure
  names the source of the limit.

- **Faster matched rows are reported, never failed.** A span with no samples now is shown as faster;
  an unmatched hot baseline function is an error requiring inspection/update. A function that is
  ≥ 3 % of this run, own code, and has no baseline row is printed as a note.

**What a share can and cannot see.** Rows are shares of one run's CPU, so a large win in one function raises
every sibling's share (a function at 40 % that halves lifts the others by about 25 %, which is over a 5 %
tolerance). Update the baseline in the same PR as such a change. The total-CPU row is the one that sees a
whole-service slowdown that leaves the shares alone.

A flag on a re-run is still a flag; raw profiles of every CI run are kept as CI artefacts (CI job, not obs).

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
