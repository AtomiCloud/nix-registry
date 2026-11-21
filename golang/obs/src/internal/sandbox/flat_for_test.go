package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

const flatPendingUID = "payments-checkout-pending-warning"

// flatFor1d copies the look-seeded fixture and adds a flat warning tier whose for is 1d.
func flatFor1d(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "observability")
	if err := os.CopyFS(dir, os.DirFS("../../../tests/fixtures/look-seeded")); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"obs.yaml": "platform: payments\nservice: checkout\n",
		"alerts/pending/warning.yaml": "title: Payment errors sustained\ndatasource: metrics\n" +
			"expr: 'sum(rate(checkout_requests_total{outcome=\"error\"}[5m])) > 100'\nfor: 1d\nsummary: s\ndescription: d\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A flat rule with for: 1d is a duration the sandbox reads like obs alerts does: the live window is
// opened wide enough to hold the 24h pending period plus two evaluation intervals.
func TestSeedWindowHoldsAFlatRulesDayLongPendingPeriod(t *testing.T) {
	dir := flatFor1d(t)
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
	// The incident must outlast the pending period for the live window to fit it.
	incs[0].Window.Duration = "30h"
	incs[0].Expect.Fire = []string{flatPendingUID}
	now := time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC)
	from, to, live, err := seedWindow(sd, incs, set, now, 0, true)
	if err != nil {
		t.Fatalf("for: 1d must be read as 24h: %v", err)
	}
	// from = to - start - open, open = for + 2 intervals (the flat rule's interval is 1m)
	if want := 3*time.Minute + 24*time.Hour + 2*time.Minute; to.Sub(from) != want || live != "payment-spike" {
		t.Fatalf("window %v (from %v to %v live %q), want %v: the 24h pending period held", to.Sub(from), from, to, live, want)
	}
}

// The native forecast says the same: a rule with for: 1d is Firing 24h plus the first evaluation after provisioning.
func TestNativeForecastReadsAFlatRulesDayLongPendingPeriod(t *testing.T) {
	dir := flatFor1d(t)
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
	rules, err := nativeForecast(dir, dir+"/seed.yaml", set, ds, now, []string{flatPendingUID})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if rule.UID == flatPendingUID {
			want := now.Add(firingDeadline(24*time.Hour, time.Minute)).UnixMilli()
			if rule.For != "24h0m0s" || rule.FiringAt != want {
				t.Fatalf("for 1d: For %q FiringAt %d, want 24h0m0s and %d (%+v)", rule.For, rule.FiringAt, want, rule)
			}
			return
		}
	}
	t.Fatal("flat rule forecast absent")
}
