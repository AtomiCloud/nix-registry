package alerts

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/seed"
	"github.com/AtomiCloud/obs/internal/tempoalert"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"gopkg.in/yaml.v3"
)

// traceTestEngine: two error spans per second during [start+10m, start+12m), one
// ok span per second all along.
func traceTestEngine(start time.Time) *tempo.TempoEngine {
	var traces []*seed.TTrace
	n := 0
	span := func(at time.Time, status int, outcome string, dur time.Duration) {
		n++
		traces = append(traces, &seed.TTrace{TraceID: [16]byte{byte(n), byte(n >> 8), byte(n >> 16), 9}, Spans: []*seed.TSpan{{
			SpanID: [8]byte{byte(n), byte(n >> 8), byte(n >> 16), 1}, Name: "POST /charge", Kind: 2, Status: status, Service: "checkout",
			StartNs: uint64(at.UnixNano()), DurNs: uint64(dur), Attrs: map[string]seed.TAttrVal{"outcome": seed.TStr(outcome)},
		}}})
	}
	for s := 0; s < 20*60; s++ {
		at := start.Add(time.Duration(s)*time.Second + 300*time.Millisecond)
		span(at, 1, "ok", 50*time.Millisecond)
		if s >= 600 && s < 720 {
			span(at.Add(100*time.Millisecond), 2, "error", 80*time.Millisecond)
			span(at.Add(200*time.Millisecond), 2, "error", 80*time.Millisecond)
		}
	}
	return tempo.NewTempoEngine(traces)
}

func traceRule(t *testing.T, q Query) *cRule {
	return compile(t, Alert{ID: "traces", Condition: "C", NoDataState: "NoData", Queries: map[string]Query{"A": q}, Expressions: map[string]ExprDef{
		"B": {Reduce: &ReduceDef{Input: "A", Function: "last"}},
		"C": {Threshold: &ThresholdDef{Input: "B", Type: "gt", Params: []float64{1}}},
	}})
}

func TestTraceQLInstantAlertIsASeriesInput(t *testing.T) {
	start := time.Unix(1800000000, 0).UTC()
	eng := Engines{Datasources: map[string]DatasourceEngine{"traces": tracesEngine{traceTestEngine(start)}}}
	m := tempoalert.Model{Query: `{ status = error } | rate() by (span.outcome)`, QueryType: "traceql", MetricsQueryType: "instant"}
	q := Query{Datasource: "traces", UID: "fixture-traces", Expr: m.Query, Tempo: &m, From: time.Minute}
	run := Run{Name: "spike", Start: start, End: start.Add(20 * time.Minute)}
	d := fetchQuery(eng, q, run, time.Minute)
	if !d.seriesFrames || d.perTick == nil {
		t.Fatalf("Tempo answers per tick with series frames: %+v", d)
	}
	cr := traceRule(t, q)
	if cr.light {
		t.Fatal("a trace rule must not take the numbers-only light path")
	}
	// Inside the burst: one series, labels as the plugin writes them, value ~2/s.
	in := d.at(start.Add(11 * time.Minute))
	if in.err != nil || !in.isRange || len(in.series) != 1 {
		t.Fatalf("tick in burst: %+v", in)
	}
	if want := []Label{{Name: "span.outcome", Value: `"error"`}}; !reflect.DeepEqual(in.series[0].labels, want) {
		t.Fatalf("labels %v want %v", in.series[0].labels, want)
	}
	if v := in.series[0].vs; len(v) != 1 || v[0] != 2 || in.series[0].ts[0] != start.Add(11*time.Minute).UnixMilli() {
		t.Fatalf("one point at the evaluation time, 2 error spans/s: ts=%v vs=%v", in.series[0].ts, v)
	}
	res := cr.evalTick(start.Add(11*time.Minute), map[string]qTick{"A": in}, nil)
	if len(res) != 1 || res[0].state != stAlerting || !reflect.DeepEqual(res[0].labels, data.Labels{"span.outcome": `"error"`}) {
		t.Fatalf("burst: %+v", res)
	}
	// Outside: Tempo returns no series, Grafana sees no frames: NoData with the query's uid.
	out := d.at(start.Add(5 * time.Minute))
	res = cr.evalTick(start.Add(5*time.Minute), map[string]qTick{"A": out}, nil)
	if len(res) != 1 || res[0].state != stNoData || !reflect.DeepEqual(res[0].labels, data.Labels{"datasource_uid": "fixture-traces", "ref_id": "A"}) {
		t.Fatalf("quiet: %+v", res)
	}
	// A whole run evaluates through the state machine: fires at 11m and 12m; at 13m the
	// series is gone (NoData) and the alerting instance resolves as a missing series
	// after its second missed evaluation, so 13m still counts as firing.
	ar := evalRuleRun(eng, &queryCache{m: map[qKey]*qEntry{}}, cr, run, false, nil)
	if !ar.Fired || ar.FirstFireAt != "11m0s" || ar.FiringSteps != 3 || ar.FiringTimes[2] != start.Add(13*time.Minute).UnixMilli() {
		t.Fatalf("run: fired=%v first=%s steps=%d", ar.Fired, ar.FirstFireAt, ar.FiringSteps)
	}
}

