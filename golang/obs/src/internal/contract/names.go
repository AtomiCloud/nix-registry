package contract

import (
	"sort"
	"strings"
)

// Norm is the spelling VictoriaMetrics-side checks compare names in: dots become underscores,
// so `payment.provider` and `payment_provider` are the same label.
func Norm(s string) string { return strings.ReplaceAll(s, ".", "_") }

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

// PlatformLabelSet is Norm of PlatformLabels and QueryLabels.
func PlatformLabelSet() map[string]bool {
	m := map[string]bool{}
	for _, l := range PlatformLabels {
		m[Norm(l)] = true
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
	Labels   map[string]bool // Norm of the label names it may carry (contract labels + platform labels)
}

var platformSuffixes = []string{"", "_total", "_seconds", "_seconds_bucket", "_seconds_sum", "_seconds_count", "_bytes", "_bytes_bucket", "_bytes_sum", "_bytes_count", "_bucket", "_sum", "_count"}

// Lookup resolves a stored series name (as a query writes it) to the contract metric behind it.
func (c *Contract) Lookup(stored string) (StoredMetric, bool) {
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		for _, s := range StoredNames(n, m) {
			if s == stored {
				return StoredMetric{Name: n, Metric: m, Labels: c.metricLabels(m)}, true
			}
		}
	}
	for _, k := range c.platformKeys() {
		base := StoredBase(k)
		for _, suf := range platformSuffixes {
			if stored == base+suf {
				ls := map[string]bool{}
				for l := range PlatformLabelSet() {
					ls[l] = true
				}
				for _, l := range c.Platform[k] {
					ls[Norm(l)] = true
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
		ls[Norm(l)] = true
	}
	if m.PerPod {
		for _, k := range c.EffectivePodKeys() {
			ls[Norm(k)] = true
		}
	}
	return ls
}

// StoredNameList lists every stored series name a query may read, for "did you mean" hints.
func (c *Contract) StoredNameList() []string {
	var out []string
	for _, n := range c.MetricOrder {
		out = append(out, StoredNames(n, c.Metrics[n])...)
	}
	for _, k := range c.platformKeys() {
		out = append(out, StoredBase(k))
	}
	return out
}

// LabelType returns the type of a contract label of metric m (by its Norm spelling).
func (c *Contract) LabelType(m Metric, label string) (Type, bool) {
	for _, l := range m.LabelOrder {
		if Norm(l) == label || l == label {
			t, ok := c.Types[m.Labels[l]]
			return t, ok
		}
	}
	return Type{}, false
}

// AllLabels is the union of every label any metric may carry (Norm spelling): for group-by
// modifiers, which the expression tree does not tie to one selector.
func (c *Contract) AllLabels() map[string]bool {
	ls := PlatformLabelSet()
	for _, n := range c.MetricOrder {
		for _, l := range c.Metrics[n].LabelOrder {
			ls[Norm(l)] = true
		}
	}
	for _, k := range c.platformKeys() {
		for _, l := range c.Platform[k] {
			ls[Norm(l)] = true
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
		return PlatformLabelSet()[Norm(name)] || c.platformHas(name)
	case "span", "":
		for _, n := range c.SpanOrder {
			for _, a := range c.Spans[n].AttrOrder {
				if a == name || Norm(a) == Norm(name) {
					return true
				}
			}
		}
		return false
	}
	return true
}

func (c *Contract) platformHas(name string) bool {
	for _, ls := range c.Platform {
		for _, l := range ls {
			if Norm(l) == Norm(name) {
				return true
			}
		}
	}
	return false
}

// StreamFields is the set of VictoriaLogs stream fields: the bounded where-from set
// (concept 3.3) plus any bounded stream_labels a log event declares.
func (c *Contract) StreamFields() map[string]bool {
	m := map[string]bool{}
	for _, l := range PlatformLabels {
		switch l {
		case "k8s.pod.uid", "k8s.pod.name", "k8s.container.name", "k8s.node.name", "service.instance.id":
			continue
		}
		m[l] = true
	}
	for _, k := range c.LogOrder {
		for l := range c.Logs[k].StreamLabels {
			m[l] = true
		}
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
