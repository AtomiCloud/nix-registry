package live

import (
	"fmt"
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
	sh := analyze(s, time.Hour)
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
	sh := analyze(s, time.Hour)
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
	sh := analyze(s, time.Hour)
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
	sh := analyze(s, time.Hour)
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
	sh := analyze(s, time.Hour)
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
	sh := analyze(kept, time.Hour)
	if sh.Gaps.Count != 2 {
		t.Fatalf("gaps: %+v", sh.Gaps)
	}
	if sh.Gaps.Length != "15h" {
		t.Fatalf("total gap length: %v", sh.Gaps.Length)
	}
}

func TestShapeTrailingGapWhenDataStopped(t *testing.T) {
	s := series(2, time.Hour, func(int64, int) float64 { return 5 })
	sh := Analyze(s, time.Hour, time.Unix(s[0].T, 0), end(s).Add(6*time.Hour))
	if sh.Gaps.Count != 1 || sh.Gaps.Length != "6h" {
		t.Fatalf("data that stopped 6 h before the window end is a gap: %+v", sh.Gaps)
	}
}

func TestShapeShortWindowSaysWhatItCannotKnow(t *testing.T) {
	s := series(1, 5*time.Minute, func(int64, int) float64 { return 7 })
	s = s[:48] // 4 hours
	sh := analyze(s, 5*time.Minute)
	if sh.Trend.Known || sh.Cycle.DailyKnown || sh.Cycle.WeekendFactor != nil {
		t.Fatalf("4 hours know no trend, daily cycle or weekend: %+v", sh)
	}
}

func TestShapeNoSamples(t *testing.T) {
	sh := Analyze(nil, time.Hour, time.Unix(monday, 0), time.Unix(monday, 0))
	if sh.Points != 0 {
		t.Fatalf("%+v", sh)
	}
}

func TestShapeNoiseIsTheWiggleAsAFractionOfTypical(t *testing.T) {
	flat := series(7, time.Hour, func(int64, int) float64 { return 100 })
	if sh := analyze(flat, time.Hour); sh.Noise != 0 {
		t.Fatalf("flat: %v", sh.Noise)
	}
	noisy := series(7, time.Hour, func(t int64, i int) float64 { return 100 + float64(i%5-2)*4 })
	sh := analyze(noisy, time.Hour)
	if sh.Noise < 0.03 || sh.Noise > 0.08 {
		t.Fatalf("noise of +-8 around 100: %v", sh.Noise)
	}
}

// analyze describes a series that was asked for over exactly its own length: the window starts at
// the first sample and ends one step after the last, as a range query of that many steps does.
func analyze(s []Sample, step time.Duration) Shape {
	return Analyze(s, step, time.Unix(s[0].T, 0), end(s).Add(step))
}

func TestShapePeakHourUsesTheSampleTimestamps(t *testing.T) {
	// A cosine that peaks at 20:00 UTC, sampled every 5 minutes starting at :04. Bucketing by the
	// clock hour put the peak in hour 19, whose samples straddle it from one side only.
	start := time.Date(2026, 11, 2, 0, 4, 0, 0, time.UTC).Unix()
	var s []Sample
	for i := 0; i < 3*288; i++ {
		ts := start + int64(i)*300
		h := float64(ts%86400) / 3600
		s = append(s, Sample{T: ts, V: 100 * (1 + 0.4*math.Cos(2*math.Pi*(h-20)/24))})
	}
	sh := analyze(s, 5*time.Minute)
	near(t, "peak hour", sh.Cycle.PeakHour, 20, 0.2)
	near(t, "daily amplitude", sh.Cycle.DailyAmplitude, 0.4, 0.02)
	if got := fmt.Sprintf("%02.0f", sh.Cycle.PeakHour); got != "20" {
		t.Fatalf("printed peak hour %q", got)
	}
}

func TestShapeLeadingGapIsCounted(t *testing.T) {
	// A 6 hour window on a store that holds only the last 4 hours: the first 2 hours are a gap.
	// Only gaps between samples and after the last one were counted ("gaps none").
	winStart := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	step := 5 * time.Minute
	var s []Sample
	for ts := winStart.Add(2 * time.Hour); !ts.After(winStart.Add(6 * time.Hour)); ts = ts.Add(step) {
		s = append(s, Sample{T: ts.Unix(), V: 10})
	}
	sh := Analyze(s, step, winStart, winStart.Add(6*time.Hour))
	if sh.Gaps.Count != 1 || sh.Gaps.Length != "2h" {
		t.Fatalf("the missing first two hours: %+v", sh.Gaps)
	}
	// both ends and the middle
	var m []Sample
	for _, x := range s {
		if x.T >= winStart.Add(3*time.Hour).Unix() && x.T < winStart.Add(3*time.Hour+30*time.Minute).Unix() {
			continue
		}
		if x.T > winStart.Add(5*time.Hour).Unix() {
			continue
		}
		m = append(m, x)
	}
	sh = Analyze(m, step, winStart, winStart.Add(6*time.Hour))
	if sh.Gaps.Count != 3 {
		t.Fatalf("leading, middle and trailing: %+v", sh.Gaps)
	}
}

func TestShapeWeeklyPartSurvivesASevenDayWindowMinusOneStep(t *testing.T) {
	// The first and last sample of a 7 day window read at 1 hour are 6 days 23 hours apart. The
	// weekly part (weekend factor) needed a span of 7 days and so vanished from the real week.
	s := series(7, time.Hour, func(t int64, i int) float64 {
		wd := time.Unix(t, 0).UTC().Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			return 60
		}
		return 100
	})
	win := 7 * 24 * time.Hour
	sh := Analyze(s, time.Hour, time.Unix(monday, 0), time.Unix(monday, 0).Add(win))
	if sh.Cycle.WeekendFactor == nil {
		t.Fatalf("a 7 day window with %d samples must show the weekend factor", len(s))
	}
	near(t, "weekend factor", *sh.Cycle.WeekendFactor, 0.6, 1e-9)
	// the same data in a 3 day window cannot show it, and a 7 day window with 3 days of data neither
	if sh := Analyze(s[:72], time.Hour, time.Unix(monday, 0), time.Unix(monday, 0).Add(72*time.Hour)); sh.Cycle.WeekendFactor != nil {
		t.Fatal("3 days know no weekend")
	}
	if sh := Analyze(s[:72], time.Hour, time.Unix(monday, 0), time.Unix(monday, 0).Add(win)); sh.Cycle.WeekendFactor != nil {
		t.Fatal("3 days of data in a 7 day window do not show a weekly cycle")
	}
	if !sh.Trend.Known || !sh.Cycle.DailyKnown {
		t.Fatalf("a week knows its trend and daily cycle: %+v", sh)
	}
}
