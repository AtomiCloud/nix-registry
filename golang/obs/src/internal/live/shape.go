package live

import (
	"github.com/AtomiCloud/obs/internal/seed"
	"math"
	"sort"
	"strings"
	"time"
)

// Shape is how one metric behaves over a window, in the five parts the owner asked for. The parts
// ARE seed.ShapeSettings (level, trend, cycle, spikes, gaps, noise): the real column and the seed
// column of `obs live shape` are both a Shape, so a column can be pasted into a seed's
// traffic.settings and read back with the same names. What a window is too short to show is flagged
// beside the settings (TrendKnown, DailyKnown; a nil weekend factor is under 7 days). All times are UTC.
//
//   - level: lowest, typical (the median) and highest value
//   - trend.change_per_day: a fraction of typical (0.03 is up 3 % a day)
//   - cycle: daily_amplitude is half the peak-to-trough of the average hour of day over the mean,
//     peak_hour the UTC hour of the peak, weekend_factor the weekend over the weekday average
//   - spikes: how many separate upward jumps, and the biggest as a multiple of that hour's typical value
//   - gaps: runs of two or more steps without data (and data that stopped before the window end) and
//     their total length as a duration string such as 15h or 11m
//   - noise: the wiggle left after the hour-of-day shape, as a fraction of typical
type Shape struct {
	Points int   `json:"points"`
	From   int64 `json:"from_unix,omitempty"`
	To     int64 `json:"to_unix,omitempty"`
	seed.ShapeSettings
	TrendKnown bool `json:"trend_known"` // false when the window is under 2 days
	DailyKnown bool `json:"daily_known"` // false when the window is under 2 days
	gapTotal   time.Duration
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	if len(c)%2 == 1 {
		return c[len(c)/2]
	}
	return (c[len(c)/2-1] + c[len(c)/2]) / 2
}

func hourOf(t int64) int { return int(((t % 86400) + 86400) % 86400 / 3600) }

// peakHourOf is the UTC hour of day at which the daily wave peaks, from the first harmonic of the
// samples' own timestamps (so a sample at 20:04 counts at 20:04, not in hour 19 or 20). It is
// rounded to a tenth of an hour. A flat series has no peak: 0.
func peakHourOf(s []Sample, mean float64) float64 {
	const w = 2 * math.Pi / 86400
	var a, b, scale float64
	for _, x := range s {
		tod := float64(((x.T % 86400) + 86400) % 86400)
		d := x.V - mean
		a += d * math.Cos(w*tod)
		b += d * math.Sin(w*tod)
		scale += math.Abs(d)
	}
	if scale == 0 || math.Hypot(a, b) < 1e-9*scale {
		return 0
	}
	h := math.Atan2(b, a) / w / 3600
	if h < 0 {
		h += 24
	}
	h = math.Round(h*10) / 10
	if h >= 24 {
		h = 0
	}
	return h
}

