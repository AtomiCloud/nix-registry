package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/AtomiCloud/obs/internal/check"
	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/gen"
)

// runCheck is `obs check` (concept section 11.4), the static check of the observability folder in
// one run and one summary. It does what it always did and what `obs query --static` does:
//
//	limits    the worst-case series per metric and the log stream count; fails on a free metric label, a
//	          free stream label, a calendar date without window_days, a metric over budget and a gauge
//	          with neither across_pods nor per_pod
//	versions  the evaluator versions this obs embeds, against <dir>/obs.yaml
//	queries   every alert and dashboard query parsed by the embedded engines and checked against the
//	          contract (each finding keeps its rule id); a folder with no queries is fine
func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "observability", "the observability folder (holds contract.yaml)")
	pods := fs.Int("pods", 1, "pods per landscape, for the worst case x pods line (from the landscape config)")
	landscapes := fs.Int("landscapes", 1, "landscapes that share the log store, for the stream count")
	contracts := fs.String("contracts", "auto", "which contracts to read: auto (this folder's and every other observability/contract.yaml of the repository) or none (this folder's only)")
	names := fs.Bool("names", false, "print the names each query reads")
	eventRule := fs.String("event-rule", "auto", "a LogsQL alert must name a contract event: auto (only when <dir>/contract.yaml exists), on, off")
	vlSettings := fs.String("vl-settings", "", "VictoriaLogs chart args YAML; overrides obs.yaml thresholds.logs (default <dir>/victorialogs-settings.yaml, else obs.yaml, else pinned defaults)")
	tempoConfig := fs.String("tempo-config", "", "Tempo config file whose query_frontend end cutoff and metrics limits trace panels run under; overrides obs.yaml thresholds.traces (default Tempo 3.1.1: 30s, 24h, 10000)")
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
	switch *eventRule {
	case "auto", "on", "off":
	default:
		fmt.Fprintf(stderr, "obs check: --event-rule %q: want auto, on or off\n", *eventRule)
		return 2
	}
	of, ok := loadObsYaml(*dir, "obs check", stderr)
	if !ok {
		return 2
	}
	versionFails := printObsYamlCheck(of, *dir, stdout)
	var c *contract.Contract
	contractFails := 0 // contract problems printed above (the queries half does not print them twice)
	contractPath := ""
	noContract := !fileExists(filepath.Join(*dir, "contract.yaml"))
	if noContract {
		// Nothing to measure: the limits half is skipped, the queries half still runs (it reads no contract names then).
		fmt.Fprintf(stdout, "note: %s/contract.yaml not found: limits (series per metric, log streams) not checked\n", *dir)
	} else {
		// Not loadContract: that prints its own "obs check: FAILED" line, and this command has one summary.
		cpath := filepath.Join(*dir, "contract.yaml")
		var err error
		c, err = contract.Load(cpath)
		if err != nil {
			fmt.Fprintf(stderr, "obs check: %v\n", err)
			return 2
		}
		if is := c.Validate(); len(is) > 0 {
			for _, i := range is {
				fmt.Fprintf(stdout, "FAIL %s %s\n", cpath, i)
			}
			contractFails = len(is)
			contractPath = cpath
			c = nil
		}
	}
	// reasons is what fails the run, one entry per half; the one summary line at the end lists them.
	var reasons []string
	if versionFails > 0 {
		reasons = append(reasons, fmt.Sprintf("%d version mismatch(es) in %s", versionFails, of.Path))
	}
	if contractFails > 0 {
		// Every issue is printed above; the contract cannot be used.
		reasons = append([]string{fmt.Sprintf("%d contract failures in %s", contractFails, contractPath)}, reasons...)
	}
	okLine := ""
	if noContract {
		okLine = "no contract.yaml, limits not checked"
	}
	if c != nil {
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
			contractFails += otherFails
			reasons = append(reasons, fmt.Sprintf("%d contract failures in the repository's other contracts or across contracts", otherFails))
		} else {
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
			okLine = fmt.Sprintf("%d metrics, %d log events, %d spans, %d types, contract hash %s",
				len(c.MetricOrder), len(c.LogOrder), len(c.SpanOrder), len(c.TypeOrder), shortHash(c))
		}
	}

	// The queries half: what `obs query --static` does (cmd_parts.go), on the same folder.
	set, err := files.Load(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "obs check: %v\n", err)
		return 2
	}
	qo := check.Options{Dir: *dir, VLSettings: *vlSettings, TempoConfig: *tempoConfig, Names: *names, EventRule: *eventRule, Obs: of, Out: stdout, NoOtherContracts: *contracts == "none", AllowEmpty: true}
	rep, _ := queryReport(set, qo, false)
	if contractFails > 0 {
		// The contract problems were printed above; the query rules must not print them again.
		kept := rep.Findings[:0]
		for _, f := range rep.Findings {
			if f.Rule != "contract" {
				kept = append(kept, f)
			}
		}
		rep.Findings = kept
	}
	rep.Print(stdout)
	if rep.Failures() > 0 {
		reasons = append(reasons, fmt.Sprintf("%d query failures", rep.Failures()))
	}

	if len(reasons) > 0 {
		tail := ""
		if okLine != "" && !noContract {
			tail = fmt.Sprintf(" (the contract itself is ok: %s; queries: %s)", okLine, rep.Totals())
		}
		fmt.Fprintf(stdout, "obs check: FAILED: %s%s\n", strings.Join(reasons, "; "), tail)
		return 1
	}
	fmt.Fprintf(stdout, "obs check: ok: %s; queries: %s\n", okLine, rep.Totals())
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
