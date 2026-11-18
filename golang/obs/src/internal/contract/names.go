package contract

import (
	"sort"
	"strings"
)

// Under is the underscore form of a name: dots become underscores. VictoriaMetrics STORES a label
// with its dots (concept 2.1: `{"durian.cloud.service"="checkout"}`; a scraped `durian_cloud_*`
// label is turned back into dots by the vminsert labelmap), so Under is never a stored spelling.
// It only recognises the wrong spelling of a dotted label, to name the right one.
func Under(s string) string { return strings.ReplaceAll(s, ".", "_") }

// Dotted returns the stored label of set that written is the underscore form of ("durian_cloud_service"
// for the stored "durian.cloud.service"), or "" when written is a stored spelling already or matches none.
func Dotted(written string, set map[string]bool) string {
	if set[written] {
		return ""
	}
	best := ""
	for k := range set {
		if k != written && strings.Contains(k, ".") && Under(k) == written && (best == "" || k < best) {
			best = k
		}
	}
	return best
}

// PlatformLabels are the labels every signal carries without the contract (concept section 2),
// in their dotted spelling, plus the labels of histogram series and of the standard metrics.
var PlatformLabels = []string{
	"durian.cloud.landscape", "durian.cloud.platform", "durian.cloud.service", "durian.cloud.module",
	"service.name", "service.namespace", "deployment.environment.name",
	"k8s.pod.uid", "k8s.pod.name", "k8s.container.name", "k8s.node.name", "k8s.namespace.name",
	"service.instance.id",
}

// QueryLabels are labels that exist on query results rather than on stored series.
var QueryLabels = []string{"le", "quantile", "vmrange", "__name__"}

// PlatformLabelSet is PlatformLabels (stored, dotted spelling) and QueryLabels, without the pod keys: a business
// metric has no pod key in the store (concept 4.1), so only per_pod and platform metrics add them.
func PlatformLabelSet() map[string]bool {
	m := map[string]bool{}
	for _, l := range PlatformLabels {
		if !in(DefaultPodKeys, l) {
			m[l] = true
		}
	}
	for _, l := range QueryLabels {
		m[l] = true
	}
	return m
}

// StoredMetric is one stored series name a query may read, and what the contract says of it.
type StoredMetric struct {
	Name     string // the contract metric name, or the platform metric key
	Metric   Metric
	Platform bool
	Labels   map[string]bool // the label names it may carry, in their stored spelling (contract labels + platform labels)
}

// Lookup resolves a stored series name (as a query writes it) to the contract metric behind it.
func (c *Contract) Lookup(stored string) (StoredMetric, bool) {
	for _, key := range c.MetricOrder {
		m := c.Metrics[key]
		n := m.NameOr(key)
		for _, s := range StoredNames(n, m) {
			if s == stored {
				return StoredMetric{Name: n, Metric: m, Labels: c.metricLabels(m)}, true
			}
		}
	}
	for _, k := range c.platformKeys() {
		for _, name := range platformStored(k) {
			if stored == name {
				ls := map[string]bool{}
				for l := range PlatformLabelSet() {
					ls[l] = true
				}
				for _, k := range c.EffectivePodKeys() {
					ls[k] = true // a platform metric is in no aggregation match, so it keeps its pod keys
				}
				for _, l := range c.Platform[k] {
					ls[l] = true
				}
				return StoredMetric{Name: k, Platform: true, Labels: ls}, true
			}
		}
	}
	return StoredMetric{}, false
}

