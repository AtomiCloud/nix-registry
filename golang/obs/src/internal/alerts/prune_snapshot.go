package alerts

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
)

type pruneSeriesSnapshot struct {
	Labels map[string]string `json:"labels"`
	Points [][2]any          `json:"points"`
	Nulls  []int64           `json:"nulls"`
}
type pruneTickSnapshot struct {
	Series []pruneSeriesSnapshot `json:"series"`
	Error  string                `json:"error,omitempty"`
}
type pruneQuerySnapshot struct {
	Series  []pruneSeriesSnapshot        `json:"series"`
	Ticks   map[string]pruneTickSnapshot `json:"ticks,omitempty"`
	Error   string                       `json:"error,omitempty"`
	Range   bool                         `json:"range"`
	RangeNS int64                        `json:"range_ns"`
	StepNS  int64                        `json:"step_ns"`
}

func snapshotResults(rs []Result) []pruneSeriesSnapshot {
	out := make([]pruneSeriesSnapshot, 0, len(rs))
	for _, r := range rs {
		s := pruneSeriesSnapshot{Labels: map[string]string{}, Points: make([][2]any, 0, len(r.Points)), Nulls: []int64{}}
		for _, l := range r.Labels {
			s.Labels[l.Name] = l.Value
		}
		var ts []int64
		for t := range r.Points {
			ts = append(ts, t)
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
		for _, t := range ts {
			s.Points = append(s.Points, [2]any{t, jf(r.Points[t])})
		}
		for t, null := range r.Nulls {
			if null {
				s.Nulls = append(s.Nulls, t)
			}
		}
		sort.Slice(s.Nulls, func(i, j int) bool { return s.Nulls[i] < s.Nulls[j] })
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := json.Marshal(out[i])
		b, _ := json.Marshal(out[j])
		return string(a) < string(b)
	})
	return out
}

// capturePruneSnapshot keeps the raw inputs the evaluator actually read. Capture
// each recording stage before cache invalidation, then the final alert stage:
// state-only comparisons could miss a wrong numeric input hidden by a threshold.
// No history or serialization is allocated outside the explicit verify mode.
func (c *queryCache) capturePruneSnapshot(stage string) {
	if os.Getenv("OBS_VERIFY_PRUNE_SNAPSHOT") == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pruneHistory == nil {
		c.pruneHistory = map[string]pruneQuerySnapshot{}
	}
	for k, e := range c.m {
		if e.d == nil {
			continue
		}
		d := e.d
		key, _ := json.Marshal(struct {
			Datasource, UID, Expr, Original, Range, Run string
			FromNS, ToNS, IntervalNS                    int64
		}{k.ds, k.uid, k.expr, k.original, k.rng, k.run, int64(k.from), int64(k.to), int64(k.interval)})
		s := pruneQuerySnapshot{Series: snapshotResults(d.res), Range: d.isRange, RangeNS: int64(d.rng), StepNS: int64(d.step)}
		if d.err != nil {
			s.Error = d.err.Error()
		}
		if len(d.perTick) > 0 || len(d.tickErr) > 0 {
			s.Ticks = map[string]pruneTickSnapshot{}
			for t, rs := range d.perTick {
				s.Ticks[strconv.FormatInt(t, 10)] = pruneTickSnapshot{Series: snapshotResults(rs)}
			}
			for t, err := range d.tickErr {
				if err != nil {
					key := strconv.FormatInt(t, 10)
					tick := s.Ticks[key]
					if tick.Series == nil {
						tick.Series = []pruneSeriesSnapshot{}
					}
					tick.Error = err.Error()
					s.Ticks[key] = tick
				}
			}
		}
		c.pruneHistory[stage+" "+string(key)] = s
	}
}
