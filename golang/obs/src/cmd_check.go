package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/AtomiCloud/obs/internal/contract"
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pods < 1 || *landscapes < 1 {
		fmt.Fprintln(stderr, "obs check: --pods and --landscapes must be at least 1")
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
	total := int64(0)
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		w, _ := c.WorstCase(n)
		total += w
		podsNote := "pod label dropped (stored once per landscape)"
		if m.PerPod {
			podsNote = fmt.Sprintf("per pod: x %d pods = %d", *pods, w*int64(*pods))
		}
		budget := "no budget"
		if m.Budget > 0 {
			budget = fmt.Sprintf("budget %d", m.Budget)
		}
		fmt.Fprintf(stdout, "metric %s (%s): worst case %d series, %s; %s\n", n, m.Kind, w, budget, podsNote)
	}
	fmt.Fprintf(stdout, "metrics total: worst case %d series per landscape\n", total)
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func shortHash(c *contract.Contract) string { return c.Hash[:12] }