func (c *Contract) platformKeys() []string {
	out := make([]string, 0, len(c.Platform))
	for k := range c.Platform {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (c *Contract) metricLabels(m Metric) map[string]bool {
	ls := PlatformLabelSet()
	for _, l := range m.LabelOrder {
		ls[l] = true
	}
	if m.PerPod || m.IsSource() {
		// a source: metric is stored as scraped, with the pod keys it carries (it is in no aggregation)
		for _, k := range c.EffectivePodKeys() {
			ls[k] = true
		}
	}
	return ls
}

// StoredNameList lists every stored series name a query may read, for "did you mean" hints.
func (c *Contract) StoredNameList() []string {
	var out []string
	for _, key := range c.MetricOrder {
		m := c.Metrics[key]
		out = append(out, StoredNames(m.NameOr(key), m)...)
	}
	for _, k := range c.platformKeys() {
		out = append(out, StoredBase(k))
	}
	return out
}

// LabelType returns the type of a contract label of metric m (by its stored spelling).
func (c *Contract) LabelType(m Metric, label string) (Type, bool) {
	for _, l := range m.LabelOrder {
		if l == label {
			t, ok := c.Types[m.Labels[l]]
			return t, ok
		}
	}
	return Type{}, false
}

// AllLabels is the union of every label any metric may carry (stored spelling): for group-by
// modifiers, which the expression tree does not tie to one selector.
func (c *Contract) AllLabels() map[string]bool {
	ls := PlatformLabelSet()
	for _, n := range c.MetricOrder {
		for _, l := range c.Metrics[n].LabelOrder {
			ls[l] = true
		}
	}
	for _, k := range c.platformKeys() {
		for _, l := range c.Platform[k] {
			ls[l] = true
		}
	}
	return ls
}

// HasSpanAttribute reports whether a TraceQL attribute is known: scope "span" (or "") means a
// span attribute some contract span declares; "resource" means a platform label; any other
// scope (intrinsics, event, link) is not the contract's to judge, so it is true. The TraceQL
// checker takes this through its SpanAttributeNames interface.
func (c *Contract) HasSpanAttribute(scope, name string) bool {
	switch scope {
	case "resource":
		return c.resourceHas(name)
	case "span":
		return c.spanHas(name)
	case "":
		// an unscoped attribute (.name) matches a span attribute OR a resource attribute
		return c.spanHas(name) || c.resourceHas(name)
	}
	return true
}

// resourceHas: a traced resource carries every platform label of concept section 2, the pod keys
// included (a trace has its pod name; only business METRICS drop it, concept 4.1).
func (c *Contract) resourceHas(name string) bool {
	for _, l := range PlatformLabels {
		if l == name {
			return true
		}
	}
	return PlatformLabelSet()[name] || c.platformHas(name)
}

// DottedSpanAttribute returns the declared (dotted) spelling a TraceQL attribute name is the underscore
// form of, or "". Tempo keeps attribute names as sent, dots included (concept 2.1), so the underscore
// form selects nothing; the checker names the right one.
func (c *Contract) DottedSpanAttribute(scope, name string) string {
	if !strings.Contains(name, "_") {
		return ""
	}
	var cands []string
	if scope == "resource" || scope == "" {
		cands = append(cands, PlatformLabels...)
		for _, ls := range c.Platform {
			cands = append(cands, ls...)
		}
	}
	if scope == "span" || scope == "" {
		for _, n := range c.SpanOrder {
			cands = append(cands, c.Spans[n].AttrOrder...)
		}
	}
	best := ""
	for _, k := range cands {
		if k != name && strings.Contains(k, ".") && Under(k) == name && (best == "" || k < best) {
			best = k
		}
	}
	return best
}

func (c *Contract) spanHas(name string) bool {
	for _, n := range c.SpanOrder {
		for _, a := range c.Spans[n].AttrOrder {
			if a == name {
				return true
			}
		}
	}
	return false
}

func (c *Contract) platformHas(name string) bool {
	for _, ls := range c.Platform {
		for _, l := range ls {
			if l == name {
				return true
			}
		}
	}
	return false
}

// StreamFields is the set of VictoriaLogs stream fields: only the bounded where-from set (concept 3.3);
// a log event declares none of its own.
func (c *Contract) StreamFields() map[string]bool {
	m := map[string]bool{}
	for _, l := range PlatformLabels {
		switch l {
		case "k8s.pod.uid", "k8s.pod.name", "k8s.container.name", "k8s.node.name", "service.instance.id":
			continue
		}
		m[l] = true
	}
	return m
}

// Suggest returns the closest candidate within an edit distance of 3 (a "did you mean" hint), or "".
func Suggest(word string, candidates []string) string {
	best, bd := "", 4
	for _, c := range candidates {
		if d := lev(word, c); d < bd {
			best, bd = c, d
		}
	}
	return best
}

func lev(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
