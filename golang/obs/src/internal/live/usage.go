package live

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/assert"
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
	Unknown     string        // metric that is not in the contract: warn, fail (default) or ignore
	Top         int           // keep only the N rows furthest from the contract (0: all)
	OverOnly    bool          // keep only rows that are not ok
	AllServices bool          // do not restrict to the contract's service
	Estimates   Estimates
	Now         func() time.Time
}

// LandscapeUsage is what one landscape holds of a metric: the budget is per landscape, so every
// comparison is made one landscape at a time.
type LandscapeUsage struct {
	Landscape string  `json:"landscape"`
	Series    int64   `json:"active_series"`
	DPM       float64 `json:"data_points_per_min"`
}

// MetricRow is one contract metric (all its stored names together) on the target. Series and DPM
// are those of the fullest landscape.
type MetricRow struct {
	Metric      string           `json:"metric"`
	Kind        string           `json:"kind,omitempty"`
	Stored      []string         `json:"stored_names"`
	Landscapes  []LandscapeUsage `json:"landscapes,omitempty"`
	Series      int64            `json:"active_series"`
	Worst       int64            `json:"worst_case"`
	Budget      int              `json:"budget,omitempty"`
	Limit       int64            `json:"limit"` // the budget when the contract has one, else the worst case
	SeriesRatio float64          `json:"ratio_of_limit"`
	DPM         float64          `json:"data_points_per_min"`
	WorstDPM    float64          `json:"worst_case_data_points_per_min"`
	BudgetDPM   float64          `json:"budget_data_points_per_min,omitempty"`
	LimitDPM    float64          `json:"limit_data_points_per_min"`
	DPMRatio    float64          `json:"dpm_ratio_of_limit"`
	Deviation   float64          `json:"deviation"` // the larger of the two ratios: how far from the contract
	DPMOver     bool             `json:"dpm_over"`  // points/min above the worst case (the budget when it is over that too)
	DPMNote     string           `json:"data_points_note,omitempty"`
	Status      Status           `json:"status"`
	Why         string           `json:"why,omitempty"`
	rank        int
}

// Finding is one of assert's label checks (and its over-the-worst-case check) on the target.
type Finding struct {
	Level  string `json:"level"`
	Metric string `json:"metric"`
	Rule   string `json:"rule"`
	Msg    string `json:"message"`
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
	Compared      bool    `json:"compared_with_estimates"`
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
	Findings     []Finding    `json:"findings"`
	Logs         *LogsUsage   `json:"logs"`
	Traces       *TracesUsage `json:"traces"`
	IsOverBudget bool         `json:"over_budget"`
	Reason       string       `json:"exit_reason"`
	TotalMetrics int          `json:"metrics_total"` // rows before --top and --over-only
}

// OverBudget is true when the command exits 1.
func (r *UsageResult) OverBudget() bool { return r.IsOverBudget }

// maxDPMNames bounds the per-name data point queries (one request per stored metric name).
const maxDPMNames = 400

