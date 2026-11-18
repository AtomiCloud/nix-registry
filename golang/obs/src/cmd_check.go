package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

// runCheck is `obs check` (concept section 11.4): it prints the worst-case series per metric and
// the log stream count, and fails on a free metric label, a free stream label, a calendar date
// without window_days, a metric over budget and a gauge with neither across_pods nor per_pod.
func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "observability", "the observability folder (holds contract.yaml)")
	pods := fs.Int("pods", 1, "pods per landscape, for the worst case x pods line (from the landscape config)")
	landscapes := fs.Int("landscapes", 1, "landscapes that share the log store, for the stream count")
	contracts := fs.String("contracts", "auto", "which contracts to read: auto (this folder's and every other observability/contract.yaml of the repository) or none (this folder's only)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pods < 1 || *landscapes < 1 {
		fmt.Fprintln(stderr, "obs check: --pods and --landscapes must be at least 1")
		return 2
	}
	if *contracts != "auto" && *contracts != "none" {
		fmt.Fprintf(stderr, "obs check: --contracts %q: want auto or none\n", *contracts)
		return 2
	}
	of, ok := loadObsYaml(*dir, "obs check", stderr)
	if !ok {
		return 2
	}
	versionFails := printObsYamlCheck(of, *dir, stdout)
	c, rc := loadContract(*dir, "obs check", stdout, stderr)
	if c == nil {
		if rc == 1 && versionFails > 0 {
			fmt.Fprintf(stdout, "obs check: FAILED: %d version mismatch(es) in %s\n", versionFails, of.Path)
		}
		return rc
	}
	printWarnings(c, stdout)
	// The repository's other contracts: each is validated and printed, and no stored name may be in two.
	var others []*contract.Contract
	otherFails := 0
	if *contracts == "auto" {
		found, derr := contract.Discover(*dir)
		if derr != nil {
			fmt.Fprintf(stdout, "FAIL %s\n", derr)
			otherFails++
		}
		for _, p := range found {
			oc, err := contract.Load(p)
			if err != nil {
				fmt.Fprintf(stdout, "FAIL %s\n", strings.ReplaceAll(err.Error(), p, contract.RelPath(p)))
				otherFails++
				continue
			}
			if is := oc.Validate(); len(is) > 0 {
				for _, i := range is {
					fmt.Fprintf(stdout, "FAIL %s %s\n", oc.Rel(), i)
				}
				otherFails += len(is)
				continue
			}
			printWarnings(oc, stdout)
			others = append(others, oc)
		}
		for _, i := range contract.Clashes(append([]*contract.Contract{c}, others...)) {
			fmt.Fprintf(stdout, "FAIL %s %s\n", c.Rel(), i)
			otherFails++
		}
	}
	if otherFails > 0 {
		fmt.Fprintf(stdout, "obs check: FAILED: %d contract failures in the repository's other contracts or across contracts\n", otherFails)
		return 1
	}
	total := printMetrics(c, *pods, stdout)
	fmt.Fprintf(stdout, "metrics total: worst case %d series per landscape\n", total)
	for _, oc := range others {
		fmt.Fprintf(stdout, "contract %s (service %s, read with this one):\n", gen.ContractPath(oc), oc.Service)
		ot := printMetrics(oc, *pods, stdout)
		fmt.Fprintf(stdout, "metrics total (%s): worst case %d series per landscape\n", oc.Service, ot)
	}
	fmt.Fprintf(stdout, "log streams: worst case %d (service %s x %d landscapes x modules %d; log events add no streams)\n",
		c.StreamCount(*landscapes), c.Service, *landscapes, maxInt(len(c.Modules), 1))
	fmt.Fprintf(stdout, "events=%v\n", c.EventNames())
	if versionFails > 0 {
		fmt.Fprintf(stdout, "obs check: FAILED: %d version mismatch(es) in %s (the contract itself is ok: %d metrics, %d log events, %d spans, %d types, contract hash %s)\n",
			versionFails, of.Path, len(c.MetricOrder), len(c.LogOrder), len(c.SpanOrder), len(c.TypeOrder), shortHash(c))
		return 1
	}
	fmt.Fprintf(stdout, "obs check: ok: %d metrics, %d log events, %d spans, %d types, contract hash %s\n",
		len(c.MetricOrder), len(c.LogOrder), len(c.SpanOrder), len(c.TypeOrder), shortHash(c))
	return 0
}

// printMetrics prints one line per metric and returns the worst-case total. A source: metric is not
// ours: it is stored as scraped (no pod label dropped), says who makes it and who uses it, and its
// worst case counts every estimate at its max. The churn is how many new series a day the estimates
// start; it is information, not part of the budget (the budget is the series stored at once).
func printMetrics(c *contract.Contract, pods int, stdout io.Writer) int64 {
	total := int64(0)
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		w, _ := c.WorstCase(n)
		total += w
		budget := "no budget"
		if m.Budget > 0 {
			budget = fmt.Sprintf("budget %d", m.Budget)
		}
		if m.IsSource() {
			by := "nobody named"
			if len(m.UsedBy) > 0 {
				by = strings.Join(m.UsedBy, ", ")
			}
			note := ""
			if ch := c.ChurnPerDay(n); ch > 0 {
				note = fmt.Sprintf("; about %d new series a day from churn", ch)
			}
			fmt.Fprintf(stdout, "metric %s (%s): worst case %d series (estimate max), %s; source %s; used by %s%s\n", n, m.Kind, w, budget, m.Source, by, note)
			continue
		}
		podsNote := "pod label dropped (stored once per landscape)"
		if m.PerPod {
			podsNote = fmt.Sprintf("per pod: x %d pods = %d", pods, w*int64(pods))
		}
		fmt.Fprintf(stdout, "metric %s (%s): worst case %d series, %s; %s\n", n, m.Kind, w, budget, podsNote)
	}
	return total
}

// printWarnings prints the contract's warnings (a source: entry that does not say who uses it); they do not fail the check.
func printWarnings(c *contract.Contract, stdout io.Writer) {
	for _, w := range c.Warnings() {
		fmt.Fprintf(stdout, "WARN %s %s\n", c.Rel(), w)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func shortHash(c *contract.Contract) string { return c.Hash[:12] }
