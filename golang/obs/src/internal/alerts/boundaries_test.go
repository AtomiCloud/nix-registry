package alerts

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func TestThresholdBoundaries(t *testing.T) {
	cases := []struct {
		typ    string
		params []float64
		want   []bool
	}{
		{"gt", []float64{1}, []bool{false, false, true, true, true, false}},
		{"lt", []float64{3}, []bool{true, true, true, false, false, false}},
		{"eq", []float64{2}, []bool{false, false, true, false, false, false}},
		{"ne", []float64{2}, []bool{true, true, false, true, true, true}},
		{"gte", []float64{2}, []bool{false, false, true, true, true, false}},
		{"lte", []float64{2}, []bool{true, true, true, false, false, false}},
		{"within_range", []float64{1, 3}, []bool{false, false, true, false, false, false}},
		{"outside_range", []float64{1, 3}, []bool{true, false, false, false, true, false}},
		{"within_range_included", []float64{1, 3}, []bool{false, true, true, true, false, false}},
		{"outside_range_included", []float64{1, 3}, []bool{true, true, false, true, true, false}},
	}
	values := []float64{0, 1, 2, 3, 4, math.NaN()}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			th, err := newThresh(tc.typ, tc.params)
			if err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				if got := th.eval(v); got != tc.want[i] {
					t.Fatalf("%s(%v)=%v want %v", tc.typ, v, got, tc.want[i])
				}
				inverted := th
				inverted.invert = true
				if got := inverted.eval(v); got == tc.want[i] {
					t.Fatalf("recovery inversion %s(%v)=%v", tc.typ, v, got)
				}
			}
		})
	}
}

func TestNoDataErrorExactPendingBoundary(t *testing.T) {
	for _, input := range []int{stNoData, stError} {
		for _, mapping := range []string{stName[input], "Alerting"} {
			t.Run(stName[input]+"/"+mapping, func(t *testing.T) {
				a := Alert{For: "2m"}
				if input == stNoData {
					a.NoDataState = mapping
				} else {
					a.ExecErrState = mapping
				}
				rs := newRuleStates(compile(t, a))
				base := time.Unix(1700000000, 0)
				final := input
				if mapping == "Alerting" {
					final = stAlerting
				}
				for minute, want := range []int{stPending, stPending, final, final} {
					rs.process(base.Add(time.Duration(minute)*time.Minute), []evalResult{{key: "{}", state: input}})
					if got := rs.states["{}"].state; got != want {
						t.Fatalf("minute %d: %s want %s", minute, stName[got], stName[want])
					}
				}
			})
		}
	}
}

func TestErrorOriginLabelsOnlyForErrorMapping(t *testing.T) {
	for _, mapping := range []string{"Error", "Alerting", "OK", "KeepLast"} {
		t.Run(mapping, func(t *testing.T) {
			cr := compile(t, Alert{ExecErrState: mapping})
			q := map[string]qTick{"A": {err: fmt.Errorf("fixture error")}}
			for _, res := range [][]evalResult{cr.evalTick(time.Now(), q, nil), cr.evalTickLight(q, nil)} {
				if len(res) != 1 || res[0].state != stError {
					t.Fatalf("bad eval error %+v", res)
				}
				if mapping == "Error" {
					want := data.Labels{"datasource_uid": "fixture-metrics", "ref_id": "A"}
					if res[0].labels["datasource_uid"] != want["datasource_uid"] || res[0].labels["ref_id"] != "A" {
						t.Fatalf("Error mapping lost origin: %v", res[0].labels)
					}
				} else if len(res[0].labels) != 0 || res[0].key != "{}" {
					t.Fatalf("%s must have empty error labels, got %+v", mapping, res)
				}
			}
		})
	}
}
