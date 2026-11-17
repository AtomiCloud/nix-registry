package alerts

import (
	"github.com/AtomiCloud/obs/internal/seed"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"testing"
	"time"
)

func TestRuleLabelsPreserveHysteresisIdentity(t *testing.T) {
	r := compile(t, Alert{Labels: map[string]string{"team": "fixture"}})
	rs := newRuleStates(r)
	rs.r.usesHyst = true
	labels := data.Labels{"__name__": "requests", "provider": "card"}
	key := lblKey(labels)
	rs.process(time.Now(), []evalResult{{labels: labels, key: key, state: stAlerting}})
	if !rs.loaded()[key] {
		t.Fatal("rule labels changed the hysteresis input fingerprint")
	}
	for _, s := range rs.states {
		if s.labels["team"] != "fixture" || s.labels["__name__"] != "requests" {
			t.Fatal("labels lost", s.labels)
		}
	}
	res := evalResult{state: stError, labels: data.Labels{"datasource_uid": "real", "ref_id": "A"}, key: "origin"}
	r.a.Labels = map[string]string{"datasource_uid": "overridden", "ref_id": "B"}
	rs = newRuleStates(r)
	rs.process(time.Now(), []evalResult{res})
	for _, s := range rs.states {
		if s.labels["datasource_uid"] != "real" || s.labels["ref_id"] != "A" {
			t.Fatal("rule labels overrode reserved Error origin", s.labels)
		}
	}
}

func TestSkipFootprintsAndTimeSensitivity(t *testing.T) {
	p := &Project{Seed: seed.Seed{Metrics: map[string]seed.MetricDef{"requests": {Type: "counter"}}}}
	inc := &Incident{Change: []seed.Change{{Metric: "requests", Where: map[string]string{"provider": "card"}}}}
	for _, tc := range []struct {
		q        string
		affected bool
	}{
		{`rate(requests_total{provider="card"}[5m])`, true},
		{`rate(requests_total{provider="bank"}[5m])`, false},
		{`sum(rate(other_total[5m]))`, false},
		{`time() > 1`, true},
		{`year()`, true},
		{`requests_total @ 1700000000`, true},
		{`timestamp(requests_total)`, true},
		{`timestamp_with_name(requests_total)`, true},
		{`tfirst_over_time(requests_total[5m])`, true},
		{`tlast_over_time(requests_total[5m])`, true},
		{`tlast_change_over_time(requests_total[5m])`, true},
		{`tmin_over_time(requests_total[5m])`, true},
		{`tmax_over_time(requests_total[5m])`, true},
	} {
		a := Alert{Queries: map[string]Query{"A": {Datasource: "metrics", Expr: tc.q}}}
		if got := affected(p, &a, inc, nil, 0); got != tc.affected {
			t.Fatalf("%s affected=%v want %v", tc.q, got, tc.affected)
		}
	}
}

type timestampEngine struct{}

func (timestampEngine) Instant(_ Query, at time.Time) ([]Result, error) {
	return []Result{{Points: map[int64]float64{at.UnixMilli(): float64(at.Unix())}}}, nil
}

func TestTimestampInputsNeverReuseShiftedConditions(t *testing.T) {
	oldVerify, oldSkip := verifySkip, noSkip
	verifySkip, noSkip = true, false
	defer func() { verifySkip, noSkip = oldVerify, oldSkip }()
	SkipChecked, SkipMismatch = 0, 0
	a := Alert{ID: "timestamp", Condition: "$A > 1700001800", Queries: map[string]Query{"A": {Datasource: "metrics", Expr: "timestamp(requests_total)"}}}
	p := &Project{Alerts: []Alert{a}, Files: map[string][]byte{"seed.yaml": []byte("fixture")}}
	base := time.Unix(1700000000, 0)
	inc := &Incident{Name: "spike"}
	runs := []Run{{Name: "baseline", Start: base, End: base.Add(20 * time.Minute)}, {Name: "spike", Start: base.Add(time.Hour), End: base.Add(80 * time.Minute), Incident: inc}}
	eng := Engines{Datasources: map[string]DatasourceEngine{"metrics": timestampEngine{}}}
	ars, _ := evaluateAll(p, runs, eng, &queryCache{m: map[qKey]*qEntry{}}, 1, false, skipPlan(p))
	if ars[0].Fired || !ars[1].Fired || ars[1].Reused != "" || SkipMismatch != 0 {
		t.Fatalf("shifted timestamp condition reused: %+v mismatches=%d", ars, SkipMismatch)
	}
	first, ok := resultCacheKey(p, &a, "binary", base)
	if !ok {
		t.Fatal("deterministic timestamp query should be cacheable at the same base")
	}
	second, ok := resultCacheKey(p, &a, "binary", base.Add(time.Hour))
	if !ok || first == second {
		t.Fatal("timestamp-valued query cache key omitted base")
	}
	dir := t.TempDir()
	cachePut(dir, first, base.UnixMilli(), ars[:1])
	if _, hit := cacheGet(dir, second, base.Add(time.Hour).UnixMilli()); hit {
		t.Fatal("persistent cache reused the old timestamp condition at a different base")
	}
	for _, expr := range []string{"now()", "rand()", "rand_normal()"} {
		a.Queries["A"] = Query{Datasource: "metrics", Expr: expr}
		if _, ok := resultCacheKey(p, &a, "binary", base); ok {
			t.Fatalf("volatile query %s must not persist results", expr)
		}
	}
}

