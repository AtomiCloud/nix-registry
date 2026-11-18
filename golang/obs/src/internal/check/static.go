package check

import (
	"strings"
	"time"

	"github.com/VictoriaMetrics/metricsql"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/logevent"
	"github.com/AtomiCloud/obs/internal/vars"
)

const eventField = "durian.cloud/event"

// Static runs `obs query --static`: every query parsed by the embedded engines' own
// parsers, and the rules of concept section 11.3 that need no data.
func Static(set *files.Set, o Options) *Report {
	r := newReport()
	for _, p := range set.Problems {
		r.add(Fail, p.File, "", "file", "%s", p.Msg)
	}
	if !o.AllowEmpty && set.Alerts == 0 && len(set.Dashboards) == 0 && len(set.Problems) == 0 {
		r.add(Fail, o.Dir, "", "empty", "no alerts/**/*.yaml and no dashboards/**/*.json found")
	}
	c, problems := loadContract(&o)
	for _, p := range problems {
		r.add(Fail, o.contractPath(), "", "contract", "%s", p)
	}
	if c != nil && o.SpanAttributes == nil {
		o.SpanAttributes = c // the contract names the span attributes a TraceQL query may read
	}
	eventOn := o.eventRuleOn()
	if c != nil {
		for _, d := range set.Dashboards {
			contractVariables(r, d, c)
		}
	}
	for i := range set.Queries {
		q := &set.Queries[i]
		if q.Problem != "" {
			r.add(Fail, q.File, q.Where, "datasource", "%s", q.Problem)
			continue
		}
		switch q.Kind {
		case files.Loki:
			r.Counts["loki"]++
			r.add(Fail, q.File, q.Where, "loki-datasource",
				"datasource %q (type %q) is a Loki-type datasource: logs are LogsQL on VictoriaLogs, so a dashboard or alert query on Loki is refused",
				firstNonEmpty(q.DSUID, q.DSType), q.DSType)
		case files.Traces:
			r.Counts["traces"]++
			staticTraces(r, q, o)
		case files.Metrics:
			r.Counts["metrics"]++
			staticMetrics(r, q, o, c)
		case files.Logs:
			r.Counts["logs"]++
			staticLogs(r, q, o, eventOn, c)
		}
	}
	return r
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// staticFill fills a dashboard query's variables with the values a static check
// can know: a variable's current value, else a placeholder. Alerts are returned as is.
func staticFill(q *files.Query) string {
	if q.Dash == nil {
		return q.Expr
	}
	from, to := dashRange(q.Dash)
	vs := map[string][]string{}
	for _, v := range q.Dash.Vars {
		val := v.Current
		if val == "" && len(v.Options) > 0 {
			val = v.Options[0]
		}
		if val == "" && (v.Type == "constant" || v.Type == "custom") {
			val = firstNonEmpty(strings.Split(v.Query, ",")[0])
		}
		if val == "" && v.Type == "interval" {
			val = "5m"
		}
		if val == "" || val == "$__all" {
			val = "x"
		}
		vs[v.Name] = []string{val}
	}
	now := time.Now()
	return vars.Fill(vars.Case{
		Expr: q.Expr, FromMs: now.Add(-from).UnixMilli(), ToMs: now.Add(-to).UnixMilli(),
		QueryInterval: q.Interval, MaxDataPoints: q.MaxDataPoints, Scrape: "15s", Vars: vs,
	}).Expr
}

// dashRange reads time.from / time.to (`now-6h`, `now`) as ages.
func dashRange(d *files.Dashboard) (from, to time.Duration) {
	return age(d.From, 6*time.Hour), age(d.To, 0)
}

func age(s string, def time.Duration) time.Duration {
	s = strings.TrimSpace(s)
	if s == "now" {
		return 0
	}
	if rest, ok := strings.CutPrefix(s, "now-"); ok {
		if d, err := vars.ParseDuration(rest); err == nil {
			return d
		}
	}
	return def
}

func staticMetrics(r *Report, q *files.Query, o Options, c *contract.Contract) {
	expr := staticFill(q)
	e, err := metricsql.Parse(expr)
	if err != nil {
		if m := unsupportedFnRe.FindStringSubmatch(err.Error()); m != nil {
			r.add(Fail, q.File, q.Where, "metricsql-unsupported-function",
				"function %q is not supported by VictoriaMetrics (Prometheus-only); query: %s", m[1], oneLine(expr))
			return
		}
		r.add(Fail, q.File, q.Where, "metricsql-syntax", "%v; query: %s", err, oneLine(expr))
		return
	}
	for _, f := range unsupportedFuncs(e) {
		r.add(Fail, q.File, q.Where, "metricsql-unsupported-function",
			"function %q is not supported by VictoriaMetrics (Prometheus-only); query: %s", f, oneLine(expr))
	}
	for _, f := range windowlessRollups(e) {
		r.add(Warn, q.File, q.Where, "rollup-no-window",
			"%s(...) has no [window]: the window then comes from the query step; write [5m] or similar", f)
	}
	if c != nil {
		contractMetrics(r, q, e, c)
	}
	if o.Names {
		m, l := metricNames(e)
		r.add(Note, q.File, q.Where, "names", "reads metrics=%v labels=%v", m, l)
	}
}

func staticLogs(r *Report, q *files.Query, o Options, eventOn bool, c *contract.Contract) {
	expr := staticFill(q)
	parsed, err := vl.Parse(expr, time.Now())
	if err != nil {
		r.add(Fail, q.File, q.Where, "logsql-syntax", "%v; query: %s", err, oneLine(expr))
		return
	}
	labels, statsErr := parsed.StatsLabels()
	if q.IsAlert && statsErr != nil {
		r.add(Fail, q.File, q.Where, "logsql-alert-needs-stats",
			"a LogsQL alert query is a `stats` query (the datasource calls /select/logsql/stats_query): %v", statsErr)
	}
	filters, pipes, wildcard := parsed.Fields()
	if statsErr == nil {
		if len(withoutTime(filters)) == 0 {
			r.add(Warn, q.File, q.Where, "logsql-namespace-wide",
				"stats over every row: the query has no field or stream filter besides _time (concept 6.4); narrow it")
		}
	}
	if c != nil {
		contractLogs(r, q, parsed, c, filters)
	}
	if q.IsAlert {
		hasEvent := contains(filters, eventField) || contains(pipes, eventField)
		switch {
		case !eventOn:
			r.add(Note, q.File, q.Where, "event-rule", "event rule skipped: no contract.yaml")
		case c != nil:
			a, err := logevent.Analyze(expr, c)
			if err != nil {
				id := "logsql-alert-event"
				msg := err.Error()
				if strings.Contains(msg, "must filter on the event") {
					id = "logsql-alert-needs-event"
				}
				if strings.HasPrefix(msg, "event ") && strings.Contains(msg, "not in the contract") {
					id = "contract-undeclared-event"
				}
				if strings.Contains(msg, "event filter form not checkable") {
					id = "contract-event-filter-form"
				}
				if strings.Contains(msg, "matches no contract event") {
					id = "contract-event-regexp-no-match"
				}
				if strings.HasPrefix(msg, "event prefix ") && strings.Contains(msg, "matches no contract event") {
					id = "contract-undeclared-event"
				}
				r.add(Fail, q.File, q.Where, id, "%v", err)
			} else {
				r.add(Note, q.File, q.Where, "event-rule", "events=%v", a.Events)
				for _, assumed := range a.Assumed {
					r.add(Note, q.File, q.Where, "assumed-filter", "assumed true (free-form fields): %s", assumed)
				}
			}
		case !hasEvent:
			r.add(Fail, q.File, q.Where, "logsql-alert-needs-event",
				"a log alert must filter on the event field %q (exact :=, phrase, prefix, negated, regex :~ or in(...)); contract.yaml is present", eventField)
		default:
			r.add(Note, q.File, q.Where, "event-rule", "event filter present; no contract event list")
		}
	}
	if o.Names {
		r.add(Note, q.File, q.Where, "names", "reads filter fields=%v pipe fields=%v wildcard=%v stats labels=%v", filters, pipes, wildcard, labels)
	}
}

func withoutTime(a []string) []string {
	var out []string
	for _, s := range a {
		if s != "_time" {
			out = append(out, s)
		}
	}
	return out
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
