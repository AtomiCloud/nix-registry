// obs checks observability queries and links with embedded store engines,
// seeded data, no network and no containers.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/AtomiCloud/obs/internal/check"
	"github.com/AtomiCloud/obs/internal/files"
)

// version is set by the nix build (-X main.version=...).
var version = "dev"

// Engine pins: each equals the version in the charts (concept section 1).
const (
	pinVictoriaMetrics = "v1.156.0"
	pinVictoriaLogs    = "v1.54.0 (tag commit 6433dae5, vendored in third_party/VictoriaLogs)"
)

const usage = `obs: the observability check tool

usage:
  obs query --static [--dir observability] [--names] [--event-rule auto|on|off]
  obs query --live   [--dir observability] [--seed seed.yaml] [--base unix] [--names]
  obs alerts [--dir observability] [--seed seed.yaml] [--base unix] [--out results.json]
  obs check [--dir observability] [--pods n] [--landscapes n]
  obs gen [--check] [--dir observability] [--lang go,dotnet,dart,bun]
  obs links [--dir observability] [--seed seed.yaml] [--base unix]
  obs version

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

	case "links":
		return runLinks(args[1:], stdout, stderr)
	case "query":
		return runQuery(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "gen":
		return runGen(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "obs: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "obs %s\n", version)
	fmt.Fprintln(w, "engine Tempo 3.1.1 (commit 9db7df79d2af, TraceQL)")
	fmt.Fprintf(w, "engine VictoriaMetrics %s (MetricsQL)\n", pinVictoriaMetrics)
	fmt.Fprintf(w, "engine VictoriaLogs %s (LogsQL)\n", pinVictoriaLogs)
	fmt.Fprintln(w, "engine Grafana 13.3.0 (commit 752e3462; alert math and state machine)")
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
	base := fs.Int64("base", 0, "unix seconds of the seed's start (default: ends in the past)")
	names := fs.Bool("names", false, "print the names each query reads")
	eventRule := fs.String("event-rule", "auto", "a LogsQL alert must name a contract event: auto (only when <dir>/contract.yaml exists), on, off")
	if err := fs.Parse(args); err != nil {
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
	o := check.Options{Dir: *dir, SeedPath: *seedPath, Base: *base, Names: *names, EventRule: *eventRule, Out: stdout}
	set, err := files.Load(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "obs query: %v\n", err)
		return 2
	}
	cmd := "obs query --static"
	rep := check.Static(set, o)
	if *live {
		cmd = "obs query --live"
		lr, err := check.Live(set, o)
		if err != nil {
			fmt.Fprintf(stderr, "obs query --live: %v\n", err)
			return 2
		}
		// A live run also applies the static rules: a query that does not parse cannot run.
		rep.Findings = append(rep.Findings, lr.Findings...)
		rep.Counts = lr.Counts
	}
	rep.Print(stdout)
	fmt.Fprintln(stdout, rep.Summary(cmd))
	if rep.Failures() > 0 {
		return 1
	}
	return 0
}
