// Package aggregation generates the VictoriaMetrics stream-aggregation file from the contract
// (concept section 4.1): the one place that says which pod keys are dropped and which metrics
// are added up across pods. The same file feeds the single VM (-streamAggr.config) and a vmagent
// aggregator (-remoteWrite.streamAggr.config); its `without` list also feeds the router
// vmagents' ignoreLabels.
//
// One rule per kind: counters and histograms (`_bucket`, `_sum`, `_count`) get `total`; gauges get
// `sum_samples`, `avg`, `max` or `last`, each with dedup_interval = interval; every rule has
// keep_metric_names: true and `without` = pod_keys. Match style: an allowlist of contract
// metric names. per_pod metrics are left out of every match and keep their pod keys.
package aggregation

import (
	"fmt"
	"strings"

	"github.com/AtomiCloud/obs/internal/contract"
)

// Header lines of the file are the caller's (gen) business: Generate takes them in.

var gaugeOutput = []struct{ How, Output string }{
	{"sum", "sum_samples"},
	{"avg", "avg"},
	{"max", "max"},
	{"last", "last"},
}

// Generate returns the aggregation YAML for a valid contract. header is the comment block
// (already prefixed with "# ") written first.
func Generate(c *contract.Contract, header string) []byte {
	var b strings.Builder
	b.WriteString(header)
	interval := c.EffectiveInterval()
	without := make([]string, 0, len(c.EffectivePodKeys()))
	for _, k := range c.EffectivePodKeys() {
		without = append(without, "'"+k+"'") // single quotes: the registry formatter (prettier) writes YAML that way
	}
	withoutList := "[" + strings.Join(without, ", ") + "]"

	var counters []string
	gauges := map[string][]string{}
	var aggregated, perPod []string
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		if m.PerPod {
			perPod = append(perPod, n)
			continue
		}
		aggregated = append(aggregated, n)
		stored := contract.StoredNames(n, m)
		switch m.Kind {
		case contract.MetricCounter, contract.MetricHistogram:
			counters = append(counters, stored...)
		case contract.MetricGauge:
			gauges[m.AcrossPod] = append(gauges[m.AcrossPod], stored...)
		}
	}
	fmt.Fprintf(&b, "# service: %s; interval: %s\n", c.Service, interval)
	fmt.Fprintf(&b, "# Aggregated across pods: %s\n", orNone(aggregated))
	fmt.Fprintf(&b, "# Per pod (in no match, so stored unchanged): %s; plus any metric the contract does not list.\n", orNone(perPod))
	fmt.Fprintf(&b, "# without (also the router vmagents' -remoteWrite.shardByURL.ignoreLabels): %s\n", strings.Join(c.EffectivePodKeys(), ","))
	if len(counters) == 0 && len(gauges) == 0 {
		b.WriteString("[]\n")
		return []byte(b.String())
	}
	if len(counters) > 0 {
		b.WriteString("# counters and histogram parts: reset-aware sum over pods\n")
		fmt.Fprintf(&b, "- match: '{__name__=~\"%s\"}'\n", strings.Join(counters, "|"))
		fmt.Fprintf(&b, "  interval: %s\n", interval)
		fmt.Fprintf(&b, "  without: %s\n", withoutList)
		b.WriteString("  outputs: [total]\n")
		b.WriteString("  keep_metric_names: true\n")
	}
	for _, g := range gaugeOutput {
		names := gauges[g.How]
		if len(names) == 0 {
			continue
		}
		fmt.Fprintf(&b, "# gauges, %s across pods: last value per pod per interval (dedup), then %s\n", g.How, g.How)
		fmt.Fprintf(&b, "- match: '{__name__=~\"%s\"}'\n", strings.Join(names, "|"))
		fmt.Fprintf(&b, "  interval: %s\n", interval)
		fmt.Fprintf(&b, "  dedup_interval: %s\n", interval)
		fmt.Fprintf(&b, "  without: %s\n", withoutList)
		fmt.Fprintf(&b, "  outputs: [%s]\n", g.Output)
		b.WriteString("  keep_metric_names: true\n")
	}
	return []byte(b.String())
}

func orNone(a []string) string {
	if len(a) == 0 {
		return "(none)"
	}
	return strings.Join(a, ", ")
}
