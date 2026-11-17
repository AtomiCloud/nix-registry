package check

import (
	"reflect"
	"testing"

	"github.com/VictoriaMetrics/metricsql"
)

func parse(t *testing.T, q string) metricsql.Expr {
	t.Helper()
	e, err := metricsql.Parse(q)
	if err != nil {
		t.Fatalf("parse %q: %v", q, err)
	}
	return e
}

func TestStripThresholds(t *testing.T) {
	cases := []struct {
		in   string
		want string
		n    int
	}{
		{`sum(rate(x_total[5m])) > 10`, `sum(rate(x_total[5m]))`, 1},
		{`10 < sum(rate(x_total[5m]))`, `sum(rate(x_total[5m]))`, 1},
		{`a_total > 14.4 * 0.001`, `a_total`, 1},
		{`(a_total > 1) and (b_total > 2)`, `a_total and b_total`, 2},
		{`a_total > b_total`, `a_total > b_total`, 0},         // a comparison of two series stays
		{`a_total > bool 1`, `a_total > bool 1`, 0},           // `bool` keeps every row
		{`sum(a_total > 1) by (x)`, `sum(a_total) by (x)`, 1}, // inside an aggregate
		{`absent(x_total)`, `x_total`, 1},
		{`absent_over_time(x_total[5m])`, `count_over_time(x_total[5m])`, 1},
		{`sum(absent(x_total) > 0)`, `sum(x_total)`, 2},
	}
	for _, c := range cases {
		e, n := stripThresholds(parse(t, c.in))
		got := string(e.AppendString(nil))
		want := string(parse(t, c.want).AppendString(nil))
		if got != want || n != c.n {
			t.Errorf("%q: got %q (%d), want %q (%d)", c.in, got, n, want, c.n)
		}
	}
}

func TestUnsupportedAndWindowless(t *testing.T) {
	if got := windowlessRollups(parse(t, `sum(rate(x_total)) + increase(y_total[5m])`)); !reflect.DeepEqual(got, []string{"rate"}) {
		t.Errorf("windowless: %v", got)
	}
	if got := windowlessRollups(parse(t, `sum(rate(x_total[5m]))`)); len(got) != 0 {
		t.Errorf("windowed rollup flagged: %v", got)
	}
	// VictoriaMetrics' parser itself refuses a Prometheus-only function.
	if _, err := metricsql.Parse(`double_exponential_smoothing(x[5m], 0.5, 0.5)`); err == nil {
		t.Errorf("double_exponential_smoothing should be refused by the VictoriaMetrics parser")
	} else if m := unsupportedFnRe.FindStringSubmatch(err.Error()); m == nil || m[1] != "double_exponential_smoothing" {
		t.Errorf("error does not name the function: %v", err)
	}
	// Every common Prometheus function VictoriaMetrics implements is accepted.
	for _, q := range []string{
		`histogram_quantile(0.9, sum by (le) (rate(x_bucket[5m])))`, `label_replace(x, "a", "$1", "b", "(.*)")`,
		`predict_linear(x[1h], 3600)`, `absent(x)`, `increase(x[5m])`, `irate(x[5m])`, `deriv(x[5m])`, `clamp_min(x, 0)`,
		`topk(3, x)`, `quantile_over_time(0.9, x[5m])`, `scalar(sum(x))`, `time() - process_start_time_seconds`,
	} {
		if got := unsupportedFuncs(parse(t, q)); len(got) != 0 {
			t.Errorf("%q flagged: %v", q, got)
		}
	}
}

func TestMetricNames(t *testing.T) {
	m, l := metricNames(parse(t, `sum by (pod) (rate(a_total{job="x", env=~"p.*"}[5m])) / on(pod) b_total`))
	if !reflect.DeepEqual(m, []string{"a_total", "b_total"}) {
		t.Errorf("metrics %v", m)
	}
	if !reflect.DeepEqual(l, []string{"env", "job", "pod"}) {
		t.Errorf("labels %v", l)
	}
}
