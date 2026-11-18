package live

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/contract"
)

// Status of one metric row of `obs live usage`.
type Status string

const (
	StatusOK            Status = "ok"
	StatusAboveWorst    Status = "above worst case"
	StatusOverBudget    Status = "OVER BUDGET"
	StatusNotInContract Status = "not in contract"
	StatusPlatform      Status = "platform (no limit)"
)

// UsageOptions of Usage.
type UsageOptions struct {
	Window      time.Duration // how far back rates (data points, lines, spans) are measured
	Pods        int           // a per_pod metric may hold worst case x Pods series
	Unknown     string        // metric that is not in the contract: warn, fail or ignore
	Top         int           // keep only the N rows furthest from the contract (0: all)
	OverOnly    bool          // keep only rows that are not ok
	AllServices bool          // do not restrict to the contract's service
	Estimates   Estimates
	Now         func() time.Time
}

// MetricRow is one contract metric (all its stored names together) on the target.
type MetricRow struct {
	Metric  string   `json:"metric"`
	Kind    string   `json:"kind,omitempty"`
	Stored  []string `json:"stored_names"`
	Series  int64    `json:"active_series"`
	Worst   int64    `json:"worst_case"`
	Budget  int      `json:"budget,omitempty"`
	Limit   int64    `json:"limit"` // the budget when the contract has one, else the worst case
	Ratio   float64  `json:"ratio_of_limit"`
	DPM     float64  `json:"data_points_per_min"`
	MaxDPM  float64  `json:"max_data_points_per_min"`
	DPMOver bool     `json:"dpm_over"`
	Status  Status   `json:"status"`
	rank    int
}

// LogsUsage is the log volume of the service on the target.
type LogsUsage struct {
	Available     bool    `json:"available"`
	Note          string  `json:"note,omitempty"`
	LinesPerMin   float64 `json:"lines_per_min"`
	BytesPerMin   float64 `json:"msg_bytes_per_min"`
	Streams       int64   `json:"streams"`
	StreamWorst   int64   `json:"stream_worst_case"`
	LinesEstimate float64 `json:"lines_estimate,omitempty"`
	LinesRatio    float64 `json:"lines_ratio,omitempty"`
	BytesEstimate float64 `json:"bytes_estimate,omitempty"`
	BytesRatio    float64 `json:"bytes_ratio,omitempty"`
	StreamsOver   bool    `json:"streams_over"`
}

// TracesUsage is the span volume on the target.
type TracesUsage struct {
	SpansAvailable bool    `json:"spans_available"`
	SpansPerMin    float64 `json:"spans_per_min"`
	SpansSource    string  `json:"spans_source,omitempty"`
	SpansNote      string  `json:"spans_note,omitempty"`
	BytesAvailable bool    `json:"bytes_available"`
	BytesPerMin    float64 `json:"bytes_per_min"`
	BytesSource    string  `json:"bytes_source,omitempty"`
	BytesNote      string  `json:"bytes_note,omitempty"`
	SpansEstimate  float64 `json:"spans_estimate,omitempty"`
	SpansRatio     float64 `json:"spans_ratio,omitempty"`
	BytesEstimate  float64 `json:"bytes_estimate,omitempty"`
	BytesRatio     float64 `json:"bytes_ratio,omitempty"`
}

// UsageResult is everything `obs live usage` found.
type UsageResult struct {
	Target       string       `json:"target"`
	Window       string       `json:"window"`
	Service      string       `json:"service,omitempty"`
	Scope        string       `json:"scope"`
	Pods         int          `json:"pods"`
	Metrics      []MetricRow  `json:"metrics"`
	Logs         *LogsUsage   `json:"logs"`
	Traces       *TracesUsage `json:"traces"`
	IsOverBudget bool         `json:"over_budget"`
	Reason       string       `json:"exit_reason"`
	TotalMetrics int          `json:"metrics_total"` // rows before --top and --over-only
}

// OverBudget is true when the command exits 1: some metric is over its budget (or an undeclared
// metric with --unknown fail), or the log streams are over the worst case.
func (r *UsageResult) OverBudget() bool { return r.IsOverBudget }