func TestTraceQLRangeAlertUsesStepAndWindow(t *testing.T) {
	start := time.Unix(1800000000, 0).UTC()
	te := traceTestEngine(start)
	step := "1m"
	m := tempoalert.Model{Query: `{ status = error } | count_over_time() with (exemplars=false)`, QueryType: "traceql", Step: &step}
	q := Query{Datasource: "traces", UID: "fixture-traces", Expr: m.Query, Tempo: &m, From: 3 * time.Minute}
	rs, err := tracesEngine{te}.Instant(q, start.Add(12*time.Minute))
	if err != nil || len(rs) != 1 {
		t.Fatalf("range: %+v %v", rs, err)
	}
	total := 0.0
	for ms, v := range rs[0].Points {
		if ms%60000 != 0 {
			t.Fatalf("point %d is not on the 1m step", ms)
		}
		if v == v {
			total += v
		}
	}
	if total != 240 {
		t.Fatalf("error spans in [9m,12m]: got %v want 240", total)
	}
	// The cache key separates instant from range and the step.
	other := m
	other.MetricsQueryType = "instant"
	if tempoKey(q) == tempoKey(Query{Tempo: &other}) {
		t.Fatal("cache key ignores metricsQueryType")
	}
	// An empty relative range is Tempo's validation error: an Error input, not data.
	if _, err := (tracesEngine{te}).Instant(Query{Datasource: "traces", Expr: m.Query, Tempo: &m}, start); err == nil || !strings.Contains(err.Error(), "end must be greater than start") {
		t.Fatalf("empty window: %v", err)
	}
}

func TestTraceFootprintOnlyChangesTraceSources(t *testing.T) {
	var traces yaml.Node
	if err := yaml.Unmarshal([]byte(`[{from: {metric: req, where: {outcome: error}}, server: s}]`), &traces); err != nil {
		t.Fatal(err)
	}
	p := &Project{Seed: seed.Seed{
		Metrics: map[string]MetricDef{
			"req":   {Series: []seed.SeriesDef{{Where: map[string]string{"outcome": "ok"}}, {Where: map[string]string{"outcome": "error"}}}},
			"other": {Series: []seed.SeriesDef{{Where: map[string]string{}}}},
		},
		Traces: *traces.Content[0],
	}}
	a := &Alert{Queries: map[string]Query{"A": {Datasource: "traces", Expr: "{} | rate()"}}}
	for _, tc := range []struct {
		change seed.Change
		want   bool
	}{
		{seed.Change{Metric: "req", Where: map[string]string{"outcome": "error"}, Rate: "1/s"}, true},
		{seed.Change{Metric: "req", LatencyX: 3}, true}, // matches the error series too
		{seed.Change{Metric: "req", Where: map[string]string{"outcome": "ok"}, LatencyX: 3}, false},
		{seed.Change{Metric: "other", Rate: "1/s"}, false},
	} {
		inc := &Incident{Change: []seed.Change{tc.change}}
		if got := affected(p, a, inc, nil, 0); got != tc.want {
			t.Errorf("change %+v: affected=%v want %v", tc.change, got, tc.want)
		}
	}
	if got := traceSourceMetrics(&p.Seed); !reflect.DeepEqual(got, map[string]bool{"req": true}) {
		t.Fatalf("trace sources %v", got)
	}
}
