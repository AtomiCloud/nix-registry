package check

import (
	"fmt"
	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/files"
	"math"
	"strings"
	"time"
)

// SpanAttributeNames is implemented by the contract package. A nil interface
// reports names without rejecting them; resource/platform names are allowed by
// the implementation as well as the contract's span attributes.
type SpanAttributeNames interface{ HasSpanAttribute(scope, name string) bool }

func staticTraces(r *Report, q *files.Query, o Options) {
	expr := staticFill(q)
	id, modeErr := traceQueryMode(q)
	if modeErr != nil {
		r.add(Fail, q.File, q.Where, "traceql-query-type", "%v; query: %s", modeErr, oneLine(expr))
		return
	}
	if id {
		if q.IsAlert {
			r.add(Fail, q.File, q.Where, "traceql-alert-deferred", "TraceQL alerts await obs alerts integration; trace ID: %s", oneLine(expr))
			return
		}
		if err := tempo.ValidateTraceID(expr); err != nil {
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
	if q.IsAlert {
		r.add(Fail, q.File, q.Where, "traceql-alert-deferred", "TraceQL alerts await obs alerts integration; query: %s", oneLine(expr))
		return
	}
	for _, a := range p.Attributes {
		if o.SpanAttributes != nil && !o.SpanAttributes.HasSpanAttribute(a.Scope, a.Name) {
			r.add(Fail, q.File, q.Where, "traceql-attribute", "undeclared attribute %s; query: %s", a, oneLine(expr))
		}
	}
}

func (l *liveRun) tracePanel(q *files.Query, expr string, start, end time.Time, step time.Duration) (bool, error) {
	id, err := traceQueryMode(q)
	if err != nil {
		return true, nil
	}
	if id {
		hits, err := l.tempo.Lookup(expr)
		return len(hits) > 0, err
	}
	p, err := tempo.Parse(expr)
	if err != nil {
		return true, nil
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

func traceQueryMode(q *files.Query) (bool, error) {
	switch strings.ToLower(q.QueryType) {
	case "traceid":
		return true, nil
	case "", "traceql", "traceqlsearch":
		return false, nil
	default:
		return false, fmt.Errorf("Tempo queryType %q is not a TraceQL query or traceId lookup; legacy search models are not supported", q.QueryType)
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
