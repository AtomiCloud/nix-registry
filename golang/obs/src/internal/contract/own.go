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

// platformUnits are the unit words a platform metric may carry in its stored name: none, every word of
// VictoriaMetrics' unit table (vmUnitWords: seconds, milliseconds, bytes, hertz, percent, celsius, ...,
// generated from the pinned converter) and ratio (the token it adds to a unit 1 gauge such as
// process.cpu.utilization). Not covered: a unit outside that table (VictoriaMetrics appends it as written)
// and a rate unit such as By/s (it appends _per_second).
var platformUnits = func() []string {
	out := []string{"", "_ratio"}
	for _, w := range vmUnitWords {
		out = append(out, "_"+w)
	}
	return out
}()

// timeUnitSuffixes are the unit words of vmUnitWords that measure time. TestDurationUnitsAreTimeWordsOfTheTable
// keeps each of them in the table. A platform key ending in _duration is stored with one of these or with none.
var timeUnitSuffixes = []string{"_days", "_hours", "_minutes", "_seconds", "_milliseconds", "_microseconds", "_nanoseconds"}

var durationUnits = append([]string{""}, timeUnitSuffixes...)

// platformStored is every stored name a platform metric may have. The kind and unit of a platform
// metric are not in the contract, so this is a SUPERSET, not a prediction: for each base name (below)
// and each unit in platformUnits (a key ending in _duration: only the time units, timeUnitSuffixes), added
// unless the name already ends in it, the plain name, _total and the histogram parts _bucket, _sum and _count. A name kept that nobody sends costs nothing, a name dropped loses data. Keep-list,
// name lookup and the clash check all read this one list.
//
// The base names are the key as written (dots to _), the key split into tokens the way
// VictoriaMetrics splits it (a double _ collapses; / - : and space split too), and each of those with
// a total or ratio token taken out of where it stands: VictoriaMetrics moves that token to the end,
// after the unit word it appends (jobs.total.done, a counter, is stored jobs_done_total; requests_total
// with unit s is stored requests_seconds_total; the unitless one stays requests_total, the key as written).
func platformStored(key string) []string {
	var out []string
	seen := map[string]bool{}
	for _, base := range platformBases(key) {
		units := platformUnits
		if strings.HasSuffix(base, "_duration") {
			units = durationUnits // a duration is in time, never bytes or a ratio
		}
		for _, unit := range units {
			b := base
			if unit != "" && !strings.HasSuffix(b, unit) {
				b += unit
			}
			for _, s := range []string{b, b + "_total", b + "_bucket", b + "_count", b + "_sum"} {
				if !seen[s] {
					seen[s] = true
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// platformBases are the base names platformStored builds on (see there); the key as written comes first.
func platformBases(key string) []string {
	toks := strings.FieldsFunc(key, func(r rune) bool { return strings.ContainsRune("/_.-: ", r) })
	bases := []string{StoredBase(key), strings.Join(toks, "_")}
	for _, word := range []string{"total", "ratio"} {
		for i, t := range toks {
			if t == word {
				rest := append(append([]string(nil), toks[:i]...), toks[i+1:]...)
				bases = append(bases, strings.Join(rest, "_"))
				break
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, b := range bases {
		if b != "" && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	return out
}