// Analyze describes samples taken every step in the window windowStart..windowEnd. Samples with
// no data at a step are simply absent: that is how gaps are found, at the start and the end of
// the window as well as between samples. What the window is long enough to show (trend and daily
// cycle: two days, weekly cycle: a week) is judged on the window asked for, not on the first and
// last sample: a seven day window read at a step holds samples that span seven days minus one step.
func Analyze(s []Sample, step time.Duration, windowStart, windowEnd time.Time) Shape {
	var sh Shape
	sh.Points = len(s)
	if len(s) == 0 {
		return sh
	}
	sort.Slice(s, func(i, j int) bool { return s[i].T < s[j].T })
	sh.From, sh.To = s[0].T, s[len(s)-1].T
	vals := make([]float64, len(s))
	sh.Level.Min, sh.Level.Max = s[0].V, s[0].V
	for i, x := range s {
		vals[i] = x.V
		sh.Level.Min = math.Min(sh.Level.Min, x.V)
		sh.Level.Max = math.Max(sh.Level.Max, x.V)
	}
	typical := median(vals)
	sh.Level.Typical = typical
	span := time.Duration(sh.To-sh.From) * time.Second
	window := windowEnd.Sub(windowStart)
	if windowStart.IsZero() || window < span {
		window = span + step
	}
	covers := func(need time.Duration) bool { return window >= need && span >= need-2*step }

	// Trend: least squares slope per day over the typical value.
	if covers(48 * time.Hour) {
		sh.TrendKnown = true
		var sx, sy, sxx, sxy float64
		n := float64(len(s))
		for _, x := range s {
			d := float64(x.T-sh.From) / 86400
			sx, sy, sxx, sxy = sx+d, sy+x.V, sxx+d*d, sxy+d*x.V
		}
		if den := n*sxx - sx*sx; den != 0 && typical != 0 {
			sh.Trend.ChangePerDay = (n*sxy - sx*sy) / den / typical
		}
	}

	// Cycle: average per hour of day, and weekend over weekday.
	hourVals := map[int][]float64{}
	for _, x := range s {
		hourVals[hourOf(x.T)] = append(hourVals[hourOf(x.T)], x.V)
	}
	if covers(48 * time.Hour) {
		sh.DailyKnown = true
		var all, peak, trough float64
		peak, trough = math.Inf(-1), math.Inf(1)
		for _, x := range s {
			all += x.V
		}
		mean := all / float64(len(s))
		for _, v := range hourVals {
			sum := 0.0
			for _, x := range v {
				sum += x
			}
			m := sum / float64(len(v))
			peak, trough = math.Max(peak, m), math.Min(trough, m)
		}
		if mean != 0 {
			sh.Cycle.DailyAmplitude = (peak - trough) / (2 * math.Abs(mean))
		}
		sh.Cycle.PeakHour = peakHourOf(s, mean)
	}
	if covers(7 * 24 * time.Hour) {
		var we, wd []float64
		for _, x := range s {
			switch time.Unix(x.T, 0).UTC().Weekday() {
			case time.Saturday, time.Sunday:
				we = append(we, x.V)
			default:
				wd = append(wd, x.V)
			}
		}
		if len(we) > 0 && len(wd) > 0 {
			mean := func(v []float64) float64 {
				t := 0.0
				for _, x := range v {
					t += x
				}
				return t / float64(len(v))
			}
			if m := mean(wd); m != 0 {
				f := mean(we) / m
				sh.Cycle.WeekendFactor = &f
			}
		}
	}

	// Spikes: a point far above the typical value of its hour (its day-shape), merged when adjacent.
	base := func(t int64) float64 { return typical }
	if sh.DailyKnown {
		hm := map[int]float64{}
		for h, v := range hourVals {
			hm[h] = median(v)
		}
		base = func(t int64) float64 { return hm[hourOf(t)] }
	}
	dev := make([]float64, len(s))
	abs := make([]float64, len(s))
	for i, x := range s {
		dev[i] = x.V - base(x.T)
		abs[i] = math.Abs(dev[i] - 0) // deviation from the hour's typical value
	}
	mad := 1.4826 * median(abs)
	if typical != 0 {
		sh.Noise = mad / math.Abs(typical)
	}
	thr := math.Max(5*mad, 0.5*math.Abs(typical))
	inSpike := false
	var peakRatio float64
	for i, x := range s {
		spike := dev[i] > thr && dev[i] > 0
		if spike {
			b := base(x.T)
			if b <= 0 {
				b = typical
			}
			r := 0.0
			if b > 0 {
				r = x.V / b
			}
			if !inSpike {
				sh.Spikes.Count++
				inSpike = true
			}
			peakRatio = math.Max(peakRatio, r)
		} else {
			inSpike = false
		}
	}
	sh.Spikes.Size = peakRatio

	// Gaps: two or more steps without data, before the first sample and after the last one too.
	stepS := int64(step.Seconds())
	if stepS > 0 {
		if !windowStart.IsZero() {
			if lead := (sh.From - windowStart.Unix()) / stepS; lead >= 2 {
				sh.Gaps.Count++
				sh.gapTotal += time.Duration(lead) * step
			}
		}
		for i := 1; i < len(s); i++ {
			missing := (s[i].T-s[i-1].T)/stepS - 1
			if missing >= 2 {
				sh.Gaps.Count++
				sh.gapTotal += time.Duration(missing) * step
			}
		}
		if tail := (windowEnd.Unix() - sh.To) / stepS; tail >= 2 {
			sh.Gaps.Count++
			sh.gapTotal += time.Duration(tail) * step
		}
	}
	sh.Gaps.Length = DurText(sh.gapTotal)
	return sh
}

// DurText writes a duration the way the seed files do: 90m, 15h, 45s.
func DurText(d time.Duration) string {
	s := d.Round(time.Second).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
