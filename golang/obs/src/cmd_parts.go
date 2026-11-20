package main

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/AtomiCloud/obs/internal/check"
	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/links"
	"github.com/AtomiCloud/obs/internal/seed"
)

// The parts shared by the old commands and the regrouped ones: `obs query --static` is the query half
// of `obs check`; `obs query --live`, `obs alerts` and `obs links` are the three parts of `obs test`.
// Each old command prints exactly what it printed before (tests/compat.sh proves it).

// queryReport runs the query rules the way `obs query --static` (live false) and `obs query --live`
// (live true) do: a live run also applies the static rules. A live error is the caller's exit 2.
func queryReport(set *files.Set, o check.Options, live bool) (*check.Report, error) {
	rep := check.Static(set, o)
	if live {
		lr, err := check.Live(set, o)
		if err != nil {
			return nil, err
		}
		// A live run also applies the static rules: a query that does not parse cannot run.
		rep.AppendLive(lr)
	}
	return rep, nil
}

// linksReport builds the seed and runs the link checks of `obs links`. A non-zero rc is the exit code
// of an input error, already explained on stderr.
func linksReport(dir, file string, base int64, stderr io.Writer) (rep *links.Report, seedFile string, rc int) {
	if file == "" {
		file = filepath.Join(dir, "seed.yaml")
	}
	s, err := seed.Load(file)
	if err != nil {
		fmt.Fprintln(stderr, "obs links:", err)
		return nil, file, 2
	}
	start := time.Unix(base, 0).UTC()
	if base == 0 {
		start = time.Now().UTC().Truncate(time.Minute).Add(-s.Duration() - 10*time.Minute)
	}
	if start.Add(s.Duration()).After(time.Now()) {
		fmt.Fprintln(stderr, "obs links: seed ends in the future")
		return nil, file, 2
	}
	ds, err := seed.Build(s, start)
	if err != nil {
		fmt.Fprintf(stderr, "obs links: %s: %v\n", file, err)
		return nil, file, 2
	}
	l, err := vl.New(ds)
	if err != nil {
		fmt.Fprintln(stderr, "obs links:", err)
		return nil, file, 2
	}
	defer l.Close()
	return links.Check(ds, l, tempo.NewTempoEngine(ds.Traces)), file, 0
}

// printLinkFindings prints the FAIL lines of a links report.
func printLinkFindings(r *links.Report, file string, stdout io.Writer) {
	for _, f := range r.Findings {
		fmt.Fprintf(stdout, "FAIL %s %s: [%s] %s; query: %s\n", file, f.Subject, f.Kind, f.Error, f.Query)
	}
}
