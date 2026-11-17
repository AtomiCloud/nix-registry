package alerts

import (
	"math"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/alerts/gfexpr/mathexp"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func TestReducerExactValuesAndMappings(t *testing.T) {
	// Literal expectations are independent of the vendored reducer/mapping code.
	// Order: mean, min, max, sum, count, last, median.
	names := []string{"mean", "min", "max", "sum", "count", "last", "median"}
	p := func(v float64) *float64 { return &v }
	nan, inf, negInf := math.NaN(), math.Inf(1), math.Inf(-1)
	expected := func(xs ...float64) []*float64 {
		out := make([]*float64, len(xs))
		for i, v := range xs {
			out[i] = p(v)
		}
		return out
	}
	emptyDrop := []*float64{nil, nil, nil, p(0), p(0), nil, nil}
	cases := []struct {
		name                  string
		input                 []*float64
		strict, drop, replace []*float64
	}{
		{"finite", []*float64{p(8), p(1), p(4), p(2)}, expected(3.75, 1, 8, 15, 4, 2, 3), expected(3.75, 1, 8, 15, 4, 2, 3), expected(3.75, 1, 8, 15, 4, 2, 3)},
		{"NaN", []*float64{p(8), p(nan), p(4), p(2)}, expected(nan, nan, nan, nan, 4, 2, nan), expected(14.0/3, 2, 8, 14, 3, 2, 4), expected(5, 2, 8, 20, 4, 2, 5)},
		{"positive infinity", []*float64{p(8), p(inf), p(4), p(2)}, expected(inf, 2, inf, inf, 4, 2, 6), expected(14.0/3, 2, 8, 14, 3, 2, 4), expected(5, 2, 8, 20, 4, 2, 5)},
		{"negative infinity", []*float64{p(8), p(negInf), p(4), p(2)}, expected(negInf, negInf, 8, negInf, 4, 2, 3), expected(14.0/3, 2, 8, 14, 3, 2, 4), expected(5, 2, 8, 20, 4, 2, 5)},
		{"null", []*float64{p(8), nil, p(4), p(2)}, expected(nan, nan, nan, nan, 4, 2, nan), expected(14.0/3, 2, 8, 14, 3, 2, 4), expected(5, 2, 8, 20, 4, 2, 5)},
		{"last null", []*float64{p(8), p(1), p(4), nil}, []*float64{p(nan), p(nan), p(nan), p(nan), p(4), nil, p(nan)}, expected(13.0/3, 1, 8, 13, 3, 4, 4), expected(4.75, 1, 8, 19, 4, 6, 5)},
		{"last NaN", []*float64{p(8), p(1), p(4), p(nan)}, expected(nan, nan, nan, nan, 4, nan, nan), expected(13.0/3, 1, 8, 13, 3, 4, 4), expected(4.75, 1, 8, 19, 4, 6, 5)},
		{"all nonnumbers", []*float64{nil, p(nan), p(inf)}, expected(nan, nan, nan, nan, 3, inf, nan), emptyDrop, expected(6, 6, 6, 18, 3, 6, 6)},
		{"empty", nil, expected(nan, nan, nan, 0, 0, nan, nan), emptyDrop, expected(6, 6, 6, 0, 0, 6, 6)},
	}
	for _, tc := range cases {
		for _, mode := range []string{"strict", "dropNN", "replaceNN"} {
			for i, name := range names {
				t.Run(tc.name+"/"+mode+"/"+name, func(t *testing.T) {
					wants := tc.strict
					if mode == "dropNN" {
						wants = tc.drop
					}
					if mode == "replaceNN" {
						wants = tc.replace
					}
					want := wants[i]
					replace := 6.0
					r := compile(t, Alert{Condition: "B", Expressions: map[string]ExprDef{"B": {Reduce: &ReduceDef{Input: "A", Function: name, Mode: mode, ReplaceWith: &replace}}}})
					series := mathexp.NewSeries("A", data.Labels{"provider": "fixture"}, len(tc.input))
					for i, v := range tc.input {
						series.SetPoint(i, time.Unix(int64(i), 0), v)
					}
					got, err := r.exprs[0].exec(time.Now(), mathexp.Vars{"A": {Values: mathexp.Values{series}}}, nil)
					if err != nil {
						t.Fatal(err)
					}
					if len(got.Values) != 1 {
						t.Fatalf("got %d results", len(got.Values))
					}
					number, ok := got.Values[0].(mathexp.Number)
					if !ok {
						t.Fatalf("got %T", got.Values[0])
					}
					actual := number.GetFloat64Value()
					if want == nil {
						if actual != nil {
							t.Fatalf("want null, got %v", *actual)
						}
					} else {
						if actual == nil {
							t.Fatalf("want %v, got null", *want)
						}
						same := *actual == *want || (math.IsNaN(*actual) && math.IsNaN(*want)) || (!math.IsNaN(*actual) && !math.IsInf(*actual, 0) && !math.IsInf(*want, 0) && math.Abs(*actual-*want) < 1e-12)
						if !same {
							t.Fatalf("got %v want %v", *actual, *want)
						}
					}
					state, _, has := stateFromVal(actual)
					wantState, wantHas := stAlerting, true
					if want == nil {
						wantState, wantHas = stNoData, false
					} else if *want == 0 {
						wantState = stNormal
					}
					if state != wantState || has != wantHas {
						t.Fatalf("state %s/%v want %s/%v", stName[state], has, stName[wantState], wantHas)
					}
					if number.GetLabels()["provider"] != "fixture" {
						t.Fatal("reducer discarded instance labels")
					}
				})
			}
		}
	}
}
