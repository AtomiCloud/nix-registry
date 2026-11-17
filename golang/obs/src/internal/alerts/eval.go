// SPDX-License-Identifier: AGPL-3.0-only
// Grafana expression/state port. Source read: grafana v13.3.0 (commit 752e3462).
// See gfexpr/LICENSE-grafana-AGPL and gfexpr/PROVENANCE.md for source and changes.
package alerts

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
)

type StepRecord struct {
	T      int64             `json:"t"`             // unix ms
	E      map[string]string `json:"e,omitempty"`   // instance -> eval result (Normal|Alerting|NoData|Error)
	V      map[string]jf     `json:"v,omitempty"`   // instance -> condition value
	States map[string]string `json:"s,omitempty"`   // instance -> state after the state machine
	Errors []string          `json:"err,omitempty"` // eval errors
}

type AlertRun struct {
	Alert       string       `json:"alert"`
	Run         string       `json:"run"`
	Fired       bool         `json:"fired"`
	FirstFireMs int64        `json:"first_fire_ms,omitempty"`
	FirstFireAt string       `json:"first_fire_at,omitempty"` // offset from run start
	FiringTimes []int64      `json:"firing_times,omitempty"`
	FiringSteps int          `json:"firing_steps"` // ticks with any instance Alerting or Recovering
	NoDataSteps int          `json:"nodata_steps,omitempty"`
	ErrorSteps  int          `json:"error_steps,omitempty"`
	Err         string       `json:"error,omitempty"` // compile/setup error
	Reused      string       `json:"reused,omitempty"`
	Transitions []Transition `json:"transitions,omitempty"`
	Steps       []StepRecord `json:"steps,omitempty"`
}

// ---------- query data, cached per (ds, expr, range, run, interval) ----------

type qKey struct {
	ds, uid, expr, rng, run string
	from, to                time.Duration
	interval                time.Duration
}

type qData struct {
	err        error
	isRange    bool
	rng        time.Duration
	step       time.Duration
	res        []Result       // instant: one point per tick; range: points on the step grid
	sorted     []sortedSeries // range: res as sorted arrays
	dls        []data.Labels  // instant: res labels, converted once
	labelsOnce sync.Once
	perTick    map[int64][]Result // fallback when the whole-run query failed (limits are per instant query)
	tickErr    map[int64]error
}

type queryCache struct {
	mu sync.Mutex
	m  map[qKey]*qEntry
}

type qEntry struct {
	once sync.Once
	d    *qData
}

func (c *queryCache) get(k qKey, f func() *qData) *qData {
	c.mu.Lock()
	e := c.m[k]
	if e == nil {
		e = &qEntry{}
		c.m[k] = e
	}
	c.mu.Unlock()
	e.once.Do(func() { e.d = f() })
	return e.d
}

// Range-type alert queries: Grafana 13.3 sends them with a 1 s step over
// [now-range, now] for both Prometheus (VictoriaMetrics) and Loki; the
// datasource timeInterval (15s) is not applied (MEASURED via /api/v1/eval).
var promMinStep = time.Second

func alignDown(t time.Time, step time.Duration) time.Time {
	ms := t.UnixMilli()
	s := step.Milliseconds()
	return time.UnixMilli(ms - ((ms%s)+s)%s).UTC()
}

func fetchQuery(eng Engines, q Query, run Run, interval time.Duration) *qData {
	engine := eng.Datasources[q.Datasource]
	if engine == nil {
		return &qData{err: fmt.Errorf("no engine registered for datasource %s", q.Datasource)}
	}
	return fetchEngine(engine, q, run, interval)
}

func (d *qData) at(t time.Time) qTick {
	ms := t.UnixMilli()
	if d.err != nil {
		return qTick{err: d.err}
	}
	if d.perTick != nil {
		if err := d.tickErr[ms]; err != nil {
			return qTick{err: err}
		}
		var out []instVal
		for _, r := range d.perTick[ms] {
			for _, v := range r.Points {
				out = append(out, instVal{labels: toDataLabels(r.Labels, KeepNameLabel), v: v})
			}
		}
		return qTick{nums: out}
	}
	if !d.isRange {
		d.labelsOnce.Do(func() {
			d.dls = make([]data.Labels, len(d.res))
			for i, r := range d.res {
				d.dls[i] = toDataLabels(r.Labels, KeepNameLabel)
			}
		})
		var out []instVal
		for i, r := range d.res {
			if v, ok := r.Points[ms]; ok {
				out = append(out, instVal{labels: d.dls[i], v: v})
			}
		}
		return qTick{nums: out}
	}
	from := alignDown(t.Add(-d.rng), d.step).UnixMilli()
	to := alignDown(t, d.step).UnixMilli()
	var out []sortedSeries
	for _, s := range d.sorted {
		i := sort.Search(len(s.ts), func(i int) bool { return s.ts[i] >= from })
		j := sort.Search(len(s.ts), func(i int) bool { return s.ts[i] > to })
		if i < j {
			out = append(out, sortedSeries{labels: s.labels, ts: s.ts[i:j], vs: s.vs[i:j]})
		}
	}
	return qTick{isRange: true, series: out}
}

