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

## obs store

`obs store move <tempo|pyroscope|victorialogs|victoriametrics> --config move.yaml --dry-run`
prints the mandatory steps without commands or HTTP requests. Remove `--dry-run` to execute;
`--rollback` restores routing. Kubernetes access uses `kubectl` on PATH with the explicit context
and namespace from the config. Nothing runs automatically after a failed step; inspect the
reported state and request rollback explicitly. There is no skip/resume-step option.

The move config is a chart/operator adapter, not a chart generator. Paths are relative to the
config file. For example (fixture names only):

```yaml
context: fixture-only
namespace: observability-fixture
scaled_manifest: scaled.yaml
single_manifest: single.yaml # rollback; start with role labels false
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

The scaled manifest must contain every listed workload with positive explicit replicas, initially
without active role labels; every workload in the manifest must be listed for readiness checks.
It cannot change the old single, Alloy configuration, or the eight stable Services (including
their Endpoints/EndpointSlices). Headless Services are allowed, including Tempo's
`publishNotReadyAddresses: true` and Pyroscope's metastore Service. List custom-named Alloy
ConfigMaps/Secrets in `alloy_config_objects` (for example `[configmap/collector-config]`);
Alloy names/labels are also protected. Kind aliases such as `sts/tempo` are normalized.
Include all parts (Tempo: Kafka-compatible queue and `tempo-traces` topic).
Render the §10.4/10.5 store settings in the
manifests, including object storage from day one. Every workload must have a nonempty `matchLabels`
selector without expressions; role labels must not appear in that immutable selector. The tool
assigns each target only its configured role labels; it never labels Kafka as a read/write pod.
New roles become active and Ready before old roles are removed, so Services do not become empty.
The old single is never rolled by changing its template before its drain. Suspend external GitOps
reconciliation of these workloads while moving so it cannot undo a step.

Alloy must have at least two replicas. Tempo requires a verified compaction-control adapter
(`kubectl exec` with argv, no local shell), or a **separate** `compactor: deployment/backend-worker`
that can safely scale to zero (also set its original positive `compactor_replicas` for rollback). Using the old single itself as `compactor` is refused. Its drain
waits for `tempo_live_store_blocks_completed_total` and
`tempo_live_store_local_blocks_flushed_total` to rise and for flushed blocks to catch up with
completed blocks before scaling the single to zero. No `/flush` endpoint is assumed. Source:
Tempo 3.1.1 `modules/livestore/complete_block_lifecycle.go:18–22`; its downscale routes in
`cmd/tempo/app/modules.go:725–730` only change ring/shutdown state
(`modules/livestore/downscale.go:130–234`), so they do not replace this durability check.
An idle store that produces no new completed block times out safely; feed a known dataset before moving.

For Pyroscope, use `obs.diene/pyroscope-write` and `obs.diene/pyroscope-query` roles. Add
`metastore_manifest: metastore.yaml`: the old StatefulSet becomes the v2 metastore, retaining
its PVC and stable raft identity; no drain or deletion occurs. Set `profile_type` (for example
`process_cpu:cpu:nanoseconds:cpu:nanoseconds`) and a label selector in `query`;
`read_url` ends in `/querier.v1.QuerierService/SelectSeries`.
The metastore manifest must contain no active read/write labels; the tool explicitly persists
false labels on its pod template after conversion so a restart cannot rejoin those Services.

Tempo/Pyroscope rollback restores an isolated single and waits for Ready, activates its roles,
clears both scaled pod and template roles, rolls Alloy, checks scaled receive counters stay flat,
then verifies the data window. Provide `scaled_metrics_urls` for every scaled receiver pod, or
`metrics_url` for an endpoint aggregating the whole scaled receive tier. A receiving scaled pod
or counter reset stops rollback; scaled data/PVCs remain intact.

For VictoriaLogs and VictoriaMetrics omit the single/Alloy/role fields. Provide `scaled` storage
workloads, `routers` insert/select workloads, `routing_manifest`, `original_routing_manifest`,
and `metrics_url` (the insert-side `/metrics`). Routing manifests change only the `-storageNode`
lists (plus `-dedup.minScrapeInterval=1ms` for VM copies) and keep the first node. There is no drain,
rebalance, PVC deletion, or Alloy restart. VL checks the OTLP received counter rising and remote
send errors flat; VM checks inserted rows rising. Read URLs end in `/select/logsql/stats_query_range`
(VL) or `/select/0/prometheus/api/v1/query_range` (VM). Supply a query counting the known dataset
per minute, aggregated to one series. Rollback removes the new endpoints and **retains the pods
and PVCs**; data written there is unreadable until they are added back.

Verification takes one fixed, closed 60-minute window before the move and queries the same
window through the read Service afterward. Missing minutes, empty/all-zero baselines, errors,
and unequal counts fail. `tenant` optionally sets `X-Scope-OrgID` on range queries. Query
responses use each store's real format: Tempo `series[].samples[].timestampMs/value`,
Pyroscope `series[].points[]`, and Victoria stores' Prometheus matrices.
Counter endpoints must expose only the intended old single or insert tier; absent/reset counters fail.
`--interval` defaults to 15s and `--timeout` to 15m. **NOT TESTED:** real cluster moves/rollback,
chart compaction adapters, Tempo/Pyroscope drain durability, or live store HTTP endpoints.
Unit tests use fake Kubernetes and HTTP transports; they never contact a real cluster.

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
