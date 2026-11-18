package check

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

func tracedSeed() *seed.Dataset {
	at := time.Unix(1800000000, 0)
	return &seed.Dataset{End: at.Add(time.Minute), Traces: []*seed.TTrace{{TraceID: [16]byte{1}, Spans: []*seed.TSpan{{
		Service: "checkout", StartNs: uint64(at.UnixNano()), DurNs: uint64(time.Second), Attrs: map[string]seed.TAttrVal{"provider": seed.TStr("stripe")},
	}}}}}
}

func traceAlertQuery(expr, metricsType string, from, to int64) files.Query {
	model := map[string]any{"query": expr, "queryType": "traceql", "metricsQueryType": metricsType, "datasource": map[string]any{"type": "tempo", "uid": "fixture-tempo"}}
	return files.Query{File: "alerts/traffic/critical.yaml", Where: "rule traffic query A", Kind: files.Traces, IsAlert: true,
		Expr: expr, RangeSecs: from, ToSecs: to, QueryType: "traceql", DSType: "tempo", DSUID: "fixture-tempo", Model: model}
}

// --live must run every alert query: a Tempo alert query runs through the embedded Tempo with the
// Tempo datasource's request (instant or range, whole seconds, end cutoff), and a query that gives
// no rows, errors, or cannot be run is a FAIL naming the file, the rule and the query.
func TestLiveRunsTraceAlertQueries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		q     files.Query
		ds    *seed.Dataset
		rule  string // "" = green
		token string // must appear in the failure text
	}{
		{"instant rate on seeded spans", traceAlertQuery(`{} | rate()`, "instant", 300, 0), tracedSeed(), "", ""},
		{"range rate on seeded spans", traceAlertQuery(`{} | rate() with(exemplars=false)`, "range", 300, 0), tracedSeed(), "", ""},
		{"filter on a seeded attribute", traceAlertQuery(`{span.provider = "stripe"} | rate()`, "instant", 300, 0), tracedSeed(), "", ""},
		{"filter that matches no span", traceAlertQuery(`{span.provider = "adyen"} | rate()`, "instant", 300, 0), tracedSeed(), "no-rows", `span.provider = "adyen"`},
		{"no spans in the seed", traceAlertQuery(`{} | rate()`, "instant", 300, 0), &seed.Dataset{End: tracedSeed().End}, "no-rows", "{} | rate()"},
		{"unseeded attribute in a by()", traceAlertQuery(`{} | rate() by (span.drifted)`, "instant", 300, 0), tracedSeed(), "query-run", "attribute span.drifted"},
		{"window inside Tempo's end cutoff", traceAlertQuery(`{} | rate()`, "instant", 20, 0), tracedSeed(), "tempo-alert-run", "{} | rate()"},
		{"search, not a metrics query", traceAlertQuery(`{}`, "instant", 300, 0), tracedSeed(), "live-unsupported-query", "tempo-alert-search"},
		{"TraceQL that does not parse", traceAlertQuery(`{ | rate()`, "instant", 300, 0), tracedSeed(), "live-unsupported-query", "does not parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runLive(&files.Set{Alerts: 1, Queries: []files.Query{tc.q}}, Options{}, tc.ds, nil, nil)
			if r.Counts["traces"] != 1 {
				t.Fatalf("the trace alert query was not counted: %v", r.Counts)
			}
			text := fmt.Sprint(r.Findings)
			if tc.rule == "" {
				if len(r.Findings) != 0 {
					t.Fatalf("baseline: %s", text)
				}
				return
			}
			if r.Failures() != 1 || !hasRule(r, tc.rule) || !strings.Contains(text, tc.q.File) || !strings.Contains(text, tc.q.Where) || !strings.Contains(text, tc.token) {
				t.Fatalf("want one %s failure naming file, rule and %q, got %s", tc.rule, tc.token, text)
			}
		})
	}
}

// Anything obs cannot run live fails loudly; it is never counted and skipped.
func TestLiveRefusesAlertKindsItCannotRun(t *testing.T) {
	jaeger := traceAlertQuery(`{} | rate()`, "instant", 300, 0)
	jaeger.DSType = "jaeger"
	loki := files.Query{File: "alerts/old/critical.yaml", Where: "rule old query A", Kind: files.Loki, IsAlert: true, Expr: `{app="x"} |= "boom"`, DSType: "loki"}
	for _, q := range []files.Query{jaeger, loki} {
		r := runLive(&files.Set{Alerts: 1, Queries: []files.Query{q}}, Options{}, tracedSeed(), nil, nil)
		text := fmt.Sprint(r.Findings)
		if r.Failures() != 1 || !hasRule(r, "live-unsupported-query") || !strings.Contains(text, q.File) || !strings.Contains(text, q.Where) {
			t.Fatalf("%s %s: want one live-unsupported-query failure naming file and rule, got %s", q.Kind, q.DSType, text)
		}
	}
}
