package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/AtomiCloud/obs/internal/sizing"
)

func runSize(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs size", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var in sizing.Input
	fs.Float64Var(&in.RequestsPerSecond, "req-per-second", 0, "requests per second")
	fs.IntVar(&in.Services, "services", 0, "typical services")
	fs.IntVar(&in.Pods, "pods", 0, "total application pods")
	fs.IntVar(&in.ProfiledPods, "profiled-pods", 0, "profiled pods (at most pods)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "obs size: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	e, err := sizing.Calculate(in)
	if err != nil {
		fmt.Fprintf(stderr, "obs size: %v\n", err)
		return 2
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
