package check

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/VictoriaMetrics/metricsql"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/logevent"
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
	var settingsNote string
	// Trace panels run under the resolved Tempo query-frontend: Tempo's defaults, then
	// obs.yaml thresholds.traces, then --tempo-config. Resolved only when a trace query
	// will run, so a bad --tempo-config is not an error for a repo without trace panels.
	for _, q := range set.Queries {
		if q.Problem == "" && q.Kind == files.Traces {
			fe, err := o.Obs.TempoFrontend(o.TempoConfig)
			if err != nil {
				return nil, err
			}
			o.TempoFrontend = fe
			break
		}
	}
	if needVM {
		e, err := vm.New(ds)
		if err != nil {
			return nil, err
		}
		defer e.Close()
		e.SetMaxPoints(o.Obs.MetricsMaxPoints())
		m = e
	}
	if needVL {
		limits, note, err := o.Obs.VLLimits(o.Dir, o.VLSettings)
		if err != nil {
			return nil, err
		}
		settingsNote = note
		e, err := vl.NewWithLimits(ds, limits)
		if err != nil {
			return nil, err
		}
		defer e.Close()
		l = e
	}
	r := runLive(set, o, ds, m, l)
	if settingsNote != "" {
		r.add(Note, "", "", "vl-settings", "%s", settingsNote)
	}
	return r, nil
}

