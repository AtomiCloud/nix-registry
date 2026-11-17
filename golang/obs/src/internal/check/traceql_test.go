package check

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

type traceNames map[string]bool

func (m traceNames) HasSpanAttribute(scope, name string) bool { return m[scope+"."+name] }

func TestTraceQLStaticContractAdapter(t *testing.T) {
	q := files.Query{File: "dashboards/traces.json", Where: `panel 1 "Trace search" target A`, Kind: files.Traces, Expr: `{span.provider="stripe" && resource.service.name="checkout"}`}
	s := &files.Set{Queries: []files.Query{q}, Dashboards: []*files.Dashboard{{}}}
	for _, tc := range []struct {
		names SpanAttributeNames
		fail  bool
	}{
		{nil, false}, {traceNames{"span.provider": true, "resource.service.name": true}, false}, {traceNames{"resource.service.name": true}, true},
	} {
		r := Static(s, Options{Names: true, SpanAttributes: tc.names})
		if (r.Failures() > 0) != tc.fail {
			t.Fatalf("%v", r.Findings)
		}
		if tc.fail {
			text := fmt.Sprint(r.Findings)
			for _, want := range []string{q.File, q.Where, "span.provider"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
		}
	}
	s.Queries[0].Expr = `{span.planted =`
	r := Static(s, Options{})
	if r.Failures() != 1 || r.Findings[0].Rule != "traceql-syntax" || !strings.Contains(r.Findings[0].Msg, "planted") {
		t.Fatalf("%v", r.Findings)
	}
}

func TestTempoQueryTypes(t *testing.T) {
	for _, tc := range []struct{ kind, expr, rule string }{
		{"traceId", "01000000000000000000000000000000", ""},
		{"traceId", "planted-not-hex", "trace-id"},
		{"search", "service.name=checkout", "traceql-query-type"},
		{"nativeSearch", "", "traceql-query-type"},
		{"traceql", "01000000000000000000000000000000", ""},
		{"", " ABCDEF0123456789 ", ""},
		{"serviceMap", "not TraceQL", ""},
		{"upload", "not TraceQL", ""},
		{"clear", "", ""},
		{"futureModel", "not TraceQL", ""},
	} {
		t.Run(tc.kind+"/"+tc.expr, func(t *testing.T) {
			q := files.Query{File: "dashboards/traces.json", Where: "panel 7 target A", Kind: files.Traces, Dash: &files.Dashboard{}, QueryType: tc.kind, Expr: tc.expr}
			r := Static(&files.Set{Queries: []files.Query{q}, Dashboards: []*files.Dashboard{{}}}, Options{})
			if tc.rule == "" {
				if r.Failures() != 0 {
					t.Fatal(r.Findings)
				}
				if tc.kind == "serviceMap" || tc.kind == "upload" || tc.kind == "clear" || tc.kind == "futureModel" {
					if len(r.Findings) != 1 || r.Findings[0].Level != Note {
						t.Fatalf("missing skip note: %+v", r.Findings)
					}
					if live := runLive(&files.Set{Queries: []files.Query{q}}, Options{}, &seed.Dataset{}, nil, nil); live.Failures() != 0 {
						t.Fatal(live.Findings)
					}
				}
			} else if r.Failures() != 1 || r.Findings[0].Rule != tc.rule {
				t.Fatal(r.Findings)
			}
		})
	}
}

func TestNonTempoTraceDatasourcesStayNotes(t *testing.T) {
	for _, ds := range []string{"jaeger", "zipkin"} {
		t.Run(ds, func(t *testing.T) {
			q := files.Query{File: "dashboards/traces.json", Where: "panel 7 target A", Kind: files.Traces, Dash: &files.Dashboard{}, DSType: ds, Expr: "not TraceQL"}
			set := &files.Set{Queries: []files.Query{q}, Dashboards: []*files.Dashboard{{}}}
			r := Static(set, Options{})
			if r.Failures() != 0 || len(r.Findings) != 1 || r.Findings[0].Level != Note || !strings.Contains(r.Findings[0].Msg, ds) {
				t.Fatalf("%s: %+v", ds, r.Findings)
			}
			if live := runLive(set, Options{}, &seed.Dataset{}, nil, nil); live.Failures() != 0 {
				t.Fatalf("%s live: %+v", ds, live.Findings)
			}
		})
	}
}

func TestTraceAlertsStayDeferredNotes(t *testing.T) {
	for _, expr := range []string{"{}", "{span.planted=", "01000000000000000000000000000000"} {
		q := files.Query{File: "alerts/traces/warning.yaml", Where: "rule traces query A", Kind: files.Traces, Dash: &files.Dashboard{}, IsAlert: true, Expr: expr}
		r := Static(&files.Set{Alerts: 1, Queries: []files.Query{q}}, Options{})
		if r.Failures() != 0 || len(r.Findings) != 1 || r.Findings[0].Level != Note || !strings.Contains(r.Findings[0].Msg, "alerts") {
			t.Fatalf("%s: %+v", expr, r.Findings)
		}
	}
}

func TestTraceQLLiveProjectionAttributeDrift(t *testing.T) {
	at := time.Unix(1800000000, 0)
	ds := &seed.Dataset{End: at.Add(time.Minute), Traces: []*seed.TTrace{{TraceID: [16]byte{1}, Spans: []*seed.TSpan{{
		Service: "checkout", StartNs: uint64(at.UnixNano()), DurNs: uint64(time.Second), Attrs: map[string]seed.TAttrVal{"provider": seed.TStr("stripe")},
	}}}}}
	for _, expr := range []string{"{} | select(span.%s)", "{} | count_over_time() by (span.%s)"} {
		t.Run(expr, func(t *testing.T) {
			for _, attr := range []string{"provider", "drifted"} {
				q := files.Query{File: "dashboards/traces.json", Where: "panel 7 target A", Kind: files.Traces, Dash: &files.Dashboard{}, Expr: fmt.Sprintf(expr, attr)}
				set := &files.Set{Queries: []files.Query{q}, Dashboards: []*files.Dashboard{{}}}
				r := runLive(set, Options{}, ds, nil, nil)
				if attr == "provider" {
					if r.Failures() != 0 {
						t.Fatalf("baseline %s: %+v", q.Expr, r.Findings)
					}
				} else {
					text := fmt.Sprint(r.Findings)
					if r.Failures() != 1 || !strings.Contains(text, q.File) || !strings.Contains(text, q.Where) || !strings.Contains(text, "attribute span.drifted") {
						t.Fatalf("drift %s: %+v", q.Expr, r.Findings)
					}
				}
			}
		})
	}
}

func TestTraceQLBareIDLiveLookup(t *testing.T) {
	ds := &seed.Dataset{End: time.Now(), Traces: []*seed.TTrace{{TraceID: [16]byte{1}, Spans: []*seed.TSpan{{Service: "checkout"}}}}}
	for _, tc := range []struct {
		id   string
		fail bool
	}{{"01000000000000000000000000000000", false}, {"02000000000000000000000000000000", true}} {
		q := files.Query{File: "dashboards/traces.json", Where: "panel 7 target A", Kind: files.Traces, Dash: &files.Dashboard{}, QueryType: "traceql", Expr: tc.id}
		r := runLive(&files.Set{Queries: []files.Query{q}}, Options{}, ds, nil, nil)
		if (r.Failures() > 0) != tc.fail {
			t.Fatalf("%s: %+v", tc.id, r.Findings)
		}
	}
}
