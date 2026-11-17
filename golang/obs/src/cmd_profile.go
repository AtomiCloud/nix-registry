package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/profile"
)

const profileUsage = `usage:
  obs profile summarize --format pprof|cpuprofile|speedscope [--own REGEX]... [--run run.json] [--top N] [--json] FILE
  obs profile baseline  --format F --service S --scenario N --own REGEX [--own REGEX]... [--profiler P] [--dir e2e/profile-baseline] [--update] RUN1 RUN2 RUN3
  obs profile compare   --format F (--baseline FILE | --service S --scenario N [--dir DIR]) [--limits e2e/profile-limits.yaml] [--run run.json] FILE

A run's OS CPU and runner signal come from <FILE>.run.json next to the profile (or --run).
All subcommands accept --sample-period DURATION for evented profiles (default 1ms for dotnet-trace).
--source-root DIR overrides inferred checkout-root normalization; baselines store the root used.
exit 0 when clean, 1 on a regression or missing baseline function, 2 on a usage or input error.
`

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// parseInterspersed lets flags come before, between or after the positional files.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func runProfile(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, profileUsage)
		return 2
	}
	switch args[0] {
	case "summarize":
		return runProfileSummarize(args[1:], stdout, stderr)
	case "baseline":
		return runProfileBaseline(args[1:], stdout, stderr)
	case "compare":
		return runProfileCompare(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, profileUsage)
		return 0
	}
	fmt.Fprintf(stderr, "obs profile: unknown subcommand %q\n\n%s", args[0], profileUsage)
	return 2
}

// loadRun reads one profile and its run meta (explicit path, else <file>.run.json).
func loadRun(file, format, runPath string, own []string, samplePeriod time.Duration, sourceRoot string) (*profile.Summary, error) {
	p, err := profile.ReadWithSamplePeriod(file, format, float64(samplePeriod))
	if err != nil {
		return nil, err
	}
	explicit := runPath != ""
	if !explicit {
		runPath = file + ".run.json"
	}
	m, ok, err := profile.ReadRunMeta(runPath)
	if err != nil {
		return nil, err
	}
	if !ok && explicit {
		return nil, fmt.Errorf("%s: no such run meta file", runPath)
	}
	var meta *profile.RunMeta
	if ok {
		meta = &m
	}
	return profile.Summarize(p, meta, profile.Options{Own: own, SourceRoot: sourceRoot})
}

func runProfileSummarize(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs profile summarize", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "", "pprof, cpuprofile or speedscope")
	samplePeriod := fs.Duration("sample-period", time.Millisecond, "evented sampling period used for collection (default 1ms, dotnet-trace)")
	sourceRoot := fs.String("source-root", "", "explicit checkout root (default: infer from own sources)")
	var own stringList
	fs.Var(&own, "own", "regular expression: a function or file matching it is the service's own code (repeatable; default: all code)")
	runPath := fs.String("run", "", "run meta file (default <FILE>.run.json)")
	top := fs.Int("top", 25, "functions to print (0 = all)")
	asJSON := fs.Bool("json", false, "print the summary as JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if *format == "" || len(pos) != 1 {
		fmt.Fprint(stderr, "obs profile summarize: give --format and exactly one profile file\n\n"+profileUsage)
		return 2
	}
	sum, err := loadRun(pos[0], *format, *runPath, own, *samplePeriod, *sourceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "obs profile summarize: %v\n", err)
		return 2
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(sum)
		return 0
	}
	sum.Print(stdout, *top)
	return 0
}

