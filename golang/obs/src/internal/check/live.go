package check

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/VictoriaMetrics/metricsql"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
	"github.com/AtomiCloud/obs/internal/vars"
)

const maxValuesPerVar = 8

// metricsEngine and logsEngine are what a live run needs from the embedded engines;
// the parity test (parity_test.go, tag `parity`) wraps them to compare with real stores.
type metricsEngine interface {
	Instant(q string, t time.Time) ([]vm.Series, error)
	Range(q string, start, end time.Time, step time.Duration) ([]vm.Series, error)
}

type logsEngine interface {
	Stats(qs string, start, end time.Time, withoutTrailingFilters bool) ([]vl.Result, error)
	Rows(qs string, start, end time.Time) ([]vl.Result, error)
}

// makeData reads the seed and makes its data on a time line that ends in the past.
func makeData(o Options) (*seed.Dataset, error) {
	seedPath := o.SeedPath
	if seedPath == "" {
		seedPath = o.Dir + "/seed.yaml"
	}
	sd, err := seed.Load(seedPath)
	if err != nil {
		return nil, err
	}
	base := time.Unix(o.Base, 0).UTC()
	if o.Base == 0 {
		// A time line that ends in the past: stores drop samples dated ahead.
		base = time.Now().UTC().Truncate(time.Minute).Add(-sd.Duration() - 10*time.Minute)
	} else if end := base.Add(sd.Duration()); end.After(time.Now()) {
		return nil, fmt.Errorf("--base %d: the seed's time line ends %s in the future; stores drop future data", o.Base, time.Until(end).Round(time.Second))
	}
	return seed.Build(sd, base)
}

// Live runs `obs query --live`: the seed is made, loaded into the embedded
// VictoriaMetrics and VictoriaLogs, and every alert and dashboard query runs on it
// and must return rows.
func Live(set *files.Set, o Options) (*Report, error) {
	ds, err := makeData(o)
	if err != nil {
		return nil, err
	}
	needVM, needVL := false, false
	for _, q := range set.Queries {
		if q.Problem != "" {
			continue
		}
		needVM = needVM || q.Kind == files.Metrics
		needVL = needVL || q.Kind == files.Logs
	}
	var m metricsEngine
	var l logsEngine
	if needVM {
		e, err := vm.New(ds)
		if err != nil {
			return nil, err
		}
		defer e.Close()
		m = e
	}
	if needVL {
		e, err := vl.New(ds)
		if err != nil {
			return nil, err
		}
		defer e.Close()
		l = e
	}
	return runLive(set, o, ds, m, l), nil
}

// runLive runs every query of set on the given engines.
func runLive(set *files.Set, o Options, ds *seed.Dataset, m metricsEngine, l logsEngine) *Report {
	r := newReport()
	lv := &liveRun{r: r, o: o, ds: ds, end: ds.End, vm: m, vl: l}
	lv.logFields = logFieldSet(ds)
	lv.metricNames = toSet(ds.MetricNames())
	lv.labelNames = labelNameSet(ds)
	for i := range set.Queries {
		q := &set.Queries[i]
		if q.Problem != "" || (q.Kind != files.Metrics && q.Kind != files.Logs) {
			continue
		}
		r.Counts[q.Kind.String()]++
		if q.IsAlert {
			lv.alert(q)
		} else {
			lv.panel(q)
		}
	}
	return r
}

type liveRun struct {
	r           *Report
	o           Options
	ds          *seed.Dataset
	end         time.Time
	vm          metricsEngine
	vl          logsEngine
	logFields   map[string]bool
	metricNames map[string]bool
	labelNames  map[string]bool
}

func toSet(a []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range a {
		m[s] = true
	}
	return m
}

func logFieldSet(ds *seed.Dataset) map[string]bool {
	m := map[string]bool{"_time": true, "_msg": true, "_stream": true, "_stream_id": true}
	for _, st := range ds.Streams {
		for _, f := range st.Fields {
			m[f.Name] = true
			m["_stream:"+f.Name] = true
		}
		// Rows of different events carry different attributes: one pass per distinct name set.
		seen := map[string]bool{}
		for _, row := range st.Rows {
			sig := fmt.Sprint(len(row.Fields))
			for _, f := range row.Fields {
				sig += "|" + f.Name
			}
			if seen[sig] {
				continue
			}
			seen[sig] = true
			for _, f := range row.Fields {
				m[f.Name] = true
			}
		}
	}
	return m
}

