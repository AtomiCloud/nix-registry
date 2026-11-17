package alerts

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/AtomiCloud/obs/internal/check"
	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/logevent"
	"github.com/AtomiCloud/obs/internal/seed"
)

type Options struct {
	Dir, SeedPath, Out, Steps, Recorded, VLSettings string
	Base                                            int64
	Workers                                         int
	LogsGuard                                       QueryGuard
}

// Check evaluates each rule at every minute on the baseline and each incident.
func Check(o Options, stdout, stderr io.Writer) int {
	started := time.Now()
	verifyFast = os.Getenv("OBS_VERIFY_FAST") == "1"
	noFast = os.Getenv("OBS_NO_FAST") == "1"
	verifySkip = os.Getenv("OBS_VERIFY_SKIP") == "1"
	noSkip = os.Getenv("OBS_NO_SKIP") == "1"
	FastTicks.Store(0)
	FullTicks.Store(0)
	VerifiedTicks.Store(0)
	VerifyMismatch.Store(0)
	verifyExamples.Store(0)
	SkippedRuns, SkipChecked, SkipMismatch = 0, 0, 0
	p, err := loadProject(o.Dir, o.SeedPath)
	if err != nil {
		fmt.Fprintln(stderr, "obs alerts:", err)
		return 2
	}
	var events logevent.Events
	contractPath := filepath.Join(o.Dir, "contract.yaml")
	if _, err := os.Stat(contractPath); err == nil {
		c, err := contract.Load(contractPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if issues := c.Validate(); len(issues) > 0 {
			fmt.Fprintf(stderr, "%s: %v\n", contractPath, issues)
			return 2
		}
		events = c
		p.Files["contract.yaml"], _ = os.ReadFile(contractPath)
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var seedEvents []string
	for _, l := range p.Seed.Logs {
		seedEvents = append(seedEvents, l.Event)
	}
	if err := logevent.ValidateSeed(seedEvents, events); err != nil {
		fmt.Fprintf(stderr, "%s/seed.yaml: %v\n", o.Dir, err)
		return 1
	}
	// The shared reader and static checker enforce the same language and event rules.
	set, err := files.Load(o.Dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var queries []files.Query
	for _, q := range set.Queries {
		if q.IsAlert {
			queries = append(queries, q)
		}
	}
	set.Queries, set.Dashboards = queries, nil
	rep := check.Static(set, check.Options{Dir: o.Dir})
	rep.Print(stdout)
	if rep.Failures() > 0 {
		return 1
	}
	limits := vl.DefaultLimits()
	hasLogs := false
	for _, q := range queries {
		hasLogs = hasLogs || q.Kind == files.Logs
	}
	if hasLogs || o.VLSettings != "" {
		var note string
		limits, note, err = vl.LoadLimits(o.Dir, o.VLSettings)
		if err != nil {
			fmt.Fprintln(stderr, "obs alerts:", err)
			return 2
		}
		if note != "" && hasLogs {
			fmt.Fprintln(stdout, "note: [vl-settings]", note)
		}
		// Include effective settings, not unrelated chart/auth args, in cache keys.
		p.Files["victorialogs-query-limits"] = []byte(fmt.Sprintf("%#v", limits))
	}
	for _, group := range [][]Alert{p.Alerts, p.Records} {
		for i := range group {
			for ref, q := range group[i].Queries {
				if q.Datasource != "logs" {
					continue
				}
				analysis, err := logevent.Analyze(q.Expr, events)
				if err != nil {
					fmt.Fprintf(stderr, "%s rule %s query %s: %v\n", group[i].File, group[i].ID, ref, err)
					return 1
				}
				q.Expr = analysis.Expr
				group[i].Queries[ref] = q
			}
		}
	}
	for _, a := range append(append([]Alert{}, p.Records...), p.Alerts...) {
		if _, err := compileRule(&a); err != nil {
			fmt.Fprintf(stderr, "%s rule %s: %v\n", a.File, a.ID, err)
			return 1
		}
	}
	base := time.Unix(o.Base, 0).UTC()
	if o.Base == 0 {
		base = time.Now().UTC().Truncate(time.Minute).Add(-time.Duration(len(p.Incidents)+1)*(p.Seed.Duration()+p.Gap) - time.Hour)
	}
	runs := planRuns(p, base)
	if runs[len(runs)-1].End.After(time.Now()) {
		fmt.Fprintf(stderr, "obs alerts: --base %d: time line ends in the future; stores drop future data\n", o.Base)
		return 2
	}
	cacheDir := os.Getenv("OBS_CACHE_DIR")
	if verifyFast || verifySkip || o.Steps != "" || o.Recorded != "" || o.LogsGuard != nil {
		cacheDir = ""
	}
	allAlerts := p.Alerts
	var cached []AlertRun
	keys := map[string]string{}
	if cacheDir != "" {
		var misses []Alert
		for _, a := range allAlerts {
			k, cacheable := resultCacheKey(p, &a, buildID(), base)
			if !cacheable {
				misses = append(misses, a)
				continue
			}
			keys[a.ID] = k
			if ars, ok := cacheGet(cacheDir, k, base.UnixMilli()); ok && len(ars) == len(runs) {
				cached = append(cached, ars...)
			} else {
				misses = append(misses, a)
			}
		}
		p.Alerts = misses
	}
	var ars []AlertRun
	var recordedSeries []*Series
	pruned, recorded := 0, 0
	if len(p.Alerts) > 0 || len(allAlerts) == 0 {
		var used []*Alert
		for i := range p.Alerts {
			used = append(used, &p.Alerts[i])
		}
		for i := range p.Records {
			used = append(used, &p.Records[i])
		}
		wantLogs := needsDS(used, "logs")
		sd := p.Seed
		// Alerts evaluate metrics/logs. Their pruning may remove a metric used
		// only by a trace definition; do not build or validate unused traces.
		sd.Traces = yaml.Node{}
		if !wantLogs {
			sd.Logs = nil
		}
		// Generate only seed metrics required by metric selectors or seeded log events.
		names := neededMetricNames(used)
		if names != nil {
			sd.Metrics = map[string]seed.MetricDef{}
			logMetrics := map[string]bool{}
			for _, l := range sd.Logs {
				logMetrics[l.From.Metric] = true
			}
			for name, def := range p.Seed.Metrics {
				needed := logMetrics[name]
				for _, stored := range storedNames(name, def) {
					needed = needed || names[stored]
				}
				if needed {
					sd.Metrics[name] = def
				} else {
					pruned += len(def.Series)
				}
			}
		}
		ds, err := buildRuns(&sd, runs)
		if err != nil {
			fmt.Fprintln(stderr, "obs alerts:", err)
			return 2
		}
		pruned += pruneMetrics(ds, used)
		eng := Engines{Datasources: map[string]DatasourceEngine{}}
		var refused sync.Map
		refusalKey := func(q Query) qKey {
			return qKey{ds: q.Datasource, uid: q.UID, original: q.OriginalExpr, from: q.From, to: q.To}
		}
		onRefusal := func(q Query, err error) { refused.Store(refusalKey(q), err) }
		reportRefusals := func() bool {
			failed := false
			for _, group := range [][]Alert{p.Alerts, p.Records} {
				for _, a := range group {
					for ref, q := range a.Queries {
						if err, ok := refused.Load(refusalKey(q)); ok {
							fmt.Fprintf(stdout, "FAIL %s rule %s query %s: %v\n", a.File, a.ID, ref, err)
							failed = true
						}
					}
				}
			}
			return failed
		}
		// VM process globals allow one instance. Evaluation inside it is parallel.
		if needsDS(used, "metrics") || len(p.Records) > 0 {
			m, err := vm.New(ds)
			if err != nil {
				fmt.Fprintln(stderr, "obs alerts VM:", err)
				return 2
			}
			defer m.Close()
			eng.Metrics = m
			eng.Datasources["metrics"] = metricEngine{m}
		}
		if wantLogs {
			l, err := vl.NewWithLimits(ds, limits)
			if err != nil {
				fmt.Fprintln(stderr, "obs alerts VL:", err)
				return 2
			}
			defer l.Close()
			eng.Datasources["logs"] = logsEngine{e: l, guard: checkedLogsGuard{limits, o.LogsGuard}, onRefusal: onRefusal}
		}
		cache := &queryCache{m: map[qKey]*qEntry{}}
		recorded, err = runRecording(p, runs, eng, cache, &recordedSeries)
		if err != nil {
			reportRefusals()
			fmt.Fprintln(stderr, "obs alerts:", err)
			return 1
		}
		ars, _ = evaluateAll(p, runs, eng, cache, o.Workers, o.Steps != "", skipPlan(p))
		if reportRefusals() {
			return 1
		}
		if cacheDir != "" {
			byAlert := map[string][]AlertRun{}
			for _, ar := range ars {
				byAlert[ar.Alert] = append(byAlert[ar.Alert], ar)
			}
			for id, rs := range byAlert {
				if key := keys[id]; key != "" {
					cachePut(cacheDir, key, base.UnixMilli(), rs)
				}
			}
		}
	}
	p.Alerts = allAlerts
	ars = append(ars, cached...)
	vs := verdicts(p, runs, ars)
	failed := 0
	for _, v := range vs {
		mark := "ok"
		if !v.Pass {
			mark = "FAIL"
			failed++
		}
		file := ""
		for _, a := range p.Alerts {
			if a.ID == v.Alert {
				file = a.File
				break
			}
		}
		fmt.Fprintf(stdout, "%s %s rule %s window %s expect %s: %s\n", mark, file, v.Alert, v.Run, v.Expect, v.Why)
		if !v.Pass {
			for _, a := range p.Alerts {
				if a.ID == v.Alert {
					input, _ := json.Marshal(a)
					fmt.Fprintf(stdout, "  rule input: %s\n", input)
				}
			}
		}
	}
	if VerifyMismatch.Load() > 0 || SkipMismatch > 0 {
		failed++
	}
	fmt.Fprintf(stdout, "obs alerts: alerts=%d recording=%d runs=%d expectations=%d failed=%d cached=%d pruned=%d recorded_samples=%d\n", len(allAlerts), len(p.Records), len(runs), len(vs), failed, len(cached)/len(runs), pruned, recorded)
	fmt.Fprintf(stdout, "verify fast: compared=%d mismatches=%d; verify skip: compared=%d mismatches=%d; reused=%d\n", VerifiedTicks.Load(), VerifyMismatch.Load(), SkipChecked, SkipMismatch, SkippedRuns)
	if o.Out != "" {
		if err := writeJSON(o.Out, map[string]any{"alert_runs": stripSteps(ars), "verdicts": vs, "failed": failed, "elapsed_ms": time.Since(started).Milliseconds()}); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if o.Steps != "" {
		if err := writeJSON(o.Steps, ars); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if o.Recorded != "" {
		var rows []map[string]any
		for _, s := range recordedSeries {
			labels := map[string]string{}
			for _, l := range s.Labels {
				labels[l.Name] = l.Value
			}
			rows = append(rows, map[string]any{"labels": labels, "timestamps": s.Ts, "values": s.Vals})
		}
		if err := writeJSON(o.Recorded, rows); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

type checkedLogsGuard struct {
	limits vl.Limits
	extra  QueryGuard
}

func (g checkedLogsGuard) Check(expr string, start, end time.Time) error {
	if err := g.limits.Check(expr, start, end); err != nil {
		return err
	}
	if g.extra != nil {
		return g.extra.Check(expr, start, end)
	}
	return nil
}

func buildRuns(sd *seed.Seed, runs []Run) (*Dataset, error) {
	parts := make([]*Dataset, len(runs))
	errs := make([]error, len(runs))
	jobs := make(chan int, len(runs))
	var wg sync.WaitGroup
	for w := 0; w < min(len(runs), runtime.GOMAXPROCS(0)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				parts[i], errs[i] = seed.BuildIncident(sd, runs[i].Start, runs[i].Incident)
			}
		}()
	}
	for i := range runs {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	ds := &Dataset{Start: runs[0].Start, End: runs[len(runs)-1].End, Series: map[string]*Series{}, Streams: map[string]*seed.Stream{}}
	for i, part := range parts {
		if errs[i] != nil {
			return nil, errs[i]
		}
		for k, s := range part.Series {
			if old := ds.Series[k]; old != nil {
				old.Ts = append(old.Ts, s.Ts...)
				old.Vals = append(old.Vals, s.Vals...)
			} else {
				ds.Series[k] = s
			}
		}
		for k, s := range part.Streams {
			if old := ds.Streams[k]; old != nil {
				old.Rows = append(old.Rows, s.Rows...)
			} else {
				ds.Streams[k] = s
			}
		}
	}
	return ds, nil
}

func stripSteps(rs []AlertRun) []AlertRun {
	out := append([]AlertRun{}, rs...)
	for i := range out {
		out[i].Steps = nil
	}
	return out
}
func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Clean(path), b, 0o644)
}