func runProfileBaseline(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs profile baseline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "", "pprof, cpuprofile or speedscope")
	samplePeriod := fs.Duration("sample-period", time.Millisecond, "evented sampling period used for collection (stored in the baseline)")
	sourceRoot := fs.String("source-root", "", "explicit checkout root (stored in the baseline)")
	service := fs.String("service", "", "service name")
	scenario := fs.String("scenario", "", "scenario name (e2e/profile-scenarios/<name>.json)")
	profiler := fs.String("profiler", "", "profiler the runs used; dotnet-trace gives every row the 30 % tolerance")
	var own stringList
	fs.Var(&own, "own", "regular expression for the service's own code (repeatable; kept in the baseline)")
	dir := fs.String("dir", "e2e/profile-baseline", "baseline folder")
	update := fs.Bool("update", false, "replace an existing baseline (only in a PR)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if *format == "" || *service == "" || *scenario == "" {
		fmt.Fprint(stderr, "obs profile baseline: give --format, --service and --scenario\n\n"+profileUsage)
		return 2
	}
	if len(pos) != 3 {
		fmt.Fprintf(stderr, "obs profile baseline: a baseline is the median of exactly 3 runs; got %d profile files\n", len(pos))
		return 2
	}
	if len(own) == 0 {
		fmt.Fprintln(stderr, "obs profile baseline: give at least one --own expression for the service's own code")
		return 2
	}
	if strings.ContainsAny(*service+*scenario, "/\\") {
		fmt.Fprintln(stderr, "obs profile baseline: service and scenario must not contain a slash")
		return 2
	}
	out := profile.BaselinePath(*dir, *service, *scenario)
	if _, err := os.Stat(out); err == nil && !*update {
		fmt.Fprintf(stderr, "obs profile baseline: %s exists; baselines change only through a PR: pass --update to replace it\n", out)
		return 2
	}
	var runs []*profile.Summary
	for _, f := range pos {
		sum, err := loadRun(f, *format, "", own, *samplePeriod, *sourceRoot)
		if err != nil {
			fmt.Fprintf(stderr, "obs profile baseline: %v\n", err)
			return 2
		}
		runs = append(runs, sum)
	}
	b, err := profile.BuildBaseline(*service, *scenario, *profiler, own, runs)
	if err != nil {
		fmt.Fprintf(stderr, "obs profile baseline: %v\n", err)
		return 2
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintf(stderr, "obs profile baseline: %v\n", err)
		return 2
	}
	data := b.Marshal()
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "obs profile baseline: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "obs profile baseline: wrote %s: median of 3 runs, %d samples, %d functions, %d spans, %d bytes\n",
		out, b.Samples, len(b.Functions), len(b.Spans), len(data))
	b.PrintSourceRoot(stdout)
	if runs[0].SourceRootNote != "" {
		fmt.Fprintf(stdout, "note: %s\n", runs[0].SourceRootNote)
	}
	if b.CPUPerRequestNs == 0 {
		fmt.Fprintln(stdout, "note: the runs had no run meta (requests, os_cpu_ns): the baseline has no total CPU per request")
	}
	return 0
}

func runProfileCompare(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs profile compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "", "pprof, cpuprofile or speedscope")
	samplePeriod := fs.Duration("sample-period", time.Millisecond, "evented sampling period used for this collection (must match baseline)")
	sourceRoot := fs.String("source-root", "", "explicit checkout root (default: baseline explicit root, otherwise infer)")
	basePath := fs.String("baseline", "", "baseline file")
	service := fs.String("service", "", "service name (with --scenario: finds <dir>/<service>.<scenario>.json)")
	scenario := fs.String("scenario", "", "scenario name")
	dir := fs.String("dir", "e2e/profile-baseline", "baseline folder")
	limitsPath := fs.String("limits", "e2e/profile-limits.yaml", "limit overrides (each needs a why)")
	runPath := fs.String("run", "", "run meta file (default <FILE>.run.json)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if *basePath == "" && *service != "" && *scenario != "" {
		*basePath = profile.BaselinePath(*dir, *service, *scenario)
	}
	if *format == "" || *basePath == "" || len(pos) != 1 {
		fmt.Fprint(stderr, "obs profile compare: give --format, a baseline (--baseline or --service and --scenario) and exactly one profile file\n\n"+profileUsage)
		return 2
	}
	b, err := profile.LoadBaseline(*basePath)
	if err != nil {
		fmt.Fprintf(stderr, "obs profile compare: %v\n", err)
		return 2
	}
	if b.Format != *format {
		fmt.Fprintf(stderr, "obs profile compare: %s was made from %s profiles, this run is --format %s\n", *basePath, b.Format, *format)
		return 2
	}
	limits, err := profile.LoadLimits(*limitsPath)
	if err != nil {
		fmt.Fprintf(stderr, "obs profile compare: %v\n", err)
		return 2
	}
	if *sourceRoot == "" && b.SourceRootExplicit {
		*sourceRoot = b.SourceRoot
	}
	now, err := loadRun(pos[0], *format, *runPath, b.Own, *samplePeriod, *sourceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "obs profile compare: %v\n", err)
		return 2
	}
	if b.SamplePeriodNs != now.SamplePeriodNs {
		fmt.Fprintf(stderr, "obs profile compare: %s: sample period differs: baseline %g ns, now %g ns; pass the actual --sample-period used for collection\n", *basePath, b.SamplePeriodNs, now.SamplePeriodNs)
		return 2
	}
	if (b.SourceRootExplicit || now.SourceRootExplicit) && (b.SourceRootExplicit != now.SourceRootExplicit || b.SourceRoot != now.SourceRoot) {
		fmt.Fprintf(stderr, "obs profile compare: %s: explicit source roots differ: baseline %q, now %q; use the baseline's --source-root or regenerate with --update\n", *basePath, b.SourceRoot, now.SourceRoot)
		return 2
	}
	b.PrintSourceRoot(stdout)
	now.PrintSourceRoot(stdout)
	rows, extra := profile.Compare(b, now, limits)
	failed := profile.PrintCompare(stdout, *basePath, rows, extra)
	if failed > 0 {
		fmt.Fprintf(stdout, "obs profile compare: FAILED: %d of %d rows failed (%s, %s)\n", failed, len(rows), b.Service, b.Scenario)
		return 1
	}
	fmt.Fprintf(stdout, "obs profile compare: ok: %d rows, none slower than its limit (%s, %s)\n", len(rows), b.Service, b.Scenario)
	return 0
}
