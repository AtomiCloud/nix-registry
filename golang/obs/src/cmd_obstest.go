package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AtomiCloud/obs/internal/alerts"
	"github.com/AtomiCloud/obs/internal/check"
	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/obstest"
	"github.com/AtomiCloud/obs/internal/seed"
)

// runTest is `obs test`: the three runs that need the seed, in one command and one summary.
//
//	queries    what `obs query --live` does: every alert and dashboard query runs on the seed and returns rows
//	alerts     what `obs alerts` does: each alert fires inside its incident and stays quiet outside
//	links      what `obs links` does: trace, log, metric and profile links resolve on the seed
//	incidents  new: every incident is caught by at least one alert (internal/obstest)
//
// Exit 0 when every part passes, 1 when a part failed, 2 on a usage or input error. The old commands
// stay (their output is unchanged); this is their grouped form.
func runTest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "observability", "the observability folder")
	seedPath := fs.String("seed", "", "seed file (default <dir>/seed.yaml)")
	base := fs.Int64("base", 0, "unix seconds of the seed's start (default: ends in the past)")
	names := fs.Bool("names", false, "print the names each query reads")
	vlSettings := fs.String("vl-settings", "", "VictoriaLogs chart args YAML; overrides obs.yaml thresholds.logs (default <dir>/victorialogs-settings.yaml, else obs.yaml, else pinned defaults)")
	tempoConfig := fs.String("tempo-config", "", "Tempo config file whose query_frontend end cutoff and metrics limits trace panels and rules run under; overrides obs.yaml thresholds.traces")
	eventRule := fs.String("event-rule", "auto", "a LogsQL alert must name a contract event: auto (only when <dir>/contract.yaml exists), on, off")
	contracts := fs.String("contracts", "auto", "which contracts to read: auto (this folder's and every other observability/contract.yaml of the repository) or none (this folder's only)")
	workers := fs.Int("workers", 0, "alert evaluation workers (default all CPUs)")
	out := fs.String("out", "", "write the alert results as JSON (as obs alerts --out)")
	steps := fs.String("steps", "", "write per-minute alert results as JSON (as obs alerts --steps)")
	recorded := fs.String("recorded", "", "write recording-rule samples as JSON (as obs alerts --recorded)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *workers < 0 {
		fmt.Fprintln(stderr, "obs test: unexpected argument or negative --workers")
		return 2
	}
	if *contracts != "auto" && *contracts != "none" {
		fmt.Fprintf(stderr, "obs test: --contracts %q: want auto or none\n", *contracts)
		return 2
	}
	switch *eventRule {
	case "auto", "on", "off":
	default:
		fmt.Fprintf(stderr, "obs test: --event-rule %q: want auto, on or off\n", *eventRule)
		return 2
	}
	of, ok := loadObsYaml(*dir, "obs test", stderr)
	if !ok {
		return 2
	}
	if rc := gateObsYaml(of, "obs test", stdout); rc != 0 {
		return rc
	}
	set, err := files.Load(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "obs test: %v\n", err)
		return 2
	}
	sp := *seedPath
	if sp == "" {
		sp = filepath.Join(*dir, "seed.yaml")
	}
	var lines []string // one total per part
	var failed []string
	inputError := false
	part := func(name, total string, rc int) {
		switch rc {
		case 0:
			lines = append(lines, fmt.Sprintf("obs test: %s: ok: %s", name, total))
		case 1:
			lines = append(lines, fmt.Sprintf("obs test: %s: FAILED: %s", name, total))
			failed = append(failed, name)
		default:
			lines = append(lines, fmt.Sprintf("obs test: %s: ERROR: %s", name, total))
			failed = append(failed, name)
			inputError = true
		}
	}

	// 1. queries: obs query --live
	co := check.Options{Dir: *dir, SeedPath: *seedPath, VLSettings: *vlSettings, TempoConfig: *tempoConfig, Base: *base, Names: *names, EventRule: *eventRule, Obs: of, Out: stdout, NoOtherContracts: *contracts == "none", AllowEmpty: true}
	rep, err := queryReport(set, co, true)
	if err != nil {
		fmt.Fprintf(stderr, "obs test: queries: %v\n", err)
		part("queries", "the live run could not start (exit 2)", 2)
	} else {
		rep.Print(stdout)
		rc := 0
		if rep.Failures() > 0 {
			rc = 1
		}
		part("queries", rep.Totals(), rc)
	}

	// 2. alerts: obs alerts (a folder with no alert rule has nothing to run; its incidents are then uncaught)
	noRules := len(set.Rules) == 0
	var res alerts.RunResult
	if noRules {
		fmt.Fprintf(stdout, "note: no alert rules under %s/alerts: obs alerts has nothing to run\n", *dir)
		part("alerts", "0 alerts", 0)
	} else {
		ao := alerts.Options{Dir: *dir, SeedPath: *seedPath, Out: *out, Steps: *steps, Recorded: *recorded, VLSettings: *vlSettings, TempoConfig: *tempoConfig, Obs: of, Base: *base, Workers: *workers, EventRule: *eventRule, AllowEmptyExpect: true}
		var rc int
		res, rc = alerts.CheckResult(ao, stdout, stderr)
		total := "the run stopped before its verdicts"
		if res.Judged {
			total = fmt.Sprintf("%d alerts (%d recording rules), %d runs, %d expectations, %d failed", res.Alerts, res.Records, res.Runs, res.Expectations, res.Failed)
		}
		part("alerts", total, rc)
	}

	// 3. links: obs links. Skipped only when nothing could be linked: the seed has no traces AND the contract
	// declares no spans AND no query is TraceQL. A contract that declares spans (or a TraceQL query) with a seed
	// that has no traces is a real finding of obs links, so obs test runs it.
	sd, serr := seed.Load(sp)
	if serr == nil && sd.Traces.IsZero() && !tracesExpected(set, *dir) {
		lines = append(lines, "obs test: links: skipped: the seed has no traces and nothing declares or queries any (no contract span, no TraceQL query), so there is no trace, log or profile link to follow")
	} else {
		lr, seedFile, rc := linksReport(*dir, *seedPath, *base, stderr)
		if lr == nil {
			part("links", "the links run could not start (exit 2)", rc)
		} else {
			printLinkFindings(lr, seedFile, stdout)
			if len(lr.Findings) > 0 {
				rc = 1
			}
			part("links", fmt.Sprintf("trace-to-logs=%d metric-to-traces=%d span-to-profile=%d, %d failures", lr.Checked["trace-to-logs"], lr.Checked["metric-to-traces"], lr.Checked["span-to-profile"], len(lr.Findings)), rc)
		}
	}

	// 4. incidents: every incident is caught by at least one alert
	switch {
	case res.Judged:
		inc := incidentPart(res.Incidents, res.Verdicts, false, stdout)
		part("incidents", inc.total, inc.rc)
	case noRules && serr == nil:
		incs, ierr := seed.LoadIncidentsForTest(sd, *dir)
		if ierr != nil {
			fmt.Fprintf(stderr, "obs test: incidents: %v\n", ierr)
			part("incidents", "the incident files could not be read (exit 2)", 2)
			break
		}
		inc := incidentPart(incs, nil, true, stdout)
		part("incidents", inc.total, inc.rc)
	default:
		lines = append(lines, "obs test: incidents: not judged: obs alerts did not reach its verdicts")
	}

	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	switch {
	case len(failed) == 0:
		fmt.Fprintln(stdout, "obs test: ok")
		return 0
	case inputError:
		fmt.Fprintf(stdout, "obs test: FAILED: %s\n", strings.Join(failed, ", "))
		return 2
	}
	fmt.Fprintf(stdout, "obs test: FAILED: %s\n", strings.Join(failed, ", "))
	return 1
}