// runLive runs every query of set on the given engines.
func runLive(set *files.Set, o Options, ds *seed.Dataset, m metricsEngine, l logsEngine) *Report {
	r := newReport()
	lv := &liveRun{r: r, o: o, ds: ds, end: ds.End, vm: m, vl: l, varSpelling: map[string]bool{}}
	lv.tempo = tempo.NewTempoEngine(ds.Traces)
	lv.frontend = tempo.DefaultFrontend
	if o.TempoFrontend != nil {
		lv.frontend = *o.TempoFrontend
	}
	lv.logFields = logFieldSet(ds)
	lv.metricNames = toSet(ds.MetricNames())
	lv.labelNames = labelNameSet(ds)
	// The contract decides the event rule exactly as --static does: when it applies, a log alert's
	// filters on free-form fields are assumed true and dropped, as `obs alerts` does.
	lv.eventOn = lv.o.eventRuleOn()
	if c, problems := loadContract(&lv.o); len(problems) == 0 && !lv.o.metricsOnly {
		lv.contract = c
	}
	// A recording rule's output exists only once `obs alerts` evaluates the rule: the seed never holds it.
	lv.recorded = map[string]string{}
	for _, rule := range set.Rules {
		if rec := rule.Definition.Record; rec != nil && rec.Metric != "" {
			lv.recorded[rec.Metric] = rec.Metric
		}
	}
	for i := range set.Queries {
		q := &set.Queries[i]
		if q.Problem != "" || (q.Kind != files.Metrics && q.Kind != files.Logs && q.Kind != files.Traces && !(q.IsAlert && q.Kind == files.Loki)) {
			continue
		}
		r.Counts[q.Kind.String()]++
		if q.Kind == files.Metrics && lv.spelling(q) {
			continue // a wrong label spelling is reported once, with the right one; the run would only say no-rows
		}
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
	varSpelling map[string]bool // dashboard file + variable: a wrong label_values spelling already reported
	tempo       *tempo.TempoEngine
	contract    *contract.Contract
	eventOn     bool
	recorded    map[string]string // metric a recording rule writes
	// frontend is the Tempo query-frontend setting trace panels and trace alerts are
	// validated and run under.
	frontend tempo.Frontend
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
		if names := l.recordedReads(e); len(names) > 0 {
			// Not silent and not a failure: the query cannot run on the seed, and `obs alerts` checks it.
			l.r.add(Note, q.File, q.Where, "recording-rule", "reads recording rule %s: checked by obs alerts", strings.Join(names, ", "))
			return
		}
		e, n := stripThresholds(e)
		expr := string(e.AppendString(nil))
		if n > 0 {
			l.r.add(Note, q.File, q.Where, "threshold-removed", "ran the query without its %d comparison(s) or absence check(s): a threshold filters rows out on a quiet seed; query run: %s", n, oneLine(expr))
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
		expr := q.Expr
		if l.eventOn && l.contract != nil {
			// Same analysis as --static and `obs alerts`: filters on free-form fields are assumed true.
			a, err := logevent.Analyze(expr, l.contract)
			if err != nil {
				return // reported by the static check
			}
			expr = a.Expr
			for _, assumed := range a.Assumed {
				l.r.add(Note, q.File, q.Where, "assumed-filter", "assumed true (free-form fields): %s", assumed)
			}
		}
		parsed, err := vl.Parse(expr, l.end)
		if err != nil {
			return
		}
		if _, err := parsed.StatsLabels(); err != nil {
			return // reported by the static check
		}
		rs, err := l.vl.Stats(expr, l.end.Add(-rng), l.end, true)
		if err != nil {
			l.r.add(Fail, q.File, q.Where, "logsql-run", "%v; query: %s", err, oneLine(expr))
			return
		}
		if len(rs) == 0 || allZeroNoBy(rs) {
			l.noRows(q, expr, fmt.Sprintf("; ran as the datasource does, start=end-%s, end=%s", rng, l.end.Format(time.RFC3339)))
		}
	case files.Traces:
		l.traceAlert(q) // livetrace.go
	default:
		l.r.add(Fail, q.File, q.Where, "live-unsupported-query", "datasource kind %s: obs cannot run this alert query live, so it was not run", q.Kind)
	}
}

// recordedReads names the recording-rule outputs the expression reads that the seed does not hold.
func (l *liveRun) recordedReads(e metricsql.Expr) []string {
	ms, _ := metricNames(e)
	var out []string
	for _, m := range ms {
		if _, ok := l.recorded[m]; ok && !l.metricNames[m] {
			out = append(out, m)
		}
	}
	return out
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
			vs := l.varValues(q, v, start, end)
			if len(vs) == 0 {
				return
			}
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
	case files.Traces:
		return l.tracePanel(q, res.Expr, start, end, time.Duration(max(res.StepMs, 1000))*time.Millisecond)
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
func (l *liveRun) varValues(q *files.Query, v files.Variable, start, end time.Time) []string {
	switch v.Type {
	case "query":
		if metric, label, ok := parseLabelValues(v.Query); ok {
			vs := l.ds.LabelValues(metric, label, start.UnixMilli(), end.UnixMilli())
			if len(vs) > 0 {
				return vs
			}
			// VictoriaMetrics stores a dotted label with its dots (concept 2.1): name the right spelling.
			if d := contract.Dotted(label, l.labelNames); d != "" {
				if k := q.File + "\x00" + v.Name; !l.varSpelling[k] { // once per variable, not once per panel that uses it
					l.varSpelling[k] = true
					l.r.add(Fail, q.File, q.Where, "label-spelling", "variable $%s: %s reads label %q, which VictoriaMetrics stores as %q: write %q", v.Name, v.Query, label, d, d)
				}
				return nil // the panel is not run: its values are unknown, and it would only add a no-rows
			}
			l.r.add(Warn, "", "", "variable", "variable $%s: %s has no values on the seed", v.Name, v.Query)
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

// spelling fails a metrics query that reads the underscore form of a label the seed stores dotted
// (VictoriaMetrics keeps the dots, concept 2.1), naming the right spelling. It needs no contract: it
// compares against the label names of the seed itself, so a grouping-only query is judged too.
// It reports whether it failed the query.
func (l *liveRun) spelling(q *files.Query) bool {
	e, err := metricsql.Parse(staticFill(q))
	if err != nil {
		return false // the static check reports it
	}
	seen := map[string]bool{}
	var wrong []string
	// check reports a read of a label this query did not create that the seed stores dotted. A label the
	// query itself makes (label_set, label_replace, label_join, label_copy, label_move, count_values) is
	// not a stored label: reading it back is fine, whatever its spelling.
	check := func(names []string, created map[string]bool) {
		for _, name := range names {
			if created[name] {
				continue
			}
			if d := contract.Dotted(name, l.labelNames); d != "" && !seen[name] {
				seen[name] = true
				wrong = append(wrong, fmt.Sprintf("label %q is stored as %q: write %q (a selector is {%q=\"...\"}, a grouping is by (%q))", name, d, d, d, d))
			}
		}
	}
	var walk func(e metricsql.Expr) map[string]bool
	union := func(es []metricsql.Expr) map[string]bool {
		c := map[string]bool{}
		for _, x := range es {
			for k := range walk(x) {
				c[k] = true
			}
		}
		return c
	}
	walk = func(e metricsql.Expr) map[string]bool {
		switch t := e.(type) {
		case *metricsql.MetricExpr:
			var names []string
			for _, lfs := range t.LabelFilterss {
				for _, lf := range lfs {
					names = append(names, lf.Label)
				}
			}
			check(names, nil) // a selector reads stored labels
			return map[string]bool{}
		case *metricsql.RollupExpr:
			c := walk(t.Expr)
			if t.At != nil {
				walk(t.At)
			}
			return c
		case *metricsql.FuncExpr:
			c := union(t.Args)
			reads, makes := labelFuncLabels(t)
			if len(reads) > 0 && len(t.Args) > 0 {
				check(reads, walk(t.Args[0]))
			}
			for _, m := range makes {
				c[m] = true
			}
			return c
		case *metricsql.AggrFuncExpr:
			c := union(t.Args)
			check(t.Modifier.Args, c)
			name := strings.ToLower(t.Name)
			// A REDUCING aggregation keeps only the labels it names: by() its list, without() all but its
			// list, none with no modifier. A query-made label it removes is gone from the output, so an outer
			// reader of it reads nothing. A SELECTOR aggregation (topk, bottomk, limitk, any, outliersk,
			// topk_*, zscore ...) returns the input series whole, so by()/without() change nothing there; an
			// aggregation not in the reducing list is treated that way (the lenient side).
			if reducingAggregations[name] {
				switch strings.ToLower(t.Modifier.Op) {
				case "by":
					c = keepOnly(c, t.Modifier.Args)
				case "without":
					c = dropAll(c, t.Modifier.Args)
				default:
					c = map[string]bool{}
				}
			}
			if name == "count_values" && len(t.Args) > 0 {
				if se, ok := t.Args[0].(*metricsql.StringExpr); ok {
					c[se.S] = true // the label count_values makes is on its output
				}
			}
			return c
		case *metricsql.BinaryOpExpr:
			cl, cr := walk(t.Left), walk(t.Right)
			both := map[string]bool{}
			for k := range cl {
				both[k] = true
			}
			for k := range cr {
				both[k] = true
			}
			check(t.GroupModifier.Args, both)
			check(t.JoinModifier.Args, both)
			// The result follows VictoriaMetrics' adjustBinaryOpTags. With no on()/ignoring() and no group
			// modifier, a scalar-like side (a number, scalar(), vector(2), a label-less single series) leaves
			// the other side's labels whole. Otherwise the result carries the labels of one side: the left
			// side's for a one-to-one match and for group_left, the right side's for group_right; group_left
			// / group_right then set each listed label from the other side (a listed label the other side
			// lacks is removed), except labels in on(), which stay. A query-made label on the other side
			// that is not listed is dropped. or and default also return the right side's series.
			var c map[string]bool
			skip := map[string]bool{}
			if strings.EqualFold(t.GroupModifier.Op, "on") {
				for _, k := range t.GroupModifier.Args {
					skip[k] = true
				}
			}
			switch {
			case t.GroupModifier.Op == "" && t.JoinModifier.Op == "" && bareOperator(t.Op) && labelFree(t.Left):
				c = dropAll(cr, nil)
			case t.GroupModifier.Op == "" && t.JoinModifier.Op == "" && bareOperator(t.Op) && labelFree(t.Right):
				c = dropAll(cl, nil)
			case strings.EqualFold(t.JoinModifier.Op, "group_left"):
				c = withListed(cl, cr, t.JoinModifier.Args, skip, l.labelNames)
			case strings.EqualFold(t.JoinModifier.Op, "group_right"):
				c = withListed(cr, cl, t.JoinModifier.Args, skip, l.labelNames)
			default:
				c = dropAll(cl, nil)
				if op := strings.ToLower(t.Op); op == "or" || op == "default" {
					for k := range cr {
						c[k] = true
					}
				}
				// Arithmetic and comparison one-to-one matching with on() keeps only the on() labels, with
				// ignoring() drops them; the set operators and default/if/ifnot keep the labels whole whatever
				// on()/ignoring() says.
				if matchResetsLabels(t) {
					switch strings.ToLower(t.GroupModifier.Op) {
					case "on":
						c = keepOnly(c, t.GroupModifier.Args)
					case "ignoring":
						c = dropAll(c, t.GroupModifier.Args)
					}
				}
			}
			return c
		}
		return map[string]bool{}
	}
	walk(e)
	for _, w := range wrong {
		l.r.add(Fail, q.File, q.Where, "label-spelling", "VictoriaMetrics keeps a label's dots: %s", w)
	}
	return len(wrong) > 0
}

// matchResetsLabels reports whether a one-to-one match of this operator reduces the result labels to the
// on() list (or drops the ignoring() list): arithmetic and comparison go through adjustBinaryOpTags and do;
// the set operators and default/if/ifnot have their own functions and keep the labels whole.
func matchResetsLabels(t *metricsql.BinaryOpExpr) bool {
	switch t.Op {
	case "+", "-", "*", "/", "%", "^", "atan2", "==", "!=", ">", "<", ">=", "<=":
		return true
	}
	return false
}

// bareOperator is an operator that goes through the scalar fast path of adjustBinaryOpTags (arithmetic and
// comparison), not the set operators or default/if/ifnot.
func bareOperator(op string) bool { return matchResetsLabels(&metricsql.BinaryOpExpr{Op: op}) }

// labelFree reports whether the expression is statically a single series with no labels, which the engine
// treats as a scalar in a binary operation (isScalar): numbers, durations, scalar() and the time functions,
// vector() of such, parentheses and arithmetic of such, and a reducing aggregation with no labels left.
func labelFree(e metricsql.Expr) bool {
	switch t := e.(type) {
	case *metricsql.NumberExpr, *metricsql.DurationExpr:
		return true
	case *metricsql.BinaryOpExpr:
		return labelFree(t.Left) && labelFree(t.Right)
	case *metricsql.FuncExpr:
		switch strings.ToLower(t.Name) {
		case "now", "pi", "scalar", "start", "end", "step", "time", "timezone_offset":
			return true
		case "vector", "":
			return len(t.Args) == 1 && labelFree(t.Args[0])
		}
	case *metricsql.AggrFuncExpr:
		name := strings.ToLower(t.Name)
		return reducingAggregations[name] && name != "count_values" && strings.EqualFold(t.Modifier.Op, "by") && len(t.Modifier.Args) == 0 ||
			reducingAggregations[name] && name != "count_values" && t.Modifier.Op == ""
	}
	return false
}

// reducingAggregations are the aggregations whose output carries only the labels by()/without() leave
// (one series with none when there is no modifier).
var reducingAggregations = map[string]bool{
	"sum": true, "avg": true, "min": true, "max": true, "count": true, "group": true, "stddev": true, "stdvar": true,
	"quantile": true, "median": true, "sum2": true, "geomean": true, "mad": true, "distinct": true, "mode": true,
	"count_values": true, "histogram": true,
}

// withListed is the labels of the kept side with each listed label set from the other side, as group_left /
// group_right do: a listed label the other side has is present; one it lacks is removed (the other side's
// empty value replaces it), except labels in on() (skip), which stay. A listed name that is a stored label of the
// seed is taken as present (the other side's stored value is copied; only made labels are tracked here).
func withListed(kept, other map[string]bool, listed []string, skip, stored map[string]bool) map[string]bool {
	out := dropAll(kept, nil)
	for _, k := range listed {
		switch {
		case skip[k]:
		case other[k] || stored[k]:
			out[k] = true
		default:
			delete(out, k)
		}
	}
	return out
}

// keepOnly is the labels of set that are in keep; dropAll is the labels of set that are not in drop.
func keepOnly(set map[string]bool, keep []string) map[string]bool {
	out := map[string]bool{}
	for _, k := range keep {
		if set[k] {
			out[k] = true
		}
	}
	return out
}

func dropAll(set map[string]bool, drop []string) map[string]bool {
	out := map[string]bool{}
	for k := range set {
		out[k] = true
	}
	for _, k := range drop {
		delete(out, k)
	}
	return out
}

// ---- messages ----

// noRows reports a query with no rows, with the names it reads that the seed does not hold.
func (l *liveRun) noRows(q *files.Query, expr, extra string) {
	var missing []string
	switch q.Kind {
	case files.Traces:
		missing = l.missingTraceAttrs(expr)
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
					// VictoriaMetrics stores a dotted label with its dots (concept 2.1): name the right spelling.
					if d := contract.Dotted(x, l.labelNames); d != "" {
						missing = append(missing, fmt.Sprintf("label %s (stored as %q: write {%q=...})", x, d, d))
					} else {
						missing = append(missing, "label "+x)
					}
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
