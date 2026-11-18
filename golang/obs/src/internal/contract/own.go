package contract

import (
	"sort"
	"strings"
)

// Own is the contract without the entries somebody else makes: no source: metric, no estimate
// type. The code generators and the aggregation file only ever see this view, so obs gen makes
// no type for a source: entry. The hash and the file stay the contract's. The original is not changed.
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
	for _, n := range c.TypeOrder {
		if t := c.Types[n]; t.Kind != KindEstimate {
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

// platformStored is the stored names of a platform metric. OTel names every duration metric in
// seconds and as a histogram (http.server.request.duration), so that one is exact. For any other
// platform metric the kind is not in the contract, so every name the metric can be stored under is
// listed: a name kept that nobody sends costs nothing, a name dropped loses data.
func platformStored(key string) []string {
	base := StoredBase(key)
	if strings.HasSuffix(base, "_duration") {
		base += "_seconds"
		return []string{base + "_bucket", base + "_count", base + "_sum"}
	}
	var out []string
	for _, unit := range []string{"", "_seconds", "_bytes"} {
		b := base
		if unit != "" && !strings.HasSuffix(b, unit) {
			b += unit
		}
		out = append(out, b, b+"_total", b+"_bucket", b+"_count", b+"_sum")
	}
	return out
}
