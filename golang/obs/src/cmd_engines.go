package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/AtomiCloud/obs/internal/chartcheck"
	"github.com/AtomiCloud/obs/internal/pins"
)

// runEngines is `obs engines --check-chart <dir>`: the chart's engine versions against the pins.
func runEngines(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs engines", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("check-chart", "", "a Helm chart folder (Chart.yaml and values.yaml) to compare with the engine pins")
	pinsFile := fs.String("pins", "", "pins file to compare with (default: the pins built into obs; for tests)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "obs engines: give --check-chart <chart folder>")
		return 2
	}
	pin := pins.All()
	if *pinsFile != "" {
		var err error
		if pin, err = pins.Load(*pinsFile); err != nil {
			fmt.Fprintf(stderr, "obs engines: %v\n", err)
			return 2
		}
	}
	res, err := chartcheck.Check(*dir, pin)
	if err != nil {
		fmt.Fprintf(stderr, "obs engines --check-chart: %v\n", err)
		return 2
	}
	for _, m := range res.Matches {
		fmt.Fprintf(stdout, "ok   %s\n", m)
	}
	for _, n := range res.Notes {
		fmt.Fprintf(stdout, "note %s\n", n)
	}
	for _, f := range res.Findings {
		fmt.Fprintf(stdout, "FAIL %s\n", f)
	}
	if len(res.Findings) > 0 {
		fmt.Fprintf(stdout, "obs engines --check-chart: %d drift(s), %d match(es)\n", len(res.Findings), len(res.Matches))
		return 1
	}
	fmt.Fprintf(stdout, "obs engines --check-chart: ok: %d match(es)\n", len(res.Matches))
	return 0
}
