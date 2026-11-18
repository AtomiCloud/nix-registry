package vm

import (
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/seed"
)

func tinyDataset(t *testing.T) (*seed.Dataset, time.Time) {
	t.Helper()
	end := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	var ts []int64
	var vals []float64
	for i := 0; i < 60; i++ {
		ts = append(ts, end.Add(-time.Duration(60-i)*time.Minute).UnixMilli())
		vals = append(vals, float64(i))
	}
	s := &seed.Series{Labels: []seed.Label{{Name: "__name__", Value: "demo_total"}}, Ts: ts, Vals: vals}
	return &seed.Dataset{End: end, Series: map[string]*seed.Series{"demo_total": s}}, end
}

// A points-per-series limit below what the window needs is an error that names the
// obs.yaml key, never a VictoriaMetrics "BUG: too many points" panic.
func TestRangeAboveMaxPointsIsAnErrorNamingTheKey(t *testing.T) {
	ds, end := tinyDataset(t)
	e, err := New(ds)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, limit := range []int{1, 2, 100} {
		e.SetMaxPoints(limit)
		// one hour at one point per second is 3601 points
		for name, run := range map[string]func() error{
			"Range": func() error { _, err := e.Range("demo_total", end.Add(-time.Hour), end, time.Second); return err },
			"SamplesRange": func() error {
				_, err := e.SamplesRange("demo_total", end.Add(-time.Hour), end, time.Second)
				return err
			},
		} {
			err := run()
			if err == nil {
				t.Errorf("limit %d %s: 3601 points were accepted", limit, name)
				continue
			}
			for _, want := range []string{"thresholds.metrics.max_points_per_series", "3601", "limit"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("limit %d %s: error lacks %q: %v", limit, name, want, err)
				}
			}
		}
	}
}

// The default limit and a window that fits the limit still run; an instant query is one point.
func TestRangeWithinMaxPointsRuns(t *testing.T) {
	ds, end := tinyDataset(t)
	e, err := New(ds)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := e.Range("demo_total", end.Add(-time.Hour), end, time.Minute); err != nil {
		t.Fatalf("default limit: %v", err)
	}
	e.SetMaxPoints(61) // exactly the 61 points of one hour at one minute
	if _, err := e.Range("demo_total", end.Add(-time.Hour), end, time.Minute); err != nil {
		t.Fatalf("limit 61 with 61 points: %v", err)
	}
	e.SetMaxPoints(60)
	if _, err := e.Range("demo_total", end.Add(-time.Hour), end, time.Minute); err == nil {
		t.Fatal("limit 60 with 61 points was accepted")
	}
	e.SetMaxPoints(1)
	if _, err := e.Instant("demo_total", end); err != nil {
		t.Fatalf("an instant query is one point: %v", err)
	}
}
