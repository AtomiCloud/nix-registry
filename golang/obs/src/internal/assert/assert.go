package assert

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/AtomiCloud/obs/internal/contract"
)

// Options of Check.
type Options struct {
	// Pods is the pods per landscape: a per_pod metric may hold worst case x Pods series.
	Pods int
	// Unknown says what a metric that is neither in the contract nor in platform_metrics does:
	// "warn" (default), "fail" or "ignore".
	Unknown string
}

// Level of a finding.
type Level int

const (
	Fail Level = iota
	Warn
	Note
)

func (l Level) String() string { return [...]string{"FAIL", "warn", "note"}[l] }

// Finding names the metric, the rule and the bad token.
type Finding struct {
	Level  Level
	Metric string
	Rule   string
	Msg    string
}

// Count is the number of series of one contract metric in one landscape.
type Count struct {
	Metric    string
	Landscape string
	Series    int
	Worst     int64 // allowed: worst case, x pods for a per_pod metric
	Platform  bool  // a platform metric: counted, no worst case
	Stored    []string
}

// Result of Check.
type Result struct {
	Counts   []Count
	Findings []Finding
	Total    int
	Unknown  int
}

// Failures is the number of FAIL findings.
func (r *Result) Failures() int {
	n := 0
	for _, f := range r.Findings {
		if f.Level == Fail {
			n++
		}
	}
	return n
}

const landscapeLabel = "durian_cloud_landscape"

type group struct {
	metric    string
	landscape string
	sm        contract.StoredMetric
	series    []Series
	stored    map[string]bool
}

// Check counts the series per contract metric (per landscape when the series carry a
// durian.cloud.landscape label) and fails any count above the worst case. A histogram counts
// its _bucket, _sum and _count series together, like the worst case does.
func Check(c *contract.Contract, series []Series, o Options) *Result {
	if o.Pods < 1 {
		o.Pods = 1
	}
	if o.Unknown == "" {
		o.Unknown = "warn"
	}
	res := &Result{Total: len(series)}
	groups := map[string]*group{}
	var order []string
	unknown := map[string]int{}
	for _, s := range series {
		norm := normLabels(s.Labels)
		sm, ok := c.Lookup(s.Name)
		if !ok {
			unknown[s.Name]++
			continue
		}
		land := norm[landscapeLabel]
		k := sm.Name + "\x00" + land
		g := groups[k]
		if g == nil {
			g = &group{metric: sm.Name, landscape: land, sm: sm, stored: map[string]bool{}}
			groups[k] = g
			order = append(order, k)
		}
		g.series = append(g.series, Series{Name: s.Name, Labels: norm})
		g.stored[s.Name] = true
	}
	sort.Strings(order)
	for _, k := range order {
		g := groups[k]
		cnt := Count{Metric: g.metric, Landscape: g.landscape, Series: len(g.series), Platform: g.sm.Platform}
		for n := range g.stored {
			cnt.Stored = append(cnt.Stored, n)
		}
		sort.Strings(cnt.Stored)
		if !g.sm.Platform {
			w, _ := c.WorstCase(g.metric)
			if g.sm.Metric.PerPod {
				w *= int64(o.Pods)
			}
			cnt.Worst = w
			if int64(cnt.Series) > w {
				res.Findings = append(res.Findings, overFinding(c, g, cnt, o.Pods))
			}
			res.Findings = append(res.Findings, holes(c, g)...)
		}
		res.Counts = append(res.Counts, cnt)
	}
	names := make([]string, 0, len(unknown))
	for n, k := range unknown {
		names = append(names, n)
		res.Unknown += k
	}
	sort.Strings(names)
	if o.Unknown != "ignore" {
		lv := Warn
		if o.Unknown == "fail" {
			lv = Fail
		}
		for _, n := range names {
			res.Findings = append(res.Findings, Finding{Level: lv, Metric: n, Rule: "metric-not-in-contract",
				Msg: fmt.Sprintf("%d series of metric %q, which is neither a contract metric nor in platform_metrics: a raw OTel call?", unknown[n], n)})
		}
	}
	return res
}

func normLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[contract.Norm(k)] = v
	}
	return out
}

func overFinding(c *contract.Contract, g *group, cnt Count, pods int) Finding {
	where := ""
	if g.landscape != "" {
		where = fmt.Sprintf(" in landscape %q", g.landscape)
	}
	budget := ""
	if g.sm.Metric.Budget > 0 {
		budget = fmt.Sprintf(" (budget %d)", g.sm.Metric.Budget)
	}
	mult := ""
	if g.sm.Metric.PerPod {
		mult = fmt.Sprintf(" x %d pods", pods)
	}
	return Finding{Level: Fail, Metric: g.metric, Rule: "series-over-worst-case",
		Msg: fmt.Sprintf("%d series%s (%s), above the contract's worst case of %d%s%s: a hole around the generated call surface",
			cnt.Series, where, strings.Join(cnt.Stored, ", "), cnt.Worst, mult, budget)}
}

