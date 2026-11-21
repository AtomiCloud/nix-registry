package sandbox

import (
	"fmt"
	"sort"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

// seedWindow places the latest incident over now while loading only data up to
// now. An explicit seed base keeps the caller's historical timeline.
func seedWindow(sd *seed.Seed, incs []seed.Incident, set *files.Set, now time.Time, base int64, live bool) (from, to time.Time, name string, err error) {
	to = now.UTC().Truncate(time.Minute)
	if base != 0 {
		from = time.Unix(base, 0).UTC()
		to = from.Add(sd.Duration())
		if to.After(now) {
			return from, to, "", fmt.Errorf("--seed-base %d: seed ends in the future", base)
		}
		return from, to, "", nil
	}
	if !live || len(incs) == 0 {
		return to.Add(-sd.Duration()), to, "", nil
	}
	last := &incs[0]
	endOf := func(i *seed.Incident) time.Duration {
		s, _ := time.ParseDuration(i.Window.Start)
		d, _ := time.ParseDuration(i.Window.Duration)
		return s + d
	}
	for j := range incs {
		if endOf(&incs[j]) > endOf(last) {
			last = &incs[j]
		}
	}
	start, _ := time.ParseDuration(last.Window.Start)
	duration, _ := time.ParseDuration(last.Window.Duration)
	required := time.Duration(0)
	for _, r := range set.Rules {
		uid := r.Definition.UID
		if uid == "" {
			uid = r.Name
		}
		for _, id := range last.Expect.Fire {
			if uid != id {
				continue
			}
			pending := time.Duration(0)
			if r.Definition.For != "" {
				pending, err = files.ParseRuleDuration(r.Definition.For)
				if err != nil {
					return from, to, "", fmt.Errorf("%s rule %s: invalid for %q", r.File, id, r.Definition.For)
				}
			}
			interval := time.Minute
			if r.Interval != "" {
				interval, err = time.ParseDuration(r.Interval)
				if err != nil || interval <= 0 {
					return from, to, "", fmt.Errorf("%s rule %s: invalid interval %q", r.File, id, r.Interval)
				}
			}
			required = max(required, pending+2*interval)
		}
	}
	open := max(required, duration/2)
	if open >= duration {
		return from, to, "", fmt.Errorf("incidents/%s.yaml: live window %s must exceed for + 2 evaluation intervals (%s); use --seed-base for historical replay", last.Name, last.Window.Duration, required)
	}
	to = now.UTC().Truncate(time.Second)
	return to.Add(-start - open), to, last.Name, nil
}

// clipDataset prevents future samples, logs or spans from reaching a real store.
func clipDataset(ds *seed.Dataset, to time.Time) {
	ds.End = to
	for _, sr := range ds.Series {
		j := sort.Search(len(sr.Ts), func(j int) bool { return sr.Ts[j] > to.UnixMilli() })
		sr.Ts = sr.Ts[:j]
		sr.Vals = sr.Vals[:j]
	}
	for _, st := range ds.Streams {
		j := sort.Search(len(st.Rows), func(j int) bool { return st.Rows[j].TsNs > to.UnixNano() })
		st.Rows = st.Rows[:j]
	}
	traces := ds.Traces[:0]
	for _, tr := range ds.Traces {
		spans := tr.Spans[:0]
		for _, sp := range tr.Spans {
			if sp.StartNs > uint64(to.UnixNano()) {
				continue
			}
			sp.DurNs = min(sp.DurNs, uint64(to.UnixNano())-sp.StartNs)
			spans = append(spans, sp)
		}
		tr.Spans = spans
		if len(spans) > 0 {
			traces = append(traces, tr)
		}
	}
	ds.Traces = traces
}
