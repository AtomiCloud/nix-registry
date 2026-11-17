package check

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/tempoalert"
)

// traceAlert runs a Tempo alert query in a live run the way obs alerts run it
// (internal/alerts/traces.go): the request the Grafana Tempo datasource sends, an
// instant or range TraceQL metrics query over [end-from, end-to] in whole seconds,
// through the embedded Tempo's query-frontend rules (end cutoff, step alignment).
// The evaluation moment is the end of the seeded time line, as for metric and log
// alerts. A query obs cannot run is a FAIL, never a skip.
func (l *liveRun) traceAlert(q *files.Query) {
	if t := strings.ToLower(q.DSType); t == "jaeger" || t == "zipkin" {
		l.r.add(Fail, q.File, q.Where, "live-unsupported-query", "datasource type %q: obs runs trace alerts only on Tempo, so this query was not run", q.DSType)
		return
	}
	m := tempoalert.FromModel(q.Model, q.QueryType)
	if m.Query == "" {
		m.Query = q.Expr
	}
	if refusal := m.Check(); refusal != nil {
		// Grafana would not evaluate this as an alert input (the static rules name it too).
		l.r.add(Fail, q.File, q.Where, "live-unsupported-query", "not run: %s [%s]; query: %s", refusal.Reason, refusal.Rule, oneLine(m.Query))
		return
	}
	p, err := tempo.Parse(m.Query)
	if err != nil {
		l.r.add(Fail, q.File, q.Where, "live-unsupported-query", "not run: TraceQL does not parse: %v; query: %s", err, oneLine(m.Query))
		return
	}
	// A projection (by, select, a metrics function's attribute) on a name no seeded span carries is
	// seed drift, as for a dashboard panel; filter attributes go through the no-rows path.
	if missing := l.seedLacks(p.Projections); len(missing) > 0 {
		l.r.add(Fail, q.File, q.Where, "query-run", "not in the seed: %s; query: %s", strings.Join(missing, ", "), oneLine(m.Query))
		return
	}
	from := time.Duration(q.RangeSecs) * time.Second
	to := time.Duration(q.ToSecs) * time.Second
	start, end := tempoalert.Window(l.end, from, to)
	var frames []tempoalert.Frame
	if m.Instant() {
		resp, err := l.tempo.QueryInstant(tempo.DefaultFrontend, l.end, m.Query, start, end)
		if err != nil {
			l.r.add(Fail, q.File, q.Where, "tempo-alert-run", "%v; instant query over %s..%s; query: %s", err, start.Format(time.RFC3339), end.Format(time.RFC3339), oneLine(m.Query))
			return
		}
		frames = tempoalert.InstantFrames(resp, l.end)
	} else {
		step, err := m.RangeStep(start, end)
		if err != nil {
			l.r.add(Fail, q.File, q.Where, "tempo-alert-step", "%v; query: %s", err, oneLine(m.Query))
			return
		}
		resp, err := l.tempo.QueryRange(tempo.DefaultFrontend, l.end, m.Query, start, end, step)
		if err != nil {
			l.r.add(Fail, q.File, q.Where, "tempo-alert-run", "%v; range query over %s..%s step %s; query: %s", err, start.Format(time.RFC3339), end.Format(time.RFC3339), step, oneLine(m.Query))
			return
		}
		frames = tempoalert.RangeFrames(resp)
	}
	for _, f := range frames {
		// An instant metrics query with no `by` always answers one unlabeled series, 0 when no span
		// matched (as `stats count()` does in LogsQL): that is no data, not a quiet metric.
		quiet := m.Instant() && len(f.Labels) == 1 && f.Labels[0].Name == "__name__"
		for _, p := range f.Points {
			if !math.IsNaN(p.V) && !(quiet && p.V == 0) {
				return
			}
		}
	}
	l.noRows(q, m.Query, fmt.Sprintf("; ran as the Tempo datasource does, %s over %s..%s at the end of the seed", map[bool]string{true: "instant", false: "range"}[m.Instant()], start.Format(time.RFC3339), end.Format(time.RFC3339)))
}
