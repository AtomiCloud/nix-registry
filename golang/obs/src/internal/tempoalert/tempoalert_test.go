package tempoalert

import (
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/grafana/tempo/pkg/tempopb/common/v1"
)

func TestModelReadsPluginFieldsModelQueryTypeWins(t *testing.T) {
	m := FromModel(map[string]any{"query": "{} | rate()", "queryType": "traceql", "metricsQueryType": "instant", "step": "30s", "exemplars": float64(0)}, "range")
	if m.Query != "{} | rate()" || m.QueryType != "traceql" || !m.Instant() || m.Step == nil || *m.Step != "30s" || m.Exemplars == nil || *m.Exemplars != 0 {
		t.Fatalf("model: %+v", m)
	}
	// AlertQuery.PreSave: the model's queryType overrides the rule's; absent, the rule's stays.
	if got := FromModel(map[string]any{"query": "x"}, "traceql").QueryType; got != "traceql" {
		t.Fatalf("rule queryType lost: %q", got)
	}
	if got := FromModel(map[string]any{"query": "x", "queryType": ""}, "traceql").QueryType; got != "" {
		t.Fatalf("model queryType must win even when empty: %q", got)
	}
	// Only an explicit "instant" is instant (isInstantQuery); unset is a range query.
	for _, mt := range []string{"", "range", "Instant"} {
		if FromModel(map[string]any{"metricsQueryType": mt}, "").Instant() {
			t.Fatalf("metricsQueryType %q treated as instant", mt)
		}
	}
}

func TestCheckRoutesLikeThePlugin(t *testing.T) {
	step := func(s string) *string { return &s }
	for _, tc := range []struct {
		name string
		m    Model
		rule string // "" = accepted
	}{
		{"instant metrics", Model{Query: `{status=error} | rate()`, QueryType: "traceql", MetricsQueryType: "instant"}, ""},
		{"traceqlSearch type also runs TraceQL", Model{Query: `{} | count_over_time()`, QueryType: "traceqlSearch", MetricsQueryType: "instant"}, ""},
		{"range with exemplars off", Model{Query: `{} | quantile_over_time(duration, .9) with (exemplars=false)`, QueryType: "traceql"}, ""},
		{"range with exemplars=0", Model{Query: `{} | rate() with (exemplars=0)`, QueryType: "traceql", Step: step("1m")}, ""},
		{"range with exemplars", Model{Query: `{} | rate()`, QueryType: "traceql", MetricsQueryType: "range"}, "tempo-alert-range-exemplars"},
		{"range with exemplars=true", Model{Query: `{} | rate() with (exemplars=true)`, QueryType: "traceql"}, "tempo-alert-range-exemplars"},
		{"trace id lookup", Model{Query: "0123abcd", QueryType: "traceId"}, "tempo-alert-query-type"},
		{"unset query type", Model{Query: `{} | rate()`, MetricsQueryType: "instant"}, "tempo-alert-query-type"},
		{"service map", Model{Query: `{} | rate()`, QueryType: "serviceMap", MetricsQueryType: "instant"}, "tempo-alert-query-type"},
		{"empty query", Model{QueryType: "traceql", MetricsQueryType: "instant"}, "tempo-alert-query"},
		{"search", Model{Query: `{status=error}`, QueryType: "traceql", MetricsQueryType: "instant"}, "tempo-alert-search"},
		{"metrics fn the plugin pattern misses", Model{Query: `{} | count_over_time() | topk(1)`, QueryType: "traceql", MetricsQueryType: "instant"}, ""},
		{"bad step", Model{Query: `{} | rate() with (exemplars=false)`, QueryType: "traceql", Step: step("soon")}, "tempo-alert-step"},
		{"negative step", Model{Query: `{} | rate() with (exemplars=false)`, QueryType: "traceql", Step: step("-1")}, "tempo-alert-step"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.m.Check()
			switch {
			case tc.rule == "" && r != nil:
				t.Fatalf("refused: %v", r)
			case tc.rule != "" && (r == nil || r.Rule != tc.rule):
				t.Fatalf("got %+v, want rule %s", r, tc.rule)
			case r != nil && !strings.Contains(r.Reason, "pkg/"):
				t.Fatalf("refusal cites no source line: %s", r.Reason)
			}
		})
	}
}

func TestIsMetricsQueryPattern(t *testing.T) {
	for q, want := range map[string]bool{
		`{} | rate()`:                                true,
		`{}|count_over_time ()`:                      true,
		`{} | quantile_over_time(duration, .9)`:      true,
		`{} | histogram_over_time(duration)`:         true,
		`{} | compare({status=error})`:               true,
		`{} | select(span.x)`:                        false,
		`{ name = "rate()" }`:                        false,
		`{} | stddev_over_time(duration)`:            false, // not in the plugin's list: routed to search
		`{} | sum_over_time(span.amount) by (.name)`: true,
	} {
		if got := IsMetricsQuery(q); got != want {
			t.Errorf("%s: got %v want %v", q, got, want)
		}
	}
}

func TestLabelsQuoteStringsLikeThePlugin(t *testing.T) {
	kv := func(k string, v *v1.AnyValue) v1.KeyValue { return v1.KeyValue{Key: k, Value: v} }
	got := Labels([]v1.KeyValue{
		kv("span.outcome", &v1.AnyValue{Value: &v1.AnyValue_StringValue{StringValue: "error"}}),
		kv("p", &v1.AnyValue{Value: &v1.AnyValue_DoubleValue{DoubleValue: 0.9}}),
		kv("span.http.response.status_code", &v1.AnyValue{Value: &v1.AnyValue_IntValue{IntValue: 502}}),
		kv("span.retry", &v1.AnyValue{Value: &v1.AnyValue_BoolValue{BoolValue: true}}),
	})
	want := []Label{{"p", "0.9"}, {"span.http.response.status_code", "502"}, {"span.outcome", `"error"`}, {"span.retry", "true"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("labels %v want %v", got, want)
	}
}

func TestWindowAndSteps(t *testing.T) {
	now := time.Unix(1800000060, 900_000_000)
	start, end := Window(now, 5*time.Minute, time.Minute)
	if start.Unix() != 1800000060-300 || end.Unix() != 1800000060-60 || start.Nanosecond() != 0 || end.Nanosecond() != 0 {
		t.Fatalf("window %s..%s: want whole seconds now-5m..now-1m", start, end)
	}
	for in, want := range map[string]time.Duration{"60": time.Minute, "1.5": 1500 * time.Millisecond, "2m": 2 * time.Minute, "0": 0} {
		if got, err := ParseStep(in); err != nil || got != want {
			t.Fatalf("ParseStep(%q) = %s, %v", in, got, err)
		}
	}
	s := "1m"
	if d, err := (Model{Step: &s}).RangeStep(start, end); err != nil || d != time.Minute {
		t.Fatalf("explicit step: %s %v", d, err)
	}
	// Tempo's default for a 4 minute window: 240 points at 1 s (DefaultQueryRangeStep).
	if d, err := (Model{}).RangeStep(start, end); err != nil || d != time.Second {
		t.Fatalf("default step: %s %v", d, err)
	}
	if d, err := (Model{}).RangeStep(now.Add(-time.Hour), now); err != nil || d != 15*time.Second {
		t.Fatalf("default 1 h step: %s %v", d, err)
	}
}
