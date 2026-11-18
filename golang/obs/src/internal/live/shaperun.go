package live

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/seed"
)

// ShapeOptions of ShapeReport.
type ShapeOptions struct {
	Window, Step time.Duration
	Only         []string // contract metric names; empty: every metric
	AllServices  bool
	Seed         *seed.Seed // nil: no seed side
	SeedNote     string     // why there is no seed side
	Now          func() time.Time
}

// MetricShape is one metric: the real data and the seed side by side, from the same query.
type MetricShape struct {
	Metric   string `json:"metric"`
	Kind     string `json:"kind"`
	Query    string `json:"query"`
	Real     *Shape `json:"real"`
	RealNote string `json:"real_note,omitempty"`
	Seed     *Shape `json:"seed"`
	SeedNote string `json:"seed_note,omitempty"`
}

// ShapeResult of `obs live shape`.
type ShapeResult struct {
	Target     string        `json:"target"`
	Window     string        `json:"window"`
	Step       string        `json:"step"`
	SeedLength string        `json:"seed_length,omitempty"`
	Scope      string        `json:"scope"`
	Metrics    []MetricShape `json:"metrics"`
}

// shapeQuery is the one expression read on both sides: a counter is its summed rate per second, a
// histogram the summed rate of its observations (_count), a gauge its summed value.
func shapeQuery(ct *contract.Contract, name string, m contract.Metric, scope string, step time.Duration) string {
	win := step
	if win < 2*time.Minute {
		win = 2 * time.Minute
	}
	stored := contract.StoredNames(name, m)
	sel := func(n string) string {
		if scope == "" {
			return n
		}
		return n + "{" + strings.TrimPrefix(scope, ",") + "}"
	}
	switch m.Kind {
	case contract.MetricHistogram:
		for _, s := range stored {
			if strings.HasSuffix(s, "_count") {
				return fmt.Sprintf("sum(rate(%s[%ds]))", sel(s), int(win.Seconds()))
			}
		}
	case contract.MetricGauge:
		return "sum(" + sel(stored[0]) + ")"
	}
	return fmt.Sprintf("sum(rate(%s[%ds]))", sel(stored[0]), int(win.Seconds()))
}

// ShapeReport reads the shape of every contract metric on the target and, when a seed is given,
// on the seed's own data, and returns both. It does not judge.
func ShapeReport(ctx context.Context, c *Client, t *Target, ct *contract.Contract, o ShapeOptions) (*ShapeResult, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	now := o.Now()
	res := &ShapeResult{Target: t.Name, Window: o.Window.String(), Step: o.Step.String()}
	scope := ""
	if !o.AllServices && ct.Service != "" {
		scope = fmt.Sprintf(`,"durian.cloud.service"=%s`, strconv.Quote(ct.Service))
		res.Scope = fmt.Sprintf("series of service %q only", ct.Service)
	} else {
		res.Scope = "every series on the target"
	}
	names := ct.MetricOrder
	if len(o.Only) > 0 {
		var keep []string
		for _, w := range o.Only {
			found := false
			for _, n := range names {
				if ct.Metrics[n].NameOr(n) == w || n == w {
					keep, found = append(keep, n), true
				}
			}
			if !found {
				return nil, fmt.Errorf("--metric %q is not in the contract (metrics: %s)", w, strings.Join(names, ", "))
			}
		}
		names = keep
	}
	var eng *vm.Engine
	var ds *seed.Dataset
	if o.Seed != nil {
		res.SeedLength = o.Seed.Length
		start := now.UTC().Truncate(time.Minute).Add(-o.Seed.Duration() - 10*time.Minute)
		d, err := seed.Build(o.Seed, start)
		if err != nil {
			return nil, fmt.Errorf("seed: %v", err)
		}
		e, err := vm.New(d)
		if err != nil {
			return nil, fmt.Errorf("seed: %v", err)
		}
		defer e.Close()
		eng, ds = e, d
	}
	failed := 0
	var lastErr error
	for _, n := range names {
		m := ct.Metrics[n]
		ms := MetricShape{Metric: m.NameOr(n), Kind: m.Kind, Query: shapeQuery(ct, m.NameOr(n), m, scope, o.Step)}
		mats, err := c.Range(ctx, t.Metrics, ms.Query, now.Add(-o.Window), now, o.Step)
		switch {
		case err != nil:
			ms.RealNote, lastErr = err.Error(), err
			failed++
		case len(mats) == 0 || len(mats[0].Samples) == 0:
			ms.RealNote = "the target has no data for this metric in the window"
		default:
			sh := Analyze(mats[0].Samples, o.Step, now.Add(-o.Window), now)
			ms.Real = &sh
		}
		switch {
		case eng != nil:
			rs, err := eng.SamplesRange(ms.Query, ds.Start, ds.End, o.Step)
			if err != nil {
				ms.SeedNote = err.Error()
			} else if len(rs) == 0 {
				ms.SeedNote = "the seed has no data for this metric"
			} else {
				var ss []Sample
				for i, ts := range rs[0].Ts {
					if v := rs[0].Vals[i]; !math.IsNaN(v) {
						ss = append(ss, Sample{T: ts / 1000, V: v})
					}
				}
				sh := Analyze(ss, o.Step, ds.Start, ds.End)
				ms.Seed = &sh
			}
		default:
			ms.SeedNote = o.SeedNote
		}
		res.Metrics = append(res.Metrics, ms)
	}
	if failed > 0 && failed == len(names) {
		return nil, fmt.Errorf("cannot read the target's metrics: %w", lastErr)
	}
	sort.SliceStable(res.Metrics, func(i, j int) bool { return res.Metrics[i].Metric < res.Metrics[j].Metric })
	return res, nil
}

