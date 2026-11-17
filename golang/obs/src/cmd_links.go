package main

import (
	"flag"
	"fmt"
	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/links"
	"github.com/AtomiCloud/obs/internal/seed"
	"io"
	"path/filepath"
	"time"
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
	if *file == "" {
		*file = filepath.Join(*dir, "seed.yaml")
	}
	s, err := seed.Load(*file)
	if err != nil {
		fmt.Fprintln(stderr, "obs links:", err)
		return 2
	}
	start := time.Unix(*base, 0).UTC()
	if *base == 0 {
		start = time.Now().UTC().Truncate(time.Minute).Add(-s.Duration() - 10*time.Minute)
	}
	if start.Add(s.Duration()).After(time.Now()) {
		fmt.Fprintln(stderr, "obs links: seed ends in the future")
		return 2
	}
	ds, err := seed.Build(s, start)
	if err != nil {
		fmt.Fprintf(stderr, "obs links: %s: %v\n", *file, err)
		return 2
	}
	l, err := vl.New(ds)
	if err != nil {
		fmt.Fprintln(stderr, "obs links:", err)
		return 2
	}
	defer l.Close()
	r := links.Check(ds, l, tempo.NewTempoEngine(ds.Traces))
	for _, f := range r.Findings {
		fmt.Fprintf(stdout, "FAIL %s %s: [%s] %s; query: %s\n", *file, f.Subject, f.Kind, f.Error, f.Query)
	}
	fmt.Fprintf(stdout, "obs links: trace-to-logs=%d metric-to-traces=%d span-to-profile=%d failures=%d\n", r.Checked["trace-to-logs"], r.Checked["metric-to-traces"], r.Checked["span-to-profile"], len(r.Findings))
	if len(r.Findings) > 0 {
		return 1
	}
	return 0
}
