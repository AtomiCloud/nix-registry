package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

// A CR rule with a fractional for (1.5m) passed 0.5, which read durations with Go's time.ParseDuration.
// The sandbox keeps reading it: the 0.5 fallback of files.ParseRuleDuration is protected here, through
// both call sites (the live window and the native forecast).
func fractionalForDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "observability")
	if err := os.CopyFS(dir, os.DirFS("../../../tests/fixtures/look-seeded")); err != nil {
		t.Fatal(err)
	}
	alert := filepath.Join(dir, "alerts", "errors", "warning.yaml")
	b, err := os.ReadFile(alert)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Replace(string(b), "for: 1m", "for: 1.5m", 1)
	if out == string(b) {
		t.Fatal("the fixture has no for: 1m")
	}
	if err := os.WriteFile(alert, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSeedWindowReadsAFractionalCRFor(t *testing.T) {
	dir := fractionalForDir(t)
	sd, err := seed.Load(dir + "/seed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	incs, err := seed.LoadIncidents(sd, dir)
	if err != nil {
		t.Fatal(err)
	}
	set, err := files.Load(dir)
	if err != nil || len(set.Problems) != 0 {
		t.Fatalf("%v %v", err, set.Problems)
	}
	// A 3m incident: half of it (90s) is below for + 2 intervals (90s + 20s), so the pending period decides the window.
	incs[0].Window.Duration = "3m"
	now := time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC)
	from, to, _, err := seedWindow(sd, incs, set, now, 0, true)
	if err != nil {
		t.Fatalf("for: 1.5m read by 0.5 must still be read: %v", err)
	}
	if want := 3*time.Minute + 110*time.Second; to.Sub(from) != want {
		t.Fatalf("window %v, want start 3m + 110s (1m30s for + 2 x 10s) = %v", to.Sub(from), want)
	}
}

func TestNativeForecastReadsAFractionalCRFor(t *testing.T) {
	dir := fractionalForDir(t)
	orig := "../../../tests/fixtures/look-seeded"
	sd, err := seed.Load(orig + "/seed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	incs, err := seed.LoadIncidents(sd, orig)
	if err != nil {
		t.Fatal(err)
	}
	origSet, err := files.Load(orig)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	from, to, _, err := seedWindow(sd, incs, origSet, now, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := seed.BuildIncidents(sd, from, incs)
	if err != nil {
		t.Fatal(err)
	}
	clipDataset(ds, to)
	set, err := files.Load(dir)
	if err != nil || len(set.Problems) != 0 {
		t.Fatalf("%v %v", err, set.Problems)
	}
	rules, err := nativeForecast(dir, dir+"/seed.yaml", set, ds, now, incs[0].Expect.Fire)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if rule.UID == "payment-pending" {
			want := now.Add(firingDeadline(90*time.Second, 10*time.Second)).UnixMilli()
			if rule.For != "1m30s" || rule.FiringAt != want {
				t.Fatalf("for 1.5m: For %q FiringAt %d, want 1m30s and %d (%+v)", rule.For, rule.FiringAt, want, rule)
			}
			return
		}
	}
	t.Fatal("pending rule forecast absent")
}
