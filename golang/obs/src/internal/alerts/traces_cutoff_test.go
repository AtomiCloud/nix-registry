package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/seed"
	"github.com/AtomiCloud/obs/internal/tempoalert"
	"gopkg.in/yaml.v3"
)

// Tempo 3.1.1's query_end_cutoff (30 s by default) is applied against the frontend's
// now, which is the evaluation tick: an instant window of 30 s ending at the tick is
// entirely inside the cutoff, so Tempo answers 400 and every evaluation is an Error.
func TestTraceQLInstantWindowInsideEndCutoffIsAnErrorEveryTick(t *testing.T) {
	start := time.Unix(1800000000, 0).UTC()
	eng := Engines{Datasources: map[string]DatasourceEngine{"traces": tracesEngine{e: traceTestEngine(start)}}}
	m := tempoalert.Model{Query: `{ status = error } | rate()`, QueryType: "traceql", MetricsQueryType: "instant"}
	q := Query{Datasource: "traces", UID: "fixture-traces", Expr: m.Query, Tempo: &m, From: 30 * time.Second}
	run := Run{Name: "spike", Start: start, End: start.Add(20 * time.Minute)}
	ar := evalRuleRun(eng, &queryCache{m: map[qKey]*qEntry{}}, traceRule(t, q), run, true, nil)
	if len(ar.Steps) == 0 || ar.ErrorSteps != len(ar.Steps) || ar.Fired {
		t.Fatalf("relativeTimeRange from 30 s, instant: want an Error on all %d ticks, got error_steps=%d fired=%v", len(ar.Steps), ar.ErrorSteps, ar.Fired)
	}
	for _, s := range ar.Steps {
		if len(s.Errors) == 0 || !strings.Contains(strings.Join(s.Errors, " "), "query window falls entirely within query_end_cutoff") {
			t.Fatalf("tick %d: want Tempo's cutoff error, got %v", s.T, s.Errors)
		}
	}
}

// A range query's end is clamped to now - 30 s and then rounded down to the step, so
// with step 1m the (t-60s, t] bucket is never in the answer.
func TestTraceQLRangeDropsTheBucketInsideEndCutoff(t *testing.T) {
	start := time.Unix(1800000000, 0).UTC()
	te := traceTestEngine(start)
	step := "1m"
	m := tempoalert.Model{Query: `{ status = error } | count_over_time() with (exemplars=false)`, QueryType: "traceql", Step: &step}
	q := Query{Datasource: "traces", UID: "fixture-traces", Expr: m.Query, Tempo: &m, From: 3 * time.Minute}
	at := start.Add(12 * time.Minute) // error spans in [10m, 12m)
	rs, err := tracesEngine{e: te}.Instant(q, at)
	if err != nil || len(rs) != 1 {
		t.Fatalf("range: %+v %v", rs, err)
	}
	total := 0.0
	for ms, v := range rs[0].Points {
		if ms > at.Add(-time.Minute).UnixMilli() {
			t.Fatalf("point at %s: the bucket (t-60s, t] is inside the 30 s cutoff", time.UnixMilli(ms).UTC().Sub(start))
		}
		if v == v {
			total += v
		}
	}
	if total != 120 {
		t.Fatalf("error spans in (10m, 11m]: got %v want 120 (the (11m, 12m] bucket must be cut)", total)
	}
}

// A range Tempo request reads from AlignRequest's start: now-from rounded down to
// the step, minus one more step. Runs must be farther apart than that, or a run's
// first ticks count the previous run's spans.
func TestTraceQLRangeLookbackCoversTwoSteps(t *testing.T) {
	step := "30m"
	m := tempoalert.Model{Query: `{ status = error } | count_over_time() with (exemplars=false)`, QueryType: "traceql", Step: &step}
	q := Query{Datasource: "traces", UID: "fixture-traces", Expr: m.Query, Tempo: &m, From: 10 * time.Minute}
	if got, want := queryLookback(q, time.Minute), 10*time.Minute+2*30*time.Minute+5*time.Minute; got < want {
		t.Fatalf("lookback %s, want at least from + 2*step + 5m = %s", got, want)
	}
	// The previous run ends at prevEnd with error spans in its last 5 minutes.
	prevEnd := time.Unix(1800000000, 0).UTC().Truncate(time.Hour)
	var traces []*seed.TTrace
	for s := 0; s < 300; s++ {
		at := prevEnd.Add(-5*time.Minute + time.Duration(s)*time.Second)
		traces = append(traces, &seed.TTrace{TraceID: [16]byte{byte(s), byte(s >> 8), 3}, Spans: []*seed.TSpan{{
			SpanID: [8]byte{byte(s), byte(s >> 8), 1}, Name: "POST /charge", Kind: 2, Status: 2, Service: "checkout",
			StartNs: uint64(at.UnixNano()), DurNs: uint64(time.Millisecond),
		}}})
	}
	te := tempo.NewTempoEngine(traces)
	gap := max(30*time.Minute, queryLookback(q, time.Minute)) // loadProject's p.Gap
	next := prevEnd.Add(gap)
	for k := 0; k < 30; k++ {
		at := next.Add(time.Duration(k) * time.Minute)
		rs, err := tracesEngine{e: te}.Instant(q, at)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			for ms, v := range r.Points {
				if v == v && v != 0 {
					t.Fatalf("tick %s of the next run (gap %s) reads %v spans of the previous run at %s", at.Sub(next), gap, v, time.UnixMilli(ms).UTC().Sub(prevEnd))
				}
			}
		}
	}
}

