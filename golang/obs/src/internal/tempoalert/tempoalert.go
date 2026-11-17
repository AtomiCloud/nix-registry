// SPDX-License-Identifier: AGPL-3.0-only
// Port of the Grafana Tempo datasource's alert-query path. Source read:
// github.com/grafana/grafana-tempo-datasource v13.3.0 (commit 7bbc067f7b95), the
// plugin release paired with Grafana 13.3.0 (Grafana preinstalls "tempo" since
// commit 61ec98144cd4 moved it out of core; pkg/setting/setting_plugins.go:43).
// The plugin is AGPL-3.0, like Grafana; see PROVENANCE.md.

// Package tempoalert decides what Grafana 13.3.0 does with a Tempo query in an
// alert rule: which plugin path runs it, which Tempo endpoint it calls with which
// start, end and step, and how the plugin turns Tempo's series into data frames.
package tempoalert

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/tempo/pkg/tempopb"
	v1 "github.com/grafana/tempo/pkg/tempopb/common/v1"
	"github.com/grafana/tempo/pkg/traceql"
)

// PluginVersion is the Tempo datasource whose alert path this package ports.
const PluginVersion = "grafana-tempo-datasource v13.3.0 (commit 7bbc067f7b95)"

// Model is the part of a Tempo query model the plugin reads for an alert
// (pkg/tempo/kinds/dataquery/types_dataquery.go: query, queryType,
// metricsQueryType, step, exemplars).
type Model struct {
	Query            string
	QueryType        string
	MetricsQueryType string
	Step             *string
	Exemplars        *int64
}

// FromModel reads a rule query's model. ngalert's AlertQuery.PreSave copies
// model.queryType over the rule's queryType (pkg/services/ngalert/models/
// alert_query.go:297-315, Grafana v13.3.0), so the model's value wins.
func FromModel(model map[string]any, ruleQueryType string) Model {
	m := Model{QueryType: ruleQueryType}
	m.Query, _ = model["query"].(string)
	if qt, ok := model["queryType"].(string); ok {
		m.QueryType = qt
	}
	m.MetricsQueryType, _ = model["metricsQueryType"].(string)
	if s, ok := model["step"].(string); ok {
		m.Step = &s
	}
	switch v := model["exemplars"].(type) {
	case float64:
		n := int64(v)
		m.Exemplars = &n
	case int:
		n := int64(v)
		m.Exemplars = &n
	case int64:
		m.Exemplars = &v
	}
	return m
}

// Instant is isInstantQuery (pkg/tempo/traceql_query.go:214-219): only an explicit
// metricsQueryType "instant" calls /api/metrics/query; anything else is a range query.
func (m Model) Instant() bool { return m.MetricsQueryType == "instant" }

// metricsQuery is isMetricsQuery's pattern (pkg/tempo/traceql_query.go:221-224).
var metricsQuery = regexp.MustCompile(`\|\s*(rate|count_over_time|avg_over_time|sum_over_time|max_over_time|min_over_time|quantile_over_time|histogram_over_time|compare)\s*\(`)

// IsMetricsQuery reports whether the plugin runs q as a TraceQL metrics query.
// Any other text goes to the trace search path (traceql_query.go:42-46).
func IsMetricsQuery(q string) bool { return metricsQuery.MatchString(q) }

// Refusal is a query Grafana cannot turn into a numeric alert input. Rule names the
// check (for the report), Reason says why with the source lines.
type Refusal struct{ Rule, Reason string }

func (r *Refusal) Error() string { return r.Reason }

// Check returns the reason Grafana 13.3.0 would not evaluate this query as a
// numeric alert input, or nil. It does not parse the TraceQL; callers run Tempo's
// parser too (syntax and attribute names are checked there).
func (m Model) Check() *Refusal {
	switch m.QueryType {
	case "traceql", "traceqlSearch":
	case "traceId":
		return &Refusal{"tempo-alert-query-type", "queryType traceId is a trace lookup: the plugin returns the trace itself, not numbers an alert condition can read (pkg/tempo/tempo.go:107-113)"}
	default:
		return &Refusal{"tempo-alert-query-type", fmt.Sprintf("queryType %q: the Tempo datasource answers only traceql, traceqlSearch and traceId and fails the query with \"unsupported query type\" (pkg/tempo/tempo.go:106-127); alert queries use traceql", m.QueryType)}
	}
	if strings.TrimSpace(m.Query) == "" {
		return &Refusal{"tempo-alert-query", "model.query is empty: the plugin fails with \"query is required\" (pkg/tempo/traceql_query.go:38-40)"}
	}
	if !IsMetricsQuery(m.Query) {
		return &Refusal{"tempo-alert-search", "not a TraceQL metrics query (no | rate(), count_over_time(), quantile_over_time() ... stage the plugin recognises, pkg/tempo/traceql_query.go:42-46,221-224): a search returns a trace table, which Grafana's expression engine cannot read as numbers"}
	}
	if !m.Instant() {
		off, err := ExemplarsDisabled(m.Query)
		if err == nil && !off {
			return &Refusal{"tempo-alert-range-exemplars", "range query (metricsQueryType is not \"instant\") with exemplars: Tempo 3.1.1 returns up to 100 exemplars by default (modules/frontend/metrics_query_range_handler.go:235-255), the plugin adds them as extra frames with a traceId column (pkg/tempo/traceql/exemplars.go:11-38, metrics.go:58-65), and Grafana's expression engine refuses a frame that is not a wide series (pkg/expr/converter.go:79-90); set metricsQueryType: instant (the documented alert mode) or end the query with `with(exemplars=false)`"}
		}
		if m.Step != nil && *m.Step != "" {
			if _, err := ParseStep(*m.Step); err != nil {
				return &Refusal{"tempo-alert-step", fmt.Sprintf("step %q: %v (Tempo pkg/api/http.go:685-712)", *m.Step, err)}
			}
		}
	}
	return nil
}