// ---------- one rule over one run ----------

func evalRuleRun(eng Engines, cache *queryCache, cr *cRule, run Run, keepSteps bool, onTick func(t time.Time, res []evalResult)) AlertRun {
	a := cr.a
	ar := AlertRun{Alert: a.ID, Run: run.Name}
	data := map[string]*qData{}
	for _, ref := range cr.queries {
		q := a.Queries[ref]
		data[ref] = cache.get(qKey{ds: q.Datasource, uid: q.UID, expr: q.Expr, rng: q.Range, run: run.Name, from: q.From, to: q.To, interval: cr.interval}, func() *qData { return fetchQuery(eng, q, run, cr.interval) })
	}
	rs := newRuleStates(cr)
	ticks := map[string]qTick{}
	from, to := tickRange(a, run)
	for t := from; !t.After(to); t = t.Add(cr.interval) {
		for ref, d := range data {
			ticks[ref] = d.at(t)
		}
		res := cr.evalTickAuto(t, ticks, rs.loaded())
		if onTick != nil {
			onTick(t, res)
		}
		if cr.record != nil {
			continue
		}
		trs := rs.process(t, res)
		ar.Transitions = append(ar.Transitions, trs...)
		firing, nodata, errs := false, false, false
		for _, s := range rs.states {
			switch s.state {
			case stAlerting, stRecovering:
				firing = true
			case stNoData:
				nodata = true
			case stError:
				errs = true
			}
		}
		if firing {
			ar.FiringSteps++
			ar.FiringTimes = append(ar.FiringTimes, t.UnixMilli())
			if !ar.Fired {
				ar.Fired = true
				ar.FirstFireMs = t.UnixMilli()
				ar.FirstFireAt = t.Sub(run.Start).String()
			}
		}
		if nodata {
			ar.NoDataSteps++
		}
		if errs {
			ar.ErrorSteps++
		}
		if keepSteps {
			rec := StepRecord{T: t.UnixMilli(), E: map[string]string{}, V: map[string]jf{}, States: map[string]string{}}
			for _, x := range res {
				rec.E[x.key] = stName[x.state]
				if x.hasVal {
					rec.V[x.key] = jf(x.value)
				}
				if x.err != "" {
					rec.Errors = append(rec.Errors, x.err)
				}
			}
			for k, s := range rs.states {
				rec.States[k] = fmtState(s.state, s.reason)
			}
			ar.Steps = append(ar.Steps, rec)
		}
	}
	return ar
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// ---------- recording rules: evaluated first, written into the metric store ----------

func runRecording(p *Project, runs []Run, eng Engines, cache *queryCache, sink *[]*Series) (int, error) {
	if len(p.Records) == 0 {
		return 0, nil
	}
	written := 0
	// order: a rule whose queries mention another rule's metric comes after it
	recs := make([]*cRule, 0, len(p.Records))
	for i := range p.Records {
		cr, err := compileRule(&p.Records[i])
		if err != nil {
			return 0, fmt.Errorf("recording %s: %w", p.Records[i].ID, err)
		}
		recs = append(recs, cr)
	}
	var ordered []*cRule
	seen := map[*cRule]int{}
	var visit func(*cRule) error
	visit = func(cr *cRule) error {
		if seen[cr] == 1 {
			return fmt.Errorf("%s rule %s: recording dependency cycle", cr.a.File, cr.a.ID)
		}
		if seen[cr] == 2 {
			return nil
		}
		seen[cr] = 1
		for _, other := range recs {
			if dependsOn(cr, other) {
				if err := visit(other); err != nil {
					return err
				}
			}
		}
		seen[cr] = 2
		ordered = append(ordered, cr)
		return nil
	}
	for _, cr := range recs {
		if err := visit(cr); err != nil {
			return 0, err
		}
	}
	recs = ordered
	for _, cr := range recs {
		var out []*Series
		var evalErr error
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, run := range runs {
			wg.Add(1)
			go func(run Run) {
				defer wg.Done()
				bySeries := map[string]*Series{}
				var local []*Series
				evalRuleRun(eng, cache, cr, run, false, func(t time.Time, res []evalResult) {
					for _, x := range res {
						if x.state == stError {
							mu.Lock()
							evalErr = fmt.Errorf("%s rule %s window %s at %s: %s", cr.a.File, cr.a.ID, run.Name, t.Format(time.RFC3339), x.err)
							mu.Unlock()
							continue
						}
						if !x.hasVal && x.state != stNormal {
							continue
						}
						v := x.value
						if x.state == stNormal {
							v = 0
						}
						ls := []Label{{"__name__", cr.record.Metric}}
						for k, val := range x.labels {
							if k != "__name__" {
								ls = append(ls, Label{k, val})
							}
						}
						for k, val := range cr.a.Labels {
							ls = append(ls, Label{k, val})
						}
						ls = sortLabels(ls)
						key := labelsKey(ls)
						s := bySeries[key]
						if s == nil {
							s = &Series{Labels: ls}
							bySeries[key] = s
							local = append(local, s)
						}
						s.Ts = append(s.Ts, t.UnixMilli())
						s.Vals = append(s.Vals, v)
					}
				})
				mu.Lock()
				out = append(out, local...)
				mu.Unlock()
			}(run)
		}
		wg.Wait()
		if evalErr != nil {
			return written, evalErr
		}
		for _, s := range out {
			written += len(s.Ts)
		}
		if sink != nil {
			*sink = append(*sink, out...)
		}
		if err := eng.Metrics.Append(out); err != nil {
			return written, err
		}
		// later rules (and alerts) must not reuse results cached before this write
		cache.mu.Lock()
		for k := range cache.m {
			if k.ds == "metrics" {
				delete(cache.m, k)
			}
		}
		cache.mu.Unlock()
	}
	return written, nil
}

func dependsOn(a, b *cRule) bool { // a reads b's metric
	for _, q := range a.a.Queries {
		if q.Datasource == "metrics" && containsWord(q.Expr, b.record.Metric) {
			return true
		}
	}
	return false
}

func containsWord(s, w string) bool {
	for i := 0; i+len(w) <= len(s); i++ {
		if s[i:i+len(w)] != w {
			continue
		}
		before := i == 0 || !isNameChar(s[i-1])
		after := i+len(w) == len(s) || !isNameChar(s[i+len(w)])
		if before && after {
			return true
		}
	}
	return false
}

func isNameChar(c byte) bool {
	return c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ---------- all alerts × runs ----------

type Verdict struct {
	Run    string `json:"run"`
	Alert  string `json:"alert"`
	Expect string `json:"expect"` // fire | quiet
	Pass   bool   `json:"pass"`
	Why    string `json:"why"`
}

type evalJob struct {
	cr  *cRule
	run Run
	idx int
}

func evaluateAll(p *Project, runs []Run, eng Engines, cache *queryCache, workers int, keepSteps bool, skip func(a *Alert, run Run) bool) ([]AlertRun, []Verdict) {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	compiled := make([]*cRule, len(p.Alerts))
	compileErr := make([]error, len(p.Alerts))
	for i := range p.Alerts {
		compiled[i], compileErr[i] = compileRule(&p.Alerts[i])
	}
	jobs := make(chan evalJob, 256)
	out := make([]AlertRun, len(p.Alerts)*len(runs))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				out[j.idx] = evalRuleRun(eng, cache, j.cr, j.run, keepSteps, nil)
			}
		}()
	}
	i := 0
	for _, run := range runs {
		for k, a := range p.Alerts {
			switch {
			case compileErr[k] != nil:
				out[i] = AlertRun{Alert: a.ID, Run: run.Name, Err: compileErr[k].Error()}
			case skip != nil && skip(&p.Alerts[k], run):
				out[i] = AlertRun{Alert: a.ID, Run: run.Name, Reused: "skip"}
			default:
				jobs <- evalJob{compiled[k], run, i}
			}
			i++
		}
	}
	close(jobs)
	wg.Wait()
	fillSkipped(p, runs, out, eng, cache, keepSteps)
	return out, verdicts(p, runs, out)
}

