package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/AtomiCloud/obs/internal/storemove"
	"gopkg.in/yaml.v3"
)

func runStore(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "move" {
		fmt.Fprintln(stderr, "usage: obs store move <tempo|pyroscope|victorialogs|victoriametrics> --config move.yaml [--dry-run] [--rollback]")
		return 2
	}
	fs := flag.NewFlagSet("obs store move", flag.ContinueOnError)
	fs.SetOutput(stderr)
	config := fs.String("config", "", "chart/operator move config YAML")
	dry := fs.Bool("dry-run", false, "print the fixed plan without running commands or requests")
	rollback := fs.Bool("rollback", false, "restore original routing; retain volumes")
	timeout := fs.Duration("timeout", 15*time.Minute, "overall operation deadline")
	interval := fs.Duration("interval", 15*time.Second, "counter observation interval")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *config == "" || *timeout <= 0 || *interval <= 0 {
		fmt.Fprintln(stderr, "obs store move: require --config, positive timeout/interval, no extra arguments")
		return 2
	}
	b, err := os.ReadFile(*config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var c storemove.Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err = dec.Decode(&c); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", *config, err)
		return 2
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		fmt.Fprintf(stderr, "%s: want exactly one YAML document\n", *config)
		return 2
	}
	for _, p := range []*string{&c.ScaledManifest, &c.SingleManifest, &c.MetastoreManifest, &c.RoutingManifest, &c.OriginalRoutingManifest} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(filepath.Dir(*config), *p)
		}
	}
	if err = c.Validate(args[1], *rollback); err != nil {
		fmt.Fprintf(stderr, "%s: store %s: %v\n", *config, args[1], err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	r := storemove.Runner{Cluster: storemove.Kubernetes{}, Metrics: storemove.HTTP{}, Out: stdout, Interval: *interval}
	if err = r.Run(ctx, args[1], c, *rollback, *dry); err != nil {
		fmt.Fprintf(stderr, "%s: store %s: %v\n", *config, args[1], err)
		return 1
	}
	return 0
}