// tracesExpected is true when the folder declares spans in contract.yaml or has a TraceQL query, so a seed
// without traces cannot be skipped over. A contract that cannot be read counts as expecting them (obs links then says why).
func tracesExpected(set *files.Set, dir string) bool {
	for _, q := range set.Queries {
		if q.Kind == files.Traces {
			return true
		}
	}
	cpath := filepath.Join(dir, "contract.yaml")
	if _, err := os.Stat(cpath); err != nil {
		return !os.IsNotExist(err)
	}
	c, err := contract.Load(cpath)
	return err != nil || len(c.SpanOrder) > 0
}

type incidentResult struct {
	total string
	rc    int
}

// incidentPart prints one [incident-uncaught] line per incident no alert caught and returns the total.
func incidentPart(incs []seed.Incident, vs []alerts.Verdict, noRules bool, stdout io.Writer) incidentResult {
	un := obstest.Uncaught(incs, vs, noRules)
	for _, f := range un {
		fmt.Fprintln(stdout, f.Line())
	}
	notes := obstest.Disturbances(incs)
	for _, n := range notes {
		fmt.Fprintln(stdout, n.Line())
	}
	rc := 0
	if len(un) > 0 {
		rc = 1
	}
	total := fmt.Sprintf("%d incidents, %d caught, %d uncaught", len(incs), len(incs)-len(un)-len(notes), len(un))
	if len(notes) > 0 {
		total += fmt.Sprintf(", %d disturbance(s)", len(notes))
	}
	return incidentResult{total, rc}
}
