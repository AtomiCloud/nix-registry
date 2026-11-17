package alerts

import (
	"reflect"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/logevent"
	"github.com/AtomiCloud/obs/internal/seed"
)

// The root accepts both events. The conditional stats pipe reads only failed,
// while unconditional count must continue to count ok as well.
func TestLazyConditionalStatsKeepsAllInputs(t *testing.T) {
	expr := `* | stats count() as total, count() if ("durian.cloud/event":="pay.failed") as failed`
	events := contractEvents{"pay.ok", "pay.failed"}
	analysis, err := logevent.Analyze(expr, events)
	if err != nil {
		t.Fatal(err)
	}
	rule := &Alert{Queries: map[string]Query{"A": {Datasource: "logs", Expr: analysis.Expr}}}
	need := neededLogEvents([]*Alert{rule}, logEventSets{analysis.Expr: rootEvents(analysis.Expr, events)})
	sd := seed.Seed{Length: "2m", RandomSeed: 7, Traffic: seed.Traffic{Requests: "1/s"}, Metrics: map[string]seed.MetricDef{"requests": {Type: "counter", Series: []seed.SeriesDef{{Where: map[string]string{}, Share: 1}}}}, Logs: []seed.LogDef{{Event: "pay.ok", From: seed.LogSource{Metric: "requests"}}, {Event: "pay.failed", From: seed.LogSource{Metric: "requests"}}}}
	start := time.Now().Add(-time.Hour).Truncate(time.Minute)
	query := func(s seed.Seed) map[string]float64 {
		ds, err := seed.BuildIncident(&s, start, nil)
		if err != nil {
			t.Fatal(err)
		}
		e, err := vl.New(ds)
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		rs, err := e.Stats(analysis.Expr, start, start.Add(2*time.Minute), false)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, r := range rs {
			v, ok := r.Float()
			if !ok {
				t.Fatalf("non-numeric stats: %+v", r)
			}
			out[r.Name] = v
		}
		return out
	}
	full := query(sd)
	if full["total"] <= full["failed"] || full["failed"] <= 0 {
		t.Fatalf("bad seed/query: %v", full)
	}
	if need != nil {
		sd.Logs, _ = keepLogs(sd.Logs, need)
	}
	lazy := query(sd)
	if !reflect.DeepEqual(full, lazy) {
		t.Fatalf("pipe event pay.failed pruned rows seen by count(): full=%v lazy=%v need=%v query=%s", full, lazy, need, expr)
	}
}

func TestLazyUnionKeepsAllInputs(t *testing.T) {
	expr := `"durian.cloud/event":="pay.failed" | union ("durian.cloud/event":="pay.ok") | stats count() as total`
	events := contractEvents{"pay.ok", "pay.failed"}
	analysis, err := logevent.Analyze(expr, events)
	if err != nil {
		t.Fatal(err)
	}
	rule := &Alert{Queries: map[string]Query{"A": {Datasource: "logs", Expr: analysis.Expr}}}
	need := neededLogEvents([]*Alert{rule}, logEventSets{analysis.Expr: rootEvents(analysis.Expr, events)})
	sd := seed.Seed{Length: "2m", RandomSeed: 7, Traffic: seed.Traffic{Requests: "1/s"}, Metrics: map[string]seed.MetricDef{"requests": {Type: "counter", Series: []seed.SeriesDef{{Where: map[string]string{}, Share: 1}}}}, Logs: []seed.LogDef{{Event: "pay.ok", From: seed.LogSource{Metric: "requests"}}, {Event: "pay.failed", From: seed.LogSource{Metric: "requests"}}}}
	start := time.Now().Add(-time.Hour).Truncate(time.Minute)
	query := func(s seed.Seed) map[string]float64 {
		ds, err := seed.BuildIncident(&s, start, nil)
		if err != nil {
			t.Fatal(err)
		}
		e, err := vl.New(ds)
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		rs, err := e.Stats(analysis.Expr, start, start.Add(2*time.Minute), false)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, r := range rs {
			v, ok := r.Float()
			if !ok {
				t.Fatalf("non-numeric stats: %+v", r)
			}
			out[r.Name] = v
		}
		return out
	}
	full := query(sd)
	if full["total"] <= 0 {
		t.Fatalf("bad seed/query: %v", full)
	}
	if need != nil {
		sd.Logs, _ = keepLogs(sd.Logs, need)
	}
	lazy := query(sd)
	if !reflect.DeepEqual(full, lazy) {
		t.Fatalf("pipe event pay.failed pruned rows seen by count(): full=%v lazy=%v need=%v query=%s", full, lazy, need, expr)
	}
}

func TestNestedEventAndStatsPolicy(t *testing.T) {
	for _, expr := range []string{
		`"durian.cloud/event":="pay.failed" | union ("durian.cloud/event":="pay.typo") | stats count()`,
		`"durian.cloud/event":="pay.failed" | union (* | stats sum(amount) as n) | stats count()`,
	} {
		a, err := logevent.Analyze(expr, contractEvents{"pay.failed", "pay.ok"})
		if err == nil {
			t.Errorf("subquery escaped event/stat-value policy: expr=%s analysis=%+v", expr, a)
		}
	}
}
