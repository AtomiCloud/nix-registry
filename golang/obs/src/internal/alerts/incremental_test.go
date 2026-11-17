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
	} {
		a := Alert{Queries: map[string]Query{"A": {Datasource: "metrics", Expr: tc.q}}}
		if got := affected(p, &a, inc, nil, 0); got != tc.affected {
			t.Fatalf("%s affected=%v want %v", tc.q, got, tc.affected)
		}
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
}
