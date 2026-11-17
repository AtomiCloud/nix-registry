package alerts

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func compile(t *testing.T, a Alert) *cRule {
	t.Helper()
	if a.Condition == "" {
		a.Condition = "$A > 1"
	}
	if a.Queries == nil {
		a.Queries = map[string]Query{"A": {Datasource: "metrics", UID: "fixture-metrics", Expr: "m"}}
	}
	r, err := compileRule(&a)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPendingRecoveryAndRelapse(t *testing.T) {
	r := compile(t, Alert{For: "2m", KeepFiringFor: "3m"})
	rs := newRuleStates(r)
	base := time.Unix(1700000000, 0)
	cases := []struct{ minute, state, want int }{
		{0, stAlerting, stPending}, {1, stAlerting, stPending}, {2, stNormal, stNormal},
		{3, stAlerting, stPending}, {4, stAlerting, stPending}, {5, stAlerting, stAlerting},
		{6, stNormal, stRecovering}, {7, stNormal, stRecovering}, {8, stAlerting, stAlerting},
		{9, stNormal, stRecovering}, {10, stNormal, stRecovering}, {11, stNormal, stRecovering}, {12, stNormal, stNormal},
	}
	for _, c := range cases {
		rs.process(base.Add(time.Duration(c.minute)*time.Minute), []evalResult{{key: "{}", state: c.state}})
		if got := rs.states["{}"].state; got != c.want {
			t.Fatalf("minute %d input %s got %s want %s", c.minute, stName[c.state], stName[got], stName[c.want])
		}
	}
}

func TestNoDataAndErrorMappings(t *testing.T) {
	for _, input := range []int{stNoData, stError} {
		choices := []string{"Alerting", "OK", "KeepLast", stName[input]}
		for _, choice := range choices {
			t.Run(stName[input]+"/"+choice, func(t *testing.T) {
				a := Alert{For: "2m", NoDataState: "NoData", ExecErrState: "Error"}
				if input == stNoData {
					a.NoDataState = choice
				} else {
					a.ExecErrState = choice
				}
				r := compile(t, a)
				rs := newRuleStates(r)
				base := time.Unix(1700000000, 0)
				for minute := 0; minute <= 3; minute++ {
					rs.process(base.Add(time.Duration(minute)*time.Minute), []evalResult{{key: "{}", state: input}})
				}
				want := input
				switch choice {
				case "Alerting":
					want = stAlerting
				case "OK", "KeepLast":
					want = stNormal
				}
				if got := rs.states["{}"].state; got != want {
					t.Fatalf("got %s want %s", stName[got], stName[want])
				}
			})
		}
	}
	// KeepLast respects elapsed pending time, including the tick that matures it.
	r := compile(t, Alert{For: "2m", NoDataState: "KeepLast"})
	rs := newRuleStates(r)
	at := time.Unix(1700000000, 0)
	for minute, state := range []int{stAlerting, stNoData, stNoData} {
		rs.process(at.Add(time.Duration(minute)*time.Minute), []evalResult{{key: "{}", state: state}})
	}
	if rs.states["{}"].state != stAlerting {
		t.Fatal("KeepLast did not mature pending")
	}
}

func TestMissingSeriesResolution(t *testing.T) {
	for _, state := range []int{stNormal, stAlerting, stPending, stNoData, stError} {
		t.Run(stName[state], func(t *testing.T) {
			r := compile(t, Alert{})
			rs := newRuleStates(r)
			base := time.Unix(1700000000, 0)
			rs.states["old"] = &instState{state: state, lastEval: base}
			rs.process(base.Add(time.Minute), []evalResult{{key: "new", state: stNormal}})
			_, exists := rs.states["old"]
			wantExists := state != stNoData && state != stError
			if exists != wantExists {
				t.Fatalf("after one missing eval: exists=%v want %v", exists, wantExists)
			}
			trs := rs.process(base.Add(2*time.Minute), []evalResult{{key: "new", state: stNormal}})
			if _, ok := rs.states["old"]; ok {
				t.Fatal("instance survived two missing evaluations")
			}
			if wantExists {
				found := false
				for _, tr := range trs {
					found = found || strings.Contains(tr.To, "MissingSeries")
				}
				if !found {
					t.Fatal("missing resolution reason absent")
				}
			}
		})
	}
}

func TestFullLightAgreement(t *testing.T) {
	values := []float64{-1, 0, 1, 2, math.NaN(), math.Inf(1), math.Inf(-1)}
	expressions := []string{"$A > 1", "$A + $B", "($A > 1 && $B < 2) || $A == 0", "-$A", "($A + 1) / ($B - 1)"}
	labels := []data.Labels{nil, {"provider": "card"}, {"provider": "card", "region": "fixture"}, {"__name__": "m"}}
	at := time.Unix(1700000000, 0)
	for _, expr := range expressions {
		r := compile(t, Alert{Condition: expr, Queries: map[string]Query{"A": {Datasource: "metrics", UID: "first"}, "B": {Datasource: "logs", UID: "second"}}})
		for _, x := range values {
			for _, y := range values {
				for _, la := range labels {
					for _, lb := range labels {
						q := map[string]qTick{"A": {nums: []instVal{{labels: la, v: x}}}, "B": {nums: []instVal{{labels: lb, v: y}}}}
						fast, full := r.evalTickLight(q, nil), r.evalTick(at, q, nil)
						if !sameResults(fast, full) {
							t.Fatalf("%s x=%v y=%v labels=%v/%v\nlight=%s\nfull=%s", expr, x, y, la, lb, fmtRes(fast), fmtRes(full))
						}
					}
				}
			}
		}
		for _, q := range []map[string]qTick{
			{"A": {}, "B": {}},
			{"A": {err: fmt.Errorf("fixture error")}, "B": {}},
			{"A": {nums: []instVal{{v: 2}, {v: 3}}}, "B": {nums: []instVal{{v: 1}}}},
		} {
			if fast, full := r.evalTickLight(q, nil), r.evalTick(at, q, nil); !sameResults(fast, full) {
				t.Fatalf("%s: %s != %s", expr, fmtRes(fast), fmtRes(full))
			}
		}
	}
}

func TestHysteresisAndReducers(t *testing.T) {
	rc := &struct {
		Type   string    `yaml:"type"`
		Params []float64 `yaml:"params"`
	}{"lt", []float64{1}}
	a := Alert{Condition: "C", Expressions: map[string]ExprDef{"B": {Reduce: &ReduceDef{Input: "A", Function: "mean"}}, "C": {Threshold: &ThresholdDef{Input: "B", Type: "gt", Params: []float64{2}, Recover: rc}}}}
	r := compile(t, a)
	at := time.Unix(1700000000, 0)
	q := map[string]qTick{"A": {nums: []instVal{{labels: data.Labels{"provider": "card"}, v: 1.5}}}}
	loaded := map[string]bool{lblKey(data.Labels{"provider": "card"}): true}
	for _, load := range []map[string]bool{nil, loaded} {
		fast, full := r.evalTickLight(q, load), r.evalTick(at, q, load)
		if !sameResults(fast, full) {
			t.Fatal("hysteresis paths differ")
		}
		want := stNormal
		if load != nil {
			want = stAlerting
		}
		if fast[0].state != want {
			t.Fatalf("loaded=%v got %s want %s", load, stName[fast[0].state], stName[want])
		}
	}
	for _, fn := range []string{"mean", "min", "max", "sum", "count", "last"} {
		r := compile(t, Alert{Condition: "B", Expressions: map[string]ExprDef{"B": {Reduce: &ReduceDef{Input: "A", Function: fn, Mode: "dropNN"}}}})
		rs := r.evalTick(at, map[string]qTick{"A": {isRange: true, series: []sortedSeries{{ts: []int64{1, 2, 3}, vs: []float64{1, math.NaN(), 3}}}}}, nil)
		if len(rs) != 1 || rs[0].state == stError || math.IsNaN(rs[0].value) {
			t.Fatalf("%s dropNN: %+v", fn, rs)
		}
	}
}

func TestUIDsCompileRefusalsAndKeys(t *testing.T) {
	r := compile(t, Alert{Condition: "$A > 1"})
	res := r.evalTick(time.Now(), map[string]qTick{"A": {}}, nil)
	if res[0].labels["datasource_uid"] != "fixture-metrics" || res[0].labels["ref_id"] != "A" {
		t.Fatalf("NoData labels: %v", res)
	}
	res = r.evalTick(time.Now(), map[string]qTick{"A": {err: fmt.Errorf("bad query")}}, nil)
	if res[0].labels["datasource_uid"] != "fixture-metrics" {
		t.Fatalf("Error labels: %v", res)
	}
	for _, a := range []Alert{
		{Condition: "A", Interval: "0s"}, {Condition: "A", For: "-1m"}, {Condition: "A", NoDataState: "typo"},
		{Condition: "$missing > 1"}, {Condition: "B", Expressions: map[string]ExprDef{"B": {Math: "$C"}, "C": {Math: "$B"}}},
	} {
		if _, err := compileRule(&a); err == nil {
			t.Fatalf("bad rule accepted: %+v", a)
		}
	}
	if lblKey(data.Labels{"a": "b,c=d"}) == lblKey(data.Labels{"a": "b", "c": "d"}) {
		t.Fatal("label delimiter collision")
	}
}