func verdicts(p *Project, runs []Run, out []AlertRun) []Verdict {
	byKey := map[string]AlertRun{}
	for _, ar := range out {
		byKey[ar.Run+"|"+ar.Alert] = ar
	}
	var vs []Verdict
	for _, run := range runs {
		if run.Incident == nil {
			for _, a := range p.Alerts {
				ar := byKey[run.Name+"|"+a.ID]
				v := Verdict{Run: run.Name, Alert: a.ID, Expect: "quiet", Pass: !ar.Fired && ar.Err == ""}
				v.Why = describe(ar)
				vs = append(vs, v)
			}
			continue
		}
		inc := run.Incident
		ws := mustDur(inc.Window.Start)
		within := mustDur(inc.Expect.FireWithin)
		if within == 0 {
			within = mustDur(inc.Window.Duration)
		}
		for _, id := range inc.Expect.Fire {
			ar, ok := byKey[run.Name+"|"+id]
			v := Verdict{Run: run.Name, Alert: id, Expect: "fire"}
			if !ok {
				v.Why = "no such alert"
			} else {
				at := time.Duration(ar.FirstFireMs-run.Start.UnixMilli()) * time.Millisecond
				v.Pass = ar.Fired && at >= ws && at <= ws+within
				v.Why = describe(ar)
				if ar.Fired && at < ws {
					v.Why += " (fired before the incident window)"
				}
				if ar.Fired && at > ws+within {
					v.Why += fmt.Sprintf(" (later than fire_within %s)", within)
				}
				end := ws + mustDur(inc.Window.Duration) + mustDur(inc.Expect.ResolveWithin)
				for _, ms := range ar.FiringTimes {
					offset := time.Duration(ms-run.Start.UnixMilli()) * time.Millisecond
					if offset < ws || offset >= end {
						v.Pass = false
						v.Why += fmt.Sprintf("; firing outside window [%s,%s) at +%s", ws, end, offset)
						break
					}
				}
				v.Why += fmt.Sprintf("; incident [%s,%s), fire_within=%s", ws, ws+mustDur(inc.Window.Duration), within)
			}
			vs = append(vs, v)
		}
		for _, id := range inc.Expect.Quiet {
			ar, ok := byKey[run.Name+"|"+id]
			v := Verdict{Run: run.Name, Alert: id, Expect: "quiet"}
			if !ok {
				v.Why = "no such alert"
			} else {
				v.Pass = !ar.Fired && ar.Err == ""
				v.Why = describe(ar)
			}
			vs = append(vs, v)
		}
	}
	return vs
}