// A reused (or cached) range Tempo result is the baseline's moved by the run offset
// (or base change), which is exact only for whole steps: AlignRequest aligns to the
// Unix epoch, not to the run.
func TestTraceQLRangeReuseNeedsWholeStepShifts(t *testing.T) {
	var traces yaml.Node
	if err := yaml.Unmarshal([]byte(`[{from: {metric: req}, server: s}]`), &traces); err != nil {
		t.Fatal(err)
	}
	p := &Project{Seed: seed.Seed{
		Metrics: map[string]MetricDef{
			"req":   {Series: []seed.SeriesDef{{Where: map[string]string{}}}},
			"queue": {Series: []seed.SeriesDef{{Where: map[string]string{}}}},
		},
		Traces: *traces.Content[0],
	}, Files: map[string][]byte{}}
	rule := func(step string, instant bool) *Alert {
		m := tempoalert.Model{Query: `{} | rate() with (exemplars=false)`, QueryType: "traceql", Step: &step}
		if instant {
			m.MetricsQueryType = "instant"
		}
		return &Alert{ID: "t", Queries: map[string]Query{"A": {Datasource: "traces", Expr: m.Query, Tempo: &m, From: 10 * time.Minute}}}
	}
	base := time.Unix(1800000000, 0).UTC().Truncate(time.Hour)
	inc := &Incident{Name: "queue", Change: []seed.Change{{Metric: "queue", Rate: "5/s"}}}
	for _, tc := range []struct {
		name   string
		a      *Alert
		offset time.Duration
		reuse  bool
	}{
		{"1m step, 72m offset", rule("1m", false), 72 * time.Minute, true},
		{"5m step, 70m offset", rule("5m", false), 70 * time.Minute, true},
		{"5m step, 72m offset", rule("5m", false), 72 * time.Minute, false},
		{"instant, 72m offset", rule("5m", true), 72 * time.Minute, true},
	} {
		runs := []Run{{Name: "baseline", Start: base}, {Name: "queue", Incident: inc, Start: base.Add(tc.offset)}}
		if got := skipPlan(p, runs)(tc.a, runs[1]); got != tc.reuse {
			t.Errorf("%s: reuse=%v want %v", tc.name, got, tc.reuse)
		}
	}
	key := func(a *Alert, b time.Time) string { k, _ := resultCacheKey(p, a, "build", b); return k }
	a := rule("5m", false)
	if key(a, base) != key(a, base.Add(10*time.Minute)) {
		t.Error("cache key: bases 2 steps apart give the same shifted answer")
	}
	if key(a, base) == key(a, base.Add(time.Minute)) {
		t.Error("cache key: a base 1m into a 5m step must not reuse the result")
	}
	if inst := rule("5m", true); key(inst, base) != key(inst, base.Add(time.Minute)) {
		t.Error("cache key: instant Tempo answers move with the base")
	}
	// Two range queries (a map): the key must not depend on the walk order.
	two := rule("5m", false)
	for i, s := range []string{"1m", "7m", "13m"} {
		m := *two.Queries["A"].Tempo
		m.Step = &s
		q := two.Queries["A"]
		q.Tempo = &m
		two.Queries[string(rune('B'+i))] = q
	}
	at := base.Add(3 * time.Minute)
	for k, first := 0, key(two, at); k < 50; k++ {
		if key(two, at) != first {
			t.Fatal("cache key changes between calls with several range Tempo queries")
		}
	}
}
