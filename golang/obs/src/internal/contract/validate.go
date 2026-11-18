package contract

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Issue is one contract failure: where in the file, which rule, and the bad token.
type Issue struct {
	Path string // YAML path, e.g. metrics.checkout.charges.labels.user_id
	Rule string // e.g. free-metric-label
	Msg  string
}

func (i Issue) String() string { return fmt.Sprintf("%s: [%s] %s", i.Path, i.Rule, i.Msg) }

var (
	nameRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`)
	identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// An event is a dotted name (checkout.charge.failed); a log key is any identifier-ish name.
	keyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// Units a metric may state: none, or one that adds its suffix to the stored name (concept 11.2).
var Units = []string{"", "s", "seconds", "By", "bytes"}

// Known languages (the registry in internal/gen decides which have a generator).
var Languages = []string{"go", "dotnet", "dart", "bun"}

// MaxWorst is the cap used when a product overflows.
const MaxWorst = int64(1) << 53

// Validate applies the schema rules and the failures of concept section 11.4:
// free on a metric label, free on a stream label, a calendar date without window_days,
// over budget, a gauge with neither across_pods nor per_pod. Issues are in a stable order.
func (c *Contract) Validate() []Issue {
	var is []Issue
	add := func(path, rule, f string, a ...any) { is = append(is, Issue{path, rule, fmt.Sprintf(f, a...)}) }

	if c.Service == "" {
		add("service", "service-missing", "service: the contract must name its service")
	} else if !nameRe.MatchString(c.Service) && !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(c.Service) {
		add("service", "service-name", "service %q: use lowercase letters, digits, - or _", c.Service)
	}
	if c.Interval != "" && !regexp.MustCompile(`^[0-9]+(s|m)$`).MatchString(c.Interval) {
		add("interval", "interval", "interval %q: want a duration such as 30s", c.Interval)
	}

	// A type only source: metrics use may list an empty value: a scraped label really is empty
	// (a pod with no priority class). An own type may not, it becomes a name in generated code.
	ownUse := c.ownTypeUse()

	// ---- types
	for _, n := range c.TypeOrder {
		t := c.Types[n]
		p := "types." + n
		if !identRe.MatchString(n) {
			add(p, "type-name", "type name %q: use lowercase letters, digits and _", n)
		}
		switch t.Kind {
		case KindEnum:
			if len(t.Values) == 0 {
				add(p, "enum-empty", "enum %q lists no values", n)
			}
			seen := map[string]bool{}
			for _, v := range t.Values {
				if v == "" && ownUse[n] {
					add(p, "enum-value", "enum %q has an empty value", n)
				}
				if seen[v] {
					add(p, "enum-duplicate", "enum %q lists value %q twice", n, v)
				}
				seen[v] = true
			}
		case KindInt:
			switch {
			case t.Min == nil || t.Max == nil:
				add(p, "int-bounds", "bounded number %q needs both min and max", n)
			case *t.Min > *t.Max:
				add(p, "int-bounds", "bounded number %q: min %d is above max %d", n, *t.Min, *t.Max)
			}
		case KindTime:
			if t.Unit != UnitHourOfDay && t.Unit != UnitWeekday {
				add(p, "time-unit", "time bucket %q: unit %q must be hour_of_day or weekday", n, t.Unit)
			}
		case KindDate:
			if t.WindowDays <= 0 {
				add(p, "date-no-window", "calendar date %q has no window_days: a date label makes a new set of series every day, so the contract must say how many values are live at once (window_days: 365)", n)
			}
		case KindFree:
			// allowed on log fields and span attributes only; use sites are checked below
		case KindEstimate:
			// allowed on source: metrics only; use sites are checked below
			switch {
			case t.Min == nil || t.Max == nil || t.ChurnPerDay == nil:
				add(p, "estimate-bounds", "estimate %q needs min, max and churn_per_day", n)
			case *t.Min < 0 || *t.Min > *t.Max:
				add(p, "estimate-bounds", "estimate %q: min %d must be 0 or more and not above max %d", n, *t.Min, *t.Max)
			case *t.Max < 1:
				add(p, "estimate-bounds", "estimate %q: max %d must be at least 1", n, *t.Max)
			case *t.ChurnPerDay < 0:
				add(p, "estimate-bounds", "estimate %q: churn_per_day %d must be 0 or more", n, *t.ChurnPerDay)
			}
		case "":
			add(p, "type-kind", "type %q has no kind (enum, int, time, date, free or estimate)", n)
		default:
			add(p, "type-kind", "type %q: unknown kind %q (enum, int, time, date, free or estimate)", n, t.Kind)
		}
	}

	resolve := func(path, label, typ string) (Type, bool) {
		t, ok := c.Types[typ]
		if !ok {
			add(path, "label-type", "label %q uses type %q, which is not declared under types:", label, typ)
		}
		return t, ok
	}

	// ---- metrics
	seenName := map[string]string{}
	seenStored := map[string]string{} // every stored series name -> the metric that owns it
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		p := "metrics." + n
		if !nameRe.MatchString(n) {
			add(p, "metric-name", "metric name %q: use lowercase dotted or underscored names (checkout.charges)", n)
		}
		stored := StoredBase(n)
		if prev, dup := seenName[stored]; dup {
			add(p, "metric-duplicate", "metric %q and %q are stored under the same name %q", n, prev, stored)
		}
		seenName[stored] = n
		// The stored names themselves must not intersect: a counter requests (requests_total) and a gauge
		// requests_total would share a series name, and a lookup or an aggregation match could not tell them apart.
		for _, sn := range StoredNames(n, m) {
			if prev, dup := seenStored[sn]; dup && prev != n {
				add(p, "metric-duplicate", "metric %q and %q are both stored as the series %q", n, prev, sn)
			} else if !dup {
				seenStored[sn] = n
			}
		}
		if !in(Units, m.Unit) {
			add(p, "metric-unit", "metric %q: unit %q is not one the stored name supports (%s); other units would store a name no OTLP to VictoriaMetrics path produces", n, m.Unit, strings.Join(Units[1:], ", "))
		}
		switch m.Kind {
		case MetricCounter, MetricHistogram, MetricGauge:
		case "":
			add(p, "metric-kind", "metric %q has no kind (counter, histogram or gauge)", n)
		default:
			add(p, "metric-kind", "metric %q: unknown kind %q (counter, histogram or gauge)", n, m.Kind)
		}
		if m.Kind == MetricGauge && !m.IsSource() {
			switch {
			case m.AcrossPod == "" && !m.PerPod:
				add(p, "gauge-pods", "gauge %q has neither across_pods (sum, avg, max, last) nor per_pod: true", n)
			case m.AcrossPod != "" && m.PerPod:
				add(p, "gauge-pods", "gauge %q has both across_pods and per_pod: pick one", n)
			}
		}
		if m.AcrossPod != "" {
			switch m.AcrossPod {
			case "sum", "avg", "max", "last":
			default:
				add(p, "across-pods", "metric %q: across_pods %q must be sum, avg, max or last", n, m.AcrossPod)
			}
			if m.Kind != MetricGauge {
				add(p, "across-pods", "metric %q: across_pods is for gauges; a %s is summed across pods by the aggregation", n, orNone(m.Kind))
			}
		}
		if m.Buckets < 0 || (m.Buckets != 0 && m.Kind != MetricHistogram) {
			add(p, "buckets", "metric %q: buckets is a histogram setting (bucket count with +Inf)", n)
		}
		seenUser := map[string]bool{}
		for _, u := range m.UsedBy {
			if strings.TrimSpace(u) == "" {
				add(p+".used_by", "used-by", "metric %q has an empty used_by entry: say who reads it (box/alert/pod-crashloop)", n)
			} else if seenUser[u] {
				add(p+".used_by", "used-by", "metric %q lists %q twice in used_by", n, u)
			}
			seenUser[u] = true
		}
		for _, l := range m.LabelOrder {
			lp := p + ".labels." + l
			if !nameRe.MatchString(l) {
				add(lp, "label-name", "label name %q: use lowercase letters, digits, _ and .", l)
			}
			if target, follows := m.Follows[l]; follows {
				if !m.IsSource() {
					add(lp, "follows-not-source", "%s: metric %q label %q follows %q: follows is only for a source: metric (somebody else's labels); an own metric label is a bounded type of its own", c.at(m.LabelLine[l]), n, l, target)
				}
				if msg := c.followsProblem(m, l); msg != "" {
					add(lp, "follows", "%s: metric %q label %q: %s", c.at(m.LabelLine[l]), n, l, msg)
				}
				continue
			}
			t, ok := resolve(lp, l, m.Labels[l])
			if !ok {
				continue
			}
			if t.Kind == KindEstimate && !m.IsSource() {
				add(lp, "estimate-not-source", "%s: metric %q label %q has type %q = estimate: an estimate is a range somebody else decides, so it is allowed only on a metric with source: (our own labels are bounded types: enum, int, time, date)", c.at(m.LabelLine[l]), n, l, m.Labels[l])
			}
			if t.Kind == KindFree {
				add(lp, "free-metric-label", "metric %q label %q has type %q = free: a free value as a metric label makes unbounded series; use enum, int or time", n, l, m.Labels[l])
			}
		}
		w, ok := c.WorstCase(n)
		if len(m.LabelOrder) > 0 && m.Budget <= 0 {
			add(p, "budget-missing", "metric %q has labels but no budget: every metric keeps a budget (worst case %d series)", n, w)
		} else if ok && m.Budget > 0 && (w > int64(m.Budget) || w >= MaxWorst) {
			add(p, "over-budget", "metric %q worst case is %s series, over its budget of %d", n, worstText(w), m.Budget)
		}
	}

	// ---- logs
	seenEv := map[string]string{}
	for _, k := range c.LogOrder {
		e := c.Logs[k]
		p := "logs." + k
		if !keyRe.MatchString(k) {
			add(p, "log-key", "log key %q: use letters, digits and _ (ChargeFailed)", k)
		}
		if !nameRe.MatchString(e.Event) {
			add(p, "event-name", "log %q: event %q must be a lowercase dotted name (checkout.charge.failed)", k, e.Event)
		}
		if prev, dup := seenEv[e.Event]; dup {
			add(p, "event-duplicate", "log %q and %q both name event %q", k, prev, e.Event)
		}
		seenEv[e.Event] = k
		if !in(Levels, e.Level) {
			add(p, "event-level", "log %q: level %q must be one of %s", k, e.Level, strings.Join(Levels, ", "))
		}
		sl := make([]string, 0, len(e.StreamLabels))
		for l := range e.StreamLabels {
			sl = append(sl, l)
		}
		sort.Strings(sl)
		for _, l := range sl {
			lp := p + ".stream_labels." + l
			t, ok := resolve(lp, l, e.StreamLabels[l])
			if !ok {
				continue
			}
			switch t.Kind {
			case KindFree:
				add(lp, "free-stream-label", "log %q stream label %q has type %q = free: a stream field must be bounded (one stream per value)", k, l, e.StreamLabels[l])
			case KindEstimate:
				add(lp, "estimate-not-source", "%s: log %q stream label %q has type %q = estimate: an estimate is allowed only on a metric with source:", c.File, k, l, e.StreamLabels[l])
			case KindDate:
				add(lp, "date-stream-label", "log %q stream label %q has type %q = date: a calendar date is never a VictoriaLogs stream field", k, l, e.StreamLabels[l])
			}
			add(lp, "stream-label-declared", "log %q declares stream label %q: the VictoriaLogs stream fields are only the bounded where-from set (concept 3.3: durian.cloud.landscape, .platform, .service, .module, service.name, service.namespace, deployment.environment.name, k8s.namespace.name); a log event adds none, so keep %q an ordinary log field", k, l, l)
		}
	}

	// ---- spans
	for _, n := range c.SpanOrder {
		s := c.Spans[n]
		p := "spans." + n
		if !nameRe.MatchString(n) {
			add(p, "span-name", "span name %q: use lowercase dotted names (checkout.charge)", n)
		}
		if s.Kind != "" && !in(SpanKinds, s.Kind) {
			add(p, "span-kind", "span %q: kind %q must be one of %s", n, s.Kind, strings.Join(SpanKinds, ", "))
		}
		for _, a := range s.AttrOrder {
			ap := p + ".attributes." + a
			if a == "" {
				add(ap, "attr-name", "span %q has an empty attribute name", n)
			}
			if t, ok := resolve(ap, a, s.Attributes[a]); ok && t.Kind == KindEstimate {
				add(ap, "estimate-not-source", "%s: span %q attribute %q has type %q = estimate: an estimate is allowed only on a metric with source:", c.File, n, a, s.Attributes[a])
			}
		}
	}

	// ---- cross-kind name uniqueness: a span and a metric may share a dotted name, a metric and
	// another metric may not (checked above); events are unique among events.

	// ---- generate targets
	for i, t := range c.Generate {
		p := fmt.Sprintf("generate[%d]", i)
		if !in(Languages, t.Lang) {
			add(p, "generate-lang", "generate: language %q must be one of %s", t.Lang, strings.Join(Languages, ", "))
		}
		if t.Out == "" {
			add(p, "generate-out", "generate: %s needs out: (a folder, e.g. internal/signals/generated)", t.Lang)
		}
	}
	return is
}

func worstText(w int64) string {
	if w >= MaxWorst {
		return "more than 2^53"
	}
	return fmt.Sprint(w)
}

func in(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

func orNone(s string) string {
	if s == "" {
		return "metric"
	}
	return s
}

// WorstCase is the exact worst-case series count of one metric per landscape, before the pod
// multiplier: the product of its label sizes, times buckets + _sum + _count for a histogram
// (14 for the default 12 buckets). ok is false when a label type is unknown or free.
func (c *Contract) WorstCase(name string) (int64, bool) {
	m, found := c.Metrics[name]
	if !found {
		return 0, false
	}
	w := int64(1)
	ok := true
	for _, l := range m.LabelOrder {
		if _, follows := m.Follows[l]; follows {
			continue // adds no series: it changes together with the label it follows
		}
		t, has := c.Types[m.Labels[l]]
		if !has || t.Kind == KindFree || t.Size() <= 0 {
			ok = false
			continue
		}
		w = satMul(w, int64(t.Size()))
	}
	return satMul(w, histogramFactor(m)), ok
}

// histogramFactor is buckets + _sum + _count for a histogram (14 for the default 12), else 1.
func histogramFactor(m Metric) int64 {
	if m.Kind != MetricHistogram {
		return 1
	}
	b := m.Buckets
	if b == 0 {
		b = DefaultBuckets
	}
	return satAdd(int64(b), 2)
}

// ChurnPerDay is the approximate number of NEW series a day a source metric starts: for each
// estimate label, its churn_per_day times the series each value carries (the other labels' sizes,
// times the histogram parts). Check prints it; it is not part of the budget, which is the series
// stored at once (the largest the estimates reach), as the concept's budget is. Zero for a metric
// with no churning estimate.
func (c *Contract) ChurnPerDay(name string) int64 {
	m, found := c.Metrics[name]
	if !found {
		return 0
	}
	total := int64(0)
	for _, l := range m.LabelOrder {
		if _, follows := m.Follows[l]; follows {
			continue
		}
		t, has := c.Types[m.Labels[l]]
		if !has || t.Kind != KindEstimate || t.ChurnPerDay == nil || *t.ChurnPerDay <= 0 {
			continue
		}
		per := histogramFactor(m)
		for _, o := range m.LabelOrder {
			if _, f := m.Follows[o]; f || o == l {
				continue
			}
			if ot, ok := c.Types[m.Labels[o]]; ok && ot.Size() > 0 {
				per = satMul(per, int64(ot.Size()))
			}
		}
		total = satAdd(total, satMul(per, int64(*t.ChurnPerDay)))
	}
	return total
}

// ownTypeUse is the types some metric without source:, a log stream label or a span attribute uses.
func (c *Contract) ownTypeUse() map[string]bool {
	use := map[string]bool{}
	for _, m := range c.Metrics {
		if m.IsSource() {
			continue
		}
		for _, t := range m.Labels {
			use[t] = true
		}
	}
	for _, s := range c.Spans {
		for _, t := range s.Attributes {
			use[t] = true
		}
	}
	for _, e := range c.Logs {
		for _, t := range e.StreamLabels {
			use[t] = true
		}
	}
	return use
}

// at is "file:line", or just the file when the line is unknown.
func (c *Contract) at(line int) string {
	if line <= 0 {
		return c.File
	}
	return fmt.Sprintf("%s:%d", c.File, line)
}

// followsProblem says why label l of m can not follow its target ("" when it can).
func (c *Contract) followsProblem(m Metric, l string) string {
	seen := map[string]bool{l: true}
	cur := l
	for {
		next, follows := m.Follows[cur]
		if !follows {
			return ""
		}
		if _, has := m.Labels[next]; !has {
			return fmt.Sprintf("follows %q, which is not a label of this metric", next)
		}
		if seen[next] {
			return fmt.Sprintf("follows %q: a cycle (labels that follow each other add no series for anyone)", next)
		}
		seen[next] = true
		cur = next
	}
}

// Warnings are the contract problems that do not fail a check: a source: entry that does not say who uses it.
func (c *Contract) Warnings() []Issue {
	var out []Issue
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		if m.IsSource() && len(m.UsedBy) == 0 {
			out = append(out, Issue{"metrics." + n, "used-by-missing", fmt.Sprintf("%s: source metric %q does not say who uses it (used_by: [box/alert/<name>, box/dash/<name>])", c.at(m.Line), n)})
		}
	}
	return out
}

// satAdd adds and saturates at MaxWorst, so b+2 can not wrap before it is multiplied.
func satAdd(a, b int64) int64 {
	if a >= MaxWorst || b >= MaxWorst || a > MaxWorst-b {
		return MaxWorst
	}
	return a + b
}

// satMul multiplies and saturates at MaxWorst, so a product that does not fit is still over any budget.
func satMul(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a >= MaxWorst || b >= MaxWorst || a > MaxWorst/b {
		return MaxWorst
	}
	return a * b
}

// StreamCount is the worst-case number of VictoriaLogs streams: services x landscapes x
// platforms x modules (the four durian.cloud keys). Log events add no streams of their own
// (concept 11.4), so for this one service it is landscapes x modules.
func (c *Contract) StreamCount(landscapes int) int64 {
	if landscapes < 1 {
		landscapes = 1
	}
	mods := len(c.Modules)
	if mods < 1 {
		mods = 1
	}
	return int64(landscapes) * int64(mods)
}

// StoredBase is the name VictoriaMetrics stores for a metric before the kind suffix: dots become _.
func StoredBase(name string) string { return strings.ReplaceAll(name, ".", "_") }

// StoredNames returns the series names a metric is stored under (concept 11.2): counters get
// _total, a unit adds its suffix, histograms store _bucket, _sum and _count.
func StoredNames(name string, m Metric) []string {
	base := StoredBase(name)
	if m.IsSource() {
		// A source name is the stored name already (it was scraped, not converted from OTel): a counter
		// keeps the name it has, a histogram has its three parts.
		if m.Kind == MetricHistogram {
			return []string{base + "_bucket", base + "_sum", base + "_count"}
		}
		return []string{base}
	}
	switch m.Unit {
	case "":
	case "s", "seconds":
		if !strings.HasSuffix(base, "_seconds") {
			base += "_seconds"
		}
	case "By", "bytes":
		if !strings.HasSuffix(base, "_bytes") {
			base += "_bytes"
		}
	}
	switch m.Kind {
	case MetricCounter:
		if strings.HasSuffix(base, "_total") {
			return []string{base}
		}
		return []string{base + "_total"}
	case MetricHistogram:
		return []string{base + "_bucket", base + "_sum", base + "_count"}
	}
	return []string{base}
}
