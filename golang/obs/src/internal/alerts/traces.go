// SPDX-License-Identifier: AGPL-3.0-only
// Grafana expression/state port. Source read: grafana v13.3.0 (commit 752e3462).
// See gfexpr/LICENSE-grafana-AGPL and gfexpr/PROVENANCE.md for source and changes.
package alerts

// TraceQL alert queries on the embedded Tempo 3.1.1 engine, plugged in through the
// DatasourceEngine hook. Each evaluation sends what the Grafana Tempo datasource
// sends (internal/tempoalert): an instant (/api/metrics/query) or range
// (/api/metrics/query_range) TraceQL metrics request over [now-from, now-to] in
// whole seconds. Tempo's query-frontend then applies query_end_cutoff (30 s by
// default) against its own time.Now(), which obs takes to be the evaluation tick:
// an instant end is pulled in by whole seconds, a range end to the step below
// now - cutoff, and a window inside the cutoff is an error (engines/tempo/alertapi.go).
// The plugin answers with time-series frames, which Grafana's expression engine
// turns into series (pkg/expr/converter.go:69-118, v13.3.0), so a trace query is a
// Series input to math/reduce/threshold, never a Number.

import (
	"fmt"
	"slices"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/seed"
	"github.com/AtomiCloud/obs/internal/tempoalert"
)

type tracesEngine struct {
	e *tempo.TempoEngine
	// fe is the Tempo query-frontend config (--tempo-config); nil means Tempo's defaults.
	fe *tempo.Frontend
}

func (t tracesEngine) frontend() tempo.Frontend {
	if t.fe != nil {
		return *t.fe
	}
	return tempo.DefaultFrontend
}

// seriesFrames marks a datasource whose per-tick answer is Grafana series frames.
type seriesFrames interface{ seriesFrames() }

func (tracesEngine) seriesFrames() {}

func (t tracesEngine) Instant(q Query, at time.Time) ([]Result, error) {
	if q.Tempo == nil {
		return nil, fmt.Errorf("traces query has no Tempo model")
	}
	start, end := tempoalert.Window(at, q.From, q.To)
	var frames []tempoalert.Frame
	if q.Tempo.Instant() {
		resp, err := t.e.QueryInstant(t.frontend(), at, q.Expr, start, end)
		if err != nil {
			return nil, fmt.Errorf("failed to execute TraceQL query: %s: %w", q.Expr, err)
		}
		frames = tempoalert.InstantFrames(resp, at)
	} else {
		step, err := q.Tempo.RangeStep(start, end)
		if err != nil {
			return nil, err
		}
		resp, err := t.e.QueryRange(t.frontend(), at, q.Expr, start, end, step)
		if err != nil {
			return nil, fmt.Errorf("failed to execute TraceQL query: %s: %w", q.Expr, err)
		}
		frames = tempoalert.RangeFrames(resp)
	}
	out := make([]Result, 0, len(frames))
	for _, f := range frames {
		r := Result{Points: make(map[int64]float64, len(f.Points))}
		for _, l := range f.Labels {
			r.Labels = append(r.Labels, Label{Name: l.Name, Value: l.Value})
		}
		for _, p := range f.Points {
			r.Points[p.Ms] = p.V
		}
		out = append(out, r)
	}
	return out, nil
}

// traceSourcesChanged reports whether an incident changes a seeded series that is the
// source of a trace definition: only those series make different traces (each
// series draws its trace randomness from its own stream, seed/synth.go).
func traceSourcesChanged(p *Project, inc *Incident) bool {
	defs, err := p.Seed.TraceDefs()
	if err != nil {
		return true
	}
	for _, c := range inc.Change {
		for _, sr := range p.Seed.Metrics[c.Metric].Series {
			if !seed.Matches(sr.Where, c.Where) {
				continue
			}
			for _, td := range defs {
				if td.From.Metric == c.Metric && seed.Matches(sr.Where, td.From.Where) {
					return true
				}
			}
		}
	}
	return false
}

// traceSourceMetrics names every metric a trace definition reads: trace rules need
// all of them, because the TraceQL a rule runs can match any seeded span.
func traceSourceMetrics(s *seed.Seed) map[string]bool {
	defs, err := s.TraceDefs()
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, td := range defs {
		out[td.From.Metric] = true
	}
	return out
}

// tempoKey is the part of a Tempo model that changes the request (query cache key).
func tempoKey(q Query) string {
	if q.Tempo == nil {
		return ""
	}
	step := ""
	if q.Tempo.Step != nil {
		step = *q.Tempo.Step
	}
	return fmt.Sprintf("type=%s instant=%t step=%q", q.Tempo.QueryType, q.Tempo.Instant(), step)
}

// hasTraceRules reports whether any alert or recording rule reads the Tempo datasource.
func hasTraceRules(p *Project) bool {
	for _, group := range [][]Alert{p.Alerts, p.Records} {
		for i := range group {
			for _, q := range group[i].Queries {
				if q.Datasource == "traces" {
					return true
				}
			}
		}
	}
	return false
}

// traceRangeSteps lists the step of every range Tempo query of the rules. Tempo
// aligns a range request to whole steps since the Unix epoch (AlignRequest,
// pkg/traceql/engine_metrics.go:175-187), so such an answer moves with a run only
// when the run moves by a whole number of steps.
func traceRangeSteps(rules []*Alert) []time.Duration {
	var out []time.Duration
	at := time.Unix(0, 0)
	for _, a := range rules {
		for _, q := range a.Queries {
			if q.Datasource != "traces" || q.Tempo == nil || q.Tempo.Instant() {
				continue
			}
			if step, err := q.Tempo.RangeStep(at.Add(-q.From), at.Add(-q.To)); err == nil && step > 0 {
				out = append(out, step)
			}
		}
	}
	slices.Sort(out) // queries are a map: keep cache keys stable
	return out
}

// traceShiftExact reports whether the rules' range Tempo answers are the same when
// every evaluation moves by d.
func traceShiftExact(rules []*Alert, d time.Duration) bool {
	for _, step := range traceRangeSteps(rules) {
		if d%step != 0 {
			return false
		}
	}
	return true
}

// traceRangePhase is the base's position inside each range Tempo step: results for
// two bases with the same phases are the same, shifted.
func traceRangePhase(rules []*Alert, base time.Time) string {
	out := ""
	for _, step := range traceRangeSteps(rules) {
		out += fmt.Sprintf("-%d", base.UnixNano()%int64(step))
	}
	return out
}