// ExemplarsDisabled reports whether the query's hints switch exemplars off, which
// is the only way a range alert query gets no exemplar frames: an unset or zero
// exemplars parameter becomes the frontend's maximum (normalizeRequestExemplars,
// modules/frontend/metrics_query_range_handler.go:235-255).
func ExemplarsDisabled(q string) (bool, error) {
	expr, err := traceql.ParseNoOptimizations(q)
	if err != nil {
		return false, err
	}
	if v, ok := expr.Hints.GetInt(traceql.HintExemplars, false); ok {
		return v <= 0, nil
	}
	if v, ok := expr.Hints.GetBool(traceql.HintExemplars, false); ok {
		return !v, nil
	}
	return false, nil
}

// ParseStep is Tempo's parseSecondsOrDuration plus its negative-step check
// (pkg/api/http.go:685-712).
func ParseStep(value string) (time.Duration, error) {
	var d time.Duration
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		ts := f * float64(time.Second)
		if ts > float64(math.MaxInt64) || ts < float64(math.MinInt64) {
			return 0, fmt.Errorf("cannot parse %q to a valid duration. It overflows int64", value)
		}
		d = time.Duration(ts)
	} else if pd, err := time.ParseDuration(value); err == nil {
		d = pd
	} else {
		return 0, fmt.Errorf("cannot parse %q to a valid duration", value)
	}
	if d < 0 {
		return 0, fmt.Errorf("cannot parse %q: step must be positive", value)
	}
	return d, nil
}

// Window is the request the plugin sends for one evaluation: ngalert's time range
// [now-from, now-to] (RelativeTimeRange.ToTimeRange) as whole Unix seconds
// (performMetricsQuery, pkg/tempo/traceql_query.go:145, createMetricsQuery :189-194).
func Window(now time.Time, from, to time.Duration) (time.Time, time.Time) {
	return time.Unix(now.Add(-from).Unix(), 0).UTC(), time.Unix(now.Add(-to).Unix(), 0).UTC()
}

// RangeStep is the step Tempo uses for a range request: the model's step when set,
// else DefaultQueryRangeStep (pkg/api/http.go:685-689; a zero step is also
// defaulted by the query-range handler, metrics_query_range_handler.go:52-54).
func (m Model) RangeStep(start, end time.Time) (time.Duration, error) {
	if m.Step != nil && *m.Step != "" {
		d, err := ParseStep(*m.Step)
		if err != nil {
			return 0, err
		}
		if d > 0 {
			return d, nil
		}
	}
	return time.Duration(traceql.DefaultQueryRangeStep(uint64(start.UnixNano()), uint64(end.UnixNano()))), nil
}

// Label is one frame label.
type Label struct{ Name, Value string }

// Labels is transformLabelsAndGetName's label map (pkg/tempo/traceql/metrics.go:116-120)
// with metricsValueToString (:97-114): a string value keeps its double quotes
// ("checkout"), numbers and booleans are plain text.
func Labels(kvs []v1.KeyValue) []Label {
	out := make([]Label, 0, len(kvs))
	seen := map[string]int{}
	for _, kv := range kvs {
		v, _ := valueToString(kv.GetValue())
		if i, ok := seen[kv.GetKey()]; ok { // a map: the last value wins
			out[i].Value = v
			continue
		}
		seen[kv.GetKey()] = len(out)
		out = append(out, Label{kv.GetKey(), v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func valueToString(value *v1.AnyValue) (string, string) {
	switch value.GetValue().(type) {
	case *v1.AnyValue_DoubleValue:
		res := strconv.FormatFloat(value.GetDoubleValue(), 'f', -1, 64)
		return res, res
	case *v1.AnyValue_IntValue:
		res := strconv.FormatInt(value.GetIntValue(), 10)
		return res, res
	case *v1.AnyValue_StringValue:
		return fmt.Sprintf("\"%s\"", value.GetStringValue()), value.GetStringValue()
	case *v1.AnyValue_BoolValue:
		res := strconv.FormatBool(value.GetBoolValue())
		return res, res
	}
	return "", ""
}

// Point is one frame row: unix milliseconds and a value.
type Point struct {
	Ms int64
	V  float64
}

// Frame is one series frame as the plugin builds it: an instant response gives one
// row per series (TransformInstantMetricsResponse, metrics.go:68-95; its time is
// the plugin's time.Now(), the caller passes the evaluation time); a range
// response gives one row per sample (TransformMetricsResponse, :16-66). Exemplar
// frames are not produced: Check refuses range queries that would carry them.
type Frame struct {
	Labels []Label
	Points []Point
}

// InstantFrames converts /api/metrics/query's response.
func InstantFrames(resp *tempopb.QueryInstantResponse, now time.Time) []Frame {
	out := make([]Frame, 0, len(resp.Series))
	for _, s := range resp.Series {
		out = append(out, Frame{Labels: Labels(s.Labels), Points: []Point{{now.UnixMilli(), s.Value}}})
	}
	return out
}

// RangeFrames converts /api/metrics/query_range's response.
func RangeFrames(resp *tempopb.QueryRangeResponse) []Frame {
	out := make([]Frame, 0, len(resp.Series))
	for _, s := range resp.Series {
		f := Frame{Labels: Labels(s.Labels), Points: make([]Point, 0, len(s.Samples))}
		for _, p := range s.Samples {
			f.Points = append(f.Points, Point{p.GetTimestampMs(), p.GetValue()})
		}
		out = append(out, f)
	}
	return out
}