// Usage reads active series and data points per minute per metric, log lines, bytes and streams
// and span volume from the target and compares them with the contract.
func Usage(ctx context.Context, c *Client, t *Target, ct *contract.Contract, o UsageOptions) (*UsageResult, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Window <= 0 {
		o.Window = 5 * time.Minute
	}
	if o.Pods < 1 {
		o.Pods = 1
	}
	if o.Unknown == "" {
		o.Unknown = "warn"
	}
	now := o.Now()
	res := &UsageResult{Target: t.Name, Window: o.Window.String(), Pods: o.Pods}
	scope := ""
	if !o.AllServices && ct.Service != "" {
		res.Service = ct.Service
		scope = fmt.Sprintf(`,"durian.cloud.service"=%s`, strconv.Quote(ct.Service))
		res.Scope = fmt.Sprintf("series of service %q only", ct.Service)
	} else {
		res.Scope = "every series on the target"
	}
	sel := `{__name__=~".+"` + scope + `}`
	win := fmt.Sprintf("%ds", int(o.Window.Seconds()))

	ats, err := c.Instant(ctx, t.Metrics, "count by (__name__) ("+sel+")", now)
	if err != nil {
		return nil, fmt.Errorf("active series: %w", err)
	}
	mins := o.Window.Minutes()
	dpm, err := c.Instant(ctx, t.Metrics, "sum by (__name__) (count_over_time("+sel+"["+win+"]) keep_metric_names)", now)
	if err != nil {
		return nil, fmt.Errorf("data points: %w", err)
	}
	res.Metrics = metricRows(ct, ats, dpm, mins, o)
	res.TotalMetrics = len(res.Metrics)
	for _, m := range res.Metrics {
		if m.Status == StatusOverBudget || (m.Status == StatusNotInContract && o.Unknown == "fail") {
			res.IsOverBudget = true
		}
	}
	res.Logs = logsUsage(ctx, c, t, ct, o, now, scope != "")
	if res.Logs.StreamsOver {
		res.IsOverBudget = true
	}
	res.Traces = tracesUsage(ctx, c, t, ct, o, now, scope != "")
	switch {
	case res.IsOverBudget:
		res.Reason = "over budget: exit 1"
	default:
		res.Reason = "nothing over budget: exit 0"
	}
	res.Metrics = filterRows(res.Metrics, o)
	return res, nil
}

func metricRows(ct *contract.Contract, ats, dpm []Vec, mins float64, o UsageOptions) []MetricRow {
	interval, err := time.ParseDuration(ct.EffectiveInterval())
	if err != nil || interval <= 0 {
		interval = 30 * time.Second
	}
	perMin := 60 / interval.Seconds()
	rows := map[string]*MetricRow{}
	var order []string
	row := func(stored string) *MetricRow {
		sm, ok := ct.Lookup(stored)
		key, kind := stored, ""
		if ok {
			key, kind = sm.Name, sm.Metric.Kind
		}
		r := rows[key]
		if r == nil {
			r = &MetricRow{Metric: key, Kind: kind}
			switch {
			case !ok:
				r.Status = StatusNotInContract
			case sm.Platform:
				r.Status = StatusPlatform
			default:
				w, _ := ct.WorstCase(key)
				if sm.Metric.PerPod {
					w *= int64(o.Pods)
				}
				r.Worst, r.Budget = w, sm.Metric.Budget
				r.Limit = w
				if r.Budget > 0 {
					r.Limit = int64(r.Budget)
				}
				r.MaxDPM = float64(w) * perMin
			}
			rows[key] = r
			order = append(order, key)
		}
		for _, s := range r.Stored {
			if s == stored {
				return r
			}
		}
		r.Stored = append(r.Stored, stored)
		return r
	}
	for _, v := range ats {
		row(v.Labels["__name__"]).Series += int64(math.Round(v.V))
	}
	for _, v := range dpm {
		row(v.Labels["__name__"]).DPM += v.V / mins
	}
	var out []MetricRow
	for _, k := range order {
		r := rows[k]
		sort.Strings(r.Stored)
		switch r.Status {
		case "":
			switch {
			case r.Limit > 0 && r.Series > r.Limit:
				r.Status = StatusOverBudget
			case r.Series > r.Worst:
				r.Status = StatusAboveWorst
			default:
				r.Status = StatusOK
			}
			if r.Limit > 0 {
				r.Ratio = float64(r.Series) / float64(r.Limit)
			}
			r.DPMOver = r.MaxDPM > 0 && r.DPM > r.MaxDPM
		}
		r.rank = map[Status]int{StatusOverBudget: 0, StatusAboveWorst: 1, StatusNotInContract: 2, StatusOK: 3, StatusPlatform: 4}[r.Status]
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.rank == 2 || a.rank == 4 {
			if a.Series != b.Series {
				return a.Series > b.Series
			}
		} else if a.Ratio != b.Ratio {
			return a.Ratio > b.Ratio
		}
		return a.Metric < b.Metric
	})
	return out
}

