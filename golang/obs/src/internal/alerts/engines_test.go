package alerts

import (
	"reflect"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/seed"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func TestLogsQLGroupingNameAndNullInstance(t *testing.T) {
	end := time.Now().Add(-time.Hour).Truncate(time.Minute)
	ds := &seed.Dataset{Streams: map[string]*seed.Stream{"fixture": {Fields: []seed.Label{{Name: "service.name", Value: "fixture"}}, Rows: []seed.Row{{TsNs: end.Add(-30 * time.Second).UnixNano(), Fields: []seed.Label{{Name: "durian.cloud/event", Value: "fixture.failed"}, {Name: "outcome", Value: "error"}}, Msg: "fixture"}}}}}
	e, err := vl.New(ds)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, tc := range []struct {
		stat, name string
		state      int
	}{
		{"count() as n", "n", stAlerting},
		{"max(latency) as m", "m", stNoData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := Query{Datasource: "logs", UID: "fixture-logs", Expr: `"durian.cloud/event":=fixture.failed | stats by (outcome) ` + tc.stat, From: time.Minute}
			eng := Engines{Datasources: map[string]DatasourceEngine{"logs": logsEngine{e: e}}}
			d := fetchQuery(eng, q, Run{Start: end, End: end}, time.Minute)
			tick := d.at(end)
			cr := compile(t, Alert{Condition: "A", NoDataState: "Alerting", Queries: map[string]Query{"A": q}})
			wantLabels := data.Labels{"__name__": tc.name, "outcome": "error"}
			for _, res := range [][]evalResult{cr.evalTick(end, map[string]qTick{"A": tick}, nil), cr.evalTickLight(map[string]qTick{"A": tick}, nil)} {
				if len(res) != 1 || res[0].state != tc.state || !reflect.DeepEqual(res[0].labels, wantLabels) {
					t.Fatalf("%s result %+v, want state=%s labels=%v", tc.stat, res, stName[tc.state], wantLabels)
				}
				if tc.state == stNoData {
					rs := newRuleStates(cr)
					rs.process(end, res)
					if s := rs.states[lblKey(wantLabels)]; s == nil || s.state != stAlerting {
						t.Fatalf("null group was dropped instead of mapping NoData to Alerting: %+v", rs.states)
					}
				}
			}
		})
	}
}

func TestUnreducedSeriesErrorsHaveOneEmptyInstance(t *testing.T) {
	cr := compile(t, Alert{Condition: "A", Queries: map[string]Query{"A": {Datasource: "metrics", Range: "1m"}}})
	tick := qTick{isRange: true, series: []sortedSeries{
		{labels: []Label{{Name: "provider", Value: "card"}}, ts: []int64{1}, vs: []float64{1}},
		{labels: []Label{{Name: "provider", Value: "bank"}}, ts: []int64{1}, vs: []float64{2}},
	}}
	res := cr.evalTick(time.Now(), map[string]qTick{"A": tick}, nil)
	if len(res) != 1 || res[0].state != stError || len(res[0].labels) != 0 || res[0].key != "{}" {
		t.Fatalf("unreduced series should dedupe to Error {}: %+v", res)
	}
}

type contextTimeEngine struct{}

func (contextTimeEngine) Instant(_ Query, at time.Time) ([]Result, error) {
	return []Result{{Points: map[int64]float64{at.UnixMilli(): float64(at.Unix())}}}, nil
}
func (contextTimeEngine) Range(q Query, start, end time.Time, step time.Duration) ([]Result, error) {
	v := end
	if q.Expr == "start()" {
		v = start
	}
	points := map[int64]float64{}
	for at := start; !at.After(end); at = at.Add(step) {
		points[at.UnixMilli()] = float64(v.Unix())
	}
	return []Result{{Points: points}}, nil
}

func TestContextTimeFunctionsUseInstantBounds(t *testing.T) {
	start := time.Unix(1700000000, 0)
	run := Run{Start: start, End: start.Add(2 * time.Minute)}
	for _, expr := range []string{"start()", "end()"} {
		d := fetchEngine(contextTimeEngine{}, Query{Datasource: "metrics", Expr: expr}, run, time.Minute)
		for at := run.Start; !at.After(run.End); at = at.Add(time.Minute) {
			got := d.at(at)
			if got.err != nil || len(got.nums) != 1 || got.nums[0].v != float64(at.Unix()) {
				t.Fatalf("%s at %s used whole-run bounds: %+v", expr, at, got)
			}
		}
	}
}
