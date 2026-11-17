package speedfixtures

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/check"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

func TestSetsLoad(t *testing.T) {
	out := t.TempDir()
	if err := Write(out); err != nil {
		t.Fatal(err)
	}
	for _, n := range Sizes {
		dir := filepath.Join(out, "n"+itoa(n))
		s, err := files.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Problems) != 0 {
			t.Fatalf("n%d: %v", n, s.Problems)
		}
		if s.Alerts != n {
			t.Errorf("n%d: %d alert files", n, s.Alerts)
		}
		logs := 0
		for _, q := range s.Queries {
			if q.Kind == files.Logs {
				logs++
				if !strings.Contains(q.Expr, "| stats") {
					t.Errorf("n%d %s: log query without stats: %s", n, q.Where, q.Expr)
				}
			}
			if strings.Contains(strings.ToLower(q.Expr), "count_over_time") || q.Kind == files.Loki {
				t.Errorf("n%d %s: LogQL left in: %s", n, q.Where, q.Expr)
			}
		}
		if n >= 20 && logs == 0 {
			t.Errorf("n%d: no log alerts", n)
		}
		for _, l := range Lengths {
			sd := filepath.Join(dir, "seed."+l+".yaml")
			if _, err := seed.Load(sd); err != nil {
				t.Errorf("n%d %s: %v", n, l, err)
			}
		}
	}
}

func TestVariantsDiffer(t *testing.T) {
	seen := map[string]string{}
	for rel, body := range Alerts(1000) {
		if prev, ok := seen[body]; ok {
			t.Fatalf("%s and %s are identical", rel, prev)
		}
		seen[body] = rel
	}
}

func TestNoRealNames(t *testing.T) {
	for _, bad := range []string{"pichu", "charlotte", "primordial"} {
		if strings.Contains(strings.ToLower(Seed("4h")), bad) {
			t.Errorf("seed names %q", bad)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestTraceSetsLoadAndPassStaticChecks(t *testing.T) {
	out := t.TempDir()
	if err := Write(out); err != nil {
		t.Fatal(err)
	}
	if len(traceBase) != 20 {
		t.Fatalf("%d base trace rules, want 20", len(traceBase))
	}
	for _, n := range TraceSizes {
		dir := filepath.Join(out, "t"+itoa(n))
		s, err := files.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Problems) != 0 || s.Alerts != n {
			t.Fatalf("t%d: %d alert files, problems %v", n, s.Alerts, s.Problems)
		}
		for _, q := range s.Queries {
			if q.Kind != files.Traces {
				t.Errorf("t%d %s: not a trace query: %s", n, q.Where, q.Expr)
			}
		}
		// Contract names, Tempo routing (metrics query, instant or exemplar-free range) and syntax.
		if r := check.Static(s, check.Options{Dir: dir}); r.Failures() != 0 {
			t.Fatalf("t%d static: %+v", n, r.Findings)
		}
		for _, l := range Lengths {
			sd, err := seed.Load(filepath.Join(dir, "seed."+l+".yaml"))
			if err != nil {
				t.Fatalf("t%d %s: %v", n, l, err)
			}
			if defs, err := sd.TraceDefs(); err != nil || len(defs) != 2 {
				t.Fatalf("t%d %s: trace definitions %v %v", n, l, defs, err)
			}
		}
	}
	// Every rule fires in at least one incident or is a declared always-quiet rule.
	quiet := 0
	for _, r := range traceBase {
		if len(r.fire) == 0 {
			quiet++
		}
	}
	if quiet != 3 {
		t.Fatalf("%d always-quiet trace rules, want 3 (traffic-low, refund-errors, slow-provider-calls)", quiet)
	}
}
