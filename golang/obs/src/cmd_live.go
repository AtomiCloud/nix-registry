package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/AtomiCloud/obs/internal/live"
	"github.com/AtomiCloud/obs/internal/obsyaml"
	"github.com/AtomiCloud/obs/internal/sandbox"
	"github.com/AtomiCloud/obs/internal/seed"
)

const liveUsage = `usage:
  obs live usage      --target <name> [--dir observability] [--top N] [--over-only] [--pods n] [--window 5m] [--unknown warn|fail|ignore] [--all-services] [--json]
  obs live shape      --target <name> [--dir observability] [--window 7d] [--step 5m] [--metric name ...] [--seed seed.yaml] [--all-services] [--json]
  obs live incidents  --target <name> [--dir observability] [--since 7d] [--window from..to ...] [--json]
  obs live look       --target <name> [--dir observability] [--match text] [--checklist file.md] [--out obs-look] [--json]

--target is local, dev, stage, prod, or any name under targets: in <dir>/obs.yaml (local needs no entry:
it uses the running sandbox, else the sandbox addresses). obs live only reads: GET to the
stores' query endpoints, never a write. Exit: usage 1 when a metric is over budget (series or
points/min), an undeclared metric is found or a label check of assert fails; shape, incidents
and look exit 0 (the LLM judges; look never fails); 2 on a usage or input error.
`

func runLive(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		w := stderr
		rc := 2
		if len(args) > 0 {
			w, rc = stdout, 0
		}
		fmt.Fprint(w, liveUsage)
		return rc
	}
	switch args[0] {
	case "usage":
		return runLiveUsage(args[1:], stdout, stderr)
	case "shape":
		return runLiveShape(args[1:], stdout, stderr)
	case "incidents":
		return runLiveIncidents(args[1:], stdout, stderr)
	case "look":
		return runLiveLook(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "obs live: unknown command %q\n\n%s", args[0], liveUsage)
	return 2
}

// liveFlags are the flags every obs live command takes.
type liveFlags struct {
	dir, target, project string
	asJSON               bool
	timeout              time.Duration
}

func (f *liveFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.dir, "dir", "observability", "the observability folder (holds obs.yaml and contract.yaml)")
	fs.StringVar(&f.target, "target", "", "local, dev, stage, prod, or a name under targets: in obs.yaml")
	fs.BoolVar(&f.asJSON, "json", false, "print JSON for an LLM instead of the table")
	fs.StringVar(&f.project, "project", "", "local only: the sandbox name (default: the repo folder name) whose running ports are read")
	fs.DurationVar(&f.timeout, "timeout", 30*time.Second, "time one request may take")
}

// resolve loads obs.yaml and resolves --target into the stores to read.
func (f *liveFlags) resolve(cmd string, stderr io.Writer) (*live.Target, *obsyaml.File, *live.Client, error) {
	if f.target == "" {
		return nil, nil, nil, errors.New("give --target local, dev, stage, prod or a name under targets: in obs.yaml")
	}
	of, err := obsyaml.Load(f.dir)
	if err != nil {
		return nil, nil, nil, err
	}
	noteUnusedRoot(f.dir, of, cmd, stderr) // the one note every obs.yaml reader prints (cmd_obsyaml.go)
	path := obsyaml.FileName
	if of != nil {
		path = of.Path
	}
	specs, err := live.ParseTargets(of.Section("targets"), path)
	if err != nil {
		return nil, nil, nil, err
	}
	var sb map[string]string
	if f.target == "local" {
		if st := sandbox.ReadState(sandbox.Config{Project: projectName(f.project, f.dir)}); st != nil {
			sb = st.URLs
		}
	}
	t, err := live.Resolve(f.target, specs, sb, nil, path)
	if err != nil {
		return nil, nil, nil, err
	}
	c := &live.Client{Headers: t.Headers}
	c.HTTP = &http.Client{Timeout: f.timeout}
	return t, of, c, nil
}