// holes checks every business series, regardless of count: undeclared labels, pod labels on a
// metric without per_pod, and label values outside the type.
func holes(c *contract.Contract, g *group) []Finding {
	var out []Finding
	allowed := g.sm.Labels
	podKey := map[string]bool{}
	for _, k := range c.EffectivePodKeys() {
		podKey[contract.Norm(k)] = true
	}
	values := map[string]map[string]bool{}
	for _, s := range g.series {
		for l, v := range s.Labels {
			if values[l] == nil {
				values[l] = map[string]bool{}
			}
			values[l][v] = true
		}
	}
	labels := make([]string, 0, len(values))
	for l := range values {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		vs := sortedKeys(values[l])
		switch {
		case podKey[l] && !g.sm.Metric.PerPod:
			out = append(out, Finding{Level: Fail, Metric: g.metric, Rule: "pod-label-on-business-metric",
				Msg: fmt.Sprintf("label %q (%d values) is a pod key and the metric is not per_pod: the aggregation must drop it", l, len(vs))})
		case !allowed[l]:
			out = append(out, Finding{Level: Fail, Metric: g.metric, Rule: "label-not-in-contract",
				Msg: fmt.Sprintf("label %q is not a label of this metric in the contract (%d values, e.g. %s)", l, len(vs), quoteN(vs, 3))})
		default:
			if t, ok := c.LabelType(g.sm.Metric, l); ok {
				if bad := outside(t, vs); len(bad) > 0 {
					out = append(out, Finding{Level: Fail, Metric: g.metric, Rule: "label-value-outside-type",
						Msg: fmt.Sprintf("label %q has %d value(s) outside its type (%s): %s", l, len(bad), describe(t), quoteN(bad, 5))})
				}
			}
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func quoteN(vs []string, n int) string {
	var q []string
	for i, v := range vs {
		if i == n {
			q = append(q, fmt.Sprintf("... %d more", len(vs)-n))
			break
		}
		q = append(q, strconv.Quote(v))
	}
	return strings.Join(q, ", ")
}

func describe(t contract.Type) string {
	switch t.Kind {
	case contract.KindEnum:
		return "enum " + strings.Join(t.Values, "|")
	case contract.KindInt:
		if t.Min != nil && t.Max != nil {
			return fmt.Sprintf("int %d..%d", *t.Min, *t.Max)
		}
	case contract.KindTime:
		return "time " + t.Unit
	}
	return t.Kind
}

// outside returns the values the type does not allow (enum, int and time types; others have no check).
func outside(t contract.Type, vs []string) []string {
	var bad []string
	for _, v := range vs {
		switch t.Kind {
		case contract.KindEnum:
			ok := false
			for _, a := range t.Values {
				if a == v {
					ok = true
				}
			}
			if !ok {
				bad = append(bad, v)
			}
		case contract.KindInt:
			n, err := strconv.Atoi(v)
			if err != nil || (t.Min != nil && n < *t.Min) || (t.Max != nil && n > *t.Max) {
				bad = append(bad, v)
			}
		case contract.KindTime:
			hi := 23
			if t.Unit == contract.UnitWeekday {
				hi = 6
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > hi {
				bad = append(bad, v)
			}
		}
	}
	return bad
}

// Print writes one line per metric, then every finding (failures first), and the summary line.
func (r *Result) Print(w io.Writer, file string) {
	for _, cnt := range r.Counts {
		land := ""
		if cnt.Landscape != "" {
			land = fmt.Sprintf(" [landscape %s]", cnt.Landscape)
		}
		if cnt.Platform {
			fmt.Fprintf(w, "metric %s%s: %d series (platform metric: no worst case in the contract)\n", cnt.Metric, land, cnt.Series)
			continue
		}
		status := "ok"
		if int64(cnt.Series) > cnt.Worst {
			status = "OVER"
		}
		fmt.Fprintf(w, "metric %s%s: %d series, worst case %d: %s\n", cnt.Metric, land, cnt.Series, cnt.Worst, status)
	}
	fs := append([]Finding{}, r.Findings...)
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].Level < fs[j].Level })
	for _, f := range fs {
		fmt.Fprintf(w, "%-4s %s metric %s: [%s] %s\n", f.Level, file, f.Metric, f.Rule, f.Msg)
	}
}

// Summary is the one-line result.
func (r *Result) Summary() string {
	over := 0
	for _, f := range r.Findings {
		if f.Rule == "series-over-worst-case" {
			over++
		}
	}
	res := "ok"
	if r.Failures() > 0 {
		res = "FAILED"
	}
	return fmt.Sprintf("obs assert: %s: %d series, %d metrics counted, %d over their worst case, %d failures", res, r.Total, len(r.Counts), over, r.Failures())
}