// Usage reads active series and data points per minute per metric, log lines, bytes and streams
// and span volume from the target and compares them with the contract. The series are read once
// (/api/v1/series) and counted by internal/assert, the code behind `obs assert`: per landscape,
// with its label checks. Data points are read with one PromQL-compatible query per metric name.
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
		o.Unknown = "fail"
	}
	now := o.Now()
	res := &UsageResult{Target: t.Name, Window: o.Window.String(), Pods: o.Pods, Findings: []Finding{}}
	scope := ""
	if !o.AllServices && ct.Service != "" {
		res.Service = ct.Service
		scope = fmt.Sprintf(`,"durian.cloud.service"=%s`, strconv.Quote(ct.Service))
		res.Scope = fmt.Sprintf("series of service %q only", ct.Service)
	} else {
		res.Scope = "every series on the target"
	}
	sel := `{__name__=~".+"` + scope + `}`

	raw, err := c.Series(ctx, t.Metrics, sel, now.Add(-o.Window), now)
	if err != nil {
		return nil, fmt.Errorf("active series: %w", err)
	}
	series, err := assert.ParseSeries("the target's /api/v1/series answer", bytes.NewReader(raw))
	if err != nil {
		// an empty answer is a valid "nothing stored"
		if !strings.Contains(err.Error(), "empty") {
			return nil, fmt.Errorf("active series: %w", err)
		}
		series = nil
	}
	chk := assert.Check(ct, series, assert.Options{Pods: o.Pods, Unknown: o.Unknown})

	names := storedNames(series)
	dpm, dpmErrs := dataPoints(ctx, c, t, names, scope, o.Window, now)
	if len(names) > 0 && len(dpmErrs) == len(names) {
		return nil, fmt.Errorf("data points: %s", firstErr(dpmErrs))
	}
	res.Metrics = metricRows(ct, series, chk, dpm, dpmErrs, o)
	res.TotalMetrics = len(res.Metrics)
	for _, f := range chk.Findings {
		res.Findings = append(res.Findings, Finding{Level: f.Level.String(), Metric: f.Metric, Rule: f.Rule, Msg: f.Msg})
		// Every failure assert would report (series over the worst case, a label outside the
		// contract, a pod label on a non-per_pod metric, a value outside its type) fails here too.
		if f.Level == assert.Fail {
			res.IsOverBudget = true
		}
	}
	for _, m := range res.Metrics {
		if m.Status == StatusOverBudget {
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
		res.Reason = "over budget or a contract hole found: exit 1"
	default:
		res.Reason = "nothing over budget: exit 0"
	}
	res.Metrics = filterRows(res.Metrics, o)
	return res, nil
}

