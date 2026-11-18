package live

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Shape is how one metric behaves over a window, in the five parts the owner asked for. The
// field names follow the seed's shape settings (seed.ShapeSettings of the obs-regroup-look
// branch) so a real column and a seed column line up. All times are UTC.
type Shape struct {
	Points int         `json:"points"`
	From   int64       `json:"from_unix,omitempty"`
	To     int64       `json:"to_unix,omitempty"`
	Level  ShapeLevel  `json:"level"`
	Trend  ShapeTrend  `json:"trend"`
	Cycle  ShapeCycle  `json:"cycle"`
	Spikes ShapeSpikes `json:"spikes"`
	Gaps   ShapeGaps   `json:"gaps"`
	Noise  float64     `json:"noise"` // typical size of the wiggle left after the hour-of-day shape, as a fraction of typical
}

// ShapeLevel: lowest, typical (the median) and highest value.
type ShapeLevel struct {
	Min     float64 `json:"min"`
	Typical float64 `json:"typical"`
	Max     float64 `json:"max"`
}

// ShapeTrend: change per day as a fraction of the typical value (0.03 is up 3 % a day).
// Known is false when the window is under 2 days.
type ShapeTrend struct {
	ChangePerDay float64 `json:"change_per_day"`
	Known        bool    `json:"known"`
}

// ShapeCycle: DailyAmplitude is half the peak-to-trough distance of the average hour of day over the
// average (0.4 is +-40 % around the mean), PeakHour is the UTC hour of the peak, WeekendFactor is the
// weekend average over the weekday average (nil under 7 days). DailyKnown is false under 2 days.
type ShapeCycle struct {
	DailyAmplitude float64  `json:"daily_amplitude"`
	PeakHour       float64  `json:"peak_hour"`
	DailyKnown     bool     `json:"daily_known"`
	WeekendFactor  *float64 `json:"weekend_factor,omitempty"`
}

// ShapeSpikes: how many separate upward jumps, and Size, the biggest one as a multiple of the typical
// value of that hour.
type ShapeSpikes struct {
	Count int     `json:"count"`
	Size  float64 `json:"size"`
}

// ShapeGaps: runs of two or more steps without data (and data that stopped before the window end),
// and their total length as a duration string such as 15h or 11m (like seed.ShapeGaps.Length).
type ShapeGaps struct {
	Count  int    `json:"count"`
	Length string `json:"length"`
	total  time.Duration
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

// Analyze describes samples taken every step up to windowEnd. Samples with no data at a step are
// simply absent: that is how gaps are found.
func Analyze(s []Sample, step time.Duration, windowEnd time.Time) Shape {
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

	// Trend: least squares slope per day over the typical value.
	if span >= 48*time.Hour {
		sh.Trend.Known = true
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
	if span >= 48*time.Hour {
		sh.Cycle.DailyKnown = true
		var all, peak, trough float64
		peak, trough = math.Inf(-1), math.Inf(1)
		for _, x := range s {
			all += x.V
		}
		mean := all / float64(len(s))
		for h, v := range hourVals {
			sum := 0.0
			for _, x := range v {
				sum += x
			}
			m := sum / float64(len(v))
			if m > peak || (m == peak && float64(h) < sh.Cycle.PeakHour) {
				peak, sh.Cycle.PeakHour = m, float64(h)
			}
			trough = math.Min(trough, m)
		}
		if mean != 0 {
			sh.Cycle.DailyAmplitude = (peak - trough) / (2 * math.Abs(mean))
		}
	}
	if span >= 7*24*time.Hour {
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
	if sh.Cycle.DailyKnown {
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

	// Gaps: two or more steps without data.
	stepS := int64(step.Seconds())
	if stepS > 0 {
		for i := 1; i < len(s); i++ {
			missing := (s[i].T-s[i-1].T)/stepS - 1
			if missing >= 2 {
				sh.Gaps.Count++
				sh.Gaps.total += time.Duration(missing) * step
			}
		}
		if tail := (windowEnd.Unix() - sh.To) / stepS; tail >= 2 {
			sh.Gaps.Count++
			sh.Gaps.total += time.Duration(tail) * step
		}
	}
	sh.Gaps.Length = DurText(sh.Gaps.total)
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
