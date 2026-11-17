package check

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/files"
)

// SpanAttributeNames is implemented by the contract package. A nil interface
// reports names without rejecting them; resource/platform names are allowed by
// the implementation as well as the contract's span attributes.
type SpanAttributeNames interface{ HasSpanAttribute(scope, name string) bool }

func staticTraces(r *Report, q *files.Query, o Options) {
	expr := staticFill(q)
	if q.IsAlert {
		r.add(Note, q.File, q.Where, "traces", "trace alerts not checked: await obs alerts integration; query: %s", oneLine(expr))
		return
	}
	mode, modeErr := traceQueryMode(q, expr)
	if modeErr != nil {
		r.add(Fail, q.File, q.Where, "traceql-query-type", "%v; query: %s", modeErr, oneLine(expr))
		return
	}
	if mode == traceSkipped {
		r.add(Note, q.File, q.Where, "traces", "trace target not checked: datasource %q queryType %q is not a Tempo TraceQL query or trace ID lookup", firstNonEmpty(q.DSType, q.DSUID, "tempo"), q.QueryType)
		return
	}
	if mode == traceID {
		if err := tempo.ValidateTraceID(strings.TrimSpace(expr)); err != nil {
			r.add(Fail, q.File, q.Where, "trace-id", "%v; trace ID: %s", err, oneLine(expr))
		}
		return
	}
	p, err := tempo.Parse(expr)
	if err != nil {
		r.add(Fail, q.File, q.Where, "traceql-syntax", "%v; query: %s", err, oneLine(expr))
		return
	}
	if o.Names {
		r.add(Note, q.File, q.Where, "names", "reads span attributes=%v", p.Attributes)
	}
	for _, a := range p.Attributes {
		if o.SpanAttributes != nil && !o.SpanAttributes.HasSpanAttribute(a.Scope, a.Name) {
			r.add(Fail, q.File, q.Where, "traceql-attribute", "undeclared attribute %s; query: %s", a, oneLine(expr))
		}
	}
}

func (l *liveRun) tracePanel(q *files.Query, expr string, start, end time.Time, step time.Duration) (bool, error) {
	mode, err := traceQueryMode(q, expr)
	if err != nil || mode == traceSkipped {
		return true, nil
	}
	if mode == traceID {
		hits, err := l.tempo.Lookup(strings.TrimSpace(expr))
		return len(hits) > 0, err
	}
	p, err := tempo.Parse(expr)
	if err != nil {
		return true, nil
	}
	if missing := l.missingTraceAttrs(expr); len(missing) > 0 {
		return false, fmt.Errorf("not in the seed: %s", strings.Join(missing, ", "))
	}
	if !p.Metrics {
		hits, err := l.tempo.Search(expr, start, end, 20)
		return len(hits) > 0, err
	}
	rows, err := l.tempo.MetricsRange(expr, start, end, step)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		for _, v := range row.Points {
			if !math.IsNaN(v) {
				return true, nil
			}
		}
	}
	return false, nil
}

type traceMode int

const (
	traceQL traceMode = iota
	traceID
	traceSkipped
)

func traceQueryMode(q *files.Query, expr string) (traceMode, error) {
	// Preserve M1's classification of other tracing plugins, without interpreting
	// their datasource-specific models as Tempo TraceQL.
	switch strings.ToLower(q.DSType) {
	case "jaeger", "zipkin":
		return traceSkipped, nil
	}
	switch strings.ToLower(q.QueryType) {
	case "traceid":
		return traceID, nil
	case "", "traceql":
		// Grafana's last core Tempo datasource (b92bfd830f9c), datasource.ts:
		// 335-338 and 369-383, routes trimmed hex-only text to a trace ID lookup.
		// Grafana v13.3.0's Tempo TraceQL editor docs retain this behavior; the
		// plugin itself was extracted from core before that tag (see README).
		if strings.Trim(strings.TrimSpace(expr), "0123456789abcdefABCDEF") == "" {
			return traceID, nil
		}
		return traceQL, nil
	case "traceqlsearch":
		return traceQL, nil
	case "nativesearch", "search":
		return traceQL, fmt.Errorf("Tempo queryType %q is a legacy search model; legacy search models are not supported", q.QueryType)
	default:
		return traceSkipped, nil
	}
}

func (l *liveRun) missingTraceAttrs(expr string) []string {
	var out []string
	p, err := tempo.Parse(expr)
	if err != nil {
		return nil
	}
	for _, a := range p.Attributes {
		found := false
		for _, t := range l.ds.Traces {
			for _, s := range t.Spans {
				_, span := s.Attrs[a.Name]
				_, res := s.Resource[a.Name]
				res = res || (a.Name == "service.name" && s.Service != "")
				if (a.Scope != "resource" && span) || (a.Scope != "span" && res) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			out = append(out, fmt.Sprintf("attribute %s", a))
		}
	}
	return out
}
