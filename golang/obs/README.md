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
allowed-keys table; no per-metric functions). Shared port/value base types come from the
language's interfaces library rather than being redefined in each generated file. Also writes
`<dir>/contract/events.json` (event names, read by the log-alert event check) and
`<dir>/contract/aggregation.yaml` (the §4.1 VictoriaMetrics stream-aggregation file: counters and
histogram parts get `total`; gauges get `sum_samples`, `avg`, `max` or `last` with `dedup_interval`;
`without` = `pod_keys`; `per_pod` metrics and unlisted metrics are in no match).
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

| Language | Hole the compiler leaves | Run-time guard (tested) | Lint rule (NOT TESTED) |
| -------- | ------------------------ | ----------------------- | ---------------------- |
| Go | `obs.New(Event\|Counter\|...)` from app code; zero enum, unset number | `Register`ed tables; empty/unset refused | `forbidigo` (below) |
| Bun | hand-written `{name, level}`; widened object with an extra key; `as` cast; a foreign table | tables registered by `signals.ts`; any other table poisons | no `as <generated type>`; no event object literal |
| .NET | `default` bounded number, `null!` enum | `Port.Install` tables; both refused | Roslyn analyzer for `default(Int<...>)` |
| Dart | app class `extends` a library base; non-const `Shard(n)` | `DurianSignalPort.install` tables | no `extends` of library bases; no non-const `Shard(...)` |

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

| Language | Default `interfaces` | In the goal | Assumed, not in goal |
| -------- | -------------------- | ----------- | -------------------- |
| Go | `github.com/ProtoDurian/diene.go-interfaces/obs` | module `ProtoDurian/diene.go-interfaces`; `obs.Add`, `obs.Int[Spec]`, `obs.NewInt`, `obs.AtTime` with an error return; `obs.Error(ctx, Event[Error], ...)` | the `/obs` package path; `IntSpec` `Name/Min/Max/Unit` (int64); `Int.Valid`; `Level` and the six markers; `NewEvent/NewCounter/NewHistogram/NewGauge/NewSpan`; `DurianSignal*` carriers and info rows (`Labels`, `Values`, `Ranges`); `Field`; `Record/Set/Warn/Info`; one-shot `Register` returning an error (a second one poisons the port), `DurianSignalTables`, `Dropped` |
| Bun | `@atomicloud/diene.interfaces` | `counter(name, labels)`; `log.error(ev, fields?, err?)`; `bounded(spec, n): Result`; `Tracer`/`Span`, `DurianSignalSpanTable` | `register(tables)` (one-shot; a second one or a foreign table poisons the port); `metrics(table)`, `histogram/gauge`, `MetricTable` (with `values`, `ranges`); `logger(eventTable)`, `EventTable`, `Level`; `dropped()`; `tracer(table)`; `Bounded<N>`; a synchronous `{ok, value}` Result (the published Result is promise-based) |
| .NET | `AtomiCloud.Diene.Interfaces` | `Port.Add(Names.X, new XLabels(...))`; `Int<Spec>.Of(n)` returning a `Result` (package `AtomiCloud.Diene.Result`: struct `Result<T, E>`, `Ok/Err`, `IsSuccess`); `Log.Error(Event<ErrorLevel>, ...)`; internal carrier constructors | namespace `AtomiCloud.Diene.Result` and `Value/Error`; `IDurianSignal*` interfaces, `Valid/Wire`; `AtTime`; `DurianSignalDate`; label attribute and info rows; `Port.Record/Set/Install/Dropped`; info row `Values`/`Ranges`; `Log.Warn` |
| Dart | `diene_interfaces` | `checkoutCharges.add(XLabels(...))`; `error(Event<ErrorLevel>, ...)`; enums; extension types; `shardSpec.of(n)` returning a `Result`; `entries` getter | barrel `diene_interfaces.dart`; `DurianSignalWire/Labels/Metric`, carrier bases and constructors; `DurianSignalBoundSpec` and `atTime`; sealed level markers; `DurianSignalPort` (one-shot `install` returning a Result, a second one poisons the port, `add/record/set/dropped`); generated `installSignals()`; `warn/info`; info rows (`labels` Type, `values`, `ranges`); local `Result/DurianSignalOk/DurianSignalErr` in place of `diene_result` |

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

| Store | Change obs makes to existing objects |
|---|---|
| Tempo | single: `spec.replicas` N to 0 and every template role `true` to `false`, in one patch, after the drain; compactor (if separate): replicas N to 0 |
| Pyroscope | single: `-target=all` to `-target=metastore` (same spelling) and every template role to `false`; replicas, claims, raft id and all else untouched |
| VictoriaLogs, VictoriaMetrics | each router: the one `-storageNode` flag gains the derived endpoints (same spelling, existing endpoints kept in order) |
| VictoriaMetrics, `copies: 2` | vminsert also gains `-replicationFactor=2`; vmselect gains `-dedup.minScrapeInterval=1ms` (VictoriaMetrics v1.156.0 docs: required on vmselect when replication is enabled); every listed existing vmstorage gains `-dedup.minScrapeInterval=1ms` at the front of its args (concept 10.5) |

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
scaled_manifest: vm-scaled.yaml   # new vmstorage StatefulSet + its headless Service
scaled: [statefulset/vmstorage-b]
routers:
  - {workload: deployment/vminsert, role: insert, port: 8400}
  - {workload: deployment/vmselect, role: select, port: 8401}
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
