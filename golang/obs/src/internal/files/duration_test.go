package files

import (
	"testing"
	"time"

	"github.com/prometheus/common/model"
)

// ParseFor accepts exactly what Prometheus's model.ParseDuration accepts (Grafana reads a rule's for
// and keepFiringFor with it): units y w d h m s ms in that order, whole numbers, "0" alone, nothing else.
func TestParseForIsPrometheusDuration(t *testing.T) {
	want := map[string]time.Duration{
		"0": 0, "0m": 0, "10m": 10 * time.Minute, "1d": 24 * time.Hour, "1w": 7 * 24 * time.Hour,
		"1y": 365 * 24 * time.Hour, "2h30m": 150 * time.Minute, "90s": 90 * time.Second, "100ms": 100 * time.Millisecond,
		"1d12h": 36 * time.Hour, "1w2d3h4m5s6ms": 7*24*time.Hour + 2*24*time.Hour + 3*time.Hour + 4*time.Minute + 5*time.Second + 6*time.Millisecond,
		"1m30s": 90 * time.Second,
	}
	for in, d := range want {
		got, err := ParseFor(in)
		if err != nil || got != d {
			t.Errorf("ParseFor(%q) = %v, %v; want %v", in, got, err, d)
		}
	}
	for _, in := range []string{
		"", "soon", "10", "-5m", "+5m", "1M", "1.5d", "1.5h", ".5h", "1h1d", "1m1h", "1d1d", "5 m", " 5m", "5m ", "1µs", "1us", "1ns", "1D", "5minutes", "0.0", "1e3s",
		"99999999999999999999s", "999999999999y",
	} {
		if d, err := ParseFor(in); err == nil {
			t.Errorf("ParseFor(%q) = %v, want a refusal", in, d)
		}
	}
}

// Both ways against the real parser: on every input of a corpus, ParseFor and model.ParseDuration agree
// on accept or refuse and on the value (negatives are refused by both: Prometheus does not allow them).
func TestParseForAgreesWithModelParseDuration(t *testing.T) {
	for _, in := range []string{
		"0", "0s", "1ms", "1s", "59s", "60s", "1m", "1h", "24h", "1d", "1d1h", "1d12h", "1w", "1w1d", "52w", "1y", "1y1w1d1h1m1s1ms",
		"", "m", "1", "1M", "1h30", "h1", "1.5d", "-1d", "1d-1h", "1d 1h", "1h1d", "1ms1s", "10s5s", "007m", "1h0m", "00", "0d",
		"1µs", "1ns", "1u", "1Y", "99999999999y", "18446744073709551615s", "9223372036854775807ms",
	} {
		m, merr := model.ParseDuration(in)
		got, err := ParseFor(in)
		if (merr == nil) != (err == nil) || (err == nil && got != time.Duration(m)) {
			t.Errorf("%q: model says %v (%v), ParseFor says %v (%v)", in, time.Duration(m), merr, got, err)
		}
	}
}

// CR rules and the engines read with ParseRuleDuration: everything 0.5 accepted (Go's time.ParseDuration)
// plus Prometheus durations; a flat file is held to the stricter ParseFor, and what that passes is read
// to the same value here.
func TestParseRuleDurationKeepsEveryDurationZeroPointFiveAccepted(t *testing.T) {
	for _, in := range []string{"0", "10m", "90s", "1.5m", "1h30m", "2.5h", "1µs", "1us", "1ns", "100ms", "-5m", "+5m", ".5h"} {
		old, oerr := time.ParseDuration(in)
		got, err := ParseRuleDuration(in)
		if oerr == nil && (err != nil || got != old) {
			t.Errorf("%q: 0.5 read %v, ParseRuleDuration says %v (%v)", in, old, got, err)
		}
	}
	for in, d := range map[string]time.Duration{"1d12h": 36 * time.Hour, "1d": 24 * time.Hour, "1y": 365 * 24 * time.Hour, "1w": 7 * 24 * time.Hour} {
		if got, err := ParseRuleDuration(in); err != nil || got != d {
			t.Errorf("%q: %v %v, want %v", in, got, err, d)
		}
	}
	for _, in := range []string{"", "soon", "10", "1M", "1h1d x"} {
		if d, err := ParseRuleDuration(in); err == nil {
			t.Errorf("%q = %v, want a refusal", in, d)
		}
	}
	for _, in := range []string{"0", "0s", "1m", "1d", "1d12h", "1y", "2h30m", "100ms", "1w2d"} {
		a, aerr := ParseFor(in)
		b, berr := ParseRuleDuration(in)
		if aerr != nil || berr != nil || a != b {
			t.Errorf("%q: ParseFor %v (%v), ParseRuleDuration %v (%v)", in, a, aerr, b, berr)
		}
	}
}