func storedNames(series []assert.Series) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range series {
		if !seen[s.Name] {
			seen[s.Name] = true
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

func firstErr(m map[string]error) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks[0] + ": " + m[ks[0]].Error()
}

const landscapeLabel = "durian.cloud.landscape"

// dataPointsQuery is the data points a metric name stored in window w, per landscape. It is
// written to parse in Prometheus and in VictoriaMetrics alike: count_over_time drops the metric
// name in both, so the name goes into the selector (one query per name) and the grouping is by
// the landscape label only. keep_metric_names is MetricsQL only and Prometheus rejects it.
func dataPointsQuery(name, scope string, w time.Duration) string {
	return fmt.Sprintf(`sum by (%s) (count_over_time({__name__=%s%s}[%ds]))`, strconv.Quote(landscapeLabel), strconv.Quote(name), scope, int(w.Seconds()))
}

// dataPoints reads, per stored name and landscape, the data points per minute. A name whose query
// fails is returned in the error map and its row says so.
func dataPoints(ctx context.Context, c *Client, t *Target, names []string, scope string, w time.Duration, now time.Time) (map[string]map[string]float64, map[string]error) {
	out := map[string]map[string]float64{}
	errs := map[string]error{}
	mins := w.Minutes()
	for i, n := range names {
		if i >= maxDPMNames {
			for _, rest := range names[i:] {
				errs[rest] = fmt.Errorf("not read: more than %d metric names on the target", maxDPMNames)
			}
			break
		}
		vs, err := c.Instant(ctx, t.Metrics, dataPointsQuery(n, scope, w), now)
		if err != nil {
			errs[n] = err
			continue
		}
		m := map[string]float64{}
		for _, v := range vs {
			m[v.Labels[landscapeLabel]] += v.V / mins
		}
		out[n] = m
	}
	return out, errs
}

func metricRows(ct *contract.Contract, series []assert.Series, chk *assert.Result, dpm map[string]map[string]float64, dpmErrs map[string]error, o UsageOptions) []MetricRow {
	interval, err := time.ParseDuration(ct.EffectiveInterval())
	if err != nil || interval <= 0 {
		interval = 30 * time.Second
	}
	perMin := 60 / interval.Seconds()
	rows := map[string]*MetricRow{}
	var order []string
	landDPM := func(stored []string, land string) (float64, string) {
		sum, note := 0.0, ""
		for _, s := range stored {
			if e, ok := dpmErrs[s]; ok {
				note = "points/min not read for " + s + ": " + e.Error()
				continue
			}
			sum += dpm[s][land]
		}
		return sum, note
	}
	add := func(key string, mk func() *MetricRow, lu LandscapeUsage, stored []string, note string) {
		r := rows[key]
		if r == nil {
			r = mk()
			rows[key] = r
			order = append(order, key)
		}
		r.Landscapes = append(r.Landscapes, lu)
		for _, s := range stored {
			found := false
			for _, x := range r.Stored {
				found = found || x == s
			}
			if !found {
				r.Stored = append(r.Stored, s)
			}
		}
		if lu.Series > r.Series {
			r.Series = lu.Series
		}
		if lu.DPM > r.DPM {
			r.DPM = lu.DPM
		}
		if note != "" {
			r.DPMNote = note
		}
	}
	for _, cnt := range chk.Counts {
		d, note := landDPM(cnt.Stored, cnt.Landscape)
		cnt := cnt
		add(cnt.Metric, func() *MetricRow {
			sm, _ := ct.Lookup(cnt.Stored[0])
			r := &MetricRow{Metric: cnt.Metric, Kind: sm.Metric.Kind}
			if cnt.Platform {
				r.Status = StatusPlatform
				return r
			}
			r.Worst, r.Budget = cnt.Worst, sm.Metric.Budget
			r.Limit = r.Worst
			if r.Budget > 0 {
				r.Limit = int64(r.Budget)
			}
			r.WorstDPM = float64(r.Worst) * perMin
			r.LimitDPM = float64(r.Limit) * perMin
			if r.Budget > 0 {
				r.BudgetDPM = float64(r.Budget) * perMin
			}
			return r
		}, LandscapeUsage{Landscape: cnt.Landscape, Series: int64(cnt.Series), DPM: d}, cnt.Stored, note)
	}
	// metrics the contract does not know: counted per name and landscape, like assert counts them
	un := map[string]map[string]int64{}
	for _, s := range series {
		if _, ok := ct.Lookup(s.Name); !ok {
			if un[s.Name] == nil {
				un[s.Name] = map[string]int64{}
			}
			un[s.Name][s.Labels[landscapeLabel]]++
		}
	}
	var unNames []string
	for n := range un {
		unNames = append(unNames, n)
	}
	sort.Strings(unNames)
	for _, n := range unNames {
		var lands []string
		for l := range un[n] {
			lands = append(lands, l)
		}
		sort.Strings(lands)
		for _, l := range lands {
			d, note := landDPM([]string{n}, l)
			add(n, func() *MetricRow { return &MetricRow{Metric: n, Status: StatusNotInContract} }, LandscapeUsage{Landscape: l, Series: un[n][l], DPM: d}, []string{n}, note)
		}
	}
	var out []MetricRow
	for _, k := range order {
		r := rows[k]
		sort.Strings(r.Stored)
		sort.SliceStable(r.Landscapes, func(i, j int) bool { return r.Landscapes[i].Landscape < r.Landscapes[j].Landscape })
		if len(r.Landscapes) == 1 && r.Landscapes[0].Landscape == "" {
			r.Landscapes = nil // one store, no landscape label: nothing to list
		}
		if r.Limit > 0 {
			r.SeriesRatio = float64(r.Series) / float64(r.Limit)
		}
		if r.LimitDPM > 0 {
			r.DPMRatio = r.DPM / r.LimitDPM
		}
		r.Deviation = math.Max(r.SeriesRatio, r.DPMRatio)
		switch r.Status {
		case "":
			overSeries, overDPM := r.Series > r.Limit, r.DPM > r.LimitDPM
			switch {
			case overSeries || overDPM:
				r.Status = StatusOverBudget
				switch {
				case overSeries && overDPM:
					r.Why = fmt.Sprintf("%d series is over %s %d; %s points/min is over %s", r.Series, limitName(r), r.Limit, num(r.DPM), num(r.LimitDPM))
				case overSeries:
					r.Why = fmt.Sprintf("%d series is over %s %d (worst case %d)", r.Series, limitName(r), r.Limit, r.Worst)
				default:
					r.Why = fmt.Sprintf("%s points/min is over %s %s (%s the worst case)", num(r.DPM), limitName(r), num(r.LimitDPM), num(r.WorstDPM))
				}
			case r.Series > r.Worst || r.DPM > r.WorstDPM:
				r.Status = StatusAboveWorst
			default:
				r.Status = StatusOK
			}
			r.DPMOver = r.DPM > r.WorstDPM
		}
		// over budget first, then a metric the contract does not know, then above the worst case
		r.rank = map[Status]int{StatusOverBudget: 0, StatusNotInContract: 1, StatusAboveWorst: 2, StatusOK: 3, StatusPlatform: 4}[r.Status]
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.rank == 1 || a.rank == 4 { // no contract number to measure against: the fullest first
			if a.Series != b.Series {
				return a.Series > b.Series
			}
		} else if a.Deviation != b.Deviation {
			return a.Deviation > b.Deviation
		}
		return a.Metric < b.Metric
	})
	// an undeclared metric has no budget at all: with --unknown fail (the default) it is over budget
	for i := range out {
		if out[i].Status == StatusNotInContract && o.Unknown == "fail" {
			out[i].Status = StatusOverBudget
			out[i].Why = "not in the contract and not a platform metric: budget 0, so every series is over budget (--unknown warn shows it without failing)"
		}
	}
	return out
}