func describe(ar AlertRun) string {
	if ar.Err != "" {
		return "error: " + ar.Err
	}
	s := "quiet"
	if ar.Fired {
		s = fmt.Sprintf("fired at +%s, firing %d min", ar.FirstFireAt, ar.FiringSteps)
	}
	if ar.NoDataSteps > 0 {
		s += fmt.Sprintf(", NoData %d", ar.NoDataSteps)
	}
	if ar.ErrorSteps > 0 {
		s += fmt.Sprintf(", Error %d", ar.ErrorSteps)
	}
	if ar.Reused != "" {
		s += " (" + ar.Reused + ")"
	}
	return s
}

// jf encodes NaN/Inf (valid query results, invalid JSON) as strings.
type jf float64

func (f jf) MarshalJSON() ([]byte, error) {
	v := float64(f)
	switch {
	case math.IsNaN(v):
		return []byte(`"NaN"`), nil
	case math.IsInf(v, 1):
		return []byte(`"+Inf"`), nil
	case math.IsInf(v, -1):
		return []byte(`"-Inf"`), nil
	}
	return []byte(strconv.FormatFloat(v, 'g', -1, 64)), nil
}

type sortedSeries struct {
	labels []Label
	ts     []int64
	vs     []float64
}

func toSorted(rs []Result) []sortedSeries {
	out := make([]sortedSeries, 0, len(rs))
	for _, r := range rs {
		s := sortedSeries{labels: r.Labels, ts: make([]int64, 0, len(r.Points))}
		for k := range r.Points {
			s.ts = append(s.ts, k)
		}
		sort.Slice(s.ts, func(i, j int) bool { return s.ts[i] < s.ts[j] })
		s.vs = make([]float64, len(s.ts))
		for i, k := range s.ts {
			s.vs[i] = r.Points[k]
		}
		out = append(out, s)
	}
	return out
}

// Live comparison mode: start each rule at the time real Grafana first evaluated it
// (by rule title), stop at LiveUntil; only the run named OnlyRun is evaluated.
var (
	LiveStarts map[string]int64
	LiveUntil  int64
	OnlyRun    string
)

func tickRange(a *Alert, run Run) (time.Time, time.Time) {
	from, to := run.Start, run.End
	if s, ok := LiveStarts[a.Title]; ok {
		from = time.Unix(s, 0).UTC()
	}
	if LiveUntil > 0 && time.Unix(LiveUntil, 0).Before(to) {
		to = time.Unix(LiveUntil, 0).UTC()
	}
	return from, to
}