func spanFlag(cmd, name, v string, stderr io.Writer) (time.Duration, bool) {
	d, err := live.ParseSpan(v)
	if err != nil || d <= 0 {
		fmt.Fprintf(stderr, "%s: --%s: %v\n", cmd, name, err)
		if err == nil {
			fmt.Fprintf(stderr, "%s: --%s %q must be above 0\n", cmd, name, v)
		}
		return 0, false
	}
	return d, true
}

func printJSON(stdout io.Writer, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(stdout, string(b))
}

func readOnlyNote(stderr io.Writer, cmd string, t *live.Target) {
	fmt.Fprintf(stderr, "%s: target %s (read-only: only GET to query endpoints)\n", cmd, t.Name)
}

func runLiveUsage(args []string, stdout, stderr io.Writer) int {
	const cmd = "obs live usage"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f liveFlags
	f.register(fs)
	top := fs.Int("top", 0, "show only the N rows furthest from the contract (0: all)")
	overOnly := fs.Bool("over-only", false, "show only rows that are not ok")
	pods := fs.Int("pods", 1, "pods per landscape: a per_pod metric may hold worst case x pods series")
	window := fs.String("window", "5m", "how far back rates (points, lines, spans per minute) are measured")
	unknown := fs.String("unknown", "fail", "a metric that is not in the contract: fail (default: it has no budget), warn or ignore")
	all := fs.Bool("all-services", false, "count every service on the target, not only the contract's service")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *top < 0 || *pods < 1 {
		fmt.Fprintln(stderr, cmd+": unexpected argument, --top below 0 or --pods below 1")
		return 2
	}
	switch *unknown {
	case "warn", "fail", "ignore":
	default:
		fmt.Fprintf(stderr, "%s: --unknown %q: want warn, fail or ignore\n", cmd, *unknown)
		return 2
	}
	win, ok := spanFlag(cmd, "window", *window, stderr)
	if !ok {
		return 2
	}
	t, of, c, err := f.resolve(cmd, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 2
	}
	ct, rc := loadContract(f.dir, cmd, stdout, stderr)
	if ct == nil {
		return rc
	}
	estPath := obsyaml.FileName
	if of != nil {
		estPath = of.Path
	}
	est, err := live.ParseEstimates(of.Section("estimates"), estPath)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	readOnlyNote(stderr, cmd, t)
	res, err := live.Usage(ctx, c, t, ct, live.UsageOptions{Window: win, Pods: *pods, Unknown: *unknown, Top: *top, OverOnly: *overOnly, AllServices: *all, Estimates: est})
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 2
	}
	if f.asJSON {
		printJSON(stdout, res)
	} else {
		res.Print(stdout)
	}
	if res.OverBudget() {
		return 1
	}
	return 0
}

func runLiveShape(args []string, stdout, stderr io.Writer) int {
	const cmd = "obs live shape"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f liveFlags
	f.register(fs)
	window := fs.String("window", "7d", "how far back to look")
	step := fs.String("step", "5m", "time between points (gaps shorter than two steps are not reported)")
	seedPath := fs.String("seed", "", "seed file for the seed column (default <dir>/seed.yaml; the column is left out when there is none)")
	all := fs.Bool("all-services", false, "read every service on the target, not only the contract's service")
	var only multiFlag
	fs.Var(&only, "metric", "a contract metric to describe (repeatable; default every metric)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, cmd+": unexpected argument")
		return 2
	}
	win, ok := spanFlag(cmd, "window", *window, stderr)
	if !ok {
		return 2
	}
	st, ok := spanFlag(cmd, "step", *step, stderr)
	if !ok {
		return 2
	}
	if st < time.Minute || win/st > 10000 {
		fmt.Fprintf(stderr, "%s: --window %s at --step %s needs %d points a series; use a step of 1m or more and at most 10000 points\n", cmd, *window, *step, win/st)
		return 2
	}
	t, _, c, err := f.resolve(cmd, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 2
	}
	ct, rc := loadContract(f.dir, cmd, stdout, stderr)
	if ct == nil {
		return rc
	}
	o := live.ShapeOptions{Window: win, Step: st, Only: only, AllServices: *all}
	sp := *seedPath
	explicit := sp != ""
	if sp == "" {
		sp = f.dir + "/seed.yaml"
	}
	if fileExists(sp) {
		sd, err := seed.Load(sp)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
			return 2
		}
		o.Seed = sd
	} else if explicit {
		fmt.Fprintf(stderr, "%s: --seed %s: no such file\n", cmd, sp)
		return 2
	} else {
		o.SeedNote = "no seed at " + sp
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	readOnlyNote(stderr, cmd, t)
	res, err := live.ShapeReport(ctx, c, t, ct, o)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 2
	}
	if f.asJSON {
		printJSON(stdout, res)
	} else {
		res.Print(stdout)
	}
	return 0
}

