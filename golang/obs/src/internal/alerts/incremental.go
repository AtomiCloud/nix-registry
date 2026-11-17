// SPDX-License-Identifier: AGPL-3.0-only
// Grafana expression/state port. Source read: grafana v13.3.0 (commit 752e3462).
// See gfexpr/LICENSE-grafana-AGPL and gfexpr/PROVENANCE.md for source and changes.
package alerts

// Speed: (1) shared seed: an incident run only re-evaluates alerts whose inputs the
// incident changes; the others reuse the baseline result (the made-up data of unchanged
// series is identical in every run, same random stream per series and second).
// (2) result cache across commits: an alert whose own file, the seed, the incidents,
// the recording rules and the obs build are unchanged reuses its cached result.
// (3) lazy load: only the metric names, logs and traces the rules read are loaded.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/VictoriaMetrics/metricsql"
)

// ---------- (1) which alerts an incident can change ----------

type selector struct {
	name    string // "" = unknown or regex
	filters []metricsql.LabelFilter
}

func metricSelectors(q string) ([]selector, bool) {
	e, err := metricsql.Parse(q)
	if err != nil {
		return nil, false
	}
	var out []selector
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		me, ok := x.(*metricsql.MetricExpr)
		if !ok {
			return
		}
		for _, lfs := range me.LabelFilterss {
			s := selector{}
			for _, lf := range lfs {
				if lf.Label == "__name__" && !lf.IsRegexp && !lf.IsNegative {
					s.name = lf.Value
					continue
				}
				s.filters = append(s.filters, lf)
			}
			out = append(out, s)
		}
		if len(me.LabelFilterss) == 0 {
			out = append(out, selector{})
		}
	})
	return out, true
}

func storedNames(metric string, def MetricDef) []string {
	n := storedName(metric)
	if def.Type == "histogram" {
		s := ""
		if def.Unit == "s" {
			s = "_seconds"
		}
		return []string{n + s + "_bucket", n + s + "_sum", n + s + "_count"}
	}
	return []string{n + "_total"}
}

// change footprint: stored names + where-labels (stored spelling) of a changed series.
type footprint struct {
	names map[string]bool
	where map[string]string
}

func incidentFootprints(p *Project, inc *Incident) []footprint {
	var fps []footprint
	for _, c := range inc.Change {
		def := p.Seed.Metrics[c.Metric]
		fp := footprint{names: map[string]bool{}, where: map[string]string{}}
		for _, n := range storedNames(c.Metric, def) {
			fp.names[n] = true
		}
		for k, v := range c.Where {
			fp.where[storedName(k)] = v
		}
		fps = append(fps, fp)
	}
	return fps
}

func selectorHits(s selector, fp footprint) bool {
	if s.name != "" && !fp.names[s.name] {
		return false
	}
	for _, lf := range s.filters {
		v, ok := fp.where[lf.Label]
		if !ok {
			continue // the changed series may carry any value for this label
		}
		if lf.IsRegexp {
			continue // conservative
		}
		if !lf.IsNegative && lf.Value != v {
			return false
		}
		if lf.IsNegative && lf.Value == v {
			return false
		}
	}
	return true
}

// affected reports whether the incident can change any input of the alert.
func affected(p *Project, a *Alert, inc *Incident, recorded map[string]*Alert, depth int) bool {
	if depth > 5 {
		return true
	}
	fps := incidentFootprints(p, inc)
	changedMetrics := map[string]bool{}
	for _, c := range inc.Change {
		changedMetrics[c.Metric] = true
	}
	for _, q := range a.Queries {
		switch q.Datasource {
		case "metrics":
			sels, ok := metricSelectors(q.Expr)
			if !ok {
				return true
			}
			for _, s := range sels {
				if r, isRec := recorded[s.name]; isRec {
					if affected(p, r, inc, recorded, depth+1) {
						return true
					}
					continue
				}
				for _, fp := range fps {
					if selectorHits(s, fp) {
						return true
					}
				}
			}
		case "logs", "traces":
			return true // conservative: datasource engines can add an incident footprint later

		}
	}
	return false
}

func wheresCompatible(a, b map[string]string) bool {
	for k, v := range a {
		if w, ok := b[k]; ok && w != v {
			return false
		}
	}
	return true
}

// ---------- (2) result cache ----------