func labelNameSet(ds *seed.Dataset) map[string]bool {
	m := map[string]bool{}
	for _, s := range ds.Series {
		for _, l := range s.Labels {
			m[l.Name] = true
		}
	}
	return m
}

// ---- alerts ----

func (l *liveRun) alert(q *files.Query) {
	switch q.Kind {
	case files.Metrics:
		e, err := metricsql.Parse(q.Expr)
		if err != nil {
			return // the static check reports it
		}
		e, n := stripThresholds(e)
		expr := string(e.AppendString(nil))
		if n > 0 {
			l.r.add(Note, q.File, q.Where, "threshold-removed", "ran the query without its %d comparison(s): a threshold filters rows out on a quiet seed; query run: %s", n, oneLine(expr))
		}
		rs, err := l.vm.Instant(expr, l.end)
		if err != nil {
			l.r.add(Fail, q.File, q.Where, "metricsql-run", "%v; query: %s", err, oneLine(expr))
			return
		}
		if len(rs) == 0 {
			l.noRows(q, expr, "")
		}
	case files.Logs:
		rng := time.Duration(q.RangeSecs) * time.Second
		if rng <= 0 {
			rng = 10 * time.Minute
		}
		parsed, err := vl.Parse(q.Expr, l.end)
		if err != nil {
			return
		}
		if _, err := parsed.StatsLabels(); err != nil {
			return // reported by the static check
		}
		rs, err := l.vl.Stats(q.Expr, l.end.Add(-rng), l.end, true)
		if err != nil {
			l.r.add(Fail, q.File, q.Where, "logsql-run", "%v; query: %s", err, oneLine(q.Expr))
			return
		}
		if len(rs) == 0 || allZeroNoBy(rs) {
			l.noRows(q, q.Expr, fmt.Sprintf("; ran as the datasource does, start=end-%s, end=%s", rng, l.end.Format(time.RFC3339)))
		}
	}
}

// allZeroNoBy: a `stats count()` with no `by` always answers one row, 0 when nothing matched.
func allZeroNoBy(rs []vl.Result) bool {
	for _, r := range rs {
		if len(r.Labels) > 0 {
			return false
		}
		if v, ok := r.Float(); !ok || v != 0 {
			return false
		}
	}
	return true
}

// ---- dashboards ----

func (l *liveRun) panel(q *files.Query) {
	from, to := dashRange(q.Dash)
	start, end := l.end.Add(-from), l.end.Add(-to)
	names := vars.Names(q.Expr)
	defs := map[string]files.Variable{}
	for _, v := range q.Dash.Vars {
		defs[v.Name] = v
	}
	values := map[string][]string{}
	for _, n := range names {
		if v, ok := defs[n]; ok {
			vs := l.varValues(v, start, end)
			if !v.Multi {
				// One value at a time, as the dashboard shows it: the current one, else the first.
				pick := vs[0]
				for _, x := range vs {
					if x == v.Current {
						pick = x
					}
				}
				vs = []string{pick}
			}
			values[n] = vs
		}
	}
	// Run once with every variable at "all values", then once per single value.
	type run struct {
		vs    map[string][]string
		label string
	}
	all := map[string][]string{}
	for n, vs := range values {
		all[n] = vs
	}
	runs := []run{{all, "all values"}}
	for _, n := range names {
		vs := values[n]
		if len(vs) < 2 {
			continue
		}
		for _, v := range vs[:min(len(vs), maxValuesPerVar)] {
			one := map[string][]string{}
			for k, x := range all {
				one[k] = x
			}
			one[n] = []string{v}
			runs = append(runs, run{one, fmt.Sprintf("$%s=%s", n, v)})
		}
	}
	for _, ru := range runs {
		res := vars.Fill(vars.Case{
			Expr: q.Expr, FromMs: start.UnixMilli(), ToMs: end.UnixMilli(),
			QueryInterval: q.Interval, MaxDataPoints: q.MaxDataPoints, Scrape: "15s", Vars: ru.vs,
		})
		ok, err := l.runPanel(q, res, start, end)
		if err != nil {
			l.r.add(Fail, q.File, q.Where, "query-run", "%v (%s); query: %s", err, ru.label, oneLine(res.Expr))
			return
		}
		if !ok {
			l.noRows(q, res.Expr, fmt.Sprintf(" (%s)", ru.label))
			return
		}
	}
}

