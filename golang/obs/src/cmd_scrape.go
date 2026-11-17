package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/sandbox"
	"github.com/AtomiCloud/obs/internal/scrape"
)

const scrapeUsage = `usage:
  obs scrape --target <url> [--target <url> ...] [--label name=value ...] [--job name]
             [--interval 15s] [--once] [--vm-url url] [--dir observability] [--project name]

Scrapes Prometheus text endpoints (an app's /metrics) into the sandbox's VictoriaMetrics, so the
app's own metrics show next to the seed in Grafana. Run it after obs sandbox up. Ctrl-C stops it.
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func runScrape(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs scrape", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var targets, labels multiFlag
	fs.Var(&targets, "target", "an http(s) URL of a Prometheus text endpoint (repeatable)")
	fs.Var(&labels, "label", "name=value added to every sample, e.g. durian_cloud_landscape=local (repeatable)")
	job := fs.String("job", "obs-scrape", "the job label")
	interval := fs.Duration("interval", 15*time.Second, "time between scrapes")
	once := fs.Bool("once", false, "scrape once and exit (exit 1 if it failed)")
	vmURL := fs.String("vm-url", "", "VictoriaMetrics base URL (default: the running sandbox's)")
	dir := fs.String("dir", "observability", "the observability folder (names the project)")
	project := fs.String("project", "", "sandbox name (default: the repo folder's name)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(targets) == 0 {
		fmt.Fprint(stderr, "obs scrape: give at least one --target\n\n"+scrapeUsage)
		return 2
	}
	lm := map[string]string{}
	for _, l := range labels {
		k, v, ok := strings.Cut(l, "=")
		if !ok || k == "" {
			fmt.Fprintf(stderr, "obs scrape: --label %q: want name=value\n", l)
			return 2
		}
		lm[k] = v
	}
	vm := *vmURL
	if vm == "" {
		st := sandbox.ReadState(sandbox.Config{Project: projectName(*project, *dir)})
		if st == nil {
			fmt.Fprintln(stderr, "obs scrape: no running sandbox found; run `obs sandbox up` or pass --vm-url")
			return 1
		}
		vm = st.URLs["vm"]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	o := scrape.Options{Targets: targets, VM: strings.TrimRight(vm, "/"), Labels: lm, Job: *job, Interval: *interval, Out: stdout}
	if *once {
		o.Count = 1
	}
	if err := scrape.Run(ctx, o); err != nil {
		fmt.Fprintf(stderr, "obs scrape: %v\n", err)
		return 1
	}
	return 0
}
