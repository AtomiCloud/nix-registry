package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/AtomiCloud/obs/internal/assert"
)

// runAssert is `obs assert` (concept section 11.4, after e2e): it counts the real series per
// metric in a VictoriaMetrics series export and fails when a count is above the contract's
// worst case. The export is a file; obs makes no network call.
func runAssert(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "note: obs assert is part of obs live usage (obs live usage --target local|dev|stage|prod reads the same series counts from a running store)")
	fs := flag.NewFlagSet("obs assert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "observability", "the observability folder (holds contract.yaml)")
	seriesFile := fs.String("series", "", "series export file: /api/v1/series JSON or /api/v1/export lines, taken after e2e")
	pods := fs.Int("pods", 1, "pods per landscape: a per_pod metric may hold worst case x pods series")
	unknown := fs.String("unknown", "warn", "a metric that is neither in the contract nor in platform_metrics: warn, fail or ignore")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *seriesFile == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "obs assert: give --series FILE (a /api/v1/series or /api/v1/export file taken after e2e)")
		return 2
	}
	if *pods < 1 {
		fmt.Fprintln(stderr, "obs assert: --pods must be at least 1")
		return 2
	}
	switch *unknown {
	case "warn", "fail", "ignore":
	default:
		fmt.Fprintf(stderr, "obs assert: --unknown %q: want warn, fail or ignore\n", *unknown)
		return 2
	}
	c, rc := loadContract(*dir, "obs assert", stdout, stderr)
	if c == nil {
		return rc
	}
	series, err := assert.ReadSeries(*seriesFile)
	if err != nil {
		fmt.Fprintf(stderr, "obs assert: %v\n", err)
		return 2
	}
	res := assert.Check(c, series, assert.Options{Pods: *pods, Unknown: *unknown})
	res.Print(stdout, *seriesFile)
	fmt.Fprintln(stdout, res.Summary())
	if res.Failures() > 0 {
		return 1
	}
	return 0
}
