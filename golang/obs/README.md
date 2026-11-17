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

## `obs profile summarize|baseline|compare`

Step 7 of the workflow (concept §11.6). An e2e run sends a fixed number of requests, takes a CPU
profile of the load window and compares each function's and each span's **share** of CPU with a
stored baseline. Files only: no network.

```text
obs profile summarize --format pprof|cpuprofile|speedscope [--own REGEX]... [--run run.json] [--top N] [--json] FILE
obs profile baseline  --format F --service S --scenario N [--profiler P] [--own REGEX]... [--dir e2e/profile-baseline] [--update] RUN1 RUN2 RUN3
obs profile compare   --format F (--baseline FILE | --service S --scenario N [--dir DIR]) [--limits e2e/profile-limits.yaml] [--run run.json] FILE
```

Exit 0 when no row is slower than its limit, 1 when one is, 2 on a usage or input error.

**Formats.** `pprof` (Go, gzip or raw; value types `samples` for the count and `cpu` for the weight),
Chrome `.cpuprofile` (Bun, Flutter web; `samples[]` or `hitCount`; `(idle)` is not CPU) and `speedscope`
(`sampled` profiles; `evented` is refused with a pointer to speedscope's importer).

**Spans.** A sample is in a span when it has the pprof label `span_name` (Go, `otel-profiling-go`), or
when its stack holds a frame `span__<name>` (Bun) or `UserTag:<name>` (Dart). Spans count
children (a sample in two nested spans is in both). A span with no sample has no row (concept §8.2:
short spans are often empty). _FROM SOURCE: the Dart `UserTag:` spelling is the name the profile
converter must give the frame; it is not yet checked against real `getCpuSamples` output (NOT TESTED)._

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
expression matched against the function name and file; repeatable; default: every frame is own code).
`baseline` stores the patterns and `compare` uses the baseline's. **Closures** (never rows): Go `.funcN`,
`.gowrap`, `-fm`; JS `(anonymous)` and empty names; Dart `<anonymous closure>`; .NET `<>c__`, `>b__`,
`lambda_method`.

**Baseline** = median of exactly 3 runs → `e2e/profile-baseline/<service>.<scenario>.json` (about 1 KB;
no times, sorted rows, shares rounded to 6 places, so two runs give byte-identical files). Rows:
functions whose median own share is ≥ 3 %, every span with samples in at least 2 of the 3 runs, the median
samples and the median OS CPU per request. An existing baseline is replaced only with `--update`
(baselines change only through a PR). `--profiler dotnet-trace` is stored and gives every row 30 %.

**Compare** prints `what / baseline / now / change (limit)` for every baseline row and exits 1 when a row is
slower than its limit. For a function or span row the change is `now share / baseline share − 1`.

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
      limit: 0.10 # a fraction: 0.10 = 10 %
      why: the page template is being rewritten # required
  ```

  The most specific matching override wins. A failure names the source of the limit.

- **Faster is reported, never failed.** A row with no samples now is shown as faster. A function that is
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

- Variable filling for LogsQL panels against the Grafana VictoriaLogs datasource (it reuses the Prometheus macros).
- Seeded log rows against a real VictoriaLogs fed through Alloy (field names are FROM SOURCE).
- LogsQL `_time:[start, end)` handling against real Grafana (the nightly parity job, a later release).
- aarch64 and macOS builds are in CI; only x86_64 Linux was run by hand.