func runLiveIncidents(args []string, stdout, stderr io.Writer) int {
	const cmd = "obs live incidents"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f liveFlags
	f.register(fs)
	since := fs.String("since", "7d", "how far back to look for alerts that fired")
	var windows multiFlag
	fs.Var(&windows, "window", "a time range you name, from..to (RFC 3339, 2026-11-10, or -6h..-2h; repeatable); reports observed firing or unknown, never a proven miss")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, cmd+": unexpected argument")
		return 2
	}
	sd, ok := spanFlag(cmd, "since", *since, stderr)
	if !ok {
		return 2
	}
	now := time.Now().UTC()
	var ws []live.Window
	for _, w := range windows {
		pw, err := live.ParseWindow(w, now)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
			return 2
		}
		ws = append(ws, pw)
	}
	t, _, c, err := f.resolve(cmd, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	readOnlyNote(stderr, cmd, t)
	res, err := live.Incidents(ctx, c, t, live.IncidentOptions{Dir: f.dir, Since: sd, Windows: ws, Now: func() time.Time { return now }})
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 2
	}
	if f.asJSON {
		b, _ := res.JSON()
		fmt.Fprintln(stdout, string(b))
	} else {
		res.Print(stdout)
	}
	return 0
}

// runLiveLook never fails: a bad flag, an unknown target or a down Grafana prints what is wrong
// and exits 0, like `obs look`.
func runLiveLook(args []string, stdout, stderr io.Writer) int {
	const cmd = "obs live look"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	var usage bytes.Buffer
	fs.SetOutput(&usage)
	var f liveFlags
	f.register(fs)
	match := fs.String("match", "", "list only dashboards whose title, folder or tag contains this text")
	checklist := fs.String("checklist", "docs/standards/grafana-dashboards/index.md", "the visual checklist (a markdown file with a \"Visual checklist\" bullet list)")
	out := fs.String("out", "obs-look", "folder the screenshots are saved in (as written in the prompt)")
	fail := func(msg string) int {
		res := &live.LookResult{Target: f.target, Error: msg, Checklist: []string{}, Dashboards: []live.LookDashboard{}}
		res.Prompt = cmd + ": not run for target " + f.target + ", nothing to review. obs live look exits 0 on purpose; this is not a CI failure.\n  what failed: " + msg + "\n"
		if f.asJSON || wantsJSON(args) {
			printJSON(stdout, res)
		} else {
			fmt.Fprint(stdout, res.Prompt)
		}
		return 0
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage.String())
			return 0
		}
		return fail("usage error: " + err.Error())
	}
	t, _, c, err := f.resolve(cmd, stderr)
	if err != nil {
		return fail(err.Error())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	readOnlyNote(stderr, cmd, t)
	res := live.Look(ctx, c, t, live.LookOptions{Checklist: *checklist, Match: *match, Out: *out})
	if f.asJSON {
		printJSON(stdout, res)
	} else {
		fmt.Fprint(stdout, res.Prompt)
	}
	return 0
}
