package seed

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func datasetHash(t *testing.T, ds *Dataset) string {
	t.Helper()
	b, err := json.Marshal(ds)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func TestLegacyShapeGolden(t *testing.T) {
	// These original full byte hashes were measured on amd64 before the change.
	// Go's pre-existing arm64 fused multiply-add math makes histogram _sum floats
	// differ from amd64 (already true in 0.4). The portable golden below still
	// pins counters, logs and histogram bucket/count bytes on every architecture.
	if runtime.GOARCH != "amd64" {
		t.Skip("full histogram _sum byte golden is amd64-specific: pre-existing arm64 fused multiply-add rounding differs; portable legacy bytes are checked separately")
	}
	want := map[string]string{"flat": "a68c095990e0d7e094ee0f0f833e9a15abe5b807e3065d1e94961d829ab601cb", "daily_wave": "bdd2ae689698682b98066ec7e5a31eb0237c77914e4e5d61525cb878c260926b"}
	for _, shape := range []string{"flat", "daily_wave"} {
		s := load(t, strings.Replace(doc, "requests: 5/s", "requests: 5/s, shape: "+shape, 1))
		ds, err := Build(s, time.Unix(1_700_000_000, 0).UTC())
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s sha256=%s", shape, datasetHash(t, ds))
		if got := datasetHash(t, ds); got != want[shape] {
			t.Errorf("legacy %s bytes changed: %s", shape, got)
		}
	}
}

func TestLegacyPortableGolden(t *testing.T) {
	// Captured from untouched 70b4358 sources, not from the new generator. Keep
	// every byte except the architecture-sensitive floating histogram sums.
	want := map[string]string{"flat": "32d7c48e185b32b442ead72424023af44363aeb7e89f75c74899430261b01e2f", "daily_wave": "14a10794cf351f75370f3dd8f71b48ae5473503a0c373c1d3c09f447e5ce91b0"}
	for _, shape := range []string{"flat", "daily_wave"} {
		s := load(t, strings.Replace(doc, "requests: 5/s", "requests: 5/s, shape: "+shape, 1))
		ds, err := Build(s, time.Unix(1_700_000_000, 0).UTC())
		if err != nil {
			t.Fatal(err)
		}
		for key, sr := range ds.Series {
			for _, label := range sr.Labels {
				if label.Name == "__name__" && strings.HasSuffix(label.Value, "_sum") {
					delete(ds.Series, key)
				}
			}
		}
		if got := datasetHash(t, ds); got != want[shape] {
			t.Errorf("legacy %s portable bytes changed: %s", shape, got)
		}
	}
}

const shapedDoc = `length: 48h
random_seed: 42
traffic:
  requests: 40/s
  settings:
    level: {min: 2, typical: 40, max: 310}
    trend: {change_per_day: 0.03}
    cycle: {daily_amplitude: 0.25, peak_hour: 20, weekend_factor: 0.6}
    spikes: {count: 4, size: 6}
    gaps: {count: 2, length: 11m}
    noise: 0.15
metrics:
  requests: {type: counter, series: [{where: {}, share: 1}]}
`

func TestShapeSettingsGolden(t *testing.T) {
	s := load(t, shapedDoc)
	// Use a small rate for the full generator; the settings themselves keep the documented numbers.
	s.Metrics["requests"] = MetricDef{Series: []SeriesDef{{Share: 0.001}}}
	ds, err := Build(s, time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	again, err := Build(s, ds.Start)
	if err != nil || !reflect.DeepEqual(ds, again) {
		t.Fatalf("same seed differs: %v", err)
	}
	t.Logf("settings sha256=%s", datasetHash(t, ds))
	if got := datasetHash(t, ds); got != "6bcee2676e7a418d1728e72d0d01756d331588212326ebd20a0301a00d764dc3" {
		t.Errorf("shape bytes changed: %s", got)
	}
	// Gaps remove samples, rather than making a zero-rate plateau.
	for _, sr := range ds.Series {
		gaps := 0
		missing := int64(0)
		for j := 1; j < len(sr.Ts); j++ {
			if sr.Ts[j]-sr.Ts[j-1] > 15000 {
				gaps++
				missing += sr.Ts[j] - sr.Ts[j-1] - 15000
			}
		}
		if gaps != 2 {
			t.Errorf("got %d visible gaps, want 2", gaps)
		}
		if missing != int64(11*time.Minute/time.Millisecond) {
			t.Errorf("gaps missing %d ms; want 11 minutes TOTAL", missing)
		}
	}
}

func TestShapePartsAndNoise(t *testing.T) {
	s := load(t, shapedDoc)
	r := newShapeRun(s, time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)) // Thursday
	r.spikes, r.gaps = nil, nil
	r.settings.Noise = 0
	peak, trough := r.rate(20*3600), r.rate(8*3600)
	if peak <= trough {
		t.Errorf("peak %g <= trough %g", peak, trough)
	}
	if r.rate(86400+20*3600) <= peak {
		t.Error("3%/day trend did not grow")
	}
	r.settings.Trend.ChangePerDay = 0
	r.settings.Noise = .15
	a, b := r.rate(100), r.rate(101)
	r.settings.Noise = 0
	if a == r.rate(100) || b == r.rate(101) {
		t.Error("noise did not change the rate")
	}
	r.settings.Cycle.DailyAmplitude = 0
	r.spikes = []shapeWindow{{time.Second, 2 * time.Second}}
	if r.rate(1) != 240 {
		t.Errorf("6x spike=%g", r.rate(1))
	}
	r.gaps = []shapeWindow{{2 * time.Second, 3 * time.Second}}
	if r.rate(2) != 0 || !r.gap(2*time.Second) {
		t.Error("gap has traffic")
	}
	r.start = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) // Saturday
	if r.rate(10) != 24 {
		t.Errorf("weekend factor=%g", r.rate(10))
	}
}

func TestShapeValidationLocation(t *testing.T) {
	for name, bad := range map[string]string{
		"level":     strings.Replace(shapedDoc, "min: 2", "min: 50", 1),
		"noise":     strings.Replace(shapedDoc, "noise: 0.15", "noise: -1", 1),
		"peak_hour": strings.Replace(shapedDoc, "peak_hour: 20", "peak_hour: 24", 1),
		"length":    strings.Replace(shapedDoc, "length: 11m", "length: forever", 1),
	} {
		f := filepath.Join(t.TempDir(), "seed.yaml")
		if err := os.WriteFile(f, []byte(bad), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(f)
		if err == nil || !strings.Contains(err.Error(), f+":") || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: expected file:line and field, got %v", name, err)
		}
	}
}
