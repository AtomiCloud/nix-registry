package alerts

import (
	"fmt"
	"math"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/VictoriaMetrics/metricsql"
)

// DatasourceEngine is the extension point for a future Tempo alert engine.
// MetricsQL and LogsQL use it today. Implementations must preserve instance labels.
type DatasourceEngine interface {
	Instant(Query, time.Time) ([]Result, error)
}
type RangeDatasourceEngine interface {
	Range(Query, time.Time, time.Time, time.Duration) ([]Result, error)
}

// QueryGuard is implemented by the chart-derived VictoriaLogs limits adapter.
type QueryGuard interface {
	Check(string, time.Time, time.Time) error
}

type Engines struct {
	Metrics     *vm.Engine // recording-rule sink
	Datasources map[string]DatasourceEngine
}

type Result struct {
	Labels []Label
	Points map[int64]float64
	Nulls  map[int64]bool // instant numeric frames with a nil value keep their labels
}

type metricEngine struct{ e *vm.Engine }

func (m metricEngine) Instant(q Query, at time.Time) ([]Result, error) {
	rs, err := m.e.SamplesInstant(q.Expr, at.Add(-q.To))
	return sampleResults(rs, q.To), err
}
func (m metricEngine) Range(q Query, start, end time.Time, step time.Duration) ([]Result, error) {
	rs, err := m.e.SamplesRange(q.Expr, start.Add(-q.To), end.Add(-q.To), step)
	return sampleResults(rs, q.To), err
}

func sampleResults(ss []*Series, shift time.Duration) []Result {
	out := make([]Result, 0, len(ss))
	for _, s := range ss {
		r := Result{Labels: sortLabels(s.Labels), Points: map[int64]float64{}}
		for i, t := range s.Ts {
			if !math.IsNaN(s.Vals[i]) {
				r.Points[t+shift.Milliseconds()] = s.Vals[i]
			}
		}
		if len(r.Points) > 0 {
			out = append(out, r)
		}
	}
	return out
}

type logsEngine struct {
	e     *vl.Engine
	guard QueryGuard
}

func (l logsEngine) Instant(q Query, at time.Time) ([]Result, error) {
	from := q.From
	if from <= 0 {
		from = 10 * time.Minute
	}
	if l.guard != nil {
		original := q.OriginalExpr
		if original == "" {
			original = q.Expr
		}
		if err := l.guard.Check(original, at.Add(-from), at.Add(-q.To)); err != nil {
			return nil, err
		}
	}
	rs, err := l.e.Stats(q.Expr, at.Add(-from), at.Add(-q.To), false)
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(rs))
	for _, r := range rs {
		// The datasource's alertingDataFrames maps an empty stats value to null
		// (NoData), and preserves the HTTP endpoint's __name__ column label.
		v, ok := r.Float()
		if !ok && r.Value != "" {
			return nil, fmt.Errorf("LogsQL stats value %q is not numeric: %s", r.Value, q.Expr)
		}
		labels := append([]Label{}, r.Labels...)
		if r.Name != "" {
			hasName := false
			for _, l := range labels {
				hasName = hasName || l.Name == "__name__"
			}
			if !hasName {
				labels = append(labels, Label{Name: "__name__", Value: r.Name})
			}
		}
		result := Result{Labels: sortLabels(labels)}
		if r.Value == "" {
			result.Nulls = map[int64]bool{at.UnixMilli(): true}
		} else {
			result.Points = map[int64]float64{at.UnixMilli(): v}
		}
		out = append(out, result)
	}
	return out, nil
}

// needsInstant is the prototype's hybrid rule: any bare selector/windowless
// rollup needs VictoriaMetrics' five-minute instant step; explicit-window queries
// can share one range request with the alert interval as its step.
func needsInstant(q string) bool {
	// Besides implicit rollup windows, query-context functions such as start()
	// and end() must see the instant request's bounds, not a whole run's range.
	if timeSensitive(&Alert{Queries: map[string]Query{"A": {Datasource: "metrics", Expr: q}}}) {
		return true
	}
	e, err := metricsql.Parse(q)
	if err != nil {
		return true
	}
	selectors, windowed := 0, 0
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		switch v := x.(type) {
		case *metricsql.MetricExpr:
			selectors++
		case *metricsql.RollupExpr:
			if _, ok := v.Expr.(*metricsql.MetricExpr); ok && v.Window != nil {
				windowed++
			}
		}
	})
	return selectors != windowed
}

func fetchEngine(engine DatasourceEngine, q Query, run Run, interval time.Duration) *qData {
	d := &qData{}
	if q.Range != "" {
		rng, err := time.ParseDuration(q.Range)
		if err != nil || rng <= 0 {
			return &qData{err: fmt.Errorf("query range %q is invalid", q.Range)}
		}
		r, ok := engine.(RangeDatasourceEngine)
		if !ok {
			return &qData{err: fmt.Errorf("datasource %s does not support range alert queries", q.Datasource)}
		}
		d.isRange, d.rng, d.step = true, rng, promMinStep
		d.res, d.err = r.Range(q, alignDown(run.Start.Add(-rng), d.step), alignDown(run.End, d.step), d.step)
		d.sorted = toSorted(d.res)
		return d
	}
	if r, ok := engine.(RangeDatasourceEngine); ok && q.Datasource == "metrics" && !needsInstant(q.Expr) {
		d.res, d.err = r.Range(q, run.Start, run.End, interval)
		return d
	}
	d.perTick, d.tickErr = map[int64][]Result{}, map[int64]error{}
	for t := run.Start; !t.After(run.End); t = t.Add(interval) {
		rs, err := engine.Instant(q, t)
		if err != nil {
			d.tickErr[t.UnixMilli()] = err
		} else {
			d.perTick[t.UnixMilli()] = rs
		}
	}
	return d
}

func queryUID(q Query) string {
	if q.UID != "" {
		return q.UID
	}
	return dsUID(q.Datasource)
}
