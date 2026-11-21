// obs checks observability queries and links with embedded store engines,
// seeded data, no network and no containers.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sort"

	"github.com/AtomiCloud/obs/internal/check"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/pins"
	"github.com/AtomiCloud/obs/internal/sandbox"
)

// version is set by the nix build (-X main.version=...).
var version = "dev"

const usage = `obs: the observability check tool

usage:
  obs query --static [--dir observability] [--names] [--event-rule auto|on|off] [--contracts auto|none]
  obs query --live   [--dir observability] [--seed seed.yaml] [--base unix] [--names] [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml]
  obs alerts [--dir observability] [--seed seed.yaml] [--base unix] [--out results.json] [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml]
              [--workers n] [--steps steps.json] [--recorded recorded.json]
  obs assert --series FILE [--dir observability] [--pods n] [--unknown warn|fail|ignore]
  obs profile summarize|baseline|compare   (obs profile help)
  obs check [--all] [--dir observability] [--pods n] [--landscapes n] [--names] [--event-rule auto|on|off] [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml] [--contracts auto|none]
              (limits, query rules and the evaluator versions against <dir>/obs.yaml: everything obs query --static does)
  obs test  [--all] [--dir observability] [--seed seed.yaml] [--base unix] [--names] [--event-rule auto|on|off] [--vl-settings victorialogs-settings.yaml] [--tempo-config tempo.yaml]
              [--contracts auto|none] [--workers n] [--out results.json] [--steps steps.json] [--recorded recorded.json]
              (obs query --live + obs alerts + obs links, and every incident is caught by an alert)
  obs gen [--check] [--all] [--dir observability] [--lang go,dotnet,dart,bun]
  obs sandbox up [--dir observability] [--seed seed.yaml] [--project name] [--port-base n] [--runtime docker|podman] [--reseed]
  obs sandbox down|status [--dir observability] [--project name] [--runtime docker|podman]
  obs scrape --target <url> [--label name=value] [--interval 15s] [--once] [--job name] [--vm-url url] [--dir observability] [--project name]
  obs look   [--base origin/main] [--all] [--json] [--keep] [--seed-base unix] [--file dashboards/x.json] [--checklist file.md] [--out obs-look] [--width 1600] [--height 1000]
           [--dir observability] [--seed seed.yaml] [--project name] [--port-base n] [--runtime docker|podman] [--reseed]
  obs links [--dir observability] [--seed seed.yaml] [--base unix]
  obs live usage|shape|incidents|look --target local|dev|stage|prod   (read-only; obs live help)
  obs version
  obs store move <store> --config move.yaml [--dry-run] [--rollback]
  obs symbols put|decode --store <dir> --file <file> [options]
  obs size --req-per-second <n> --services <n> --pods <n> --profiled-pods <n>
  obs engines --check-chart <chart folder>

exit 0 when clean, 1 when a check failed, 2 on a usage or input error.
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-version":
		printVersion(stdout)
		return 0
	case "alerts":
		return runAlerts(args[1:], stdout, stderr)
	case "profile":
		return runProfile(args[1:], stdout, stderr)
	case "scrape":
		return runScrape(args[1:], stdout, stderr)
	case "sandbox":
		return runSandbox(args[1:], stdout, stderr)
	case "look":
		return runLook(args[1:], stdout, stderr)
	case "live":
		return runLive(args[1:], stdout, stderr)
	case "links":
		return runLinks(args[1:], stdout, stderr)
	case "query":
		return runQuery(args[1:], stdout, stderr)
	case "assert":
		return runAssert(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "test":
		return runTest(args[1:], stdout, stderr)
	case "gen":
		return runGen(args[1:], stdout, stderr)
	case "store":
		return runStore(args[1:], stdout, stderr)
	case "symbols":
		return runSymbols(args[1:], stdout, stderr)
	case "size":
		return runSize(args[1:], stdout, stderr)
	case "engines":
		return runEngines(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "obs: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "obs %s\n", version)
	p := pins.All()
	var names []string
	for name := range p {
		names = append(names, name)
	}
	sort.Strings(names)
	// Built-in engines and what each evaluates; every other pin is a reference
	// (sandbox / parity containers only).
	roles := map[string]string{
		"VictoriaMetrics": "MetricsQL",
		"VictoriaLogs":    "LogsQL, vendored in third_party/VictoriaLogs",
		"Tempo":           "TraceQL",
		"Grafana":         "alert math and state machine",
	}
	for _, name := range names {
		kind := "reference"
		if roles[name] != "" {
			kind = "engine"
		}
		fmt.Fprintf(w, "%s %s %s", kind, name, p[name].Version)
		switch {
		case p[name].Commit != "" && roles[name] != "":
			fmt.Fprintf(w, " (tag commit %s; %s)", p[name].Commit, roles[name])
		case p[name].Commit != "":
			fmt.Fprintf(w, " (tag commit %s)", p[name].Commit)
		case roles[name] != "":
			fmt.Fprintf(w, " (%s)", roles[name])
		}
		fmt.Fprintln(w)
	}
	for _, line := range sandbox.PinLines() {
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w, "licence AGPL-3.0-only; source: https://github.com/AtomiCloud/nix-registry/tree/main/golang/obs (each release also attaches a source and vendored-modules tarball)")
	if bi, ok := debug.ReadBuildInfo(); ok {
		fmt.Fprintf(w, "go %s\n", bi.GoVersion)
	}
}

func runQuery(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs query", flag.ContinueOnError)
	fs.SetOutput(stderr)
	static := fs.Bool("static", false, "parse every query with the embedded engines' parsers and apply the query rules")
	live := fs.Bool("live", false, "run every alert and dashboard query on the seed; each must return rows")
	dir := fs.String("dir", "observability", "the observability folder")
	seedPath := fs.String("seed", "", "seed file (default <dir>/seed.yaml)")
	vlSettings := fs.String("vl-settings", "", "VictoriaLogs chart args YAML; overrides obs.yaml thresholds.logs (default <dir>/victorialogs-settings.yaml, else obs.yaml, else pinned defaults)")
	tempoConfig := fs.String("tempo-config", "", "Tempo config file whose query_frontend end cutoff and metrics limits trace panels run under; overrides obs.yaml thresholds.traces (default Tempo 3.1.1: 30s, 24h, 10000)")
	base := fs.Int64("base", 0, "unix seconds of the seed's start (default: ends in the past)")
	names := fs.Bool("names", false, "print the names each query reads")
	eventRule := fs.String("event-rule", "auto", "a LogsQL alert must name a contract event: auto (only when <dir>/contract.yaml exists), on, off")
	contracts := fs.String("contracts", "auto", "which contracts to read: auto (this folder's and every other observability/contract.yaml of the repository) or none (this folder's only)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *contracts != "auto" && *contracts != "none" {
		fmt.Fprintf(stderr, "obs query: --contracts %q: want auto or none\n", *contracts)
		return 2
	}
	if *static == *live {
		fmt.Fprintln(stderr, "obs query: give exactly one of --static or --live")
		return 2
	}
	switch *eventRule {
	case "auto", "on", "off":
	default:
		fmt.Fprintf(stderr, "obs query: --event-rule %q: want auto, on or off\n", *eventRule)
		return 2
	}
	of, ok := loadObsYaml(*dir, "obs query", stderr)
	if !ok {
		return 2
	}
	if rc := gateObsYaml(of, "obs query", stdout); rc != 0 {
		return rc
	}
	o := check.Options{Dir: *dir, SeedPath: *seedPath, VLSettings: *vlSettings, TempoConfig: *tempoConfig, Base: *base, Names: *names, EventRule: *eventRule, Obs: of, Out: stdout, NoOtherContracts: *contracts == "none"}
	set, err := files.Load(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "obs query: %v\n", err)
		return 2
	}
	cmd := "obs query --static"
	if *live {
		cmd = "obs query --live"
	}
	rep, err := queryReport(set, o, *live) // cmd_parts.go
	if err != nil {
		fmt.Fprintf(stderr, "obs query --live: %v\n", err)
		return 2
	}
	rep.Print(stdout)
	fmt.Fprintln(stdout, rep.Summary(cmd))
	if rep.Failures() > 0 {
		return 1
	}
	return 0
}