func pct(f float64) string { return fmt.Sprintf("%+.1f%%", f*100) }

func levelText(s *Shape) string {
	return fmt.Sprintf("min %s, typical %s, max %s", num(s.Level.Min), num(s.Level.Typical), num(s.Level.Max))
}

func trendText(s *Shape) string {
	if !s.Trend.Known {
		return "n/a (under 2 days)"
	}
	return pct(s.Trend.ChangePerDay) + " a day"
}

func cycleText(s *Shape) string {
	if !s.Cycle.DailyKnown {
		return "n/a (under 2 days)"
	}
	out := fmt.Sprintf("daily +-%.0f%%, peak %02.0f:00", s.Cycle.DailyAmplitude*100, s.Cycle.PeakHour)
	if s.Cycle.WeekendFactor != nil {
		out += fmt.Sprintf(", weekend x%.2f", *s.Cycle.WeekendFactor)
	} else {
		out += ", weekend n/a (under 7 days)"
	}
	return out
}

func noiseText(s *Shape) string { return fmt.Sprintf("%.1f%% of typical", s.Noise*100) }

func spikesText(s *Shape) string {
	if s.Spikes.Count == 0 {
		return "none"
	}
	return fmt.Sprintf("%d, biggest %sx typical", s.Spikes.Count, num(s.Spikes.Size))
}

func gapsText(s *Shape) string {
	if s.Gaps.Count == 0 {
		return "none"
	}
	return fmt.Sprintf("%d, %s in all", s.Gaps.Count, s.Gaps.Length)
}

// Print writes the side by side text. It states, never judges.
func (r *ShapeResult) Print(w io.Writer) {
	fmt.Fprintf(w, "obs live shape: target %s, window %s, step %s, %s; times are UTC\n", r.Target, r.Window, r.Step, r.Scope)
	if r.SeedLength != "" {
		fmt.Fprintf(w, "seed side: the seed's own %s window, run through the same query (a seed shorter than 2 days cannot show a trend or a daily cycle)\n", r.SeedLength)
	}
	for _, m := range r.Metrics {
		fmt.Fprintf(w, "\n%s (%s)\n  query: %s\n", m.Metric, m.Kind, m.Query)
		cell := func(s *Shape, note string, f func(*Shape) string) string {
			if s == nil {
				if note == "" {
					note = "no data"
				}
				return "n/a: " + note
			}
			return f(s)
		}
		fmt.Fprintf(w, "  %-7s %-52s | %s\n", "part", "real", "seed")
		for _, row := range []struct {
			name string
			f    func(*Shape) string
		}{{"level", levelText}, {"trend", trendText}, {"cycle", cycleText}, {"spikes", spikesText}, {"gaps", gapsText}, {"noise", noiseText}} {
			fmt.Fprintf(w, "  %-7s %-52s | %s\n", row.name, cell(m.Real, m.RealNote, row.f), cell(m.Seed, m.SeedNote, row.f))
		}
	}
	fmt.Fprintln(w, "\nobs live shape: nothing is judged here; read the two columns and decide if the seed is realistic")
}
