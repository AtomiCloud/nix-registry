package seed

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ShapeSettings describes the same five parts that obs live shape measures.
// Level is requests/second; trend is a fractional change per day, daily amplitude
// and noise are fractions of typical. PeakHour is UTC. A spike lasts one minute;
// gap Length is total missing time. Settings are opt-in: legacy shapes keep their exact data.
type ShapeSettings struct {
	Level  ShapeLevel  `yaml:"level" json:"level"`
	Trend  ShapeTrend  `yaml:"trend" json:"trend"`
	Cycle  ShapeCycle  `yaml:"cycle" json:"cycle"`
	Spikes ShapeSpikes `yaml:"spikes" json:"spikes"`
	Gaps   ShapeGaps   `yaml:"gaps" json:"gaps"`
	Noise  float64     `yaml:"noise" json:"noise"`
}
type ShapeLevel struct {
	Min     float64 `yaml:"min" json:"min"`
	Typical float64 `yaml:"typical" json:"typical"`
	Max     float64 `yaml:"max" json:"max"`
}
type ShapeTrend struct {
	ChangePerDay float64 `yaml:"change_per_day" json:"change_per_day"`
}
type ShapeCycle struct {
	DailyAmplitude float64  `yaml:"daily_amplitude" json:"daily_amplitude"`
	PeakHour       float64  `yaml:"peak_hour" json:"peak_hour"`
	WeekendFactor  *float64 `yaml:"weekend_factor,omitempty" json:"weekend_factor,omitempty"`
}
type ShapeSpikes struct {
	Count int     `yaml:"count" json:"count"`
	Size  float64 `yaml:"size" json:"size"`
}
type ShapeGaps struct {
	Count  int    `yaml:"count" json:"count"`
	Length string `yaml:"length" json:"length"`
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (s *ShapeSettings) validate(length time.Duration) error {
	l := s.Level
	if !finite(l.Min) || !finite(l.Typical) || !finite(l.Max) || l.Min < 0 || l.Typical <= 0 || l.Min > l.Typical || l.Typical > l.Max {
		return fmt.Errorf("level: need finite 0 <= min <= typical <= max, typical > 0")
	}
	if !finite(s.Trend.ChangePerDay) || 1+s.Trend.ChangePerDay*length.Hours()/24 < 0 {
		return fmt.Errorf("trend.change_per_day: must be finite and keep the level nonnegative")
	}
	if !finite(s.Noise) || s.Noise < 0 || s.Noise > 1 {
		return fmt.Errorf("noise: need a fraction in [0,1]")
	}
	if !finite(s.Cycle.DailyAmplitude) || s.Cycle.DailyAmplitude < 0 || s.Cycle.DailyAmplitude > 1 {
		return fmt.Errorf("cycle.daily_amplitude: need a fraction in [0,1]")
	}
	if !finite(s.Cycle.PeakHour) || s.Cycle.PeakHour < 0 || s.Cycle.PeakHour >= 24 {
		return fmt.Errorf("cycle.peak_hour: need a UTC hour in [0,24)")
	}
	if v := s.Cycle.WeekendFactor; v != nil && (!finite(*v) || *v < 0) {
		return fmt.Errorf("cycle.weekend_factor: need a finite nonnegative factor")
	}
	if s.Spikes.Count < 0 || !finite(s.Spikes.Size) || (s.Spikes.Count > 0 && s.Spikes.Size < 1) || s.Spikes.Count > int(length/time.Minute)/2 {
		return fmt.Errorf("spikes: count must fit the seed and size must be at least 1 times typical")
	}
	d, err := time.ParseDuration(s.Gaps.Length)
	if s.Gaps.Count < 0 || (s.Gaps.Count > 0 && (err != nil || d/time.Duration(s.Gaps.Count) < sampleEvery || d > length/2)) {
		return fmt.Errorf("gaps.length: need total duration >= count * 15s and <= half the seed length")
	}
	return nil
}

func shapeErrorLine(root *yaml.Node, err error) int {
	keys := append([]string{"traffic", "settings"}, strings.Split(strings.SplitN(err.Error(), ":", 2)[0], ".")...)
	n := root
	if len(n.Content) > 0 && n.Kind == yaml.DocumentNode {
		n = n.Content[0]
	}
	for _, k := range keys {
		if n.Kind != yaml.MappingNode {
			break
		}
		found := false
		for j := 0; j+1 < len(n.Content); j += 2 {
			if n.Content[j].Value == k {
				n = n.Content[j+1]
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	return n.Line
}

type shapeWindow struct{ from, to time.Duration }
type shapeRun struct {
	settings     *ShapeSettings
	spikes, gaps []shapeWindow
	seed         uint64
	start        time.Time
}

func newShapeRun(s *Seed, start time.Time) *shapeRun {
	if s.Traffic.Settings == nil {
		return nil
	}
	r := &shapeRun{settings: s.Traffic.Settings, seed: s.RandomSeed, start: start.UTC()}
	// One event per disjoint cell keeps counts exact and leaves visible separation.
	windows := func(count int, width time.Duration, key string) []shapeWindow {
		var out []shapeWindow
		for j := 0; j < count; j++ {
			cell := s.Duration() / time.Duration(count+1)
			jitter := time.Duration(newRnd(s.RandomSeed, key, int64(j)).float() * float64(max(time.Duration(0), cell-width)))
			at := cell/2 + time.Duration(j)*cell + jitter
			out = append(out, shapeWindow{at, at + width})
		}
		return out
	}
	r.spikes = windows(r.settings.Spikes.Count, time.Minute, "shape/spikes")
	d, _ := time.ParseDuration(r.settings.Gaps.Length)
	if r.settings.Gaps.Count > 0 {
		d /= time.Duration(r.settings.Gaps.Count)
	}
	r.gaps = windows(r.settings.Gaps.Count, d, "shape/gaps")
	return r
}

func inWindows(at time.Duration, ws []shapeWindow) bool {
	j := sort.Search(len(ws), func(j int) bool { return ws[j].to > at })
	return j < len(ws) && ws[j].from <= at
}

func (r *shapeRun) gap(at time.Duration) bool { return r != nil && inWindows(at, r.gaps) }
func (r *shapeRun) rate(sec int64) float64 {
	s := r.settings
	at := time.Duration(sec) * time.Second
	if r.gap(at) {
		return 0
	}
	t := r.start.Add(at)
	hour := float64(t.Hour()) + float64(t.Minute())/60 + float64(t.Second())/3600
	v := s.Level.Typical * (1 + s.Trend.ChangePerDay*float64(sec)/86400)
	v *= 1 + s.Cycle.DailyAmplitude*math.Cos(2*math.Pi*(hour-s.Cycle.PeakHour)/24)
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		if f := s.Cycle.WeekendFactor; f != nil {
			v *= *f
		}
	}
	v += s.Level.Typical * s.Noise * (2*newRnd(r.seed, "shape/noise", sec).float() - 1)
	if inWindows(at, r.spikes) {
		v = max(v, s.Level.Typical*s.Spikes.Size)
	}
	return max(s.Level.Min, min(s.Level.Max, v))
}