func TestTemporalRecordingInputsInvalidateDownstreamCache(t *testing.T) {
	a := Alert{ID: "a", Queries: map[string]Query{"A": {Datasource: "metrics", Expr: "recorded"}}}
	p := &Project{Records: []Alert{{Queries: map[string]Query{"A": {Datasource: "metrics", Expr: "tlast_over_time(requests_total[5m])"}}}}}
	base := time.Unix(1700000000, 0)
	x, _ := resultCacheKey(p, &a, "binary", base)
	y, _ := resultCacheKey(p, &a, "binary", base.Add(time.Hour))
	if x == y {
		t.Fatal("time-valued recording rule did not invalidate downstream alert cache")
	}
}

func TestCacheKeysAndShiftedResult(t *testing.T) {
	p := &Project{Files: map[string][]byte{"seed.yaml": []byte("seed"), "alerts/a.json": []byte("A"), "alerts/b.json": []byte("B"), "recording/r.json": []byte("record")}}
	a := &Alert{ID: "a"}
	key := ruleCacheKey(p, a, "binary")
	p.Files["alerts/b.json"] = []byte("other edit")
	if ruleCacheKey(p, a, "binary") != key {
		t.Fatal("unrelated alert invalidated cache")
	}
	p.Files["alerts/a.json"] = []byte("own edit")
	if ruleCacheKey(p, a, "binary") == key {
		t.Fatal("changed alert reused cache")
	}
	dir := t.TempDir()
	ar := AlertRun{Alert: "a", Run: "baseline", Fired: true, FirstFireMs: 123, FiringTimes: []int64{123, 183}, FiringSteps: 2, Transitions: []Transition{{T: 123, From: "Normal", To: "Alerting"}}}
	cachePut(dir, key, 100, []AlertRun{ar})
	got, ok := cacheGet(dir, key, 1100)
	if !ok || got[0].FirstFireMs != 1123 || got[0].FiringTimes[1] != 1183 || got[0].Transitions[0].T != 1123 {
		t.Fatalf("shifted result %+v", got)
	}
	if _, ok := cacheGet(dir, "badkey", 100); ok {
		t.Fatal("missing cache hit")
	}
}

func TestQueryLookbackSeparatesLongWindows(t *testing.T) {
	q := Query{Datasource: "metrics", Expr: `avg_over_time(rate(requests_total[30h])[2h:1m] offset 3h)`, From: time.Minute}
	if got := queryLookback(q, time.Minute); got < 35*time.Hour {
		t.Fatalf("long rollup can cross incident runs: gap=%s", got)
	}
}

func TestVerdictsFailOutOfWindowAndUnlistedFiring(t *testing.T) {
	inc := &Incident{Name: "spike"}
	inc.Window.Start = "10m"
	inc.Window.Duration = "5m"
	inc.Expect.Fire = []string{"a"}
	p := &Project{Alerts: []Alert{{ID: "a"}, {ID: "b"}}}
	start := time.Unix(1700000000, 0)
	run := Run{Name: "spike", Start: start, End: start.Add(30 * time.Minute), Incident: inc}
	ar := AlertRun{Alert: "a", Run: "spike", Fired: true, FirstFireMs: start.Add(11 * time.Minute).UnixMilli(), FiringTimes: []int64{start.Add(11 * time.Minute).UnixMilli()}}
	good := verdicts(p, []Run{run}, []AlertRun{ar})
	if len(good) != 2 || !good[0].Pass || !good[1].Pass {
		t.Fatalf("baseline verdicts %+v", good)
	}
	ar.FiringTimes = append(ar.FiringTimes, start.Add(16*time.Minute).UnixMilli())
	bad := verdicts(p, []Run{run}, []AlertRun{ar, {Alert: "b", Run: "spike", Fired: true}})
	if bad[0].Pass || bad[1].Pass {
		t.Fatalf("missed unexpected firing %+v", bad)
	}
	inc.Expect.ResolveWithin = "10m"
	inc.Expect.FireWithin = "10m"
	ar.FirstFireMs = start.Add(16 * time.Minute).UnixMilli()
	ar.FiringTimes = []int64{ar.FirstFireMs}
	if verdicts(p, []Run{run}, []AlertRun{ar})[0].Pass {
		t.Fatal("a recovery allowance accepted a first fire after the incident ended")
	}
}
