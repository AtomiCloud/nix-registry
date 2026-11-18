package live

import (
	"math"
	"testing"
	"time"
)

// monday is 2026-11-02 00:00 UTC, a Monday.
var monday = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC).Unix()

func series(days int, step time.Duration, f func(t int64, i int) float64) []Sample {
	var out []Sample
	n := int(time.Duration(days) * 24 * time.Hour / step)
	for i := 0; i < n; i++ {
		t := monday + int64(i)*int64(step.Seconds())
		out = append(out, Sample{T: t, V: f(t, i)})
	}
	return out
}

func end(s []Sample) time.Time { return time.Unix(s[len(s)-1].T, 0) }

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (+-%v)", name, got, want, tol)
	}
}

func TestShapeFlatSeriesHasNothingToReport(t *testing.T) {
	s := series(7, time.Hour, func(int64, int) float64 { return 100 })
	sh := Analyze(s, time.Hour, end(s))
	if sh.Level.Min != 100 || sh.Level.Typical != 100 || sh.Level.Max != 100 {
		t.Fatalf("level %+v", sh.Level)
	}
	near(t, "trend", sh.Trend.ChangePerDay, 0, 1e-9)
	near(t, "daily amplitude", sh.Cycle.DailyAmplitude, 0, 1e-9)
	if sh.Spikes.Count != 0 || sh.Gaps.Count != 0 {
		t.Fatalf("spikes %+v gaps %+v", sh.Spikes, sh.Gaps)
	}
}

func TestShapeTrendIsChangePerDayOverTypical(t *testing.T) {
	s := series(7, time.Hour, func(t int64, i int) float64 { return 100 + 3*float64(t-monday)/86400 })
	sh := Analyze(s, time.Hour, end(s))
	near(t, "change per day", sh.Trend.ChangePerDay, 3/sh.Level.Typical, 1e-6)
	if !sh.Trend.Known {
		t.Fatal("a week is enough to know the trend")
	}
}

func TestShapeDailyCycleAmplitudeAndPeakHour(t *testing.T) {
	s := series(7, time.Hour, func(t int64, i int) float64 {
		h := float64((t - monday) % 86400 / 3600)
		return 100 * (1 + 0.4*math.Sin(2*math.Pi*(h-2)/24))
	})
	sh := Analyze(s, time.Hour, end(s))
	near(t, "daily amplitude", sh.Cycle.DailyAmplitude, 0.4, 0.01)
	if sh.Cycle.PeakHour != 8 || !sh.Cycle.DailyKnown {
		t.Fatalf("peak hour: %+v", sh.Cycle)
	}
	if sh.Spikes.Count != 0 {
		t.Fatalf("a smooth daily wave has no spikes: %+v", sh.Spikes)
	}
}

func TestShapeWeekendFactor(t *testing.T) {
	s := series(14, time.Hour, func(t int64, i int) float64 {
		wd := time.Unix(t, 0).UTC().Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			return 60
		}
		return 100
	})
	sh := Analyze(s, time.Hour, end(s))
	if sh.Cycle.WeekendFactor == nil {
		t.Fatal("two weeks must give a weekend factor")
	}
	near(t, "weekend factor", *sh.Cycle.WeekendFactor, 0.6, 1e-9)
}

func TestShapeSpikesCountAndSize(t *testing.T) {
	s := series(7, time.Hour, func(t int64, i int) float64 {
		switch i {
		case 20, 90, 91: // 90 and 91 are one spike
			return 600
		case 140:
			return 400
		}
		return 100
	})
	sh := Analyze(s, time.Hour, end(s))
	if sh.Spikes.Count != 3 {
		t.Fatalf("want 3 spikes (20, 90-91, 140): %+v", sh.Spikes)
	}
	near(t, "biggest spike over typical", sh.Spikes.Size, 6, 1e-9)
}

func TestShapeGapsNeedTwoMissingSteps(t *testing.T) {
	s := series(7, time.Hour, func(int64, int) float64 { return 100 })
	var kept []Sample
	for i, x := range s {
		switch {
		case i >= 30 && i < 42: // 12 steps gone
		case i >= 80 && i < 83: // 3 steps gone
		case i == 120: // one step gone: not a gap
		default:
			kept = append(kept, x)
		}
	}
	sh := Analyze(kept, time.Hour, end(kept))
	if sh.Gaps.Count != 2 {
		t.Fatalf("gaps: %+v", sh.Gaps)
	}
	if sh.Gaps.Length != "15h" {
		t.Fatalf("total gap length: %v", sh.Gaps.Length)
	}
}

func TestShapeTrailingGapWhenDataStopped(t *testing.T) {
	s := series(2, time.Hour, func(int64, int) float64 { return 5 })
	sh := Analyze(s, time.Hour, end(s).Add(6*time.Hour))
	if sh.Gaps.Count != 1 || sh.Gaps.Length != "6h" {
		t.Fatalf("data that stopped 6 h before the window end is a gap: %+v", sh.Gaps)
	}
}

func TestShapeShortWindowSaysWhatItCannotKnow(t *testing.T) {
	s := series(1, 5*time.Minute, func(int64, int) float64 { return 7 })
	s = s[:48] // 4 hours
	sh := Analyze(s, 5*time.Minute, end(s))
	if sh.Trend.Known || sh.Cycle.DailyKnown || sh.Cycle.WeekendFactor != nil {
		t.Fatalf("4 hours know no trend, daily cycle or weekend: %+v", sh)
	}
}

func TestShapeNoSamples(t *testing.T) {
	sh := Analyze(nil, time.Hour, time.Unix(monday, 0))
	if sh.Points != 0 {
		t.Fatalf("%+v", sh)
	}
}

func TestShapeNoiseIsTheWiggleAsAFractionOfTypical(t *testing.T) {
	flat := series(7, time.Hour, func(int64, int) float64 { return 100 })
	if sh := Analyze(flat, time.Hour, end(flat)); sh.Noise != 0 {
		t.Fatalf("flat: %v", sh.Noise)
	}
	noisy := series(7, time.Hour, func(t int64, i int) float64 { return 100 + float64(i%5-2)*4 })
	sh := Analyze(noisy, time.Hour, end(noisy))
	if sh.Noise < 0.03 || sh.Noise > 0.08 {
		t.Fatalf("noise of +-8 around 100: %v", sh.Noise)
	}
}
