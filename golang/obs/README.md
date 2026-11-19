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

| Command                                    | What it does                                                                                                                                                                     |
| ------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `obs gen`                                  | Writes bounded types, event constants, `contract/events.json` and the aggregation file for bun, go, dotnet and dart from the contract.                                           |
| `obs gen --check`                          | Regenerates into a temp folder and diffs; fails on a hand edit or a stale generate.                                                                                              |
| `obs check`                                | The static check in one run: contract limits (worst-case series, budgets, log streams), the evaluator versions against `obs.yaml`, and every query rule of `obs query --static`. |
| `obs test`                                 | The seed-based check in one run: `obs query --live` + `obs alerts` + `obs links`, and every incident is caught by at least one alert.                                            |
| `obs query --static`                       | (Part of `obs check`; unchanged.) Parses every MetricsQL, LogsQL and TraceQL query in the alerts and dashboards with the real parsers and applies the rules below.               |
| `obs query --live`                         | (Part of `obs test`; unchanged.) Loads the seed into the embedded VictoriaMetrics, VictoriaLogs and Tempo and runs every alert and dashboard query; each must return rows.       |
| `obs alerts`                               | (Part of `obs test`; unchanged.) Evaluates every alert at every minute of the seed and each incident; must fire inside incident windows and stay quiet outside them.             |
| `obs links`                                | (Part of `obs test`; unchanged.) Checks trace → logs, metric → traces and backend span → profile links on the seed.                                                              |
| `obs assert`                               | Checks real metric series counts, labels and values from an e2e series export against the contract.                                                                              |
| `obs profile summarize\|baseline\|compare` | Summarizes CPU profiles, builds a median baseline and compares function and span shares against it.                                                                              |
| `obs store move <store>`                   | Runs the single → scaled store move steps in order, none skipped, with `--dry-run` and `--rollback`.                                                                             |
| `obs sandbox up`                           | Starts, stops or inspects (`down`, `status`) the local stack for people; no check needs it.                                                                                      |
| `obs look`                                 | Sets up the sandbox with the changed dashboards and prints the screenshot review prompt for the calling agent; never fails CI.                                                   |
| `obs scrape`                               | Scrapes an app's own metrics into the sandbox.                                                                                                                                   |
| `obs live usage\|shape\|incidents\|look` | Reads a running stack (the local sandbox, dev, stage or prod; read-only) and compares it with the repo: usage vs contract budgets, the shape of real data vs the seed, fired alerts vs incident files and runbooks, the dashboards already there. See [obs live](#obs-live-read-a-running-stack-and-compare-it-with-the-repo). |
| `obs symbols put\|decode`                  | The symbol store: stores debug symbol files by build id and decodes a stack trace with them.                                                                                     |
| `obs size`                                 | The sizing command: traffic cores, memory, series and storage per day for a given load.                                                                                          |
| `obs engines --check-chart <dir>`          | Compares the engine versions in a Helm chart folder with the pins; exit 1 on any drift. See [obs engines](#obs-engines---check-chart).                                           |
| `obs version`                              | The obs version and every engine pin.                                                                                                                                            |

Not tested items are collected in [Not tested](#not-tested), at the end of this file.

## Developer workflow

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

## Usage

```text
obs query --static [--dir observability] [--names] [--event-rule auto|on|off] [--contracts auto|none]
obs query --live   [--dir observability] [--seed seed.yaml] [--base unix] [--names] [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml]
obs alerts [--dir observability] [--seed seed.yaml] [--base unix] [--out results.json] [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml]
            [--workers n] [--steps steps.json] [--recorded recorded.json]
obs assert --series FILE [--dir observability] [--pods n] [--unknown warn|fail|ignore]
obs profile summarize|baseline|compare   (obs profile help)
obs check [--dir observability] [--pods n] [--landscapes n] [--names] [--event-rule auto|on|off] [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml] [--contracts auto|none]
obs test  [--dir observability] [--seed seed.yaml] [--base unix] [--names] [--event-rule auto|on|off] [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml]
          [--contracts auto|none] [--workers n] [--out results.json] [--steps steps.json] [--recorded recorded.json]
obs gen [--check] [--dir observability] [--lang go,dotnet,dart,bun]
obs sandbox up [--dir observability] [--seed seed.yaml] [--project name] [--port-base n] [--runtime docker|podman] [--reseed]
obs sandbox down|status [--dir observability] [--project name] [--runtime docker|podman]
obs scrape --target <url> [--label name=value] [--interval 15s] [--once] [--job name] [--vm-url url] [--dir observability] [--project name]
obs look   [--base origin/main] [--all] [--json] [--keep] [--seed-base unix] [--file dashboards/x.json] [--checklist file.md] [--out obs-look] [--width 1600] [--height 1000]
         [--dir observability] [--seed seed.yaml] [--project name] [--port-base n] [--runtime docker|podman] [--reseed]
obs links [--dir observability] [--seed seed.yaml] [--base unix]
obs version
obs store move <store> --config move.yaml [--dry-run] [--rollback]
obs symbols put|decode --store <dir> --file <file> [options]
obs size --req-per-second <n> --services <n> --pods <n> --profiled-pods <n>
obs engines --check-chart <chart folder>
```

Exit 0 when clean, 1 when a check failed, 2 on a usage or input error. Every failure line names
the file, the rule or panel, the rule id in `[brackets]` and the offending name or value.
`warn` lines never fail; `note` lines are information.

## Inputs

`--dir` (default `observability`) holds:

- **`alerts/<slug>/<severity>.yaml`**: one `GrafanaAlertRuleGroup` CR per file (the golden
  shape in the platform's observability standard, `alerts.md`). obs reads each rule's `data[]`:
  `datasourceUid`, `relativeTimeRange.from`, `model.expr`, `model.queryType`, and
  `model.datasource.type` when present. Grafana expression nodes (`datasourceUid: __expr__`)
  are not queries and are skipped. A flat authoring file (`title/datasource/expr`) is refused
  with a pointer to the CR. More than one YAML document in a file is refused.
- **`dashboards/**/\*.json`**: dashboard JSON. Panels (also inside collapsed rows), `targets[].expr`,
  `templating.list`, `time.from/to` are read. Annotation queries are not checked.
- **`seed.yaml`** (`--live` only; `--seed` overrides the path): see below.
- **`contract.yaml`**: the contract (see "The contract" below); its presence switches on the contract name checks and the event rule.

**Which datasource speaks what.** The plugin type decides (`prometheus`,
`victoriametrics-metrics-datasource` = MetricsQL; `victoriametrics-logs-datasource` = LogsQL;
`loki` = refused; `tempo` = TraceQL, parsed and run by obs: see the `traceql-*` and `tempo-alert-*` rules below). Without a type the datasource uid decides by name:
`loki` anywhere in it is Loki, `logs` is LogsQL, `prom`, `mimir`, `metrics` or `victoriametrics`
is MetricsQL. A uid that is none of these fails and says to set `model.datasource.type`.
A panel datasource chosen by a variable (`${ds}`) takes the type from that datasource variable.

## `obs query --static`: the rules

| Rule id                            | Kind | What                                                                                                                                                                                                                                           |
| ---------------------------------- | ---- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `metricsql-syntax`                 | fail | The query does not parse with VictoriaMetrics' `metricsql` (v0.87.6, from VictoriaMetrics v1.156.0).                                                                                                                                           |
| `metricsql-unsupported-function`   | fail | A function VictoriaMetrics does not implement (Prometheus-only, e.g. `double_exponential_smoothing`).                                                                                                                                          |
| `rollup-no-window`                 | warn | A rollup function on a selector with no `[window]` (the window then comes from the query step).                                                                                                                                                |
| `logsql-syntax`                    | fail | The query does not parse with VictoriaLogs v1.54.0 `lib/logstorage`.                                                                                                                                                                           |
| `logsql-alert-needs-stats`         | fail | A LogsQL **alert** query has no `stats` pipe. This is VictoriaLogs' own rule for `/select/logsql/stats_query`, which the Grafana datasource calls: the last `stats` pipe must exist, and a trailing `\| filter` threshold after it is allowed. |
| `logsql-namespace-wide`            | warn | A `stats` query with no field or stream filter besides `_time` (concept §6.4).                                                                                                                                                                 |
| `loki-datasource`                  | fail | A dashboard or alert query on a Loki-type datasource: logs are LogsQL.                                                                                                                                                                         |
| `datasource`                       | fail | The datasource is not one obs knows.                                                                                                                                                                                                           |
| `logsql-alert-needs-event`         | fail | A LogsQL alert has no filter on `"durian.cloud/event"`. Runs only when `contract.yaml` exists (below).                                                                                                                                         |
| `file`                             | fail | A file that is not what it claims: not YAML/JSON, not a `GrafanaAlertRuleGroup`, several documents.                                                                                                                                            |
| `traceql-syntax`                   | fail | A TraceQL query does not parse with Tempo 3.1.1.                                                                                                                                                                                               |
| `traceql-attribute`                | fail | A TraceQL span or resource attribute is not in `contract.yaml` (runs when the contract exists).                                                                                                                                                |
| `traceql-query-type`               | fail | A dashboard trace target has a query type obs cannot tell (not a TraceQL query or a trace ID lookup).                                                                                                                                          |
| `trace-id`                         | fail | A trace ID lookup is not a valid trace ID.                                                                                                                                                                                                     |
| `tempo-alert-datasource`           | fail | A trace alert query is on Jaeger or Zipkin: alerts read traces only through the Tempo datasource.                                                                                                                                              |
| `tempo-alert-search`               | fail | A Tempo alert query is a search, not a TraceQL metrics query (`rate()` and the like).                                                                                                                                                          |
| `tempo-alert-*`                    | fail | Other Tempo alert refusals: `-query-type`, `-query`, `-range-exemplars`, `-step` (see TraceQL alert rules).                                                                                                                                    |
| `contract-undeclared-metric`       | fail | A query reads a metric `contract.yaml` does not declare.                                                                                                                                                                                       |
| `contract-undeclared-label`        | fail | A query uses a label that is not a label of the metric in the contract.                                                                                                                                                                        |
| `contract-pod-label`               | fail | A business metric is grouped or filtered by a pod label (concept §4.1).                                                                                                                                                                        |
| `contract-bad-enum-value`          | fail | A label filter uses a value outside the contract's enum for that label.                                                                                                                                                                        |
| `contract-undeclared-stream-field` | fail | A LogsQL stream tag is not a stream field in the contract.                                                                                                                                                                                     |
| `contract-undeclared-event`        | fail | A log alert names an event (or event prefix) that is not in the contract.                                                                                                                                                                      |
| `contract-event-regexp-no-match`   | fail | A log alert's event regular expression matches no contract event.                                                                                                                                                                              |
| `contract-event-filter-form`       | fail | A log alert's event filter has a form obs cannot check against the events.                                                                                                                                                                     |

Dashboard variables are filled before parsing, as Grafana's Prometheus backend does:
`$__interval`, `$__interval_ms`, `$__rate_interval`, `$__rate_interval_ms`, `$__range`,
`$__range_s`, `$__range_ms`, the `${...}` and `[[...]]` spellings, and a template variable's
current value (else a placeholder). `--names` prints, for every query, the metrics, labels and
log fields it reads (filters, stream tags `_stream:<tag>`, and the fields the pipes need).

**The event rule (decided 16 Nov 2026).** The concept (§11.1) says a log alert must name a contract
event. obs 0.2.0 checks contract events. So: when `<dir>/contract.yaml` is absent, a log alert
gets parse, `stats` and live-rows checks only, and obs prints one note per log alert, `event
rule skipped: no contract.yaml`. When it is present, the alert must filter on the event field
(`logsql-alert-needs-event`), and every event it names (exact, prefix, regular expression, `in(...)`, negated) must be in the contract (`contract-undeclared-event`, `contract-event-regexp-no-match`, `contract-event-filter-form`).
A filter on any other (free-form) field is _assumed true_: obs drops it and prints `assumed true (free-form fields): <filter>`, in `--static`, `--live` and `obs alerts` alike.
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
     that counts as no rows. When the event rule applies (`contract.yaml` exists), a filter on a
     free-form field (`user_tier:=gold`) is _assumed true_, as in `--static` and `obs alerts`: it is
     dropped from the query that runs, with the note `assumed true (free-form fields): <filter>`, so
     the seed does not have to hold the field. Planted check: `tests/run.sh` runs a copy of
     `fixtures/good` whose alert adds `user_tier:=gold` (green in `--static` and `--live`; red
     with `--event-rule off`), and `TestLiveDropsAssumedFilters` covers the same.
   - **Alert over a recording rule:** a query that reads the output of a recording rule
     (`record: { metric: ... }`) cannot run on the seed, because the output exists only after
     `obs alerts` evaluates the rule. `--live` does not run it and does not fail it; it prints
     `reads recording rule <name>: checked by obs alerts` naming the file and rule. A metric that no rule
     records and the seed lacks still fails `[no-rows]`. Covered by `TestLiveNamesRecordingRuleReads`
     and the `recording-rule read` lines of `tests/run.sh`.
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
resource: # resource attributes, dots kept; become metric labels (dots kept, concept §2.1) and log fields
  durian.cloud.landscape: fixture-dev
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

`incidents:` names files in `incidents/` that `obs alerts` plays (see `obs alerts`). `traces:` defines the seed's traces (see `obs links`); they drive `obs query --live` (Tempo) and `obs links`.

### Seed shape: level, trend, cycle, spikes, gaps and noise

`traffic.settings` opts into the same five parts that `obs live shape` describes.
The old `shape: flat` and `shape: daily_wave` keep their byte-identical datasets.
With settings, `level.typical` is the request rate; `traffic.requests` remains required
for compatibility. These example numbers match the live-shape comparison example:

```yaml
length: 168h
random_seed: 42
traffic:
  requests: 40/s
  settings:
    level: {min: 2, typical: 40, max: 310} # requests/second before each series' share
    trend: {change_per_day: 0.03} # +3% of typical per day, linear
    cycle: {daily_amplitude: 0.25, peak_hour: 20, weekend_factor: 0.6}
    spikes: {count: 4, size: 6} # four one-minute spikes, at least 6 times typical
    gaps: {count: 2, length: 11m} # two gaps, 11 minutes in total
    noise: 0.15 # independent uniform +/-15% of typical each second
```

The daily cycle peaks at 20:00 UTC; the optional weekend factor makes Saturdays and
Sundays 40% lower. Trend and cycle are applied before noise and spikes; the result is
clamped to min/max. Gaps omit metric samples and requests (including their logs and
traces). Incident changes apply after the traffic shape, so they can exceed its bounds.
Spike/gap positions and noise are deterministic from `random_seed`; they do not use the
host's random generator. Validation names `seed.yaml:line` and the bad setting.
The example is illustrative: use smaller rates or series shares for CI, especially
when generating a week of logs/traces. A gap can overlap a spike; no traffic is emitted
inside a gap.

### Label spelling per store (concept §2.1)

A name is spelled the way its store keeps it. obs seeds, checks and queries every store in that spelling, so a
dashboard or alert written to the standard returns rows on an obs seed and on the real stack alike.

| Store           | Label spelling                                 | Query example                                                |
| --------------- | ---------------------------------------------- | ------------------------------------------------------------ |
| VictoriaMetrics | dots kept, quoted (metric NAMES still use `_`) | `checkout_charges_total{"durian.cloud.service"="checkout"}`  |
| VictoriaLogs    | dots kept, as sent; quoted                     | `_stream:{"durian.cloud.landscape"="fixture-dev"}`           |
| Tempo           | dots, as sent                                  | `{resource.durian.cloud.landscape="fixture-dev"}`            |
| Pyroscope       | underscores only (a dotted name is dropped)    | `{durian_cloud_service="checkout", service_name="checkout"}` |

- The underscore form of a dotted VictoriaMetrics label (`durian_cloud_service`, `payment_provider`) selects
  nothing. `obs query --static` fails it with `contract-undeclared-label` and names the dotted spelling to write;
  `--live` fails it with `label-spelling` (with a contract, the same `contract-undeclared-label` finding, reported
  once); a query form the label check does not model still fails `no-rows` with a "stored as" hint.
- **Scraped (non-OTLP) metrics** reach VictoriaMetrics as `durian_cloud_*` (Alloy drops a relabel rule whose
  `target_label` has dots), and a vminsert `-relabelConfig` `labelmap` rule (landscape, platform, service and module only: see below;
  `replacement: durian.cloud.$1`) turns them back into `durian.cloud.*`. `obs scrape` takes the underscore input
  (`--label durian_cloud_landscape=local`), exactly like the real collector. The sandbox's VictoriaMetrics is started
  with the production vminsert relabel config, copied exactly (same rules, same order:
  `labelmap` of `durian_cloud_(landscape|platform|service|module)` to `durian.cloud.$1`, then `labeldrop` of those four
  and `cluster`; `-relabelConfig=/etc/vm-relabel.yml`, copied in at `obs sandbox up`). Source: the production landscape's
  repo (the comment above `VMRelabelConfig` in `src/internal/sandbox/stack.go` names it), branch `feat/durian-labels`, commit
  `954508c79c36faa38b221d5eb37c5bada9a03068`, file `apps/vm/chart/values.yaml`, key `vminsert.relabel.config`; **that
  branch is not merged yet** (its main has no relabel config), so this follows an unmerged
  branch. Scraped samples are stored dotted and `{"durian.cloud.service"="checkout"}` returns rows there; any other
  `durian_cloud_*` label is not mapped. A sandbox created by an older obs has no labelmap: run
  `obs sandbox down` and `up` again. NOT TESTED: the labelmap on the real stack with our Alloy v1.21.3.
- **TraceQL** names are exact too: Tempo keeps attribute names as sent, so a resource or span attribute the contract
  spells dotted must be written dotted (`span.payment.provider`). The underscore form fails `traceql-attribute` and
  names the right spelling.

Seed data is made to look like stored data (concept §11.2). Metrics: counters get `_total`, seconds
histograms `_seconds_bucket`/`_sum`/`_count` with `le`; dots in a metric NAME become `_`, dots in a LABEL name stay
(see "Label spelling per store" above). Logs, as VictoriaLogs stores an OTLP
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

### Entries somebody else makes: `source:`, `estimate`, `follows`, `used_by`

One contract shape covers a metric that is ours and one that is not (kube-state-metrics, cadvisor, an
exporter). An entry with `source: <who makes it>` is **not ours**: `obs gen` makes no type for it and the
aggregation file does not list it (it is stored as scraped, pod keys and all). It stays in the contract so
`obs check` counts it against its budget, `obs query` and `obs assert` know its labels, and the keep-list
below names it.

```yaml
types:
  pod: { kind: estimate, min: 28, max: 110, churn_per_day: 25 } # a range somebody else decides
  pod_phase: { kind: enum, values: [Pending, Running, Succeeded, Failed, Unknown] }
metrics:
  kube_pod_status_phase: # the stored name, exactly as scraped
    source: kube-state-metrics
    kind: gauge
    budget: 550
    used_by: [hub/alert/pod-crashloop, hub/dash/pods] # who reads it
    labels:
      pod: pod
      uid: { follows: pod } # changes together with pod: adds no series
      namespace: { follows: pod }
      phase: pod_phase
```

- **`source:`** names who makes the metric. The metric name is the **stored** name: a counter or gauge
  keeps its name as written (no `_total` or unit suffix is added), a histogram is stored as `_bucket`,
  `_sum` and `_count`. `unit:` is not used for a `source:` entry. Declare a scraped metric as `counter`,
  `gauge` or `histogram` only: there is no `summary` kind, so a scraped summary is declared as the gauges
  its `_sum` and `_count` series are (each its own entry). The gauge rule (`across_pods` or `per_pod`)
  and the pod-key rules (`contract-pod-label`, `pod-label-on-business-metric`) do not apply to it, since
  it keeps its pod keys. `free` on a label is still refused. A `source:` must name a producer: a blank one fails
  `[source-blank]`. A `source:` metric name is the scraped name, used **as written**, so a dotted one
  (`exporter.health`) fails `[source-metric-dotted]` instead of being rewritten with underscores; write
  `exporter_health`. Both name the file and line.
- **`estimate`** is a label type with `min`, `max` and `churn_per_day` (all three required). It is allowed
  **only** on a `source:` entry: on any other metric, a log stream label or a span attribute it fails
  `[estimate-not-source]`, naming `file:line`, the metric and the label. `obs check` counts an estimate at its **max**
  for the worst case and the budget. `churn_per_day` (new values a day: a replaced pod is a new value) is **not** in
  the budget: the budget is the series stored at once, which is what the concept's budget means and what the
  drafted budgets state. `obs check` prints it as information instead: `about N new series a day from churn`
  (each estimate's churn times the series one of its values carries). No value of an estimate label is checked in a query.
- **`follows: <label>`** marks a label that adds no series because it changes together with another label of
  the same entry (a pod's `uid` follows the pod): it does not multiply the worst case. The target must be a label of the
  same metric, and a chain must not loop (`[follows]`). It is only for a `source:` entry
  (`[follows-not-source]`): our own labels are bounded types of their own.
- **`used_by: [..]`** says who uses the metric (`hub/alert/<name>`, `hub/dash/<name>`). A metric is allowed when
  it is in the contract, within its budget, and says who uses it. `obs check` prints it. A `source:` entry with
  no `used_by` is a **warning** (`WARN ... [used-by-missing]`, exit stays 0): the owner's decision text asks for
  "say who uses it" without saying a missing one must fail, so it does not. The warning is only for `source:`
  entries: an own metric is named by its own service (its `service:` and the generated code), so it needs no `used_by`.
- An enum type used only by `source:` entries may list the empty string (a pod with no priority class
  has an empty label); a type an own metric uses may not.
- **Enum values and generated names.** A value may hold any characters (`<none>`, `it's`, `100%`, `über`): the
  wire value is always the text as written, and only the member name made from it is cleaned. Every
  character that cannot be in an identifier is a word break (`<none>` becomes `None`, `(n/a)` `NA`), a value
  with no letter or digit at all is `Value`, and a member that would start with a digit gets a `V` (Dart,
  .NET) or follows the type name (Go). Dart identifiers are ASCII only, so a letter outside ASCII is a break
  there. Two values that make one name in a language (`none` and `<none>`) are **refused by `obs gen`**, naming
  both values and the contract `file:line` of the type; rename one. Bun quotes every value, so it never collides.
  Only values of types an own metric, log or span uses are generated; a `source:`-only type makes no code.

**Several contracts in one repository.** `obs check` and `obs query` read **every** `observability/contract.yaml`
below the Git root, not only `--dir`'s: a dashboard in `apps/hub/observability` may read a metric that
`apps/cluster/observability/contract.yaml` declares. `obs check` validates and prints each. One table of **stored**
series names decides ownership: a metric (ours or a `source:` one) against a metric of another contract, and a
platform metric's possible stored names (unit and histogram variants, see the keep-list) against a metric of another
contract, are `[metric-clash]` and name both files (relative to the Git root) and lines. A `platform_metrics` entry that
several contracts list is **not** a clash (every service lists `http.server.request.duration`): the lookup merges their
label lists. Two contracts may name the same `service:`; their types stay apart (the lookup names a type by its
contract file). `obs query` merges the
others' metrics and platform metrics into the lookup (each keeps its own types); logs, spans and versions
stay the `--dir` contract's. Contracts in a `fixtures` or `testdata` folder are never read as company, and
a folder in one never looks for company; outside a Git repository only `--dir`'s contract is read.
`--contracts none` reads only `--dir`'s. `obs gen` and `obs assert` stay per contract.

## `obs check`

```text
obs check [--dir observability] [--pods n] [--landscapes n] [--names] [--event-rule auto|on|off]
          [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml] [--contracts auto|none]
```

`obs check` is the whole static check in one run and one summary. It runs three things:

1. **limits** (the contract rules below);
2. **versions**: the evaluator versions this obs embeds, against `observability/obs.yaml`;
3. **queries**: everything `obs query --static` does ([the rules](#obs-query---static-the-rules)): every alert and dashboard query parsed by the embedded engines and checked against the contract. `--vl-settings`, `--tempo-config`, `--event-rule`, `--contracts` and `--names` mean what they mean there. A folder with no alert and no dashboard is fine (the `[empty]` rule belongs to `obs query --static`); a folder with no `contract.yaml` skips the limits with a note and still checks the queries. Each query finding keeps its rule id.

It exits 1 when any half fails and prints one summary line at the end, for example
`obs check: ok: 1 metrics, 3 log events, 0 spans, 2 types, contract hash 754fae475354; queries: 7 queries (3 logs, 4 metrics), 0 failures, 0 warnings`
or `obs check: FAILED: 1 contract failures in <file>; 2 query failures`. A contract problem is printed once, not again by the query half.

**Speed (MEASURED, 18 Nov 2026, x86_64 Linux, one run each):** `obs check` takes 21 ms on `tests/fixtures/good` (4 h seed fixture, 7 queries) and 23 ms on `tests/fixtures/alerts-speed/n1`; `obs gen --check` takes 36 ms. Gen plus check stay far under 1 second; the check never reads the seed, so the seed length does not matter for it.

The contract part prints the worst-case series per metric (the product of its label sizes, times buckets + 2 for a
histogram: 14 for the default 12 buckets), the worst case times `--pods` for a `per_pod` metric, the
log stream count (modules x `--landscapes`, times any declared stream label; log events add no
streams) and `events=[...]`. It fails (exit 1, `FAIL <file> <yaml path>: [rule] ...`) on:

- `free-metric-label`: `free` on a metric label;
- `stream-label-declared` (with `free-stream-label`, `date-stream-label`): a log event declares a stream field, or a free or date one;
- `date-no-window`: a calendar date type without `window_days`;
- `over-budget`, `budget-missing`: a metric over its budget, or with labels and no budget;
- `gauge-pods`: a gauge with neither `across_pods` nor `per_pod` (or both), for an entry of ours (not a `source:` one);
- `estimate-not-source`, `estimate-bounds`, `follows`, `follows-not-source`, `used-by`, `metric-clash`: the
  [`source:` rules](#entries-somebody-else-makes-source-estimate-follows-used_by) above;
- `metric-kind`: no kind or an unknown kind; and the schema rules (`label-type`, `enum-empty`, `int-bounds`, `event-level`, ...).

The after-start pod-key check of §4.1 is not here (it needs a running store: `obs assert`).

The versions half prints the evaluator versions this binary embeds and fails when `observability/obs.yaml` names a store version they do not match: see [`obs.yaml`](#obsyaml-the-versions-and-thresholds-a-repo-targets).

## `obs test`

```text
obs test [--dir observability] [--seed seed.yaml] [--base unix] [--names] [--event-rule auto|on|off]
         [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml] [--contracts auto|none]
         [--workers n] [--out results.json] [--steps steps.json] [--recorded recorded.json]
```

`obs test` is everything that needs the seed, in one run and one summary. It runs, in this order:

| Part        | Same as                    | What fails it                                                                                  |
| ----------- | -------------------------- | ---------------------------------------------------------------------------------------------- |
| `queries`   | `obs query --live`         | an alert or dashboard query that does not return rows on the seed, and every static query rule |
| `alerts`    | `obs alerts`               | an alert that does not fire inside its incident, or fires outside it                           |
| `links`     | `obs links`                | a trace, log, metric or profile link that does not resolve on the seed                         |
| `incidents` | new, `[incident-uncaught]` | an incident no alert catches                                                                   |

**`obs test` turns off the `[empty]` rule of `obs query --live`** (a folder with no alert and no dashboard is red there). Why: `obs test` is also the run that
tells a repository with incidents and no alert yet which incident is uncaught, so a folder with nothing to query is a finding of the
`incidents` part (`[incident-uncaught]`), not an input error of the `queries` part. `obs check` does the same for the same reason.

The flags mean what they mean on the old commands (`--dir`, `--seed`, `--base`, `--names`, `--event-rule`,
`--contracts`, `--vl-settings`, `--tempo-config`, `--workers`, `--out`, `--steps`, `--recorded`). `--event-rule` reaches both the
`queries` and the `alerts` part: with `--event-rule off` a log alert that names no contract event is accepted in both (`obs alerts` itself has no such flag and keeps
the rule on whenever the folder has a contract). `--contracts none` also reaches both parts: the `alerts` part then reads only `--dir`'s contract too, so an invalid or clashing contract elsewhere in the repository cannot fail an isolated run (`obs alerts` has no such flag and keeps reading them). Each part prints its own
findings as the old command does, then one total per part and one last line:

```text
obs test: queries: ok: 7 queries (3 logs, 4 metrics), 0 failures, 0 warnings
obs test: alerts: ok: 1 alerts (0 recording rules), 2 runs, 2 expectations, 0 failed
obs test: links: skipped: the seed has no traces and nothing declares or queries any (no contract span, no TraceQL query), so there is no trace, log or profile link to follow
obs test: incidents: ok: 1 incidents, 1 caught, 0 uncaught
obs test: ok
```

Exit 0 when every part passes, 1 when a part failed, 2 on a usage or input error. A part that fails does not
stop the next one. `obs.yaml` is gated first, as in `obs query` and `obs alerts`: a version it names that the embedded
evaluators do not match fails the run before any part. The `obs alerts` part still prints its own result line
(`obs alerts: alerts=...`). The `links` part is skipped, with a line saying so, only when nothing could be linked: the seed has no traces **and**
the contract declares no span **and** no query is TraceQL. A contract that declares spans (or a TraceQL query) with a seed that has no traces
is a finding of `obs links` (`[seed] no traces in seed.traces`), so `obs test` runs the part and is red as `obs links` is. A folder
with no alert rule has nothing for `obs alerts` to run (a note, not the exit 2 of the old command), so its incidents are all uncaught.

### Every incident is caught by at least one alert (`[incident-uncaught]`)

An incident in `incidents/<name>.yaml` (the files the seed names under `incidents:`) must be caught by an alert.
**Caught** means the expectations `obs alerts` already judges: at least one alert listed in the incident's `expect.fire`
passed its fire verdict, that is it fired inside the incident window (within `fire_within`, and not outside the window
plus `resolve_within`). An incident is **uncaught** when

- `expect.fire` is empty (only quiet alerts, or none; `obs test` also accepts `expect: {}` here and reports it, where `obs alerts` still refuses the file with exit 2), or
- no alert in `expect.fire` passed its fire expectation: the line says `no alert passed its fire expectation` and names each alert with its failed
  condition as `obs alerts` printed it (`quiet`, `fired before the incident window`, or `fired at +12m0s, firing 29 min; firing outside window [10m0s,25m0s) at +25m0s` for an
  alert that fired inside the window and resolved late), or
- the folder has no alert rule at all.

An alert that fires without being listed is not a catch: it already fails as an unexpected fire in the `alerts` part. The line names the
incident file, the incident and its window:

```text
FAIL incidents/slow-burn.yaml incident slow-burn window [10m0s,20m0s): [incident-uncaught] expect.fire is empty: no alert is expected to fire in this window; add the alert that should catch it to expect.fire
```

The flow when a real incident was missed (written as a pull request, no capture command): add `incidents/<name>.yaml` (the story,
the window, the change, `expect`), list it under `incidents:` in `seed.yaml` and run `obs test`: it is red with `[incident-uncaught]`.
Add the alert that would have caught it and list its uid in `expect.fire` (also in every other incident the alert fires in:
an unlisted alert is expected quiet): `obs test` is green. `tests/check-test.sh` runs exactly this flow.

An incident that is deliberately not covered by any alert (a slowdown no alert should page for, a trickle that proves the alerts stay quiet) is
red under this rule unless it says so. Mark the incident file with `disturbance: "<reason>"` (the reason is required, an empty one is an error naming file and line). A
marked incident is left out of `[incident-uncaught]` and printed as a note, and the totals count it apart:

```text
note: [disturbance] incidents/slow-burn.yaml slow-burn: a trickle of errors: proves the error alert stays quiet
obs test: incidents: ok: 3 incidents, 2 caught, 0 uncaught, 1 disturbance(s)
```

An incident **without** the marker and with no catching alert stays red (owner pick, agenda A371: every incident is caught by at least one alert; the marker is
the one way to say an incident is not one). `obs alerts` ignores the field: its output is unchanged. The quiet-only incidents of `tests/fixtures/alerts-speed/n1` and `t1` carry it.
`n1` is the green `obs test` example. `t1` is an alerts speed fixture (input of the speed benchmark), not an `obs test` example: it has traces with no logs and no profile, so its `links` part is red by construction (its `incidents` part is green).
The marker is for quiet-only incidents. An incident that lists `expect.fire` and carries `disturbance:` is an error naming file and line (exit 2: "disturbance is only for incidents with no fire expectation"), and the reason is checked after YAML merges (`<<:`), so a merged empty reason is an error too.

**Speed (MEASURED, 18 Nov 2026, x86_64 Linux, a shared box, two runs each, all four parts):** `obs test` takes 0.09 s on `tests/fixtures/test/good`
(40 min seed, one alert), 1.1 s and 1.3 s on `alerts-speed/n1` with its 4 h seed and 3.3 s and 3.2 s with its 24 h seed
(3 incidents, 1 alert, 2 queries, links skipped; green). It is not under 1 second on the 4 h or the 24 h seed. A solo run of `obs alerts` alone took 0.77 s
and `obs query --live` alone 2.0 s on the 24 h seed in an earlier measurement (each builds the seed again; not re-measured on this box).

## `obs.yaml`: the versions and thresholds a repo targets

`observability/obs.yaml` (next to `contract.yaml`) says which store and tool versions the repo's
charts run and which query limits the stores run under. One loader reads it for `obs check`,
`obs query`, `obs alerts`, `obs sandbox up` and `obs look`, so no command reads it its own way.
Every key is optional: a key that is absent keeps the version or default built into obs.

```yaml
versions:
  grafana: 13.3.0 # alert rules and dashboards
  victoriametrics: v1.156.0 # MetricsQL
  victorialogs: v1.54.0 # LogsQL
  tempo: 3.1.1 # TraceQL
  alloy: v1.21.3 # reference only: no evaluator is embedded for it
  pyroscope: 2.4.3 # the sandbox image
  dashboard_schema: 42 # Grafana 13.3.0 dashboard schemaVersion
thresholds:
  logs: # the VictoriaLogs -search.* flags, under the names below
    max_query_len: 16KiB # -search.maxQueryLen
    max_query_time_range: 0 # -search.maxQueryTimeRange
    max_query_duration: 30s # -search.maxQueryDuration
    max_queue_duration: 10s # -search.maxQueueDuration
    max_concurrent_requests: 8 # -search.maxConcurrentRequests (default: from the CPU count)
  metrics:
    max_points_per_series: 1000000 # points one series may hold in a query (obs default 1,000,000)
  traces: # Tempo query_frontend
    query_end_cutoff: 30s # query_frontend.query_end_cutoff
    metrics_max_duration: 24h # query_frontend.metrics.max_duration
    metrics_max_intervals: 10000 # query_frontend.metrics.max_intervals
  profiles: # obs profile compare default tolerances, as a fraction (0.05 is 5 %)
    tolerance_function: 0.05
    tolerance_span: 0.05
    tolerance_total: 0.15
    tolerance_dotnet_trace: 0.30
```

Per-metric series budgets stay in `contract.yaml`.

**What `obs check` prints and fails on.** It always prints the evaluator versions this binary
embeds, then compares them with `obs.yaml`:

```text
evaluators embedded in this obs:
  MetricsQL             VictoriaMetrics v1.156.0
  LogsQL                VictoriaLogs v1.54.0
  TraceQL               Tempo 3.1.1
  Grafana alert engine  Grafana 13.3.0
  dashboard schema      Grafana 13.3.0 schemaVersion 42
  reference pins        Alloy v1.21.3, Pyroscope 2.4.3 (pins; no evaluator)
```

- A store version in `versions` that the embedded evaluator does not match (`victoriametrics`,
  `victorialogs`, `tempo`, `grafana`, `dashboard_schema`) fails with exit 1:
  `FAIL <obs.yaml> versions.victorialogs: obs.yaml wants v1.53.0, this obs embeds v1.54.0 (LogsQL of VictoriaLogs)`.
  `v1.54.0` and `1.54.0` are the same release.
- `pyroscope` has no embedded evaluator, but obs runs it as a sandbox image, and the sandbox refuses a
  version whose image digest `pins.json` does not record. So a Pyroscope version with no known image
  fails the same way (exit 1, `FAIL <obs.yaml> versions.pyroscope: obs.yaml wants 2.5.0, this obs embeds
2.4.3 (sandbox image of Pyroscope: no known image digest, known: 2.4.3)`), in `obs check`, `obs query`
  and `obs alerts` alike. `alloy` has no evaluator and no image: a different version is a `note` and exit 0.
- An empty section (`versions:` or `thresholds:` with every child commented out) names nothing: it
  counts as absent, not as an error.
- An unknown key, a wrong type or a bad value is an input error (exit 2) that names the file, the
  line and the key: `obs.yaml:15: unknown key thresholds.logs.max_query_lenn`. A tolerance that is not
  a finite number (`.nan`, `.inf`) is one too.
- **No `obs.yaml`**: `obs check` prints a note and the embedded versions and exits 0, so repos that
  have no file keep working. `obs query` and `obs alerts` print nothing new.

`obs query` and `obs alerts` read the same file the same way: the same version check (exit 1 before
any work, naming the key), the same thresholds. They print one line, `versions match the evaluators
embedded in this obs; thresholds applied: logs, ...`, when the file exists.

**Order of precedence** (for each key): the defaults built into obs, then `obs.yaml`, then
`--vl-settings` / `<dir>/victorialogs-settings.yaml` (VictoriaLogs) or `--tempo-config` (Tempo).
`--vl-settings` (`obs query --live`, `obs alerts`) and `--tempo-config` (`obs query --live`, `obs alerts`)
keep working and override `obs.yaml` key by key; a key the file
does not name keeps the `obs.yaml` value. For profiles, a per-service override in
`e2e/profile-limits.yaml` beats `obs.yaml`, which beats the built-in 5 / 5 / 15 / 30 %;
`obs profile compare` reads `observability/obs.yaml` when it exists (`--obs-yaml` names another).

**The sandbox.** `obs sandbox up` and `obs look` start exactly the images `versions` names for
Grafana, VictoriaMetrics, VictoriaLogs, Tempo and Pyroscope (and print them). A version obs has no
recorded image digest for (`src/internal/pins/pins.json`: the pin, plus an optional `also` list of
`{version, digest}`) is refused: `obs sandbox up` exits 2 with `<obs.yaml>: versions.pyroscope: 2.4.2
has no known image digest for grafana/pyroscope (known: 2.4.3)`; `obs look` prints the same and, as
always, exits 0. Nothing is guessed. An `also` entry counts only with a valid image digest
(`sha256:` and 64 lower-case hex characters); an absent or malformed digest is refused by name, with the
reason, the same way. `obs sandbox down` and `status` never read the file.

**Where the thresholds apply.**

- `thresholds.metrics.max_points_per_series` caps the points one series may hold in a query. A window that
  needs more (`(end - start) / step + 1` points) is a `FAIL` line naming the key and the numbers (exit 1 in
  `obs query --live`; in `obs alerts` the rule's line says why it errored, or the run stops with the same text);
  it is never a VictoriaMetrics panic.
- `thresholds.traces` shapes every Tempo metrics query obs runs: alert rules (`obs alerts`) and TraceQL
  metrics panels in dashboards (`obs query --live`). A panel is validated and run as Tempo's query-frontend
  does, with the seed's end as "now": the end is pulled in by `query_end_cutoff`, a window longer than
  `metrics_max_duration` or finer than `metrics_max_intervals` is an error that names the limit. With
  Tempo's defaults (30s, 24h, 10000) a `{} | rate()` panel over `now-5m` to `now` is green; with
  `metrics_max_duration: 1s` it is red. Trace searches and trace ID lookups do not go through these limits.

## `obs gen`

```text
obs gen [--dir observability] [--lang go,dotnet,dart,bun]
obs gen --check [--dir observability]
```

Writes, for a valid contract: one set of **types and constants** per `generate:` target (concept
§3.4: label value types, one labels type per metric and span, a name carrier per signal, the
allowed-keys table; no per-metric functions). Shared port/value base types come from the
language's interfaces library rather than being redefined in each generated file. Also writes
`<dir>/contract/events.json` (event names, read by the log-alert event check) and
`<dir>/contract/aggregation.yaml` (the §4.1 VictoriaMetrics stream-aggregation file: counters and
histogram parts get `total`; gauges get `sum_samples`, `avg`, `max` or `last` with `dedup_interval`;
`without` = `pod_keys`; `per_pod` metrics and unlisted metrics are in no match; `source:` entries are not listed).
It also writes the **keep-list** `<dir>/contract/keep.yaml` (below). A `source:` entry gets no generated type in any language.
Output is deterministic and formatted. Every file starts with `Code generated by obs gen. DO NOT EDIT.`
and `contract-hash: sha256:<hash of the contract file>`.

`--check` regenerates into a temp dir and compares with the committed files: a file that differs is
`[gen-edited]` when its header hash equals the contract's (a hand edit) and `[gen-stale]` when it does
not (the contract changed and nobody regenerated); a missing file is `[gen-missing]`.
A header-marked file absent from the output plan is `[gen-orphan]`, including files left after
dropping a target. Each header names the contract's repository-relative path, so generated files
owned by another contract in a monorepo are ignored. The check scans from the Git root (or the project
folder outside Git) without following symlinks or entering dependency/cache directories. `--lang` checks only the selected languages.
Golden files: `tests/fixtures/gen/good` is a consumer repo with its generated files committed; the
Go unit tests and `obs gen --check` compare against it (`OBS_UPDATE_GOLDEN=1 go test ./internal/gen/...`
rewrites them). The tests find `tests/` in an ancestor of the package (or `OBS_TESTS_DIR`); a build from `src/` alone skips them, and the nix check copies `tests/` next to the source root.

### The keep-list (`contract/keep.yaml`)

The collector stores only declared metrics (owner decision): `obs gen` writes the names that may be stored, and
the platform's renderer turns them into the keep rule on the metrics write door. The file is plain YAML:

```yaml
# generated by obs gen from apps/cluster/observability/contract.yaml
# contract-hash: sha256:<hash of the contract file>
metrics:
  - container_cpu_usage_seconds_total
  - http_server_request_duration_seconds_bucket
  - http_server_request_duration_seconds_count
  - http_server_request_duration_seconds_sum
  - kube_pod_info
```

- The names are the **stored** VictoriaMetrics names of our metrics, as production stores them: OTLP goes through vminsert with
  `-opentelemetry.convertMetricNamesToPrometheus` (metric **names** only; label names keep their dots). obs's stored names match
  that flag, **not** `-opentelemetry.usePrometheusNaming` (which also rewrites label names, so the dotted labels
  `durian.cloud.service` would become `durian_cloud_service`) and not the OTel collector's translator. The rule (VictoriaMetrics
  v1.156.0, `lib/protoparser/opentelemetry/stream/sanitize.go`): the name is split at `/ _ . - :` and space (empty parts are
  dropped, so `__` collapses), the unit word (`seconds` for `s`/`seconds`, `bytes` for `By`/`bytes`) is appended **unless it already
  is a part of the name anywhere**, a counter's `total` part is moved to the end (or appended), and a histogram is stored as
  `_bucket`, `_sum` and `_count`. So `a.total.b` as a counter is `a_b_total`, and `requests_total` as a counter with unit `s` is
  `requests_seconds_total`. **MEASURED:** `TestStoredNamesEqualVictoriaMetricsConversion` feeds real OTLP protobuf through
  VictoriaMetrics' own converter (the pinned module, flag on) and asserts the stored names equal obs's for every kind (counter, gauge,
  histogram) and every unit a contract allows, over the fixture contract's names and names built to trip a hand-written rule
  (a unit or `total` inside the name, `__`, `_total` plus a unit); a new VictoriaMetrics pin that changes the rule fails it. Because
  obs mirrors the converter there is no name obs refuses for its stored form. A `source:` entry's name is already the stored name (see above).
- **Included:** our metrics, every `source:` entry (the generators skip its types, not its keep line) and `platform_metrics`.
  A platform metric's kind and unit are not in the contract, so its names are a **superset, not exact**: for every key the plain
  name, `_total`, `_bucket`, `_count` and `_sum`, each also with a unit variant for **every unit word of VictoriaMetrics'
  unit table** (`_seconds`, `_milliseconds`, `_bytes`, `_hertz`, `_percent`, `_celsius`, ... 25 words) and `_ratio` (a unit `1` gauge such as
  `process_cpu_utilization_ratio`). The words are not a hand list: `internal/contract/units_gen.go` is generated from the converter's own
  table (`unitMap`, v1.156.0) and `TestUnitWordsAreTheVictoriaMetricsTable` fails when it differs from the pinned module (rewrite it with
  `go test ./internal/contract -run UnitWords -update-units`). A key whose `total` part sits where VictoriaMetrics moves it from also lists the
  converter order: a counter `requests_total` with unit `s` is stored `requests_seconds_total`, so the keep-list has that name as well as
  `requests_total` (the unitless case). A key ending in `.duration` gets only the time words (`days`, `hours`, `minutes`, `seconds`, `milliseconds`, `microseconds`,
  `nanoseconds`, each checked against the table by `TestDurationUnitsAreTimeWordsOfTheTable`), so `http.server.request.duration` lists
  `http_server_request_duration_seconds_bucket`, `_sum`, `_count` among them. A name kept that nobody sends costs nothing, and a name dropped loses data. **MEASURED:**
  `TestPlatformKeepCoversEveryUnitOfTheTable` feeds every unit of the table, every kind and names with `total`, `ratio` and unit words in
  every position through VictoriaMetrics' converter and asserts each stored name is in the superset. Not covered: a unit outside the table
  (VictoriaMetrics appends it as written) and a rate unit such as `By/s` (it appends `per_second`). NOT TESTED: the superset against the names
  a real platform emits.
- Sorted, one per line, plain names (no regex, no dots). An empty contract writes `metrics: []`.
- `obs gen --check` reports it like the other generated files: `[gen-missing]`, `[gen-edited]` (the hash matches, the lines do not) and
  `[gen-stale]` (the contract changed). The golden file is `tests/fixtures/gen/ok-source-cluster/observability/contract/keep.yaml`,
  generated from a copy of the platform's cluster draft (32 `source:` entries) with made-up consumer names; it also pins that
  `obs gen` skips every one of them.
- **Upgrading:** after this release, `obs gen --check` fails `[gen-missing] contract/keep.yaml` in every folder with a contract until
  `obs gen` has been run once (the keep-list is a new generated file); commit the file it writes.
- The always-kept names (`up`, `scrape_*`), collecting every `apps/*/observability/contract/keep.yaml` and the vminsert relabel rule are the platform's; see _Not tested_.

**Each language compiles and runs against a stub written from the goal; NOT TESTED against the real library.**
`tests/typecheck/<lang>/stub/` holds a minimal assumed interfaces API with just enough body to run the
consumers (the Bun stub has an `index.js`, run with the `node` that nixpkgs `tsc` wraps). The nix check:

- compiles and RUNS a good consumer: every valid call returns Ok and nothing is dropped;
- requires every planted `bad-*` consumer to fail to compile with the expected diagnostic;
- requires every planted `forge-*` consumer to compile, run without throwing, and show the port's refusal
  (`dropped.unknown_event`, `unknown_metric`, `unknown_key`, `unknown_value`, `register` or `poisoned`).

The same stubs support the 36 name-fuzz contract/language cases. The .NET harness builds three separate
assemblies, Obs, Signals and App; only Signals has access to the library's internal constructors.

**Forging: three layers (concept §3.4 "one hole per language").** With shared carriers in the library,
an app can make its own carrier in Go (exported constructors) and Dart (`extends` of a library base).

1. The type hole is kept small. Go carriers have unexported fields; only the constructors are exported.
   Dart bases are `abstract base class` (an app cannot `implements` them) or sealed (level markers), the
   level of `Event<L>` comes from `L`, the bound spec class is abstract (only the generated subclass is
   made), and the bases have no generic `add/record/set`: each generated metric class has its own method
   typed to its own labels class, so an upcast cannot reach a covariant check that throws. .NET is closed
   at compile time (internal constructors, friend access for Signals only).
2. A run-time guard in all four, which checks everything against the generated tables. Go's generated
   `init()` calls `obs.Register`; Bun's generated `signals.ts` calls `register(...)` when it loads, and
   `metrics(...)`, `logger(...)` and `tracer(...)` accept only those same table objects (any other table
   poisons the port, see below); Dart's generated `installSignals()` calls `DurianSignalPort.install`;
   .NET calls `Port.Install(SignalTables.Metrics, SignalTables.EventLevels)` (no one-shot rule there: .NET
   carriers cannot be forged, so a foreign table could only drop real signals, never admit a forged one). Each metric row carries its generated
   labels type (Go, Dart), the allowed values of each enum label and the min..max of each bounded label.
   The port refuses a name not in its table, a kind or level that differs, a labels value of another type
   (even with the same keys), keys that are not the name's keys, and a value outside the allowed values or
   range. Registration is once per process: Go `Register` (called by the generated `init()`), Bun
   `register` (called by the generated `signals.ts` when it loads) and Dart `install` (called by the
   generated `installSignals()`). Nothing guarantees the generated call comes first (an independent Go
   package's `init()`, a Bun module loaded before `signals.ts`, or app code before `installSignals()` can
   register a forged table earlier), so a competing registration, earlier or later, poisons the port (in
   Bun also a table passed to `metrics/logger/tracer` that is not the registered object): it is refused
   and counted (`dropped.register`), and from then on the port refuses every signal (`dropped.poisoned`)
   until restart. A forged table is never used silently; it is a loud telemetry failure, never an app
   failure. The lint rule forbids `obs.Register`, Bun `register` and `DurianSignalPort.install` outside
   generated files.
   The tables are deep-copied at registration (in Bun also frozen; the guard reads only that copy), so
   editing the exported generated tables later changes nothing. The port counts `dropped.<reason>`, returns an
   error and never throws or panics (the type is checked before any field is read; a panic or exception
   is recovered and counted as a last resort).
3. A lint rule in the template's CI (documented here, NOT TESTED; the lint config lives in the templates).

| Language | Hole the compiler leaves                                                                   | Run-time guard (tested)                                    | Lint rule (NOT TESTED)                                   |
| -------- | ------------------------------------------------------------------------------------------ | ---------------------------------------------------------- | -------------------------------------------------------- |
| Go       | `obs.New(Event\|Counter\|...)` from app code; zero enum, unset number                      | `Register`ed tables; empty/unset refused                   | `forbidigo` (below)                                      |
| Bun      | hand-written `{name, level}`; widened object with an extra key; `as` cast; a foreign table | tables registered by `signals.ts`; any other table poisons | no `as <generated type>`; no event object literal        |
| .NET     | `default` bounded number, `null!` enum                                                     | `Port.Install` tables; both refused                        | Roslyn analyzer for `default(Int<...>)`                  |
| Dart     | app class `extends` a library base; non-const `Shard(n)`                                   | `DurianSignalPort.install` tables                          | no `extends` of library bases; no non-const `Shard(...)` |

Lint rule text per language (to copy into the templates):

- **Go** (golangci-lint v2; generated files are skipped because of their header):

  ```yaml
  linters:
    enable: [forbidigo]
    settings:
      forbidigo:
        analyze-types: true
        forbid:
          - pattern: ^obs\.(New(Event|Counter|Histogram|Gauge|Span)|Register)$
            pkg: ^github\.com/ProtoDurian/diene\.go-interfaces/obs$
            msg: only obs gen output makes signal carriers; use the generated signals package
    exclusions:
      generated: strict
  ```

  Assigning to a generated exported variable stays a `forbidigo`/`gocritic` rule as in concept §3.4, and
  `depguard` denies `unsafe` in app packages (an unsafe-made enum value is refused at run time anyway).

- **Dart**: one analyzer-plugin rule (`analysis_server_plugin`): in a file whose first line is not
  `// Code generated by obs gen. DO NOT EDIT.`, flag `extends`, `implements` or `with` of any
  `package:diene_interfaces` type named `Event` or `DurianSignal*`, and any call of
  `DurianSignalPort.install/add/record/set`; also flag a non-const `Shard(...)` of a generated extension type.
- **Bun**: in files without the header, flag `as <generated type>`, an object literal passed where an
  `Event<...>` is expected (use the generated constant), any call of `register`, and a `metrics/logger/tracer`
  argument that is not the generated `metricTable`/`eventTable`/`spanTable`.
- **.NET**: a Roslyn analyzer for `default(Int<...>)`, `null!` of a generated enum class, and reflection
  on generated types (a reflection-made enum value is refused at run time by the allowed values).

**`out:` and orphans.** In a Git repository `out:` may point anywhere under the Git root (for example
`../shared/bun`); headers name the contract's repository-relative path and the orphan walk starts at the
Git root. An `out:` that leaves the Git root, or is absolute, is refused before anything is written; the
check is made on the written path and on its resolved path, so a symlink in `out:` cannot lead outside.
With no `.git`, `out:` may not use `..` or a symlink that leads out of the project folder, and the header's contract path is relative to the folder that holds
`observability/`, whatever the working directory (so `--check` from another folder sees the same headers).

Each `generate:` target accepts optional `interfaces:` to override its dependency import. Every name
in the last column is assumed, not in the goal; the interfaces nodes reconcile them:

| Language | Default `interfaces`                             | In the goal                                                                                                                                                                                                                         | Assumed, not in goal                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| -------- | ------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go       | `github.com/ProtoDurian/diene.go-interfaces/obs` | module `ProtoDurian/diene.go-interfaces`; `obs.Add`, `obs.Int[Spec]`, `obs.NewInt`, `obs.AtTime` with an error return; `obs.Error(ctx, Event[Error], ...)`                                                                          | the `/obs` package path; `IntSpec` `Name/Min/Max/Unit` (int64); `Int.Valid`; `Level` and the six markers; `NewEvent/NewCounter/NewHistogram/NewGauge/NewSpan`; `DurianSignal*` carriers and info rows (`Labels`, `Values`, `Ranges`); `Field`; `Record/Set/Warn/Info`; one-shot `Register` returning an error (a second one poisons the port), `DurianSignalTables`, `Dropped`                                                                        |
| Bun      | `@atomicloud/diene.interfaces`                   | `counter(name, labels)`; `log.error(ev, fields?, err?)`; `bounded(spec, n): Result`; `Tracer`/`Span`, `DurianSignalSpanTable`                                                                                                       | `register(tables)` (one-shot; a second one or a foreign table poisons the port); `metrics(table)`, `histogram/gauge`, `MetricTable` (with `values`, `ranges`); `logger(eventTable)`, `EventTable`, `Level`; `dropped()`; `tracer(table)`; `Bounded<N>`; a synchronous `{ok, value}` Result (the published Result is promise-based)                                                                                                                    |
| .NET     | `AtomiCloud.Diene.Interfaces`                    | `Port.Add(Names.X, new XLabels(...))`; `Int<Spec>.Of(n)` returning a `Result` (package `AtomiCloud.Diene.Result`: struct `Result<T, E>`, `Ok/Err`, `IsSuccess`); `Log.Error(Event<ErrorLevel>, ...)`; internal carrier constructors | namespace `AtomiCloud.Diene.Result` and `Value/Error`; `IDurianSignal*` interfaces, `Valid/Wire`; `AtTime`; `DurianSignalDate`; label attribute and info rows; `Port.Record/Set/Install/Dropped`; info row `Values`/`Ranges`; `Log.Warn`                                                                                                                                                                                                              |
| Dart     | `diene_interfaces`                               | `checkoutCharges.add(XLabels(...))`; `error(Event<ErrorLevel>, ...)`; enums; extension types; `shardSpec.of(n)` returning a `Result`; `entries` getter                                                                              | barrel `diene_interfaces.dart`; `DurianSignalWire/Labels/Metric`, carrier bases and constructors; `DurianSignalBoundSpec` and `atTime`; sealed level markers; `DurianSignalPort` (one-shot `install` returning a Result, a second one poisons the port, `add/record/set/dropped`); generated `installSignals()`; `warn/info`; info rows (`labels` Type, `values`, `ranges`); local `Result/DurianSignalOk/DurianSignalErr` in place of `diene_result` |

The compile fixtures model Result locally and do not validate integration with the separately published
result libraries. .NET name constructors require the real library to grant friend access to the
consumer's generated assembly. No real adapter, runtime sink or published package compatibility is claimed.

## Contract name checks in `obs query --static`

When `<dir>/contract.yaml` exists, every name a query reads must be in the contract or in the platform
label list (concept §2): `contract-undeclared-metric` (stored name, with "did you mean"),
`contract-undeclared-label` (selector and `by`/`on` labels; a label of another metric fails),
`contract-bad-enum-value` (an equality or plain `a|b` value that is not an enum value or is outside a
bounded number; a value that holds a dashboard variable is not judged),
`contract-pod-label` (a pod key on a business metric, §4.1), `contract-undeclared-event` and
`contract-undeclared-stream-field` (a stream tag outside the where-from set). Regex metric names (`{__name__=~"..."}`) are matched against the known names and a regex that matches none is
undeclared; regex label values must match an allowed value; label functions (`label_replace`, ...) must read
labels their input has. A metric label is compared in its stored spelling, dots
kept and quoted (`{"payment.provider"="stripe"}`, `by ("payment.provider")`); the underscore form of a dotted label is
`contract-undeclared-label` and the finding names the dotted spelling (a pod key in either spelling is
`contract-pod-label`). Other log fields are free-form (§6): not judged.

## Migrating from 0.4 to 0.5

All 0.4 commands and flags remain available. For the 58 cases in `tests/compat.sh`, `obs query --static`, `obs query --live`,
`obs alerts` and `obs links` match 0.4 stdout, stderr and exit codes byte for byte (golden files captured from the 0.4 binary).
`obs assert` and `obs scrape` keep their stdout and exit codes and add one `note:` line on stderr naming `obs live usage`.
`obs check` changes as listed below. `obs profile` and `obs store move` are unchanged. 0.5 adds the grouped commands:

| In 0.4                                        | In 0.5 |
| --------------------------------------------- | ------ |
| `obs query --static`                          | `obs check`: contract limits, evaluator versions and static query rules. See below for the empty-folder and reporting differences. |
| `obs query --live`, `obs alerts`, `obs links` | `obs test`: runs seeded query checks, alert evaluation when rules exist, and link checks when traces are present, declared or queried. It also checks named incidents for a passing `expect.fire` verdict. |
| `obs sandbox up`                              | For a dashboard review, `obs look --keep` starts or reuses a compatible sandbox and tells the reviewer to leave it running. Use `obs sandbox up` for a stack without a dashboard review. |
| `obs assert`                                  | Use `obs assert` to check a saved series export. Use `obs live usage` to inspect a running store, with a recent time window, contract-service filtering by default, and live budget checks. |

`obs scrape` still writes to the sandbox by default and accepts `--vm-url`. To inspect recent sandbox samples for the contract service,
run `obs live usage --target local` against that same sandbox project.

What is new in `obs look` and `obs live` for a caller:

- After selecting a dashboard, a successful `obs look` setup provisions the supplied rules in a sandbox Grafana folder, adds a native
  alert-state panel (`Sandbox alert state (Grafana live)`) when rules exist, and marks each loaded incident with a region annotation on the
  sandbox dashboards.
- A newly loaded default seed places the latest-ending named incident over the snapshot time when that window exceeds the longest `for:`
  duration plus two evaluation intervals among its expected-firing rules. Pass a nonzero `--seed-base` whose seed ends by the current time for
  historical replay.
- When the current incident has a forecast, the prompt treats `expect.fire` rules as expected Firing and the other alert rules as expected
  Normal, including quiet-only disturbances. It prints a predicted screenshot window only for a supported Firing forecast. The forecast covers
  up to ten minutes, and the panel shows the state Grafana actually reports (see _`obs look`_).
- At a fixed start time, an unchanged 0.4-compatible seed without `traffic.settings` generates the same baseline data. A seed can opt into
  `traffic.settings` (level, trend, cycle, spikes, gaps and noise, see _Seed shape_). A successful look seed load adds synthetic Pyroscope
  profiles and sends Tempo traces when the loaded snapshot contains spans.
- `obs live` is read-only. It resolves `--target` from `targets:` in `obs.yaml`, with a local sandbox fallback. `obs live usage` also reads
  optional `estimates:`. obs 0.4 commands that load `obs.yaml` reject either key, so upgrade those readers before adding the blocks (see
  _`obs live`_).

What is new in `obs check` and `obs test` for a caller:

- `obs check` runs the same static query rules as `obs query --static` and adds limits and version checks. It skips the `[empty]` rule for a
  folder with no alerts or dashboards, reports contract failures in the contract checks, and can report query findings alongside a version
  mismatch. Keep `obs query --static` if CI relies on its `[empty]` failure or its own command output. It takes `--names`, `--event-rule`,
  `--vl-settings`, `--tempo-config` (new) next to `--contracts`.
- With a valid local contract, `--contracts none` restricts metric lookup to that contract and its platform metrics; an explicit metric
  declared only by another contract fails. With a valid local contract and `--contracts auto` (the default), obs includes the other regular
  `observability/contract.yaml` files found by discovery below the Git root, outside ignored and fixture directories.
- For an existing folder with no `contract.yaml`, `obs check` prints a note, skips limits and still checks queries (0.4 exited 2). On success it
  prints `obs check: ok: <contract status>; queries: <query totals>`. The contract status is the counts and hash when a contract exists, or
  `no contract.yaml, limits not checked` when it is absent. Exit-1 summaries use `obs check: FAILED:` and list the failed checks. Usage and input
  errors can end earlier with exit 2 and a diagnostic.
- For judged incidents, `obs test` reports `[incident-uncaught]` unless at least one alert listed in `expect.fire` passes its fire verdict. An
  incident with a nonblank `disturbance:` reason and no fire expectations is exempt from this coverage rule. Other alert, query and link checks
  still apply (see _Every incident is caught by at least one alert_).

## Migrating from 0.3 to 0.4

- `obs gen` now also writes `contract/keep.yaml` (the collector keep-list) next to the other generated files. After
  upgrading, `obs gen --check` fails `[gen-missing] contract/keep.yaml` in every folder with a contract until
  `obs gen` is run once and the new file committed.
- `obs check`, `obs query` and `obs alerts` load every `contract.yaml` in the repo; two contracts that claim the same
  stored metric name now fail `[metric-clash]`. `obs check` and `obs query` take `--contracts none` to keep the old
  single-contract behaviour; `obs alerts` has no such flag yet. Limits: discovery only selects other
  `observability/contract.yaml` files below the Git root (not under `fixtures` or `testdata`), and `obs query` and `obs alerts` skip it
  when the selected directory has no `contract.yaml` of its own, so a clash with another contract can go undetected there. A directory
  discovery cannot read fails `obs check`, `obs query` and `obs alerts` (`contract discovery: cannot read <path>`) instead of being skipped.
- Contract entries may carry `source:`, `estimate`, `follows:` and `used_by:` (see the contract section). Nothing
  changes for a contract that uses none of them.
- **Stored names of own metrics changed in one case; re-check dashboards and alerts.** A name that already holds the unit word
  as a part now stores without a second suffix, as VictoriaMetrics' converter does: an own counter `x.bytes.sent` with unit `By` used to
  resolve to `x_bytes_sent_bytes_total` and is now `x_bytes_sent_total`. This is so even when the contract uses none of the new fields, and
  `obs query --static` (also with `--contracts none`) now rejects a selector that still uses the old name. Check every dashboard and alert
  query against the names the regenerated `contract/keep.yaml` lists. Regenerating `keep.yaml` does not change your queries.

## Migrating from 0.2 to 0.3

obs 0.2 stored seeded VictoriaMetrics labels with dots turned into underscores and accepted both spellings in
checks. Concept §2.1 says VictoriaMetrics keeps the dots and a query quotes them, so obs now follows the concept.
This is a correction to the standard, not a flag or exit-code change, so it ships as a minor release. A dashboard,
alert or TraceQL query written with the underscore spelling of a dotted label now fails, once, with the fix in
the message:

| Where it fails                   | The message names                                                                                                                                                                | Fix                                                      |
| -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------- |
| `obs query --static`, a selector | `[contract-undeclared-label] label "durian_cloud_service" ... write "durian.cloud.service"`                                                                                      | `{"durian.cloud.service"="checkout"}`                    |
| `obs query --static`, a grouping | the same finding for the `by (...)` / `on (...)` label                                                                                                                           | `by ("durian.cloud.service")`                            |
| `obs query --static`, a variable | `variable $provider: [contract-undeclared-label] label_values reads label "payment_provider"`                                                                                    | `label_values(checkout_charges_total, payment.provider)` |
| `obs query --live`               | with a contract: the same `[contract-undeclared-label]` finding, once; with no contract: `[label-spelling] VictoriaMetrics keeps a label's dots: label "x_y" is stored as "x.y"` | write the dotted name, quoted in a selector              |
| TraceQL attribute                | `[traceql-attribute] undeclared attribute span.payment_provider (Tempo keeps the dots: write "payment.provider")`                                                                | `span.payment.provider`                                  |

- A pod key on a per_pod or platform metric is a label too: `k8s_pod_name` fails the same way, naming `k8s.pod.name`.
- Pyroscope labels do not change: they stay underscores.
- **Alert templates** (annotations, labels) must use `{{ index $labels "durian.cloud.service" }}`: the dotted path
  `$labels.durian.cloud.service` is a template error and the underscore form renders empty. (From review of the Grafana
  template rules; obs does not render templates.)
- **Trace panels now run under Tempo's query limits, even with no `obs.yaml`.** `obs query --live` applies
  Tempo 3.1.1's defaults (30 s end cutoff, 24 h maximum duration, 10000 intervals) or the `thresholds.traces`
  values in `obs.yaml`. A TraceQL metrics panel longer than the maximum duration or with more intervals than the
  limit now fails as it would in Grafana against real Tempo; a panel ending inside the end cutoff is not a failure
  (the end is clamped, as Tempo does). Raise the limit in `obs.yaml` to match the deployed Tempo.
- **An old sandbox must be recreated.** A sandbox made before this release has a VictoriaMetrics without the
  relabel config; `obs sandbox up` refuses it, naming `-relabelConfig`. Run `obs sandbox down` and then
  `obs sandbox up`.
- NOT TESTED: `label_values` with a quoted dotted label (`label_values(m, "payment.provider")`) in Grafana itself; obs
  accepts both quoted and bare, but only the bare form is what the fixtures use.
- The production landscape's current charts (its main, release 1.5.0, `scripts/ci/obs-query.sh`) pass with this obs: 6 charts, 0 failures.
  They have no `contract.yaml` and use no `durian.*` label, so nothing in them was judged for spelling.

## `obs sandbox`: the local stack, for people

```text
obs sandbox up     [--dir observability] [--seed seed.yaml] [--project name] [--port-base n] [--runtime docker|podman] [--reseed]
obs sandbox down   [--dir observability] [--project name] [--runtime docker|podman]
obs sandbox status [--dir observability] [--project name]
```

`up` starts five containers (docker, or podman when docker is not there; `--runtime` or
`$OBS_CONTAINER_RUNTIME` chooses) and loads them: the repo's dashboards into Grafana, the seed into
VictoriaMetrics and VictoriaLogs. This legacy command keeps its 0.4 ports, container
configuration, loaders and output. `obs look` additionally seeds Tempo and Pyroscope
(below). No check depends on a sandbox; it is for looking at the data.

| Container       | Image                                                     | Host port (default) |
| --------------- | --------------------------------------------------------- | ------------------- |
| Grafana         | `grafana/grafana:13.3.0` + VictoriaLogs datasource 0.34.0 | 3000                |
| VictoriaMetrics | `victoriametrics/victoria-metrics:v1.156.0`               | 8428                |
| VictoriaLogs    | `victoriametrics/victoria-logs:v1.54.0`                   | 9428                |
| Tempo           | `grafana/tempo:3.1.1`                                     | 3200                |
| Pyroscope       | `grafana/pyroscope:2.4.3`                                 | 4040                |

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
  18301 to 18304 when the defaults are taken. `obs look` additionally publishes
  Tempo's OTLP/HTTP receiver at 4318 by default, or base+5 (18305 here). Reserve six
  consecutive ports for look: a second sandbox at base+5 would clash with that receiver.
- **Data.** The seed's time line ends now, so a "last hours" view has data. Grafana is open to an anonymous
  viewer (admin/admin for the API). The datasource uids are the ones the dashboards name (`fixture-metrics`
  and so on); a kind no dashboard names gets `obs-metrics`, `obs-logs`, `obs-traces`, `obs-profiles`.
  A second `up` reuses the running stack, loads the dashboards again and keeps the seed (`--reseed` loads it
  again). With `obs look`, Tempo receives OTLP/HTTP JSON with the seed's original trace/span IDs, attributes,
  status and parent links; the log-to-trace IDs stay the same. Pyroscope receives three
  deterministic CPU pprofs per service/span name across the seed window: stacks are
  `main -> obs.seed.work -> <span name>`, with 10/20/30 ms CPU samples and `span_name`
  labels. Without traces the synthetic span is `obs.seed.work`. These are illustrative
  profiles, not measured application performance. A partial-success span rejection fails setup.
- `up` needs the network the first time (image pulls; Grafana downloads the datasource plugin).

## `obs look`: dashboard screenshots, reviewed by the calling agent

```text
obs look [--base origin/main] [--all] [--file dashboards/x.json] [--checklist docs/standards/grafana-dashboards/index.md]
         [--out obs-look] [--width 1600] [--height 1000] [--json] [--keep] [--seed-base unix] [sandbox flags]
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
   Panels are marked "expected empty" only when their signal has no loaded seed;
4. prints the items of the visual checklist (the bullets under the "Visual checklist" heading of the
   `--checklist` file) and the layout of the one pull request comment the agent writes: the images and a
   verdict (pass, fail, unclear) with one line of evidence per item.

The agent opens each URL in a browser, takes the screenshots, judges them and posts the comment. `--json`
prints the same content (`ok`, `error`, `warnings`, `sandbox`, `dashboards`, `checklist`, `prompt`).

`look` applies every named incident's changes using the same generator as `obs alerts`,
on the displayed seed timeline. Overlapping incidents compose in definition order.
It provisions the original alert queries, intervals, pending/recovery durations and
NoData/Error handling into a sandbox-only Grafana folder, and adds a region annotation
for each incident on each dashboard. The built-in annotation query is enabled, and a
`Sandbox alert state (Grafana live)` panel shows native Firing, Pending and Normal state
from Grafana, including inactive rules. The prompt names the marker and expected states
per dashboard.

By default the latest incident window covers now and opened at least `for:` plus two
evaluation intervals earlier. Only the seed prefix through now is loaded; future samples
and log rows are removed, and spans crossing now are shortened. `--seed-base <unix
seconds>` instead keeps an explicit historical timeline; `--base` remains the git ref
used to find changed dashboards. A seed with no incidents has no expected incident Firing.

Grafana starts pending timers when its scheduler begins evaluating, so the prompt lists
each live rule's provision time, expected Firing time (a scheduled estimate: `provisioned + for` rounded up to whole evaluation intervals `+ one interval`: for 15s at 10s it is 30s, for 1m at 10s it is 70s; Grafana staggers its evaluations inside each interval, so the real change can come a few seconds later; the alert-state panel does not poll (it only refetches when the dashboard refreshes; obs does not set a dashboard's refresh, so a board without its own auto-refresh never does), so the prompt tells the agent to reload the same URL every interval (unless the board sets its own auto-refresh) from the estimate until the printed hold time, screenshot the first load that shows Firing, and if Firing was never seen record the state it observed on each reload as the finding),
and the predicted window before the fixed snapshot ages out. The prediction uses the
same existing evaluator/state machine, projecting at most ten minutes without pushing
future data. A snapshot that cannot hold the condition through the pending duration is
called out as a finding. The calling agent screenshots during the listed window and
checks Grafana's actual state; `obs look` does not wait for pending timers.

Historical state-change annotations explicitly say `replay (computed by obs)` and name
the incident scenario. Each incident is evaluated independently by the existing `obs alerts`
state machine, even when incident windows overlap; a simultaneous change cannot erase another
scenario's firing history. The stores hold the composed timeline and its recording samples;
the native-state forecast uses that actual composed snapshot. Replay is not Grafana's own historical state. Incident
marker text labels declared expectations separately. Grafana's historic `/api/v1/eval`
can also inspect the real condition at an incident time.

The stack stays up for the calling agent's browser after the command exits. By default
the prompt asks that agent to stop it after review. `--keep` instead asks the agent to
leave it running for a person; the prompt includes the exact cleanup command. `sandbox
up|down|status` remain available. When reusing a baseline-only seed from `sandbox up`,
`look` asks for `sandbox down` first, so baseline and incident logs cannot be mixed.
Stacks started by `sandbox up` or an older obs, without the published Tempo receiver, get a recreate-first
diagnostic. A repeated default look on an expired live snapshot recreates only that
project's labelled containers, volumes and network and says it did so; it never prunes
or touches another project. So does a look after a seed load that stopped part way (say Tempo failed after the metrics and logs went in): that load is recorded as incomplete (written before anything is uploaded; a look that cannot write it stops, and the record stays until the stores are removed) and the next look clears the stores before loading again; a completed snapshot is reused. One setup or removal of a project runs at a time: `obs look`, `obs sandbox up` and `obs sandbox down` take one lock per project in one fixed place, `/tmp/obs-sandbox-<project>.lock` (read from no environment variable, so the same for every cache folder and TMPDIR; obs creates no folder and changes no permissions there, and opens the lock without following a symlink, without blocking, and only as a regular file), and a second one for the same project while the first runs is refused at once with an error naming the lock, so it never removes stores a running load is writing to. Different projects run side by side. A lock file another user created is locked read-only; if the lock cannot be taken at all, obs refuses before touching any container. A reused snapshot keeps its Firing forecast only while the checkout's rules are the ones it was computed for; after a rule changed, the forecast is dropped (Grafana's own state is still shown). A retained stack's sandbox folder is reconciled on every look: rule groups of an earlier checkout that the checkout no longer has are deleted (all of them when it has no rule left), groups in any other folder are left alone. If the live incident window ended while the setup ran, or before a reused snapshot was reached, the look does not call it current (no live incident, no forecast) and keeps the incident list as historical guidance. Explicit `--seed-base` historical snapshots remain reusable.

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
labels as `durian_cloud_*` (concept §2): pass `--label durian_cloud_landscape=local` and so on (a vminsert labelmap turns
them into `durian.cloud.*`; the sandbox VictoriaMetrics has the same labelmap, see "Label spelling per store"). `--once`
scrapes once and exits 1 when it failed; otherwise it runs until Ctrl-C and keeps going after a failed
scrape.

## `obs live`: read a running stack and compare it with the repo

`obs live usage|shape|incidents|look --target local|dev|stage|prod` pulls numbers from a running
stack (the local sandbox, or a real cluster) and compares them with what the repo declares. obs
pulls and compares; the calling LLM (or a person) judges. `--json` prints the same content for an
LLM, the default is a table.

**Read-only guarantee.** obs live sends only `GET`. Its direct request paths are `/api/v1/query`, `query_range`, `series` (VictoriaMetrics),
`/select/logsql/stats_query` (VictoriaLogs), `/api/metrics/query_range` (Tempo), and
Grafana's `/api/health`, `/api/search`, `/api/annotations`. A redirect is followed only within the same scheme, host and target path
prefix, and only to a path the guard allows, so an allowed redirect can reach a few more read endpoints (such as `status/tsdb`). The guard
would also let a `POST` through to the store query paths, never anywhere else. Every request goes through one guard
(`internal/live/client.go`, `ReadOnly`) that refuses any other method or path before the network is
touched. The guard checks the URL as it will be sent (not only the path argument): the target address must be
a plain `http://` or `https://` address with no user name, password, query string or fragment, and the client
runs the guard again on every redirect hop and refuses a hop that leaves the target's host (Go would forward
custom headers such as `X-Scope-OrgID` to another host). The Go tests run every command against fake stores that
record each request and assert, against a list of allowed methods and paths the test writes out itself (not
derived from the guard), that nothing else arrived. obs installs nothing into a real cluster: dashboards get
there only through the platform chart.

### Targets in `obs.yaml`

```yaml
targets:
  prod:
    metrics: https://vm.prod.example # VictoriaMetrics or Prometheus query API
    logs: https://vl.prod.example # VictoriaLogs
    traces: https://tempo.prod.example # Tempo
    grafana: https://grafana.prod.example
    headers: # request headers; the file names the ENVIRONMENT VARIABLE, never the value
      Authorization: OBS_PROD_AUTHORIZATION # the variable holds the whole value, e.g. "Bearer ..."
      X-Scope-OrgID: OBS_PROD_ORG

estimates: # optional: expected log and trace volume (the contract only holds series budgets)
  logs: { lines_per_min: 600, bytes_per_min: 120000 }
  traces: { spans_per_min: 3000, bytes_per_min: 900000 }
```

- A target name is anything (`local`, `dev`, `stage`, `prod`, `eu-prod`); each store URL is optional, a command that needs a missing one says which key to set.
- A header value that is not a bare environment variable name (a token written into the file) is refused, naming the key and never printing the value. A name that looks like a credential (it starts like a token: `glsa_`, `glc_`, `ghp_`, `github_pat_`, `AKIA`, `eyJ`, and so on) or is longer than 64 characters is refused the same way, because such a string passes for a variable name and would otherwise be echoed in "variable X is not set". A variable that is not set is an error naming file, line and header; the error never prints the variable name either, because a supplied value that passes the heuristic could still be a secret. A redirect refused by the client says "redirect refused" and the host at most, never the Location's path or query.
- A store URL with a user name or password (`https://user:pass@host`, which Go turns into Basic Auth), a query string or a fragment is refused, naming the key and not repeating the URL. A path prefix (a Grafana under `/grafana`) is fine.
- `--target local` needs no entry: it reads the running sandbox (`--project name` picks which; the ports it holds), else the default sandbox addresses (`127.0.0.1` ports 8428, 9428, 3200, 3000). A key written under `targets.local` wins.
- `obs check`, `obs query` and the other commands load a file with these two blocks without complaint; only `obs live` reads them. `estimates:` fills the "vs contract estimates" column; without it the column says "no estimate".

### `obs live usage`

Active time series (ATS) and data points per minute (DPM) per metric, against the contract's worst case
and budget; log lines/min, message bytes/min and streams; spans/min and bytes/min. It replaces `obs assert`
and `obs scrape` as the recommended command (`assert` keeps working, with one stderr note).

- **It reuses `obs assert`.** The series are read once (`/api/v1/series`, the series with a sample in `--window`) and counted by the code behind `obs assert` (`internal/assert`): per `durian.cloud.landscape`, a histogram's `_bucket`, `_sum` and `_count` together, a per-pod metric up to worst case x `--pods`. The budget is per landscape, so a store holding two landscapes is compared one landscape at a time (the row shows the fullest and lists each). `assert`'s label checks run too and show under FINDINGS: a label that is not in the contract, a pod label on a metric that is not `per_pod`, a value outside its type. `obs assert` keeps its stdout and exit codes; it adds one `note:` line on stderr naming `obs live usage`.
- **Both thresholds, for series and for points/min.** Each row shows the series and the points/min next to the worst case and the budget of each (a metric without a budget is compared with its worst case). Points/min are measured over `--window` (default 5m): the worst case times 60 / the contract's aggregation interval is the worst-case rate, the budget times the same is the budget rate. A row above the worst case but inside the budget says "above worst case"; above the budget (series or points/min) says "OVER BUDGET" and why.
- **Rows are ranked by the larger of the two deviations** (series / limit and points/min / limit, limit = budget else worst case): over budget first, then metrics that are not in the contract (the fullest first), then above the worst case, then the rest, platform metrics last. `--top N` keeps the first N, `--over-only` hides the rows that are fine.
- **An undeclared metric has no budget.** `--unknown fail` (the default) counts every series of a metric that is neither in the contract nor in `platform_metrics` as over budget (exit 1); `warn` shows it and exits 0; `ignore` hides it.
- Only the contract's service is counted (`"durian.cloud.service"="<service>"`); `--all-services` counts the whole target. Point the target at one landscape's store: the budget is per landscape.
- **The points/min query runs on Prometheus and on VictoriaMetrics.** `count_over_time` drops the metric name in both, and `keep_metric_names` is MetricsQL only (Prometheus rejects it), so obs sends one query per stored metric name: `sum by ("durian.cloud.landscape") (count_over_time({__name__="<name>","durian.cloud.service"="<service>"}[<window>]))`. The Go tests parse every query with the Prometheus parser and with MetricsQL. A name whose query fails says so on its row; if every name fails the command exits 2.
- Logs come from VictoriaLogs `stats_query` (`count()`, `sum_len(_msg)`, `count_uniq(_stream_id)`); lines and bytes are compared with the `estimates:` of the service and the streams with the contract's worst case **only for the service's own read**. The log streams are read per landscape (`stats by ("durian.cloud.landscape")`) and the fullest landscape is compared with the worst case of one landscape (its modules), like the series budget; lines and bytes are summed. With `--all-services` the whole target is counted and nothing is compared with a service's figure.
- Spans/min come from Tempo's TraceQL metrics endpoint when it answers (this needs Tempo's local-blocks processor), filtered to the service. Tempo's own counters in the metrics store (`tempo_distributor_spans_received_total`, `..._bytes_received_total`) cover every service, so they are read only with `--all-services`, and are never compared with a service's estimate; for one service, when Tempo's metrics endpoint does not answer, spans/min and bytes/min say "not available for one service" and print no number. The row names its source, or why it is not available.
- **Exit 1** when a row is over its budget, an undeclared metric is found (default), a label check of `assert` fails (a label outside the contract that varies, a pod label on a metric without `per_pod`, a value outside its type), or the log streams of a landscape are over the worst case; the last line says so. A series count above the worst case but inside the budget is reported as a FINDING (`series-over-worst-case`, the row says "above worst case") and does not fail the run: only the budget and the label checks do. 2 on a usage or input error, including a target that cannot be reached.

### `obs live shape`

Per contract metric over `--window` (default 7d, step `--step`, default 5m): the real data and the seed side by
side, in five parts plus noise, as `seed.ShapeSettings`. It never fails: the
LLM reads the two columns and judges if the seed is realistic.

| Part   | Field                                                                  | Meaning                                                                                               |
| ------ | ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| level  | `level.min`, `typical`, `max`                                          | lowest, median and highest value                                                                      |
| trend  | `trend.change_per_day`                                                 | least-squares change per day as a fraction of typical (0.03 is up 3 % a day); needs 2 days            |
| cycle  | `cycle.daily_amplitude`, `peak_hour`, `weekend_factor`                 | half the peak-to-trough of the average hour of day over the mean; UTC peak hour (the first harmonic of the sample timestamps, to a tenth of an hour); weekend / weekday (7 days) |
| spikes | `spikes.count`, `size`                                                 | separate upward jumps far above that hour's typical value; the biggest as a multiple of typical       |
| gaps   | `gaps.count`, `length`                                                 | runs of two or more steps with no data (and data that stopped early); total missing time              |
| noise  | `noise`                                                                | the wiggle left after the hour shape, as a fraction of typical                                        |

- **Timing.** The peak hour comes from the samples' own timestamps (a cosine that peaks at 20:00 UTC sampled every 5 minutes from :04 reports 20, not the clock hour 19 whose samples sit on one side of it). Gaps are counted before the first sample and after the last one as well as between samples: both window bounds are passed, so a 6 hour window on a store that holds 4 hours says "1 gap, 2h". What a window can show is judged on the window asked for: trend and daily cycle need 2 days, the weekly part 7 days, and the samples of a 7 day window at a step span 7 days minus one step, so the test is span >= window - 2 steps (a 7 day window with 3 days of data shows no weekly part).
- The metric is read as one number: a counter as the summed rate per second, a histogram as the summed rate of its observations (`_count`), a gauge as its summed value. The same query runs on the real store and on the seed.
- The seed column is the seed generator's own data (`seed.Build`) run through the embedded VictoriaMetrics with the same query, over the seed's length: a seed of 4 h cannot show a trend or a daily cycle, and the cell says so (`n/a`). Both columns are the same Go type, `seed.ShapeSettings` (the five parts a seed's `traffic.settings` is written with, plus the flags for what a short window cannot show), so a measured column can be pasted into a seed and read back with the same names; a test pins that the real and the seed column are one type.

### `obs live incidents`

Firing observations (or the grouped ALERTS firing list) on the target in `--since` (default 7d) plus windows you name with `--window from..to`
(RFC 3339, `2026-11-10`, `2026-11-10T14:30`, unix seconds or `-6h..-2h`; repeatable), compared with the repo's
incident files, alert rules and runbooks.

- **Source, and why.** Grafana's alert state history (`/api/annotations?type=alert`) when the target has a `grafana:` URL: Grafana-managed rules can publish transitions there when annotation history is enabled; publication is asynchronous and history can be retained or kept on another backend. These rules do not write an `ALERTS` series (only a vmalert-style ruler does). Without a Grafana URL, or when Grafana refuses, the `ALERTS{alertstate="firing"}` series in the metrics store is used; the output says which and why.
- **State and identity.** Rules are identified by explicit UID or Grafana's numeric `alertId`; numeric IDs are reconciled with explicit UID evidence across titles. A repo title match requires a unique title in both history and the repo, with no conflicting UID evidence. `Recovering` stays firing. Optional lossless `labels`/`data.labels` maps allow spans to be reconstructed from returned transitions and merged per rule, but cannot prove history is complete. If an entry's previous state disagrees with the reconstructed state, its span becomes unknown and only the direct firing observation is reported, with real observation timestamps. [Grafana 13.3.0's annotation source](https://github.com/grafana/grafana/blob/v13.3.0/pkg/services/ngalert/state/historian/annotation.go) supplies lossy label tags (colons normalized and long tags omitted), so its tag-only history reports firing observations with **unknown spans**, duration and firing count. Annotation text is not parsed as an identity.
- **Named windows: observations, never a miss verdict on any source.** The history is read from 7 days before the requested range through now. Each named window reports `caught` only when firing was directly observed in it, or `not_seen` with a reason; reconstructed spans do not decide window coverage. Grafana writes annotations asynchronously; history can be elsewhere or off, retained, or inaccessible. An empty answer means `no alert state history returned; cannot tell quiet from history not kept in annotations`. A response reaching the 10,000-entry limit is incomplete. Grafana 13.3's tag-only history proves firing observations with unknown spans, and even lossless history cannot prove a miss. The `ALERTS` fallback keeps the lookback-wide firing list grouped by alert name and rule UID. For entries seen in that grouped list, `first`/`last` are query evaluation times, labeled `time_source: query_evaluation`; they are not exact firing timestamps or named-window evidence. A rule found only in raw samples has `time_source: raw_sample` and raw first/last sample times. Only named windows read underlying samples, in chunks of at most one hour, through `GET /api/v1/query` with an `ALERTS{alertstate="firing"}[duration]` range selector ([pinned VictoriaMetrics source](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.156.0/app/vmselect/prometheus/prometheus.go#L825)), preserving millisecond sample timestamps. It never treats carried-forward `query_range` evaluation timestamps as firing times. These raw samples are observations with unknown continuous spans, duration and firing count; JSON preserves them under `observations`. For a completed window, a chunk exceeding the 64 MiB response limit leaves that window `not_seen` with reason `raw ALERTS samples too large to read`, while the report still exits 0. With no named windows, no raw samples are read. The fallback can report firing samples but cannot see Grafana-managed firing, so an empty fallback is also `not_seen`, never a miss. obs makes no current-state GET. A future-ending window is `not_yet_observable`; text shows only that status and any partial pre-cutoff evidence, and JSON puts that evidence in `partial_fired` rather than `fired`. If a later raw chunk exceeds the response limit, the future window keeps verified firing from earlier chunks in `partial_fired` and shows both the cutoff and size reasons.
- **fired-but-no-incident:** firing was observed and no incident file lists the rule under `expect.fire`. **incident-not-alerted:** repo incident-file configuration gaps only (no declared catching rule or only unknown rule references); live windows never populate this field. **alert-without-runbook:** a rule with no `runbook_url` annotation (marked when firing was observed).
- **No capture command or suggested incident entries.** The report lists observations and repo configuration gaps; it produces no missed-window verdict or suggested `incident.yaml` entry on any path. An LLM can use the observations to write an incident and its alert by hand in a pull request, then run `obs test`.
- **Exit codes:** 0 when a report is produced, including empty, incomplete and not-yet-observable windows. There is no exit 1 for unobserved firing on any source. Exit 2 means invalid flags/input/configuration, or a source read failure that prevents producing the report.

### `obs live look`

Lists the dashboards the target's Grafana already has, with addresses (last 6 hours of real data) and the visual
checklist prompt, like `obs look`. It installs nothing. A dashboard that is not deployed yet is checked with `obs look` on the seed, or by
deploying the chart to the local cluster and using `--target local`. Exits 0 always.

### Migration rows

| 0.4                  | 0.5                                                                                                                     |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `obs assert`         | `obs live usage --target local\|dev\|stage\|prod`: reads the series counts from the store itself; `assert` still works |
| `obs scrape`         | still feeds the sandbox; read what it stored with `obs live usage --target local`                                       |

The two old commands print one note on stderr naming the new one; their stdout is unchanged.

### What was measured

- **MEASURED** (Go tests, `internal/live`): every command against fake HTTP stores that record each request and assert it against an allowlist of methods and paths the test writes out itself; the read-only guard, redirect hops and target URLs; the targets and token rules; every shape part on synthetic series (peak hour, leading and trailing gaps, the weekly part); incident matching on a temp repo folder, including an alert that fired before the window; every metrics query parsed by Prometheus and by MetricsQL.
- **MEASURED** (recorded responses, `internal/live/testdata/recorded/*.json`): the real answers of a local sandbox (VictoriaMetrics v1.156.0, VictoriaLogs v1.54.0, Tempo 3.1.1, Grafana 13.3.0), recorded for the API reads of usage, usage with `--all-services`, shape, incidents and look listed in those fixture files; the incident recording contains only `/api/annotations?type=alert`, not a current-state rules/alerts GET, replayed by `recorded_cmd_test.go`. The sandbox was set up by `obs look --keep` of part B (`origin/obs-regroup-look`) on `tests/fixtures/live-recorded` (the `look-seeded` fixture: a seed with traces and a live incident that makes `payment-errors` fire in Grafana), so the recorded Grafana state history holds a real Normal -> Alerting change, and Tempo answered the TraceQL metrics query. Then `obs sandbox down`. The recordings were made before the fixture seed gained `traffic.settings` (level, daily cycle, noise): the recorded real column is the older flat seed, the seed column is computed from the current `seed.yaml` at test time, so the two columns differ on purpose (as real data and a seed do). Recording again makes the real column follow the settings.
- **MEASURED** (one real run of the built binary against that sandbox, 18 Nov 2026): `obs live usage|shape|incidents|look --target local`. The numbers it printed: 2 series in landscape `fixture` against a worst case of 2 and a budget of 4; 13.2 points/min against 4 (worst case) and 8 (budget): **OVER BUDGET, exit 1** (the sandbox seed writes a point every 15 s and the contract's 30 s interval allows 4 a minute per two series, plus the incident's extra samples); logs 120 lines/min and 1 stream; 416 spans/min from Tempo's metrics endpoint; shape of the real series against the seed side by side, with the 7 day window's missing first 167h55m counted as one gap; the then-built incidents command listed `payment-errors` (2 firings) and `payment-pending` (1), both expected by the `payment-spike` incident and both without a runbook; look listed the one dashboard. Those historical firing counts are not claims about the current command: replaying the saved tag-only response now reports one observed `payment-errors` firing transition with unknown spans and no definite duration or firing count; `payment-pending` is only Pending in that recording.
- **NOT TESTED**: `dev`, `stage` and `prod` (no access); see *Not tested*.

### Recording the fixtures

The recorded files are replayed without a network. To record them again (after a query changed: a request that is not in the file fails the test and names it): build `obs` from `origin/obs-regroup-look`, make a git repo of `tests/fixtures/live-recorded`, run `obs look --dir . --all --keep --reseed --project livesrec --port-base 24000` there (docker; hold the heavy lock, at most 10 minutes), wait for the alert to fire (`payment-errors`: about 10 seconds after the load), then

```sh
OBS_LIVE_RECORD='metrics=http://127.0.0.1:24001,logs=http://127.0.0.1:24002,traces=http://127.0.0.1:24003,grafana=http://127.0.0.1:24000' \
  go test ./internal/live -run TestRecorded
obs sandbox down --dir . --project livesrec --runtime docker
```

Each test starts four proxies in front of the real stores, runs its command through them with the clock it saves (`now_unix`), and writes the exchanges to `testdata/recorded/<scenario>.json`. Update the numbers the replay tests assert after recording again.


## Engines and pins

| Signal   | Engine or reference                                        | Version                          |
| -------- | ---------------------------------------------------------- | -------------------------------- |
| metrics  | VictoriaMetrics (`metricsql`, storage and evaluator)       | v1.156.0                         |
| logs     | VictoriaLogs `lib/logstorage`                              | v1.54.0, tag commit `6433dae5`   |
| traces   | Tempo (TraceQL parser, evaluator and frontend rules)       | 3.1.1, tag commit `9db7df79d2af` |
| alerts   | Grafana (alert math and state machine, ported)             | 13.3.0, tag commit `752e3462`    |
| profiles | Pyroscope (reference: sandbox and parity only, not linked) | 2.4.3                            |
| agent    | Grafana Alloy (reference: pin only, no image is run)       | v1.21.3                          |
| logs UI  | VictoriaLogs Grafana datasource (reference, parity only)   | v0.34.0                          |

The first four are engines linked into the binary; the last three are references only.
`obs version` prints every pin, with `engine` or `reference` in front of it, and
`obs engines --check-chart` compares the charts with them.

Each equals the version in the charts (concept §1). `obs version` prints them.
No Loki module is in `go.mod`.

## obs engines --check-chart

```text
obs engines --check-chart <chart folder>
```

Compares a Helm chart folder with the pins in `src/internal/pins/pins.json` (the same ones
`obs version` prints), so a pin that moves without the chart, or a chart that moves without
the pin, is a red check instead of a silent difference. It reads two files and never touches
the network.

`values.yaml`: every image of an engine, anywhere in the file, in these shapes:

- a map with `repository` and `tag` and/or `digest`; the engine is chosen by the last path
  segment of the repository: `victoria-metrics`, `vmstorage`, `vminsert`, `vmselect`,
  `vmagent`, `vmalert`, `vmauth` (VictoriaMetrics), `victoria-logs` (VictoriaLogs), `tempo`,
  `grafana` and `pyroscope`; other images (such as `grafana-image-renderer`, or a sidecar
  inside the Grafana values) are ignored;
- one of the engine's own `image` maps with no repository beside it, inside the values of a
  `Chart.yaml` dependency that is an engine chart, found by its `alias`, else its `name`
  (`victoria-logs-single.server.image.tag`). Only these paths below the dependency key count
  (`internal/chartcheck` `ownImage`): `image` (grafana, victoria-metrics-agent, and tempo and
  pyroscope beside `tempo.image` / `pyroscope.image`), `tempo` for the tempo chart (its image is
  `tempo.repository` and `tempo.tag` upstream, grafana-community/helm-charts `charts/tempo/values.yaml`
  lines 26-30, so a dependency named tempo is checked at `tempo.tempo.tag`), `server.image` (victoria-metrics-single
  and -alert, victoria-logs-single), `vmstorage.image`, `vmselect.image`, `vminsert.image`
  (victoria-metrics-cluster), `vlstorage.image`, `vlselect.image`, `vlinsert.image`
  (victoria-logs-cluster) and the `vmcluster`, `vmsingle`, `vmagent`, `vmalert` `spec` images
  of victoria-metrics-k8s-stack. Any other image map with no repository (`grafana.sidecar.image.tag`,
  a renderer, an init container) is not the engine and is never compared; with a repository
  that is not an engine it is ignored the same way;
- one string under an `image` key: `repo[:tag][@sha256:...]`. The tag starts after the last
  `:` that follows the last `/`, so a registry port (`myreg.io:5000/grafana/pyroscope:2.4.3`)
  is not a tag.

`Chart.yaml`: which engine charts the folder depends on, and `appVersion` when the chart
itself is an engine chart. A dependency's `version` is a chart version, not an application
version, so it is never compared with a pin.

A leading `v` is not a difference (`v1.156.0` and `1.156.0` are equal), and VictoriaMetrics'
`-cluster` image suffix is accepted. A digest is compared with the digest recorded for the
engine in `pins.json` (the same digests as `parity/images.json`, which `parity/check-pins.sh`
keeps equal): a container runtime pulls the digest when there is one, so a different digest is
drift even behind the right tag. An image pinned **by digest only** is accepted only when it
equals the recorded digest; with no digest recorded for that engine it fails with `pinned by
digest only; cannot prove version <pin>`. A tag together with a digest compares both.

Proof is per dependency and per image: two aliases of one engine each need their own resolved
version, and every engine image map or string (each component of a cluster chart that the
values set) must itself give a tag or a digest, so one matched occurrence never hides another
unresolved one. A component the values do not set at all falls back to the chart default and is
not checked. Only the dependency's own engine image proves it: an image of another engine (a Tempo
image in a Grafana sidecar) is compared with its own pin but does not prove the dependency. At an
own image path the path decides: an empty string, `null` or `{}` fails, and a mirror repository
name is still the engine's image.
A map at an own path that holds a nested `image` and no tag, digest or repository of its own (the
tempo wrapper `tempo.tempo.image.tag`) is a wrapper, not an unresolved image: the nested image is
the one checked.
The nested `image` of an own path is own at any depth (`vmselect.image.image.tag`), so a wrapper is
skipped only because its child is itself checked.

Exit 0 when every version and digest found equals its pin. Exit 1 on any failure, one line each
naming file, line, key, the chart value and the pin: `values.yaml:18 tempo.tempo.tag: chart
3.1.0, pin Tempo 3.1.1 [engine-pin-drift]`. These also exit 1, never a note: an engine the
chart uses (a dependency, or an engine chart's own name) whose version cannot be read, because
the dependency has no image tag, the tag is empty or missing (the chart default is not
checked), or the image string has no tag or digest (`[engine-pin-missing]`). Exit 2 when the
folder is missing or holds no engine at all (no engine dependency and no engine image).
Pyroscope and any engine the chart does not use are not required. `--pins FILE` compares with
another pins file instead of the built-in one (it exists so the digest-less case can be tested).

MEASURED: unit tests (`src/internal/chartcheck`) and `tests/engines.sh` with the fixture charts
in `tests/fixtures/engines` (`matching` exit 0; `drifted`, `digest-drift`, `tag-no-repository`,
`missing-tag`, `registry-port`, `two-aliases-missing`, `cluster-missing` and `digest-only-no-pin` exit 1; `digest-ok`, `two-aliases-ok`, `cluster-ok` and `sidecar-tag`, `tempo-chart-tag-ok` exit 0 (`tempo-chart-tag-drift`, `foreign-sidecar`, `empty-string`, `tempo-nested-image-drift` and `tempo-nested-image-empty` and `cluster-extra-wrapper` exit 1; `tempo-nested-image` and `cluster-extra-wrapper-ok` exit 0); `none`
exit 2). **NOT TESTED:** the real chart's location in the chart registry is not specified, so
wiring this command into a consumer's CI is not tested; the fixtures follow the common upstream
value shapes, and an umbrella chart that keeps its image tags outside `values.yaml` (for
example in a template or a separate values file) is not read (see [Not tested](#not-tested)).

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

## `obs assert`

Step 7 of the workflow (concept §11.4). After e2e, take a series export from the store the e2e run wrote to
and give it to obs as a file; obs counts the real series per metric and fails when a count is above the
contract's worst case (a hole: a cast, reflection, a raw OTel call). No network.

```text
obs assert --series FILE [--dir observability] [--pods n] [--unknown warn|fail|ignore]
```

- **Input** (`--series`): `/api/v1/series` JSON (`{"status":"success","data":[{"__name__":...}]}`) or
  `/api/v1/export` lines (`{"metric":{...},"values":[...],"timestamps":[...]}`), for example
  `curl -g "$VM/api/v1/series?match[]={\"durian.cloud.service\"=\"checkout\"}&start=<e2e start>"`. Scope the export to the
  service under test: obs counts every series it is given. A series repeated in an export counts once.
  Label names are the stored spelling, dots kept (`payment.provider`). An underscore spelling (`payment_provider`) is a
  `label-not-in-contract` finding that names the dotted spelling.
- **Count.** A series belongs to the contract metric whose stored names it carries (`checkout_charges_total`,
  `checkout_charge_duration_seconds_bucket|_sum|_count`: a histogram counts all three, like the worst case of
  `obs check`, buckets + 2 per label set). The series are counted **per landscape** (label
  `durian.cloud.landscape`; none = one group), since the worst case is per landscape.
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
matching uses exact line and column only when the entire group's location set is unchanged.
Otherwise, it pairs counted declarations in source order (line, then column). A single
declaration on each side matches regardless of location, including across the three baseline runs.
Different
declaration counts are an error when a member is ≥ 3% in either the baseline or the current
profile, naming the function/file and both counts and suggesting a rename or baseline `--update`.
Only members with ≥ 1.5% own CPU share in that profile count toward the group; smaller members
may be unsampled without changing identity. Members from 1.5% to 3% remain below the row cutoff,
but their count and source locations are retained for disambiguation. Smaller members' locations
are also retained, so crossing the 1.5% threshold with unchanged locations cannot displace a hot
match. All-cold baseline groups retain their counts too, so a newly hot duplicate cannot fall
back to a note.
Chrome's zero-based
`lineNumber` and `columnNumber` become one-based; speedscope `line` and `col` are kept, and pprof
uses `Function.StartLine` rather than instruction lines. This includes separate same-line functions
in a minified bundle. Baseline format is version 6: regenerate earlier preview baselines from
three profiles with `--own` and `--update`. **Closures** (never rows): Go `.funcN`,
`.gowrap`, `-fm`; JS `(anonymous)` and empty names; Dart `<anonymous closure>`; .NET `<>c__`, `>b__`,
`lambda_method`.

**Known limits for same-name, same-file duplicates:**

- Swapping function bodies while retaining their locations cannot be distinguished from changing
  those declarations' CPU shares.
- A simultaneous source shift and threshold crossing can change the counted members' source
  order. Profiles alone do not establish the bodies' identities in that case; rename declarations
  or inspect and update the baseline.

**Baseline** = median of exactly 3 runs → `e2e/profile-baseline/<service>.<scenario>.json` (about 1 KB;
no times, sorted rows, shares rounded to 6 places, so two runs give byte-identical files). Rows:
functions whose median own share is ≥ 3 %, every span with samples in at least 2 of the 3 runs, the median
samples and the median OS CPU per request. An existing baseline is replaced only with `--update`
(baselines change only through a PR). `--profiler dotnet-trace` is stored and gives every row 30 %.

**Compare** prints `what / baseline / now / change (limit)` for every baseline row and exits 1 when a row is
slower than its limit, or a hot baseline function has no matching samples. The latter is an
`ERROR` naming the function and suggesting `--update` after inspecting an intentional removal.
For a function or span row the change is `now share / baseline share − 1`.

- **limit = max(tolerance, 3 × noise)**, noise = `sqrt(1/n_base + 1/n_now)`. _FROM SOURCE: the concept does not say
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
  - green: `tests/fixtures/good` (a checkout service, with `contract.yaml`) and `tests/fixtures/sample-app`
    (a made-up platform's `crash-logs` and `pod-crashloop` alerts, copied and written as CRs, and a dashboard);
  - red, each naming file + rule or panel + the bad token: `bad-logsql-syntax`, `bad-metricsql-syntax`,
    `bad-logsql-no-stats`, `bad-prom-only-function`, `bad-loki-datasource`, `bad-flat-alert`,
    `bad-event-missing`, `bad-no-rows` (a metric the seed does not hold), `bad-drifted-field`
    (`severity` for `severity_text`), `bad-dashboard-label`; `ok-rollup-no-window` is green with a warning.
    Each fixture's `expect` file lists what its output must contain.

- `tests/check-test.sh` (run by `tests/run.sh`): `obs check` on every static fixture (same rule and token as `obs query --static`), its flags, an over-budget contract and a broken query red in one run with one summary, an
  `obs.yaml` mismatch, a folder with no contract; `obs test` green on `tests/fixtures/test/good` and `links/good`, each part red (queries, alerts, links, incidents), the incident flow (red `[incident-uncaught]`, then green after the alert is added),
  and a folder with no alert. It starts with `tests/compat.sh`. To refresh the golden files (only with the 0.4 binary): `tests/compat.sh <obs-0.4> --capture`.

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
inside VictoriaMetrics v1.156.0 and VictoriaLogs v1.54.0, TraceQL rules inside Tempo 3.1.1
(see [TraceQL alert rules](#traceql-alert-rules)), with no network or containers.

```text
obs alerts --dir observability
OBS_VERIFY_FAST=1 OBS_VERIFY_SKIP=1 obs alerts --dir observability
OBS_VERIFY_PRUNE=1 obs alerts --dir observability
OBS_CACHE_DIR=.cache/obs obs alerts --dir observability
obs alerts --dir observability --steps steps.json --recorded recorded.json
obs alerts --dir observability --tempo-config tempo.yaml   # Tempo's query_frontend settings for trace rules
```

The seed's `incidents: [spike]` reads `incidents/spike.yaml`. An incident declares
`window: {start: 10m, duration: 10m}`, `change` entries with a metric, matching
`where` labels and `rate`, `latency_x` or `share_x`, and
`expect: {fire: [rule-uid], quiet: [], fire_within: 5m, resolve_within: 5m}`.
Seeded traces follow the same changes: a `rate` or `share_x` change adds or removes
requests and so their traces (a series that matches a trace definition's `error_when`
makes error spans), and `latency_x` stretches the spans of the matching series inside
the window (counters included). Trace IDs stay deterministic: outside the window every
trace is byte for byte the baseline's.
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
against its event list (exact/phrase names, matching prefixes, regex and in selectors).
Subqueries, including union/join and in queries, receive the same recursive policy.
Opaque row-producing forms are refused; pruning retains every event whenever a
query can read additional sources. Other field-value filters are printed as assumptions and
removed from the parsed AST; event filters in `_stream` or `or`, unknown events
and stats reading field values are refused. Seed events must be declared too.
Without a contract, one event-rule-skipped note is printed for each log query.

The numeric light path is checked against the vendored Grafana dataframe path
with `OBS_VERIFY_FAST=1`; unchanged incident inputs are compared with their
baseline reuse by `OBS_VERIFY_SKIP=1`. Both compare over the same already-pruned
input, so a third check, `OBS_VERIFY_PRUNE=1`, evaluates the whole project twice in
two isolated child processes on one fixed base with the result cache bypassed: once
with lazy log/metric/trace pruning and once with `OBS_NO_PRUNE=1`. It compares
every per-minute value (exact; NaN = NaN, Inf exact), instance label, state, error,
verdict, recording sample and the raw datasource result per query input, and exits 1
naming the alert, rule file and query, run and minute of each difference. Any mismatch
fails the command. The result
cache includes the binary, seed, incidents, recording rules, contract and each
alert's normalized CR definition. Time-derived queries (including sample
timestamps) also include the base timestamp and do not reuse shifted baseline
results. Wall-clock/random queries and a configured query guard bypass persistent
caching. Storage uses `OBS_TMPDIR`,
otherwise a writable `/dev/shm` with at least 1 GiB free (else the system temp
directory); VictoriaMetrics caches are capped at 512 MiB.
Unused metric series are omitted and VictoriaLogs starts only when needed.

**Parallel evaluation shares the VictoriaLogs server limits.** Rules are evaluated in parallel,
`--workers` of them (default: all CPUs), and every LogsQL query goes through the one embedded
VictoriaLogs engine, which applies the server's limits as production does: `min(CPUs,16)`
concurrent slots (twice the CPUs up to four CPUs), a 10 s `-search.maxQueueDuration` wait for a
slot and a 30 s `-search.maxQueryDuration` per query. obs runs the engine and every worker on the
runner's own CPUs, while production's VictoriaLogs has its own, so a very slow or heavily shared
runner can see a timing refusal (`-search.maxQueueDuration` or `-search.maxQueryDuration`, named
in the message) that production would not. Lower the worker count to avoid it, for example
`obs alerts --dir observability --workers 2`; `--workers 1` evaluates one rule at a time and
lets VictoriaLogs use its own parallelism for that query. Do not raise the limits in
`--vl-settings` to hide a timing refusal: obs would then accept a query production refuses.

MEASURED offline: regression tests cover state transitions, numeric light/full parity,
event-filter refusals, recording outputs, cache invalidation and planted wrong
thresholds.

MEASURED against real Grafana 13.3.0 and the real stores in the parity job (the green run
https://github.com/AtomiCloud/nix-registry/actions/runs/44102413229, `TestParityAlertEval`, `TestParityAlertHistory`):
27 per-minute alert condition states and values of metric and LogsQL rules equal Grafana's
`/api/v1/eval`; 4 live Grafana scheduler transitions (including pending and
`keep_firing_for`) equal obs; 10 live Grafana recording-rule points equal obs at 1e-11
relative. `TestParityGrafanaLogs` and `TestParityHistoricMetrics` passed in the same run
(LogsQL and MetricsQL alert minutes through Grafana's datasource).

NOT TESTED: parity of trace alert rules against real Grafana and Tempo (the parity job has no
trace rule), notification and persistence behaviour, how the Grafana UI interpolates a
query exactly, and VictoriaLogs runtime timeout and concurrency limits on other hardware.
TraceQL rules: see the next section. The Grafana AGPL licence ships in `share/licenses/obs/grafana/LICENSE`.

### TraceQL alert rules

A rule query on the Tempo datasource (`model.datasource.type: tempo`) runs in the
embedded Tempo 3.1.1 engine through the same `DatasourceEngine` hook, expression port
and state machine as metric and log rules (reduce, threshold, math, `for`,
`keepFiringFor`, NoData/Error, missing series).

```yaml
- refId: A
  queryType: traceql
  datasourceUid: traces
  relativeTimeRange: { from: 60, to: 0 }
  model:
    datasource: { type: tempo, uid: traces }
    queryType: traceql
    metricsQueryType: instant # the documented alert mode; anything else is a range query
    query: '{ resource.service.name = "checkout" && status = error } | rate() by (span.outcome)'
- refId: B
  datasourceUid: __expr__
  model: { type: reduce, expression: A, reducer: last }
```

What Grafana 13.3.0 does with that query, and obs does the same in-process. FROM SOURCE:
the Tempo datasource left Grafana core (commit `61ec98144cd4`) and Grafana 13.3.0
preinstalls it (`pkg/setting/setting_plugins.go:43`); the paired plugin release is
[grafana-tempo-datasource v13.3.0](https://github.com/grafana/grafana-tempo-datasource/tree/v13.3.0)
(commit `7bbc067f`). The port is `internal/tempoalert` (provenance in its `PROVENANCE.md`).

- **Routing** (`pkg/tempo/tempo.go:106-127`, `traceql_query.go:38-46,221-224`):
  `queryType` must be `traceql` or `traceqlSearch` (the model's value wins, as ngalert's
  `AlertQuery.PreSave` copies it, `alert_query.go:297-315`); the TraceQL is read from
  `model.query` (not `expr`); it must match the plugin's metrics pattern (`| rate(`,
  `count_over_time(`, `avg/sum/min/max_over_time(`, `quantile_over_time(`,
  `histogram_over_time(`, `compare(`), else the plugin runs a trace search.
- **Request** (`traceql_query.go:143-219`): `metricsQueryType: instant` calls
  `/api/metrics/query`, anything else `/api/metrics/query_range`, with `start`/`end` =
  the rule's `[now-from, now-to]` in whole seconds and the model's `step` (unset: Tempo's
  `DefaultQueryRangeStep`, `pkg/api/http.go:685-689`). Tempo's frontend turns an instant
  query into one range bucket of `end-start` and keeps each series' first sample
  (`modules/frontend/metrics_query_handler.go:139-151,250-267`); an empty window is
  Tempo's `end must be greater than start` error (an Error input).
- **Tempo's query frontend** (`engines/tempo/alertapi.go`): obs simulates every
  evaluation at its tick `t`, and Tempo's frontend measures against its own `now`, which
  obs takes to be `t` (Grafana's request reaches Tempo milliseconds later; with whole-second
  ends and steps that gives the same request). `query_frontend.query_end_cutoff` (default
  30 s, `modules/frontend/config.go:154`) then applies, in Tempo's order:
  - instant (`metrics_query_handler.go:145-150,224-245`): the end moves back by the fewest
    whole seconds that put it at or before `now - cutoff`; a window left empty is
    `query window falls entirely within query_end_cutoff` (HTTP 400), an Error input.
    So `relativeTimeRange: {from: 60}` reads `(t-60s, t-30s]`, and `from: 30` is an Error
    on every tick;
  - range (`metrics_query_range_handler.go:143-160,199-232`): clamp the end to
    `now - cutoff`, validate, `AlignRequest` (start down to the step, then one step
    earlier; end up to the step), then clamp again and round the end down to the step
    (`AlignEndToLeft`). With `step: 1m` the `(t-60s, t]` bucket is never in the answer;
  - validation (`metrics_query_validation.go:18-54`): `metrics.max_duration` (24 h) and
    `metrics.max_intervals` (10 000) fail the query (an Error input).

  `--tempo-config FILE` reads `query_frontend.query_end_cutoff`,
  `query_frontend.metrics.max_duration` and `query_frontend.metrics.max_intervals` from a
  Tempo config file (the YAML Tempo reads with `-config.file`); absent keys keep the
  defaults above. Because a range request starts up to two steps before `now-from`, runs
  are spaced at least `from + 2*step + 5m` apart. Tempo aligns range requests to whole
  steps since the Unix epoch, so a range trace rule reuses a baseline result
  (`OBS_VERIFY_SKIP`, incident skipping) only when the run offset is a whole number of
  steps, and its cached result is keyed by the base's position inside each step.

- **Frames** (`pkg/tempo/traceql/metrics.go:16-142`): every Tempo series is a
  time + value frame, so Grafana's expression engine reads it as a **series**
  (`pkg/expr/converter.go:69-118`): reduce it before a threshold, as for range metric
  queries. Series labels are Tempo's attribute names (`span.outcome`,
  `resource.service.name`, `p` for quantiles); a string value keeps its double quotes
  (`span.outcome="\"error\""`), numbers and booleans do not. No series is NoData with
  `{datasource_uid, ref_id}`.
  An instant frame's time is the plugin's `time.Now()`; obs uses the evaluation
  minute. Only math joining two un-reduced instant series could see the difference
  (NOT TESTED; reduce first, as above).
- **Refused statically** (`obs query --static` and `obs alerts`, rule ids in brackets):
  `traceId` and other query types (`tempo-alert-query-type`), an empty `model.query`
  (`tempo-alert-query`), a search (`tempo-alert-search`), a bad `step`
  (`tempo-alert-step`), a Jaeger or Zipkin datasource (`tempo-alert-datasource`), and a
  **range query that would carry exemplars** (`tempo-alert-range-exemplars`): Tempo 3.1.1
  adds up to 100 exemplars to a range answer by default
  (`metrics_query_range_handler.go:235-255`), the plugin turns them into extra frames with
  a `traceId` column (`traceql/exemplars.go:11-38`), and Grafana refuses a frame that is
  not a wide series (`converter.go:79-90`), so the rule would be in Error in Grafana. Use
  `metricsQueryType: instant`, or end the query with `with (exemplars=false)`. The TraceQL
  is parsed by Tempo and every span/resource attribute goes through the contract's span
  attribute check (`traceql-attribute`), like a dashboard query.
- Grafana 13.3.0 runs Tempo alerts only with feature toggle `tempoAlerting` enabled
  (FROM SOURCE: without it the Tempo plugin's alerting flag is false,
  `pkg/services/featuremgmt/registry.go:1708-1714`,
  `pluginsintegration/pipeline/pipeline.go:46-50`); enable it in the Grafana chart. obs
  evaluates the rules as if it were on and prints this as one note per `obs alerts` run
  that has trace rules. Whether provisioned rules evaluate without the toggle (or only
  the rule editor hides them) is NOT TESTED.

Seed and self-checks: rules that read traces keep every trace definition and its source
metric (pruning drops traces only when no rule reads them or log trace IDs). The seed
spans are indexed once for all runs; each series' spans share one attribute map. An
incident only re-evaluates trace rules when it changes a series that is a trace source,
so `OBS_VERIFY_SKIP=1` compares the reuse of the others (range trace rules also need
a whole-step run offset, above). `OBS_VERIFY_PRUNE=1` compares
every Tempo answer per minute (the raw query input) between the pruned and the full run.
`OBS_VERIFY_FAST=1` prints a note and compares nothing for trace rules: they always take
the full Grafana path (the light path reads numbers; Tempo answers with series).

MEASURED: `tests/alerts.sh` (an instant error-rate rule and an exemplar-free range p90
rule fire inside the incident window and stay quiet on the baseline; planted: a wrong
threshold names rule, file, window and value; a rule firing on the baseline; an
undeclared span attribute; a range query with exemplars; an incident that changes only a
metric no trace comes from: `OBS_VERIFY_SKIP=1` reuses both trace rules, `reused > 0`,
`mismatches=0`; exactly one `tempoAlerting` note), `tests/alerts-verify-prune.sh`
(planted span durations make the trace inputs differ), Go tests in `internal/tempoalert`,
`internal/engines/tempo` (`alertapi_test.go`, `frontend_test.go`: the end cutoff,
validation, alignment and `--tempo-config` parsing), `internal/alerts` (`traces_test.go`,
`traces_cutoff_test.go`: `from: 30` instant is an Error on every tick, step `1m` drops
the `(t-60s, t]` bucket, the run gap covers two 30m steps, whole-step reuse and cache keys),
`internal/check`, `internal/seed`; speed in the table below. FROM SOURCE: everything in
the list above. NOT TESTED: trace alert results against real Grafana and Tempo (the
parity job, run https://github.com/AtomiCloud/nix-registry/actions/runs/44102413229, has no trace rule yet; adding one is cheap: start the parity Grafana with
`GF_FEATURE_TOGGLES_ENABLE=tempoAlerting`, provision a Tempo datasource, push the alert
seed's traces with the parity Tempo test's `loadTempo`, and add one Tempo rule to
`TestParityAlertEval`, which already compares per-minute states and values with
`/api/v1/eval`); Tempo's `max_response_series` and per-tenant `max_metrics_duration`
overrides (not modelled); whether provisioned Tempo rules evaluate without the
`tempoAlerting` toggle; a Helm chart's values file for `--tempo-config` (obs reads the
rendered Tempo config only); exemplar frames themselves (refused, never produced).
MEASURED: `obs query --live` runs every Tempo alert query, as the alert evaluator sends it
(instant or range, whole seconds, the end cutoff, against the end of the seed's time line):
`internal/check/livetrace_test.go` (a `{} | rate()` instant rule, an exemplar-free range
rule and a filtered rule on seeded spans stay green; planted: a filter that matches no span,
a seed with no span, a `by()` on an attribute the seed lacks, a window inside the end cutoff,
a search instead of a metrics query, TraceQL that does not parse; each fails naming file, rule
and query) and `tests/links.sh` (a Tempo alert in the `links` fixture; planted: no-rows and
`span.drifted`). A query kind obs cannot run live (a Jaeger or Zipkin trace alert, a Loki
alert) is a FAIL (`live-unsupported-query`), never counted and skipped.

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
are integrated in this branch, and so is `obs profile`: the parity workflow runs the profile step
(span to profile compare against Pyroscope) because the command exists.
The same comparisons passed in CI on the integration branch: every step of the parity workflow was green in
https://github.com/AtomiCloud/nix-registry/actions/runs/44102413229 (the counts per test are in
[obs alerts](#obs-alerts) and [obs links](#obs-links)).

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

## `obs alerts` speed (concept §11.1.1, re-measured with VictoriaLogs)

### VictoriaLogs speed measurements

The §11.1.1 table was measured in spike 7 with Loki as the log engine. This is the same
table with the VictoriaLogs engine, after the profiling fixes below. MEASURED: 3 runs per
cell, median time, largest peak; peak memory in GiB (`ru_maxrss` KiB / 1048576).

| Alerts             | 4 h seed | 24 h seed | Peak memory 4 h / 24 h |
| ------------------ | -------- | --------- | ---------------------- |
| 1                  | 0.35 s   | 1.30 s    | 0.23 / 0.46 GiB        |
| 20                 | 1.25 s   | 5.91 s    | 0.55 / 0.85 GiB        |
| 200                | 7.17 s   | 42.92 s   | 0.63 / 1.97 GiB        |
| 1,000              | 9.24 s   | 53.46 s   | 0.69 / 1.97 GiB        |
| 1 TraceQL (`t1`)   | 0.66 s   | 3.54 s    | 0.37 / 1.71 GiB        |
| 20 TraceQL (`t20`) | 6.09 s   | 42.38 s   | 1.08 / 2.02 GiB        |

The TraceQL rows (MEASURED 17 Nov 2026, 11:17-11:20 UTC, same box and method, load
average 12-13, `go test -tags speedbench -run TestTraceSpeedTable`) use the trace-only sets
`t1`, `t20` below. Memory holds the budget (1.2 GB at 4 h, 6 GB at 24 h); time does not at
20 rules (5x and 7x the 20-alert metric+log row). CPU profile of `t20` at 4 h on 2 CPUs:
71 % is Tempo evaluating spans (`tqSetIter.Next`, `BinaryOperation.execute`): every minute
each rule re-reads its whole window (5 to 10 minutes), so each span is evaluated 5 to 10
times per rule; TraceQL parsing is 4 %. NOT TESTED proposal: evaluate each rule once per
run on per-minute buckets and sum them per window (exact for `rate`, `count_over_time`,
`sum`/`min`/`max_over_time`; quantiles would need the bucket histograms), with a verify
mode like `OBS_VERIFY_FAST`. Before sharing span attribute maps (same day), one TraceQL rule
on the 24 h seed took 123 s and 6.8 GB (garbage collection under the 2 GiB soft limit).

Before the fixes (same runner, 1 run per cell, `obs-alerts` evaluator at aa2085a; the
runner then labelled the same KiB / 1048576 numbers "GB"):

| Alerts | 4 h seed | 24 h seed | Peak memory 4 h / 24 h |
| ------ | -------- | --------- | ---------------------- |
| 1      | 3.55 s   | 15.78 s   | 2.03 / 7.85 GiB        |
| 20     | 3.92 s   | 18.55 s   | 2.16 / 8.70 GiB        |
| 200    | 11.86 s  | 65.46 s   | 2.21 / 8.17 GiB        |
| 1,000  | 15.06 s  | 68.75 s   | 2.14 / 8.21 GiB        |

Loki numbers (spike 7, same table, as quoted there in GB) for comparison: 1 alert
0.23 / 1.10 s, 0.19 / 0.78 GB; 20 alerts 1.05 / 5.45 s, 0.76 / 4.0 GB; 200 alerts
1.92 / 10.64 s, 1.07 / 5.3 GB; 1,000 alerts 2.44 / 10.14 s, 1.22 / 5.9 GB.

**What the numbers say**

- **Memory holds the budget in every cell**: 1,000 alerts take 0.69 GiB on a 4 h seed
  (budget 1.2 GB) and 1.97 GiB on a 24 h seed (budget 6 GB). Before: 2.1 and 8.2 GiB.
- **1 alert is back near the Loki numbers**: 0.35 s / 0.23 GiB at 4 h (Loki 0.23 s /
  0.19 GB), 1.30 s at 24 h (Loki 1.10 s).
- **Time does not hold the budget at 1,000 alerts**: 9.2 s at 4 h (budget 2.5 s, 3.7x) and
  53 s at 24 h (budget ~10 s, 5.3x). 200 alerts: 7.2 s and 43 s. The cause is MEASURED
  below: one VictoriaLogs stats query per log query, per minute, per run.

**Where the time and memory went (profiled before the fixes, MEASURED)**

Profiles: `go test -cpuprofile/-memprofile` of `obs alerts` in-process, n1 and n1,000, 4 h
and 24 h, 2 CPUs, plus a heap snapshot during a 24 h n1,000 run.

1. Every log row of every event was loaded, even for 1 alert. The speed alerts read only
   `checkout.charge.failed` and `checkout.charge.declined` (about 10 % of rows);
   `succeeded` (90 %) was loaded and never read. Loading the logs was 52 % of CPU at
   1 alert and 81 % of all allocation.
2. The whole seed went into VictoriaLogs as one `LogRows` batch: a slice of every row and
   field, grown and copied (`growslice`, `memmove` 33 % of CPU) and rescanned by the GC
   (31 %).
3. At 1,000 alerts, 40-44 % of CPU is VictoriaLogs search: alerts evaluate every minute, and
   a LogsQL stats query has no range form in Grafana alerting, so each of the 124 distinct
   log queries runs once per minute per run (240 or 1,440 ticks x 4 runs). Each query
   unpacks the timestamps of a whole block (`getTimestamps` 15-17 %).
4. Each of those queries allocates fresh 64 KiB stats buffers (about 90 GB allocated over a
   24 h n1,000 run). Kept tick results held tiny strings that pointed into those buffers,
   so 3.1 GiB of buffers stayed alive: that was the 8 GiB peak at 24 h.

**What changed (results identical)**

- Lazy load for logs: only the seed events some log query can see are generated and
  loaded (`internal/alerts/lazylogs.go`). The event set comes from the query's root
  filter, `global_filter` and leading filter pipes, evaluated per event with three-valued
  logic (stream, time, field and regex leaves may be true or false). A query whose pipes
  read the event field (`stats ... if (event...)`, `by (event)`) loads every event. Each
  event's rows use their own random stream, so dropping one changes no other row or sample.
- VictoriaLogs gets the rows in blocks (as `vlinsert` does), not one batch.
- Query results are copied out of VictoriaLogs' buffers (no pinned buffers).
- Alert stats queries run with one VictoriaLogs worker each (the rule workers already use
  every CPU), and the stats labels are read once per query.
- `obs alerts` sets GOGC 400 and a 2 GiB soft memory limit unless `GOGC` / `GOMEMLIMIT` are
  set: the per-query buffers are short-lived garbage over a small live heap (n1,000 4 h,
  3 runs: 13.2 s at the default, 9.2 s at 400).
- The seed draws its per-request trace randomness only when the seed defines traces.

Same results: `OBS_VERIFY_FAST=1` and `OBS_VERIFY_SKIP=1` checks, `go test
./internal/alerts/...`, `tests/alerts.sh` (baseline and every planted failure) and every
expectation of every speed fixture run pass. The pruning rules have their own regression
tests with planted failures.

**What would close the time gap (NOT TESTED, a proposal)**

The remaining cost is the number of VictoriaLogs queries, not their size. VictoriaLogs has
its own range form (`stats by (_time:step offset o)`, used by its `stats_query_range`
endpoint): a window of k alert intervals could be computed with k bucketed queries per run
instead of one query per minute, about 100x fewer queries. It needs the relative `_time:W`
filter taken out of the parsed query, zero rows for empty global stats, and the same bucket
edges and `rate()` step as the instant query, so it belongs in the evaluator with a
verify mode (both paths, compared), like `OBS_VERIFY_FAST`.

**Box and method**

- Box: Intel Xeon Platinum 8488C, 32 vCPU, 247 GB RAM, Linux 7.0 (AWS). The job runs on
  2 CPUs (`taskset -c 0,1`), but the box is shared with other builders, so the other
  30 CPUs, the disk and the memory bus were busy: expect noise. Load average during the
  final table: 34-38 (17 Nov 2026, 06:37-06:44 UTC).
- Cold start: a new process per run, an empty `OBS_CACHE_DIR`, the set copied to
  `/dev/shm` first. The OS page cache is not dropped (needs root). Time is the process's
  wall clock; peak memory is the process's `ru_maxrss`.
- Every run also checked its own expectations: all runs exited 0 (the table has no failed
  cell; a failed run prints FAILED instead of a time).
- Run it (under the shared heavy lock; `OBS_FIXTURES` must be absolute, the test runs in
  its package folder):

  ```sh
  cd golang/obs/src
  go build -o /tmp/obs . && OBS_BIN=/tmp/obs OBS_FIXTURES=$PWD/../tests/fixtures/alerts-speed OBS_RUNS=3 \
    go test -tags speedbench -run TestSpeedTable -v -timeout 2h ./internal/alerts/speedfixtures
  ```

### Fixture sets (`tests/fixtures/alerts-speed/`)

- Only `n1` is an `obs test` example (green as a whole). `t1` and `t20` are alerts speed fixtures: their `links` part is red by construction (traces without logs or profiles), so do not read them as obs test examples.
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
- `t1`, `t20`: TraceQL-only sets (`speedfixtures/traces.go`): 20 distinct Tempo rules (17
  instant, 3 exemplar-free range, rate / count / quantile / avg / max / sum, `by (...)`, a
  structural `>` query, a two-query ratio) on the same seed plus two trace definitions (one
  trace per charge with a provider client span, one per HTTP request; 4 h: about 86,000
  traces, 130,000 spans per run; 24 h: six times that) and the same three incidents. Coverage: t1 = 4 (1 positive, 3 negative);
  t20 = 80 (17 positive, 63 negative). Each rule's expectation was written from the seed's
  rates first and then confirmed by the evaluator on both seeds (no expectation was changed).

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

NOT TESTED: no run was on a quiet box (the 3-run medians are on a shared, loaded box); the cache-hit
rows of §11.1.1 (unchanged PR 0.2-0.4 s, one changed alert 0.3-0.5 s) were not re-measured.

## obs store

`obs store move <tempo|pyroscope|victorialogs|victoriametrics> --config move.yaml --dry-run`
runs every static check and prints the mandatory steps (and, for VictoriaLogs/VictoriaMetrics, the
derived storage endpoints) without commands or HTTP requests. Remove `--dry-run` to execute;
`--rollback` undoes the move. Kubernetes access uses `kubectl` on PATH with the explicit context
and namespace from the config. Nothing runs automatically after a failed step; inspect the
reported state and re-run (it resumes) or roll back explicitly. There is no skip option.

### How obs changes the cluster

- **New objects** (the scaled workloads or new storage nodes and their headless Services,
  ConfigMaps, Pyroscope discovery RBAC) come from one hand-written `scaled_manifest`. obs creates
  them with `kubectl create` (never `apply`), so the API server refuses any object that already
  exists. obs writes the objects it checked to its own file (Lists flattened, namespace set) and adds
  to each the label `obs.diene/store-move: <store>` and the annotation
  `obs.diene/store-move-digest` (a hash of that manifest object). Before creating, it reads live:
  every object must be absent (or carry exactly that label and digest: a re-run), every claim a
  StatefulSet's `volumeClaimTemplates` would bind (`<template>-<statefulset>-<ordinal>` for every
  ordinal from `spec.ordinals.start`, default 0) must be absent, and no live pod may match a new workload's selector, so nothing existing
  is adopted.
- **Existing objects** (the single, a separate Tempo compactor, the insert/select routers,
  vmstorage) are never described by hand. obs builds each change itself from the live object as a
  JSON patch (`kubectl patch --type=json`). Every patch obs sends starts with `test` operations of
  the object's uid, its whole `spec` and its annotations as obs read them, then of every old value
  the change relies on; the API server refuses the whole patch if anything changed since obs read
  it (an appended `-replicationFactor=1`, a duplicate `-storageNode`, a retention policy set where
  none was). The inverse patch (also with `test` operations) is stored on the object in the
  annotation `obs.diene/store-move-inverse`, and `obs.diene/store-move-origin` records a digest of
  the uid and of the old value at every path the change writes. The rollback replays the inverse on
  a copy of the live object, requires the restored values to match that digest, rebuilds the
  forward patch from the result and applies the stored inverse only when everything matches
  exactly, so a hand-edited inverse (for example the compactor's replicas 1 to 7), another config or
  a pinned value changed after the move is refused. Both annotations live on the object itself: they
  catch an edited inverse, not someone who rewrites both with the same access that could change the
  workload directly. A single must run exactly one replica, so its restored count never depends on
  them. It undoes only obs's own change; other fields stay as they are.

| Store                         | Change obs makes to existing objects                                                                                                                                                                                                                                                      |
| ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Tempo                         | single: `spec.replicas` N to 0 and every template role `true` to `false`, in one patch, after the drain; compactor (if separate): replicas N to 0                                                                                                                                         |
| Pyroscope                     | single: `-target=all` to `-target=metastore` (same spelling) and every template role to `false`; replicas, claims, raft id and all else untouched                                                                                                                                         |
| VictoriaLogs, VictoriaMetrics | each router: the one `-storageNode` flag gains the derived endpoints (same spelling, existing endpoints kept in order)                                                                                                                                                                    |
| VictoriaMetrics, `copies: 2`  | vminsert also gains `-replicationFactor=2`; vmselect gains `-dedup.minScrapeInterval=1ms` (VictoriaMetrics v1.156.0 docs: required on vmselect when replication is enabled); every listed existing vmstorage gains `-dedup.minScrapeInterval=1ms` at the front of its args (concept 10.5) |

Role labels change only on pods a workload's controller owns by uid (StatefulSet pods, or
Deployment pods whose ReplicaSet the Deployment owns), one JSON patch per pod by name with `test`
operations of its uid, owner references and old labels; a pod that merely matches the selector is
refused, never relabelled, and only workloads obs created receive roles (make before break: new
pods and templates get their roles and are Ready before the single's pods lose theirs). Alloy is rolled with
`kubectl rollout restart`. Nothing else is changed: no app, Alloy config or stable Service.

### One check up front, then resume

Before step 1 obs checks everything, static and live: the manifest allow-list, every object, claim
and pod above, the stable Services (`tempo-distributor`/`tempo-query`,
`pyroscope-write`/`pyroscope-query` must each select exactly one role label `true`, and every
other selector term must be on the single's pod template and on the pod template of every scaled
workload that receives that role, so the Service routes to the scaled pods after the flip; obs
never changes these Services and re-reads them at the role step and right before the single
stops), Alloy (at least
two replicas, or a DaemonSet on at least two nodes), and that every change obs will make to an
existing object can be built from its live state now (so a Pyroscope single without exactly one
`-target=all`, a single whose template lacks its roles, a live `persistentVolumeClaimRetentionPolicy`
of `Delete`, a single with more than one replica, or a malformed `-storageNode` fails before anything
changes). The same check reads
which step the live state shows and starts there (printed as `already live; resuming`): created but
not routed restarts at step 1 (creating only absent objects, then waiting), routed restarts at the
Alloy roll, a stopped/converted single goes to verification. It never redoes a finished step and
never touches the roles of pods that already serve. Before the first step a resumed run executes, it
waits again for every scaled workload or new storage node (and the routers and storage workloads)
to be Ready, because the skipped steps' readiness may no longer hold. A state the move cannot produce (for example
the single's pods without roles while no scaled workload has them) is refused.

### Tempo and Pyroscope

```yaml
context: fixture-only
namespace: observability-fixture
scaled_manifest: scaled.yaml
scaled:
  - deployment/tempo-distributor
  - deployment/tempo-query
  - statefulset/tempo-live-store
  - statefulset/redpanda
single: statefulset/tempo
alloy: deployment/alloy
role_labels: [obs.diene/tempo-write, obs.diene/tempo-read]
role_workloads:
  deployment/tempo-distributor: [obs.diene/tempo-write]
  deployment/tempo-query: [obs.diene/tempo-read]
# Provide the actual verified adapter executable and argv in the single pod.
stop_compaction: [operator-control, stop-compaction]
resume_compaction: [operator-control, resume-compaction]
single_metrics_url: http://tempo-single.fixture.invalid:3200/metrics
read_url: http://tempo-query.fixture.invalid:3200/api/metrics/query_range
query: '{} | count_over_time()' # known immutable dataset; aggregate to one series
```

The scaled manifest holds every listed workload (StatefulSet or Deployment, positive explicit
replicas, a `matchLabels` selector without role labels and disjoint from the single's and from each
other's) and nothing that routes yet (no role label `true`). Supporting objects must carry a scaled
workload's or the single's name, alone or followed by `-` (`tempo-live-store-headless`):
headless Services (`clusterIP: None`; Tempo also `publishNotReadyAddresses: true`; no external IPs,
load balancer or node ports), ConfigMaps and, for Pyroscope's `kubernetes:///` metastore discovery
only, a ServiceAccount, a Role limited to get/list/watch on pods, endpoints and endpointslices, and a
RoleBinding of that Role to that ServiceAccount. Refused: other kinds (Secret, PVC, Endpoints,
DaemonSet, ClusterRole, ...), the eight stable Services, Alloy ConfigMaps (also custom names in
`alloy_config_objects`), the single/Alloy/compactor/routers themselves, `persistentVolumeClaim` or
`hostPath` volumes, `hostNetwork`/`hostPID`/`hostIPC`, a `serviceAccountName` the manifest does not
create, the owner/digest/inverse annotations written by hand, Kind aliases or another apiVersion
(`sts`, a CRD reusing a Kind), `items` on any kind but `List`, typed lists, another namespace,
`generateName`, the same object twice. Each assigned role goes only to its workload (Kafka is never
labelled). Suspend external GitOps reconciliation of these workloads while moving.

Tempo requires a verified compaction-control adapter (`kubectl exec` with argv, no local shell),
or a **separate** `compactor: deployment/backend-worker` (not the single, Alloy or a new workload);
obs stores its replica count in the inverse. The drain waits for
`tempo_live_store_blocks_completed_total` and `tempo_live_store_local_blocks_flushed_total` to rise
and for flushed blocks to catch up with completed blocks before the stop patch. No `/flush` endpoint
is assumed. Source: Tempo 3.1.1 `modules/livestore/complete_block_lifecycle.go:18–22`; its
downscale routes in `cmd/tempo/app/modules.go:725–730` only change ring/shutdown state
(`modules/livestore/downscale.go:130–234`), so they do not replace this durability check. An idle
store that produces no new completed block times out safely; feed a known dataset before moving.

For Pyroscope use `obs.diene/pyroscope-write` and `obs.diene/pyroscope-query`, set `profile_type`
(for example `process_cpu:cpu:nanoseconds:cpu:nanoseconds`) and a label selector in `query`;
`read_url` ends in `/querier.v1.QuerierService/SelectSeries`. The single must be a StatefulSet: it
becomes the v2 metastore in place (same PVC, same raft id) and keeps running.

Rollback reads the live single first. A single obs stopped or converted comes back through its
verified inverse: replicas and template roles (and Pyroscope's `-target=all`) in one patch, so the
first Ready pod already serves; Tempo's compaction then resumes. A single that is still running (the
move stopped before the drain) is never scaled down: only its pods get their roles back. A single at
0 replicas without obs's inverse is refused. Then the scaled pods and templates lose their roles,
Alloy is rolled, and every scaled receive counter (`scaled_metrics_urls`, or `metrics_url` for the
whole tier) must stay flat. Scaled pods, data and PVCs remain. Rolling back when nothing was moved
is refused.

### VictoriaLogs and VictoriaMetrics

```yaml
context: fixture-only
namespace: observability-fixture
scaled_manifest: vm-scaled.yaml # new vmstorage StatefulSet + its headless Service
scaled: [statefulset/vmstorage-b]
routers:
  - { workload: deployment/vminsert, role: insert, port: 8400 }
  - { workload: deployment/vmselect, role: select, port: 8401 }
# copies: 2                       # VictoriaMetrics two copies (concept 10.5)
# storage_workloads: [statefulset/vmstorage]   # with copies 2: existing vmstorage gains dedup
metrics_url: http://vminsert.fixture.invalid:8480/metrics
read_url: http://vm-select.fixture.invalid:8481/select/0/prometheus/api/v1/query_range
query: 'sum(count_over_time(fixture_known_total[1m]))'
```

New storage nodes are new StatefulSets. Their endpoints are derived, never typed:
`<statefulset>-<i>.<serviceName>.<namespace>.svc.<cluster_domain>:<router port>` for every pod
ordinal `i` from `spec.ordinals.start` (default 0) on (`cluster_domain` defaults to
`cluster.local`). `serviceName` must name a headless Service of the
same manifest that selects the pods, and every router port must be a `containerPort` of the pods.
VictoriaLogs routers dial 9491. With `copies: 2` every new vmstorage container must carry exactly
`-dedup.minScrapeInterval=1ms`; a live router with another `-replicationFactor`, a live vmselect or
vmstorage with another dedup value is refused, one that already has exactly the value is left alone (and the
rollback keeps it). obs extends only one well-formed `-storageNode` per router, read with Go flag
syntax as the stores read it (`lib/envflag`): one or two leading dashes, `=` or the next argument as
value, nothing read after `--` or a positional argument; a value after another flag without `=` is
that flag's value (the router is running, so its arguments already parse). Positional
`storageNode=…`, `---storageNode=…`, a missing value, an empty or `%{ENV}` endpoint, two
`-storageNode` flags, or a derived endpoint already listed without obs's inverse are refused. obs
inserts dedup only into the front of an existing, nonempty args list of a single-container
vmstorage. Steps: create and wait Ready; patch storage side (two copies) then routers, wait for the
rollouts; VL checks the OTLP received counter rising and remote send errors flat, VM checks inserted
rows rising; verify. There is no drain, rebalance, PVC deletion or Alloy restart. Rollback applies
the verified inverses (routers, then vmstorage) and **retains the new pods and PVCs**; data written
there is unreadable until the endpoints are added back.

### Verification and limits

Verification takes one fixed, closed 60-minute window before the move and queries the same
window through the read Service afterward. Missing minutes, empty/all-zero baselines, errors,
and unequal counts fail. `tenant` optionally sets `X-Scope-OrgID` on range queries. Query
responses use each store's real format: Tempo `series[].samples[].timestampMs/value`,
Pyroscope `series[].points[]`, and Victoria stores' Prometheus matrices.
Counter endpoints must expose only the intended old single or insert tier; absent/reset counters fail.
`--interval` defaults to 15s and `--timeout` to 15m. Unit tests drive the real implementation
against a stateful fake kubectl (objects, pods, `create` refusing existing objects, JSON patches with
`test` semantics) and fake HTTP; they never contact a real cluster. **NOT TESTED:** real cluster
moves/rollback (API-server patch and admission behaviour), chart compaction adapters,
Tempo/Pyroscope drain durability, live store HTTP endpoints, ReplicaSet/ControllerRevision
adoption by new workloads (only pods are checked), and claims of replicas added after the move.

## obs symbols

```text
obs symbols put --store .obs/symbols --kind native --build-id <gnu-id> --app service --file service.dbg
obs symbols put --store .obs/symbols --kind jit --build-id <module-MVID> --app service --file service.pdb
obs symbols put --store .obs/symbols --kind dart --build-id <id> --file app.symbols --obfuscation-map map.json
obs symbols decode --store .obs/symbols --file stack.txt
```

Native keys are `<gnu-build-id>/<app>.dbg`, JIT keys `<module-MVID>/<app>.pdb`, and Dart keys
`<build_id>.symbols` with `<build_id>.obfuscation-map.json`. IDs are normalized to lowercase.
Keys are immutable: uploading identical bytes is idempotent; different bytes under an existing
key fail. Native/Dart uploads must be ELF files whose `.note.gnu.build-id` equals `--build-id`;
mismatched identities and non-ELF files are refused. **NOT CHECKED:** a Portable PDB does not
carry the assembly's module MVID, so its MVID key is supplied by the uploader; obfuscation-map
provenance likewise comes from the build. Keep this private directory and symbols for as long as
that release can send data. An exported `Backend` interface supports object-store adapters;
no object-store implementation or remote bucket is claimed as tested.

The native decoder recognizes `at <module>+0x<offset>` frames with a `build-id: <id>` footer.
Dart/Flutter uses `build_id: '<hex>'` and frames such as
`#00 abs 00007f1234567890 virt 0000000000123456 _kDartIsolateSnapshotInstructions+0x22`;
the decoder resolves the **virt** address, not the symbol-relative `+0x22`.
then runs stock `llvm-symbolizer` found on PATH (`--symbolizer` or `OBS_SYMBOLIZER` can override it).
LLVM is a test-only dependency, not part of the obs runtime closure. If it is unavailable, decoding
prints one note to stderr, preserves native/Dart frames and keeps exit status zero; JIT decoding still works.
The development shell includes LLVM. A manual Go test skips the ELF proof clearly if LLVM is
missing; Nix checks set `OBS_REQUIRE_LLVM=1`, making absence a failure rather than a skipped proof.
Unknown identities and unresolved addresses are preserved. Decode cause stacks with different
build identities separately; ambiguous conflicting footers are refused. Split-debug ELF files
retain real Dart names; the obfuscation map is stored for other consumers, not applied as a text
substitution over stack traces.

JIT frames use `at <module>!0x06000001+0x<IL-offset> mvid:<module-MVID>`. The decoder reads
Portable PDB Document and MethodDebugInformation sequence points directly and appends `file:line`.
The exporter must supply an IL offset, never a raw JIT native PC. Unknown MVIDs pass through;
Windows/MSF PDBs are explicitly refused. **MEASURED:** a Go-built ELF resolves to function plus
source line using LLVM; a small Portable PDB fixture resolves IL offsets and rejects corruption.
**NOT TESTED:** actual NativeAOT/Flutter release artifacts or a compiler-produced .NET PDB.

## obs size

`obs size --req-per-second 1000 --services 30 --pods 90 --profiled-pods 30` prints deterministic
JSON: incremental traffic cores/GiB, pod-index MiB, typical stored series, metrics ingest memory
and disk GB/day, bucket GB/day and Class A/B requests/day. For that input: 0.27 traffic cores,
0.3 GiB, 81,000 series, 8.7 GB/day to the bucket and 8.3 GB/day of VictoriaLogs disk.
Idle base memory, query peaks, applications
and the control plane are excluded; these components are deliberately reported separately.

Traffic CPU/RAM and Class A/B retain the concept §10.7 **Loki-era reference costs**.
Current bucket storage includes only traces (4 GB/day per 1,000 req/s) and profiles
(4.7 GB/day per 30 profiled pods). `loki_logs_reference_gb_per_day` reports the historical
16.6 GB/day log figure separately; it is excluded from the bucket total. VictoriaLogs disk uses
8.3 GB/day per 1,000 req/s (concept §10.4, line 1440 in the 17 Nov corpus; spike 6, 28 B/line).
Class A/B are 17k/28k per 1,000 req/s as a historical write-path reference, not a measurement of
the current VictoriaLogs setup. Series use §3.2 (2,520/service + 60/pod), and metrics RAM/disk use §10.4
(0.6–1.1 KB/series; 5.3 GB/day/M series at 30s). Profile operations and interactive object reads
have no measured unit cost. Three checked-in golden cases cover idle, typical and busy shapes;
invalid, non-finite and negative inputs fail.

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
in `select(...)` or `by(...)` when the query otherwise returns rows. Trace alert
queries get the same parse and name checks plus the Tempo alert checks of
[TraceQL alert rules](#traceql-alert-rules), and `--live` runs them on the embedded Tempo
(an instant or range metrics query over `[end-from, end-to]`, no-rows and run errors reported
like a metric or log alert query). An alert query of a kind obs cannot run live fails with
`live-unsupported-query`.

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
attribute or value. **MEASURED against real Tempo, VictoriaLogs and Pyroscope** in the parity job
(green run https://github.com/AtomiCloud/nix-registry/actions/runs/44102413229): `TestParityTraceQL` (7 TraceQL searches and 2 range-metric queries across
6 seeded traces equal real Tempo), `TestParityLinks` (the link queries equal real VictoriaLogs and
Tempo: trace-to-logs 2, metric-to-traces 2, span-to-profile 2) and `TestParityProfiles`
(12 function self/inclusive shares and 2 span CPU shares equal real Pyroscope).

## Not tested

- Cross-architecture byte equality of histogram `_sum` floats: arm64's fused multiply-add already made
  these differ from amd64 in 0.4. The original full byte goldens run on amd64; the portable legacy
  goldens pin counter, log and histogram bucket/count bytes on every architecture.

Everything in this tool that no test, measurement or real run has proven, in one place. Each
item says what is missing; the section it came from has the detail. Everything not listed here
is MEASURED (a test or a run in this repository) or FROM SOURCE (read in the pinned upstream
source) where its section says so.

**Commands and builds**

- **`obs live` on dev, stage and prod.** No access to any of them; only the local sandbox was read for real (and recorded). The edge cases (two landscapes, a label outside the type, a failing query, an alert that fired before the window, redirects) are tested on hand-written payloads in the shapes the sandbox recorded. Not run against a real remote store: auth headers from the environment on a real gateway, and a very large metrics store (the points/min queries are one `count_over_time` per stored metric name and the series read is one `/api/v1/series` call; a store with more series than its `-search.maxSeries` answers an error, which obs reports as exit 2).
- **`obs live incidents`: what the recording does not hold.** The recorded Grafana state history holds a real Normal -> Pending and Normal -> Alerting change; its tag-only payload now proves a firing observation, with unknown spans. Optional lossless label maps, Alerting -> Normal, Recovering, overlapping instances, absent transitions, title/ID collisions, future windows and capped responses are tested on synthetic payloads. There is no current-state GET in the command or the incident recording. The `ALERTS` grouped list and raw range-selector path (a vmalert-style ruler) are tested on fake stores only, including bounded per-window reads, responses exceeding 64 MiB, merged chunk evidence and a carried-forward evaluation point that must not count as firing inside a later window.
- **`obs live usage` on a Prometheus server.** The points/min and series queries are parsed by the Prometheus PromQL parser (v0.315.1) and by MetricsQL in the Go tests, and recorded from VictoriaMetrics; no Prometheus server was run.
- **`obs live usage` with Tempo's own counters.** The sandbox's VictoriaMetrics does not scrape Tempo, so the `--all-services` bytes/min path was recorded as "no tempo_distributor_bytes_received_total"; the case where the counters exist is tested on a fake store only.
- **`obs look` on a retained stack (one real docker run, 19 Nov 2026, Grafana 13.3.0).** MEASURED: with `--seed-base`, a second look that adds a rule group leaves both groups in the sandbox folder, a third without it deletes only the removed one, a fourth with no rule left deletes the last (read back from `/api/v1/provisioning/alert-rules`). MEASURED: a look whose seed load failed after the metrics and logs went in (Pyroscope stopped while the profiles loaded; the mark `seed_incomplete` was in the state file) was followed by a look that removed the stores, loaded one new window (no sample older than the new window) and cleared the mark; a third look reused the completed snapshot. NOT TESTED for real: the same failure with Tempo (the code path is the same, the failing store differs); a live incident window that closes while a real setup runs (tested with a fake clock only).
- **`obs live look`** lists dashboards on the local sandbox only; no screenshots are taken by obs (the calling agent takes them).
- **Live label-spelling check on MetricsQL result labels.** `obs query --live` models which query-made labels
  survive a query (aggregations, binary operations, `group_left`/`group_right`, scalar sides, set operators) for the
  forms checked against the embedded engine in `src/internal/check/engine_controls_test.go`; other forms may be
  judged leniently (a wrong spelling then passes), never the other way round by design.
- `obs engines --check-chart` wired into a consumer's CI: the real chart's location in the chart registry is not specified, so the command is tested only on fixture charts ([obs engines --check-chart](#obs-engines---check-chart)). Chart pins in the nightly parity job are in the same position.
- `obs engines --check-chart` does not check a component whose image the values never set (it uses the chart default), and reads only the image paths listed under its section.
- `obs engines --check-chart` reads `Chart.yaml` and `values.yaml` only: an engine image set in a template or in another values file is not read, and a digest is compared with the recorded image digest, not resolved against a registry.
- `obs sandbox up` on aarch64 and macOS, and with podman (the box that ran it has docker only) ([`obs sandbox`](#obs-sandbox-the-local-stack-for-people)).
- Actual screenshots of the sandbox's incident markers, alert state and trace/profile panels: the calling LLM must capture and judge them; obs only prepares the stack and prompt.
- `obs look` with a pull request on GitHub: the agent posts the comment; obs only prints the prompt ([`obs look`](#obs-look-dashboard-screenshots-reviewed-by-the-calling-agent)).
- aarch64 and macOS builds run in CI; only x86_64 Linux was run by hand.

- `obs test` and `obs check` on a real repository's observability folder and in a consumer's CI: only the fixtures ran. `obs test` was not timed on a 24 h seed with traces or with many alerts.
- The old commands print no note naming the new commands (a stderr line was allowed; it was left out so that a script capturing both streams sees byte-identical output).

**Source entries and the keep-list**

- **A running vminsert with `-opentelemetry.convertMetricNamesToPrometheus`.** The stored-name parity test runs VictoriaMetrics' own OTLP parser and converter (the pinned module, the flag set) in-process; it does not start a vminsert or store anything. Not tested: names through the real write path, and the platform-metric name superset against what a real platform emits.
- **The platform renderer and the write-door keep rule.** The renderer that collects every `contract/keep.yaml`, adds `up` and `scrape_*`, and turns the names into the vminsert `-relabelConfig` `keep` action lives in the platform repository and is not run here. Not tested: that a collector with this rule drops exactly the undeclared names, and that nothing the contracts need is lost on any of the three paths (cluster Alloy, host Alloy, OTLP).
- **Platform metric names other than `*.duration`.** The stored names of such a metric are not in the contract, so all names it may have are kept; which one the store really holds was not measured on a running store.
- **A `source:` counter or gauge name is assumed to be the stored name.** Checked against the cluster draft's names and VictoriaMetrics' scrape path (names are stored as scraped); a source that reaches the store through OTLP (dotted name, unit suffix) would need the stored name written out.
- **Churn is information only.** `obs check` prints the approximate new series a day from each estimate's `churn_per_day`; the 30-day retention of the concept is not applied to the budget, and no measurement checks the estimates against a real cluster.
- **Several contracts** are discovered below the Git root; large monorepos were not timed, and a repository with no `.git` (a source archive) reads only `--dir`'s contract.

**Queries and data**

- Variable filling for LogsQL panels against the Grafana VictoriaLogs datasource: it reuses the Prometheus macros, and this exact query and time range through a real Grafana/VictoriaLogs UI is not run (the parity job is the referee) ([obs query --live](#obs-query---live)).
- Seeded log rows against a real VictoriaLogs fed through Alloy: the field names are FROM SOURCE.
- Annotation queries are not checked.
- Trace-ID routing: a trace ID typed in a TraceQL panel is routed by obs as the last core Tempo datasource did; parity with the separately shipped Tempo plugin is untested ([obs links](#obs-links)).

**Generated code**

- Each language's generated types compile and run against a stub written from the goal, not the real interfaces library ([obs gen](#obs-gen)).
- The per-language lint rules (`forbidigo` and the others in the table) are documented here; the lint config lives in the templates and is not run by obs.
- Actual NativeAOT or Flutter release artifacts, and a compiler-produced .NET PDB ([obs symbols](#obs-symbols)). Not checked: a Portable PDB carries no module MVID, so the uploader supplies it; obfuscation-map provenance comes from the build.

**obs.yaml**

- `obs sandbox up` with an image named by `obs.yaml` other than the pins: no container runtime ran it. Only the refusal of an unknown version (including an `also` entry with an absent or malformed digest), the choice of tags and the refusal to start over a container that runs another image (tested directly, with a fake runtime) are tested. `pins.json` records no extra (`also`) image yet, so today the only runnable versions are the pins.
- Trace panels run under `thresholds.traces` with the seed's end as the query-frontend's "now"; that choice is ours, not measured against a real Grafana panel refresh. A per-tenant `max_metrics_duration` override is not modelled.
- `dashboard_schema: 42` was read from the Grafana v13.3.0 source (`DashboardMigrator.ts`, `schemaversion/v42.go`), not measured against a running Grafana; `obs check` does not read the `schemaVersion` of the repo's dashboards.
- `thresholds.metrics.max_points_per_series` against a real VictoriaMetrics `-search.maxPointsPerTimeseries` (obs's default is the embedded engine's own 1,000,000, not the server's 30,000); `obs profile compare` reading `thresholds.profiles` is unit-tested, not run end to end; a Helm chart's values are not read, so the repo must keep `obs.yaml` equal to the chart by hand.

**Alerts**

- Notification and persistence behaviour, and exact UI interpolation of a query ([obs alerts](#obs-alerts)). Parity of metric and LogsQL alert evaluations and of the scheduler's transitions against real Grafana is MEASURED, not listed here: parity run https://github.com/AtomiCloud/nix-registry/actions/runs/44102413229 (`TestParityAlertEval`, `TestParityAlertHistory`).
- VictoriaLogs runtime timeout and concurrency equivalence on different hardware (this includes the timing refusal on a very slow runner described under `obs alerts`).
- Trace alert results against real Grafana and Tempo: the parity job (run https://github.com/AtomiCloud/nix-registry/actions/runs/44102413229) has no trace rule yet. Also not modelled or run: Tempo's `max_response_series` and per-tenant `max_metrics_duration` overrides; whether provisioned Tempo rules evaluate without the Grafana `tempoAlerting` toggle; math joining two un-reduced instant series (the frame time differs from obs's evaluation minute); a Helm chart's values file for `--tempo-config` (obs reads the rendered Tempo config only); exemplar frames themselves (refused by obs, never produced); the proposal to evaluate each trace rule once per run on per-minute buckets ([TraceQL alert rules](#traceql-alert-rules)).
- The §11.1.1 speed table was measured on a shared, loaded box, not a quiet one; the cache-hit rows were not re-measured, and the "what would close the time gap" proposal is untried ([speed](#obs-alerts-speed-concept-1111-re-measured-with-victorialogs)).

**Evidence and stores**

- `obs assert` against a real VictoriaMetrics export: the two accepted forms follow the documented API ([`obs assert`](#obs-assert)).
- Real `dotnet-trace` collection, and Dart `UserTag` spans: `UserTag:<name>` is obs's own converter convention, and the dart template must write the converter ([obs profile](#obs-profile-summarizebaselinecompare)).
- `obs store move` against a real cluster: moves and rollback (API-server patch and admission behaviour), chart compaction adapters, Tempo and Pyroscope drain durability, live store HTTP endpoints ([obs store](#obs-store)).