func ruleCacheKey(p *Project, a *Alert, buildID string) string {
	h := sha256.New()
	h.Write([]byte(buildID))
	keys := make([]string, 0, len(p.Files))
	for k := range p.Files {
		if strings.HasPrefix(k, "alerts/") {
			continue // other alerts do not change this one
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write(p.Files[k])
	}
	h.Write([]byte("alerts/" + a.ID + ".json"))
	h.Write(p.Files["alerts/"+a.ID+".json"])
	h.Write([]byte(os.Getenv("OBS_CACHE_SALT")))
	return hex.EncodeToString(h.Sum(nil))
}

// ---------- (3) what to load ----------

// neededMetricNames: nil means "all" (some selector has no fixed name).
func neededMetricNames(rules []*Alert) map[string]bool {
	need := map[string]bool{}
	for _, a := range rules {
		for _, q := range a.Queries {
			if q.Datasource != "metrics" {
				continue
			}
			sels, ok := metricSelectors(q.Expr)
			if !ok {
				return nil
			}
			for _, s := range sels {
				if s.name == "" {
					return nil
				}
				need[s.name] = true
			}
		}
	}
	return need
}

func needsDS(rules []*Alert, ds string) bool {
	for _, a := range rules {
		for _, q := range a.Queries {
			if q.Datasource == ds {
				return true
			}
		}
	}
	return false
}

// ---------- wiring ----------

var (
	noSkip                                 = os.Getenv("OBS_NO_SKIP") != ""
	verifySkip                             = os.Getenv("OBS_VERIFY_SKIP") != ""
	SkippedRuns, SkipChecked, SkipMismatch int
)

// shiftRun moves an alert result by d (all times are unix ms).
func shiftRun(ar AlertRun, run Run, d int64) AlertRun {
	out := ar
	out.Run = run.Name
	if ar.FirstFireMs != 0 {
		out.FirstFireMs = ar.FirstFireMs + d
	}
	out.FiringTimes = make([]int64, len(ar.FiringTimes))
	for i, t := range ar.FiringTimes {
		out.FiringTimes[i] = t + d
	}
	out.Transitions = make([]Transition, len(ar.Transitions))
	for i, t := range ar.Transitions {
		t.T += d
		out.Transitions[i] = t
	}
	if ar.Steps != nil {
		out.Steps = make([]StepRecord, len(ar.Steps))
		for i, s := range ar.Steps {
			s.T += d
			out.Steps[i] = s
		}
	}
	return out
}

// skipPlan: alert k in incident run r reuses the baseline result when the incident
// cannot change any of its inputs.
func skipPlan(p *Project) func(a *Alert, run Run) bool {
	if noSkip {
		return nil
	}
	recorded := map[string]*Alert{}
	for i := range p.Records {
		if p.Records[i].Record != nil {
			recorded[p.Records[i].Record.Metric] = &p.Records[i]
		}
	}
	return func(a *Alert, run Run) bool {
		if run.Incident == nil {
			return false
		}
		return !affected(p, a, run.Incident, recorded, 0)
	}
}

// fillSkipped copies baseline results into the skipped slots (shifted to the run).
func fillSkipped(p *Project, runs []Run, out []AlertRun, eng Engines, cache *queryCache, keepSteps bool) {
	n := len(p.Alerts)
	if len(runs) == 0 || runs[0].Incident != nil {
		return
	}
	base := runs[0]
	for ri := 1; ri < len(runs); ri++ {
		for k := 0; k < n; k++ {
			i := ri*n + k
			if out[i].Reused != "skip" {
				continue
			}
			SkippedRuns++
			b := out[k]
			if b.Err != "" {
				out[i] = AlertRun{Alert: b.Alert, Run: runs[ri].Name, Err: b.Err, Reused: "skip"}
				continue
			}
			sh := shiftRun(b, runs[ri], runs[ri].Start.UnixMilli()-base.Start.UnixMilli())
			sh.Reused = "baseline"
			if verifySkip {
				cr, err := compileRule(&p.Alerts[k])
				if err == nil {
					real := evalRuleRun(eng, cache, cr, runs[ri], keepSteps, nil)
					SkipChecked++
					if !sameRun(real, sh) {
						SkipMismatch++
						if SkipMismatch <= 5 {
							fmt.Fprintf(os.Stderr, "SKIP MISMATCH %s %s: real fired=%v steps=%d trans=%d; reused fired=%v steps=%d trans=%d\n", p.Alerts[k].ID, runs[ri].Name, real.Fired, real.FiringSteps, len(real.Transitions), sh.Fired, sh.FiringSteps, len(sh.Transitions))
							for ti := range real.Transitions {
								if ti < len(sh.Transitions) && real.Transitions[ti] != sh.Transitions[ti] {
									fmt.Fprintf(os.Stderr, "  real %+v (+%ds)\n  reus %+v\n", real.Transitions[ti], (real.Transitions[ti].T-runs[ri].Start.UnixMilli())/1000, sh.Transitions[ti])
								}
							}
						}
					}
				}
			}
			out[i] = sh
		}
	}
}

func sameRun(a, b AlertRun) bool {
	if a.Fired != b.Fired || a.FirstFireMs != b.FirstFireMs || a.FiringSteps != b.FiringSteps || a.NoDataSteps != b.NoDataSteps || a.ErrorSteps != b.ErrorSteps || len(a.Transitions) != len(b.Transitions) {
		return false
	}
	sa, sb := sortedTrans(a.Transitions), sortedTrans(b.Transitions)
	for i := range sa {
		x, y := sa[i], sb[i]
		if x.T != y.T || x.Instance != y.Instance || x.From != y.From || x.To != y.To {
			return false
		}
	}
	return true
}

// pruneMetrics drops series no rule reads (lazy load). Returns how many were dropped.
func pruneMetrics(ds *Dataset, rules []*Alert) int {
	need := neededMetricNames(rules)
	if need == nil || os.Getenv("OBS_NO_PRUNE") != "" {
		return 0
	}
	dropped := 0
	for k, s := range ds.Series {
		name := ""
		for _, l := range s.Labels {
			if l.Name == "__name__" {
				name = l.Value
			}
		}
		if !need[name] {
			delete(ds.Series, k)
			dropped++
		}
	}
	return dropped
}

// ---------- result cache wiring ----------

type cacheEntry struct {
	BaseMs int64      `json:"base_ms"`
	Runs   []AlertRun `json:"runs"`
}

var buildIDOnce struct {
	id string
}

func buildID() string {
	if buildIDOnce.id != "" {
		return buildIDOnce.id
	}
	exe, err := os.Executable()
	if err == nil {
		if b, err := os.ReadFile(exe); err == nil {
			s := sha256.Sum256(b)
			buildIDOnce.id = hex.EncodeToString(s[:])
			return buildIDOnce.id
		}
	}
	buildIDOnce.id = "unknown"
	return buildIDOnce.id
}

func cacheGet(dir, key string, baseMs int64) ([]AlertRun, bool) {
	b, err := os.ReadFile(filepath.Join(dir, key[:2], key+".json"))
	if err != nil {
		return nil, false
	}
	var e cacheEntry
	if json.Unmarshal(b, &e) != nil {
		return nil, false
	}
	d := baseMs - e.BaseMs
	out := make([]AlertRun, len(e.Runs))
	for i, ar := range e.Runs {
		out[i] = shiftRun(ar, Run{Name: ar.Run}, d)
		out[i].Reused = "cache"
	}
	return out, true
}

func cachePut(dir, key string, baseMs int64, ars []AlertRun) {
	d := filepath.Join(dir, key[:2])
	_ = os.MkdirAll(d, 0o755)
	b, _ := json.Marshal(cacheEntry{BaseMs: baseMs, Runs: stripSteps(ars)})
	_ = os.WriteFile(filepath.Join(d, key+".json"), b, 0o644)
}

// planRuns: the run layout Build uses, without making any data.
func planRuns(p *Project, base time.Time) []Run {
	length := mustDur(p.Seed.Length)
	all := append([]*Incident{nil}, ptrs(p.Incidents)...)
	runs := make([]Run, len(all))
	for i, inc := range all {
		start := base.Add(time.Duration(i) * (length + p.Gap))
		runs[i] = Run{Name: "baseline", Incident: inc, Start: start, End: start.Add(length)}
		if inc != nil {
			runs[i].Name = inc.Name
		}
	}
	if OnlyRun != "" {
		var keep []Run
		for _, r := range runs {
			if r.Name == OnlyRun {
				keep = append(keep, r)
			}
		}
		runs = keep
	}
	return runs
}

// sortedTrans: transitions at the same tick come from a map walk; order them.
func sortedTrans(ts []Transition) []Transition {
	out := append([]Transition(nil), ts...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].T != out[j].T {
			return out[i].T < out[j].T
		}
		return out[i].Instance < out[j].Instance
	})
	return out
}
