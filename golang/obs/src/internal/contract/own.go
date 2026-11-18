package contract

import (
	"sort"
	"strings"
)

// Own is the contract without the entries somebody else makes: no source: metric, no estimate
// type and no type only source: metrics use (its values are what a scraper happens to send, an
// empty one included, and would become names in code). A type an own metric, a log stream label or
// a span attribute uses stays, and so does a type nobody uses. The code generators and the
// aggregation file only ever see this view, so obs gen makes no type for a source: entry. The hash
// and the file stay the contract's. The original is not changed.
func (c *Contract) Own() *Contract {
	o := *c
	o.Metrics = map[string]Metric{}
	o.MetricOrder = nil
	for _, n := range c.MetricOrder {
		if m := c.Metrics[n]; !m.IsSource() {
			o.Metrics[n] = m
			o.MetricOrder = append(o.MetricOrder, n)
		}
	}
	o.Types = map[string]Type{}
	o.TypeOrder = nil
	sourceOnly := c.sourceOnlyTypes()
	for _, n := range c.TypeOrder {
		if t := c.Types[n]; t.Kind != KindEstimate && !sourceOnly[n] {
			o.Types[n] = t
			o.TypeOrder = append(o.TypeOrder, n)
		}
	}
	return &o
}

// KeepNames is every series name the collector must store for this contract, as VictoriaMetrics
// stores it (after the OTel to Prometheus name change: unit suffix, _total, histogram _bucket,
// _sum and _count): our metrics, the source: metrics and the platform metrics. Sorted, unique, plain names.
func (c *Contract) KeepNames() []string {
	set := map[string]bool{}
	for _, n := range c.MetricOrder {
		for _, s := range StoredNames(n, c.Metrics[n]) {
			set[s] = true
		}
	}
	for _, k := range c.platformKeys() {
		for _, s := range platformStored(k) {
			set[s] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// platformUnits are the unit words a platform metric may carry in its stored name: none, seconds
// (OTel durations), bytes, ratio (unit 1 gauges such as process.cpu.utilization) and milliseconds
// (old-semconv durations such as http.server.duration, which are in ms).
var platformUnits = []string{"", "_seconds", "_bytes", "_ratio", "_milliseconds"}

// platformStored is every stored name a platform metric may have. The kind and unit of a platform
// metric are not in the contract, so this is a SUPERSET, not a prediction: for each unit in
// platformUnits (a key ending in _duration: only seconds and milliseconds), added unless the key
// already ends in it, the plain name, _total and the histogram parts _bucket, _sum and _count. A name kept that nobody sends costs nothing, a name
// dropped loses data. Keep-list, name lookup and the clash check all read this one list.
func platformStored(key string) []string {
	base := StoredBase(key)
	units := platformUnits
	if strings.HasSuffix(base, "_duration") {
		units = []string{"", "_seconds", "_milliseconds"} // a duration is in time, never bytes or a ratio
	}
	var out []string
	for _, unit := range units {
		b := base
		if unit != "" && !strings.HasSuffix(b, unit) {
			b += unit
		}
		out = append(out, b, b+"_total", b+"_bucket", b+"_count", b+"_sum")
	}
	return out
}