func (l *liveRun) runPanel(q *files.Query, res vars.Result, start, end time.Time) (bool, error) {
	switch q.Kind {
	case files.Metrics:
		if _, err := metricsql.Parse(res.Expr); err != nil {
			return true, nil // the static check reports it
		}
		step := time.Duration(res.StepMs) * time.Millisecond
		if step <= 0 {
			step = 15 * time.Second
		}
		// Grafana's Prometheus backend aligns range endpoints to the step's
		// Unix-time grid. Evaluating at the unaligned seed end shifts rollup
		// windows and can change panel values even with identical samples.
		grid := step.Milliseconds()
		start = time.UnixMilli(start.UnixMilli() / grid * grid)
		end = time.UnixMilli(end.UnixMilli() / grid * grid)
		rs, err := l.vm.Range(res.Expr, start, end, step)
		return len(rs) > 0, err
	default:
		parsed, err := vl.Parse(res.Expr, end)
		if err != nil {
			return true, nil // the static check reports it
		}
		if _, statsErr := parsed.StatsLabels(); statsErr == nil {
			rs, err := l.vl.Stats(res.Expr, start, end, false)
			return len(rs) > 0 && !allZeroNoBy(rs), err
		}
		rs, err := l.vl.Rows(res.Expr, start, end)
		return len(rs) > 0, err
	}
}

// varValues returns the values a template variable takes on the seed.
func (l *liveRun) varValues(v files.Variable, start, end time.Time) []string {
	switch v.Type {
	case "query":
		if m := labelValuesRe.FindStringSubmatch(v.Query); m != nil {
			metric, label := "", strings.TrimSpace(m[2])
			if label == "" {
				label = strings.TrimSpace(m[1])
			} else {
				metric = strings.TrimSpace(m[1])
				if i := strings.IndexAny(metric, "{"); i >= 0 {
					metric = metric[:i]
				}
			}
			vs := l.ds.LabelValues(metric, label, start.UnixMilli(), end.UnixMilli())
			if len(vs) > 0 {
				return vs
			}
			l.r.add(Warn, "", "", "variable", "variable $%s: label_values(%s) has no values on the seed", v.Name, v.Query)
		}
	case "custom":
		var out []string
		for _, s := range strings.Split(v.Query, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	case "interval":
		if len(v.Options) > 0 {
			return v.Options[:1]
		}
		return []string{"5m"}
	}
	if v.Current != "" && v.Current != "$__all" {
		return []string{v.Current}
	}
	if v.Query != "" && (v.Type == "constant" || v.Type == "textbox") {
		return []string{v.Query}
	}
	return []string{""}
}

// ---- messages ----

// noRows reports a query with no rows, with the names it reads that the seed does not hold.
func (l *liveRun) noRows(q *files.Query, expr, extra string) {
	var missing []string
	switch q.Kind {
	case files.Metrics:
		if e, err := metricsql.Parse(expr); err == nil {
			ms, ls := metricNames(e)
			for _, m := range ms {
				if !l.metricNames[m] {
					missing = append(missing, "metric "+m)
				}
			}
			for _, x := range ls {
				if !l.labelNames[x] {
					missing = append(missing, "label "+x)
				}
			}
		}
	case files.Logs:
		if p, err := vl.Parse(expr, l.end); err == nil {
			fs, ps, _ := p.Fields()
			for _, f := range append(fs, ps...) {
				if !l.logFields[f] {
					missing = append(missing, "log field "+f)
				}
			}
		}
	}
	sort.Strings(missing)
	hint := ""
	if len(missing) > 0 {
		hint = fmt.Sprintf("; not in the seed: %s", strings.Join(uniq(missing), ", "))
	}
	l.r.add(Fail, q.File, q.Where, "no-rows", "returned no rows on the seed%s%s; query: %s", extra, hint, oneLine(expr))
}

func uniq(a []string) []string {
	var out []string
	for i, s := range a {
		if i == 0 || s != a[i-1] {
			out = append(out, s)
		}
	}
	return out
}
