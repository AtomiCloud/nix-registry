# Grafana Tempo datasource (alert path)

Source read: github.com/grafana/grafana-tempo-datasource **v13.3.0** (commit
`7bbc067f7b950057beebcaeeb910f16e98f2dacb`), the release paired with Grafana
13.3.0. Grafana commit `61ec98144cd4` ("Tempo: Remove from core plugins") moved the
datasource out of the Grafana repository; Grafana 13.3.0 preinstalls it
(`pkg/setting/setting_plugins.go:43`). The plugin is AGPL-3.0, the same licence as
Grafana (`share/licenses/obs/grafana/LICENSE`).

Ported (re-written in Go against Tempo's own protobuf types, no code copied
verbatim except the metrics-query pattern):

- `pkg/tempo/tempo.go:106-127`: query-type routing.
- `pkg/tempo/traceql_query.go:38-46, 143-224`: metrics vs search routing, the
  instant/range endpoint choice, start/end as Unix seconds, step and exemplars.
- `pkg/tempo/traceql/metrics.go:16-142`: frame shapes and label strings.
- `pkg/tempo/traceql/exemplars.go:11-65`: why a range alert query with exemplars
  fails in Grafana's expression engine (`pkg/expr/converter.go:79-90`, Grafana v13.3.0).

Tempo 3.1.1 server behaviour the port relies on (commit `9db7df79d2af`):
`modules/frontend/metrics_query_handler.go:139-155, 224-267` (instant rewrite, end
cutoff and translation), `modules/frontend/metrics_query_range_handler.go:29, 52-54,
143-160, 199-255` (end cutoff, validation, alignment, default step, exemplar default),
`modules/frontend/metrics_query_validation.go:14-54` (max_duration, max_intervals),
`modules/frontend/config.go:53-54, 89, 128-154` (query_end_cutoff and defaults),
`modules/frontend/metrics_query_range_sharder.go:111` (second alignment, trimmed by the
combiner), `pkg/api/http.go:685-712` (step parsing), `pkg/traceql/engine_metrics.go:37-78`
(DefaultQueryRangeStep), `:175-233, 2083-2085` (AlignRequest, AlignEndToLeft, bucket
bounds). The frontend part lives in `internal/engines/tempo/alertapi.go`.

Grafana 13.3.0 gates Tempo alerting behind the experimental `tempoAlerting`
feature toggle (`pkg/services/featuremgmt/registry.go:1708-1714`,
`pkg/services/pluginsintegration/pipeline/pipeline.go:46-50`): without it the
plugin's `alerting: true` is switched off.