func limitName(r *MetricRow) string {
	if r.Budget > 0 {
		return "budget"
	}
	return "the worst case"
}

func filterRows(rows []MetricRow, o UsageOptions) []MetricRow {
	var out []MetricRow
	for _, r := range rows {
		if o.Unknown == "ignore" && r.Status == StatusNotInContract {
			continue
		}
		if o.OverOnly && (r.Status == StatusOK || r.Status == StatusPlatform) {
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
	if scoped {
		// the estimates and the stream worst case are the service's: compared only when the read is too
		l.Compared = true
		l.LinesEstimate, l.BytesEstimate = o.Estimates.Logs.LinesPerMin, o.Estimates.Logs.BytesPerMin
		l.LinesRatio, l.BytesRatio = ratio(l.LinesPerMin, l.LinesEstimate), ratio(l.BytesPerMin, l.BytesEstimate)
		l.StreamsOver = l.Streams > l.StreamWorst
	} else {
		l.Note = "every service on the target is counted, so nothing is compared: the estimates and the stream worst case are this service's"
	}
	return l
}

func tracesUsage(ctx context.Context, c *Client, t *Target, ct *contract.Contract, o UsageOptions, now time.Time, scoped bool) *TracesUsage {
	tr := &TracesUsage{}
	win := fmt.Sprintf("%ds", int(o.Window.Seconds()))
	// Spans a minute: Tempo's own TraceQL metrics first (it can filter by service); Tempo's
	// distributor counter in VictoriaMetrics second. The counter covers the whole Tempo, so it
	// is used only when the whole target is asked for (--all-services): never as one service's figure.
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
	const wholeTempo = " (whole Tempo, not one service)"
	const notPerService = "not available for one service: Tempo's own counter covers every service and is not compared with this service's estimate (--all-services reads it for the whole target)"
	if !tr.SpansAvailable {
		switch {
		case scoped:
			tr.SpansNote = tempoErr + "; " + notPerService
		default:
			if v, ok := perMin("tempo_distributor_spans_received_total"); ok {
				tr.SpansAvailable, tr.SpansPerMin = true, v
				tr.SpansSource = "VictoriaMetrics tempo_distributor_spans_received_total" + wholeTempo
			} else {
				tr.SpansNote = tempoErr + "; and VictoriaMetrics has no tempo_distributor_spans_received_total"
			}
		}
	}
	switch {
	case scoped:
		tr.BytesNote = "bytes/min " + notPerService
	default:
		if v, ok := perMin("tempo_distributor_bytes_received_total"); ok {
			tr.BytesAvailable, tr.BytesPerMin = true, v
			tr.BytesSource = "VictoriaMetrics tempo_distributor_bytes_received_total" + wholeTempo
		} else {
			tr.BytesNote = "VictoriaMetrics has no tempo_distributor_bytes_received_total (Tempo's own metrics are not scraped there)"
		}
	}
	if scoped && tr.SpansAvailable {
		tr.SpansEstimate = o.Estimates.Traces.SpansPerMin
		tr.SpansRatio = ratio(tr.SpansPerMin, tr.SpansEstimate)
	}
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
	fmt.Fprintf(w, "\nMETRICS (%d on the target, sorted by the larger of series and points/min against the contract)\n", r.TotalMetrics)
	fmt.Fprintf(w, "  %-40s %7s %6s %7s | %9s %9s %9s  %s\n", "metric", "series", "worst", "budget", "points/min", "worst", "budget", "status")
	for _, m := range r.Metrics {
		worst, budget, wdpm, bdpm := "-", "-", "-", "-"
		if m.Status != StatusNotInContract && m.Status != StatusPlatform {
			worst, wdpm = strconv.FormatInt(m.Worst, 10), num(m.WorstDPM)
			if m.Budget > 0 {
				budget, bdpm = strconv.Itoa(m.Budget), num(m.BudgetDPM)
			}
		}
		fmt.Fprintf(w, "  %-40s %7d %6s %7s | %9s %9s %9s  %s\n", m.Metric, m.Series, worst, budget, num(m.DPM), wdpm, bdpm, m.Status)
		if m.Why != "" {
			fmt.Fprintf(w, "    -> %s\n", m.Why)
		}
		if m.DPMNote != "" {
			fmt.Fprintf(w, "    -> %s\n", m.DPMNote)
		}
		if len(m.Landscapes) > 0 {
			var parts []string
			for _, l := range m.Landscapes {
				parts = append(parts, fmt.Sprintf("%s: %d series, %s points/min", l.Landscape, l.Series, num(l.DPM)))
			}
			fmt.Fprintf(w, "    per landscape (each is compared on its own; the row shows the fullest): %s\n", strings.Join(parts, "; "))
		}
	}
	if len(r.Metrics) == 0 {
		fmt.Fprintln(w, "  (no rows: nothing matched, or --over-only / --unknown ignore hid them)")
	}
	if len(r.Findings) > 0 {
		fmt.Fprintf(w, "\nFINDINGS (the label checks of obs assert)\n")
		for _, f := range r.Findings {
			fmt.Fprintf(w, "  %-4s metric %s: [%s] %s\n", f.Level, f.Metric, f.Rule, f.Msg)
		}
	}
	l := r.Logs
	fmt.Fprintf(w, "\nLOGS\n")
	if !l.Available {
		fmt.Fprintf(w, "  not read: %s\n", l.Note)
	} else {
		fmt.Fprintf(w, "  lines/min: %s (%s)\n", num(l.LinesPerMin), vsCompared(l.Compared, l.LinesPerMin, l.LinesEstimate, l.LinesRatio))
		fmt.Fprintf(w, "  message bytes/min: %s (%s)\n", num(l.BytesPerMin), vsCompared(l.Compared, l.BytesPerMin, l.BytesEstimate, l.BytesRatio))
		st := "within the contract's worst case"
		switch {
		case !l.Compared:
			st = "not compared"
		case l.StreamsOver:
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
		fmt.Fprintf(w, "  spans/min: %s (%s); source: %s\n", num(tr.SpansPerMin), vsCompared(tr.SpansEstimate > 0 || tr.SpansRatio > 0, tr.SpansPerMin, tr.SpansEstimate, tr.SpansRatio), tr.SpansSource)
	} else {
		fmt.Fprintf(w, "  spans/min: not available: %s\n", tr.SpansNote)
	}
	if tr.BytesAvailable {
		fmt.Fprintf(w, "  bytes/min: %s (whole target: not compared with an estimate); source: %s\n", num(tr.BytesPerMin), tr.BytesSource)
	} else {
		fmt.Fprintf(w, "  bytes/min: not available: %s\n", tr.BytesNote)
	}
	fmt.Fprintf(w, "\nobs live usage: %s\n", strings.TrimSpace(r.Reason))
}

func vsCompared(compared bool, v, est, ratio float64) string {
	if !compared {
		return "not compared with an estimate"
	}
	return vs(v, est, ratio)
}
