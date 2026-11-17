package check

import (
	"fmt"
	"github.com/AtomiCloud/obs/internal/files"
	"strings"
	"testing"
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