func filterRows(rows []MetricRow, o UsageOptions) []MetricRow {
	var out []MetricRow
	for _, r := range rows {
		if o.Unknown == "ignore" && r.Status == StatusNotInContract {
			continue
		}
		if o.OverOnly && r.Status == StatusOK && !r.DPMOver {
			continue
		}
		if o.OverOnly && r.Status == StatusPlatform {
			continue
		}
		out = append(out, r)
	}
	if o.Top > 0 && len(out) > o.Top {
		out = out[:o.Top]
	}
	return out
}

func ratio(v, est float64) float64 {
	if est <= 0 {
		return 0
	}
	return v / est
}

func logsUsage(ctx context.Context, c *Client, t *Target, ct *contract.Contract, o UsageOptions, now time.Time, scoped bool) *LogsUsage {
	l := &LogsUsage{StreamWorst: ct.StreamCount(1)}
	if t.Logs == "" {
		l.Note = "the target has no logs URL (targets." + t.Name + ".logs)"
		return l
	}
	filter := ""
	if scoped {
		filter = " " + strconv.Quote("durian.cloud.service") + ":=" + strconv.Quote(ct.Service)
	}
	q := fmt.Sprintf("_time:%ds%s | stats count() as lines, sum_len(_msg) as msg_bytes, count_uniq(_stream_id) as streams", int(o.Window.Seconds()), filter)
	vs, err := c.InstantAt(ctx, t.Logs, "/select/logsql/stats_query", q, now)
	if err != nil {
		l.Note = err.Error()
		return l
	}
	l.Available = true
	mins := o.Window.Minutes()
	for _, v := range vs {
		switch v.Labels["__name__"] {
		case "lines":
			l.LinesPerMin = v.V / mins
		case "msg_bytes":
			l.BytesPerMin = v.V / mins
		case "streams":
			l.Streams = int64(math.Round(v.V))
		}
	}
	l.LinesEstimate, l.BytesEstimate = o.Estimates.Logs.LinesPerMin, o.Estimates.Logs.BytesPerMin
	l.LinesRatio, l.BytesRatio = ratio(l.LinesPerMin, l.LinesEstimate), ratio(l.BytesPerMin, l.BytesEstimate)
	l.StreamsOver = scoped && l.Streams > l.StreamWorst
	if !scoped {
		l.Note = "streams cover every service on the target; the worst case is this service's only"
	}
	return l
}

func tracesUsage(ctx context.Context, c *Client, t *Target, ct *contract.Contract, o UsageOptions, now time.Time, scoped bool) *TracesUsage {
	tr := &TracesUsage{}
	tr.SpansEstimate, tr.BytesEstimate = o.Estimates.Traces.SpansPerMin, o.Estimates.Traces.BytesPerMin
	win := fmt.Sprintf("%ds", int(o.Window.Seconds()))
	// Spans a minute: Tempo's own TraceQL metrics first (it can filter by service); Tempo's
	// distributor counter in VictoriaMetrics second (the whole Tempo, not one service).
	var tempoErr string
	if t.Traces == "" {
		tempoErr = "the target has no traces URL (targets." + t.Name + ".traces)"
	} else {
		q := "{ } | rate()"
		if scoped {
			q = "{ resource.durian.cloud.service = " + strconv.Quote(ct.Service) + " } | rate()"
		}
		vals, err := c.TempoRange(ctx, t.Traces, q, now.Add(-o.Window), now, time.Minute)
		switch {
		case err != nil:
			tempoErr = err.Error()
		case len(vals) == 0:
			tempoErr = "Tempo's metrics endpoint returned no samples (is the local-blocks processor on?)"
		default:
			sum := 0.0
			for _, v := range vals {
				sum += v
			}
			tr.SpansAvailable, tr.SpansPerMin, tr.SpansSource = true, sum/float64(len(vals))*60, "tempo metrics"
		}
	}
	perMin := func(metric string) (float64, bool) {
		vs, err := c.Instant(ctx, t.Metrics, "sum(rate("+metric+"["+win+"]))", now)
		if err != nil || len(vs) == 0 {
			return 0, false
		}
		return vs[0].V * 60, true
	}
	const wholeTempo = " (whole Tempo, not only this service)"
	if !tr.SpansAvailable {
		if v, ok := perMin("tempo_distributor_spans_received_total"); ok {
			tr.SpansAvailable, tr.SpansPerMin = true, v
			tr.SpansSource = "VictoriaMetrics tempo_distributor_spans_received_total" + wholeTempo
		} else {
			tr.SpansNote = tempoErr + "; and VictoriaMetrics has no tempo_distributor_spans_received_total"
		}
	}
	if v, ok := perMin("tempo_distributor_bytes_received_total"); ok {
		tr.BytesAvailable, tr.BytesPerMin = true, v
		tr.BytesSource = "VictoriaMetrics tempo_distributor_bytes_received_total" + wholeTempo
	} else {
		tr.BytesNote = "VictoriaMetrics has no tempo_distributor_bytes_received_total (Tempo's own metrics are not scraped there)"
	}
	tr.SpansRatio, tr.BytesRatio = ratio(tr.SpansPerMin, tr.SpansEstimate), ratio(tr.BytesPerMin, tr.BytesEstimate)
	return tr
}

