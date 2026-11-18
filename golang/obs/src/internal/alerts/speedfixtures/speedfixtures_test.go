package speedfixtures

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/check"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
	"gopkg.in/yaml.v3"
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
	for _, bad := range realLandscapes {
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

// realLandscape is a real landscape name; fixtures, tests and the README use made-up ones.
// (Built from two halves so this file does not contain the word.)
var realLandscape = "pi" + "chu"

// realLandscapes is every banned name: the three real landscapes. Each is built from two halves so this
// file stays the only place that spells them, and the scan below skips none of the others.
var realLandscapes = []string{realLandscape, "prim" + "ordial", "char" + "lotte"}

// TestNoRealLandscapeAnywhere scans every fixture, every Go test and the README, not only the
// speed-fixture seed.
func TestNoRealLandscapeAnywhere(t *testing.T) {
	const readmePath = "../../../../README.md"         // golang/obs/README.md, from src/internal/alerts/speedfixtures
	roots := []string{"../../..", "../../../../tests"} // the source root and the tests folder beside it
	var scanned int
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue // a build from src/ alone has no tests/ folder
		}
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == "third_party" || d.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			inTests := strings.Contains(filepath.ToSlash(p), "/tests/") || strings.HasSuffix(root, "tests")
			if !inTests && !strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			scanned++
			if filepath.Base(p) == "speedfixtures_test.go" && strings.Contains(filepath.ToSlash(p), "/alerts/speedfixtures/") {
				return nil // the banned-name list itself: the one allowed mention
			}
			low := strings.ToLower(string(b))
			for _, bad := range realLandscapes {
				if strings.Contains(low, bad) {
					t.Errorf("%s names the real landscape %q: use a made-up one such as fixture-dev", p, bad)
				}
			}
			if strings.Contains(strings.ToLower(filepath.ToSlash(p)), "charlotte") || strings.Contains(strings.ToLower(filepath.ToSlash(p)), "primordial") {
				t.Errorf("%s: the path names a real landscape", p)
			}
			return nil
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no files")
	}
	// golang/obs/README.md (the nix build copies it beside the tests folder). The tests folder is there, so the
	// README must be too: a missing README is a failure, never a silent skip.
	if _, err := os.Stat("../../../../tests"); err != nil {
		return // a build from src/ alone has no tests/ folder and no README beside it
	}
	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("cannot read golang/obs/README.md at %s: %v (the scan would silently skip it)", readmePath, err)
	}
	for _, bad := range realLandscapes {
		if strings.Contains(strings.ToLower(string(b)), bad) {
			t.Errorf("%s names the real landscape %q", readmePath, bad)
		}
	}
}

// An incident that no alert of its set is expected to fire for is a deliberate non-incident disturbance
// (it proves the alerts stay quiet): `obs test` needs the marker, with a reason, to leave it out of
// [incident-uncaught]. An incident with an alert in expect.fire must not carry it.
func TestQuietOnlyIncidentsCarryADisturbanceReason(t *testing.T) {
	sets := map[string]map[string]string{}
	for _, n := range Sizes {
		sets["n"+itoa(n)] = Incidents(n)
	}
	for _, n := range []int{1, 20} {
		sets["t"+itoa(n)] = TraceIncidents(n)
	}
	quietOnly := 0
	for set, incs := range sets {
		for name, body := range incs {
			marked := strings.Contains(body, "\ndisturbance: ")
			if strings.Contains(body, "  fire: []\n") {
				quietOnly++
				if !marked {
					t.Errorf("%s/%s has no alert in expect.fire and no disturbance reason", set, name)
				}
			} else if marked {
				t.Errorf("%s/%s has an alert in expect.fire and still carries a disturbance reason", set, name)
			}
		}
	}
	if quietOnly == 0 {
		t.Fatal("no quiet-only incident found: the fixture shape changed")
	}
}

// The checked-in fixtures (tests/fixtures/alerts-speed): the same rule, read from the files `obs test` reads.
func TestCheckedInQuietOnlyIncidentsCarryADisturbanceReason(t *testing.T) {
	const checkedIn = "../../../../tests/fixtures/alerts-speed"
	if _, err := os.Stat(checkedIn); err != nil {
		t.Skip("a build from src/ alone has no tests/ folder")
	}
	files, _ := filepath.Glob(filepath.Join(checkedIn, "*", "incidents", "*.yaml"))
	if len(files) == 0 {
		t.Fatal("found no checked-in incident files")
	}
	quietOnly := 0
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var inc seed.Incident
		if err := yaml.Unmarshal(b, &inc); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(inc.Expect.Fire) == 0 {
			quietOnly++
			if strings.TrimSpace(inc.Disturbance) == "" {
				t.Errorf("%s: no alert in expect.fire and no disturbance reason", p)
			}
		} else if inc.Disturbance != "" {
			t.Errorf("%s: an alert is in expect.fire and the file still carries a disturbance reason", p)
		}
	}
	if quietOnly == 0 {
		t.Fatal("no quiet-only incident among the checked-in files: the fixture shape changed")
	}
}
