package main

import (
	"flag"
	"fmt"
	"io"
)

func runLinks(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs links", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "observability", "observability directory")
	file := fs.String("seed", "", "seed file (default <dir>/seed.yaml)")
	base := fs.Int64("base", 0, "seed start, Unix seconds; default ends in the past")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "obs links: unexpected arguments", fs.Args())
		return 2
	}
	r, seedFile, rc := linksReport(*dir, *file, *base, stderr)
	if rc != 0 {
		return rc
	}
	printLinkFindings(r, seedFile, stdout)
	fmt.Fprintf(stdout, "obs links: trace-to-logs=%d metric-to-traces=%d span-to-profile=%d failures=%d\n", r.Checked["trace-to-logs"], r.Checked["metric-to-traces"], r.Checked["span-to-profile"], len(r.Findings))
	if len(r.Findings) > 0 {
		return 1
	}
	return 0
}