func num(v float64) string {
	switch {
	case v >= 100:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case v >= 10:
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func vs(v, est, ratio float64) string {
	if est <= 0 {
		return "no estimate in obs.yaml"
	}
	return fmt.Sprintf("estimate %s, %sx", num(est), num(ratio))
}

// Print writes the table for people.
func (r *UsageResult) Print(w io.Writer) {
	fmt.Fprintf(w, "obs live usage: target %s, rates over the last %s, %s\n", r.Target, r.Window, r.Scope)
	fmt.Fprintf(w, "\nMETRICS (%d on the target, sorted by distance from the contract)\n", r.TotalMetrics)
	fmt.Fprintf(w, "  %-44s %8s %6s %7s %9s  %s\n", "metric", "series", "worst", "budget", "points/min", "status")
	for _, m := range r.Metrics {
		worst, budget := "-", "-"
		if m.Status != StatusNotInContract && m.Status != StatusPlatform {
			worst = strconv.FormatInt(m.Worst, 10)
		}
		if m.Budget > 0 {
			budget = strconv.Itoa(m.Budget)
		}
		extra := ""
		if m.DPMOver {
			extra = fmt.Sprintf("; points/min above %s", num(m.MaxDPM))
		}
		fmt.Fprintf(w, "  %-44s %8d %6s %7s %9s  %s%s\n", m.Metric, m.Series, worst, budget, num(m.DPM), m.Status, extra)
		if m.Status == StatusOverBudget {
			fmt.Fprintf(w, "    -> %d series is over budget %d (worst case %d)\n", m.Series, m.Budget, m.Worst)
		}
	}
	if len(r.Metrics) == 0 {
		fmt.Fprintln(w, "  (no rows: nothing matched, or --over-only / --unknown ignore hid them)")
	}
	l := r.Logs
	fmt.Fprintf(w, "\nLOGS\n")
	if !l.Available {
		fmt.Fprintf(w, "  not read: %s\n", l.Note)
	} else {
		fmt.Fprintf(w, "  lines/min: %s (%s)\n", num(l.LinesPerMin), vs(l.LinesPerMin, l.LinesEstimate, l.LinesRatio))
		fmt.Fprintf(w, "  message bytes/min: %s (%s)\n", num(l.BytesPerMin), vs(l.BytesPerMin, l.BytesEstimate, l.BytesRatio))
		st := "within the contract's worst case"
		if l.StreamsOver {
			st = "OVER BUDGET"
		}
		fmt.Fprintf(w, "  streams: %d, worst case %d: %s\n", l.Streams, l.StreamWorst, st)
		if l.Note != "" {
			fmt.Fprintf(w, "  note: %s\n", l.Note)
		}
	}
	tr := r.Traces
	fmt.Fprintf(w, "\nTRACES\n")
	if tr.SpansAvailable {
		fmt.Fprintf(w, "  spans/min: %s (%s); source: %s\n", num(tr.SpansPerMin), vs(tr.SpansPerMin, tr.SpansEstimate, tr.SpansRatio), tr.SpansSource)
	} else {
		fmt.Fprintf(w, "  spans/min: not read: %s\n", tr.SpansNote)
	}
	if tr.BytesAvailable {
		fmt.Fprintf(w, "  bytes/min: %s (%s); source: %s\n", num(tr.BytesPerMin), vs(tr.BytesPerMin, tr.BytesEstimate, tr.BytesRatio), tr.BytesSource)
	} else {
		fmt.Fprintf(w, "  bytes/min: not read: %s\n", tr.BytesNote)
	}
	fmt.Fprintf(w, "\nobs live usage: %s\n", strings.TrimSpace(r.Reason))
}
