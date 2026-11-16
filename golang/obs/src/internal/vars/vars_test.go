package vars

import (
	"reflect"
	"testing"
)

func TestFill(t *testing.T) {
	c := Case{
		Expr:   `rate(x[$__rate_interval]) + rate(y[$__interval]) + z[${__range}] + $__interval_ms + $__range_s`,
		FromMs: 0, ToMs: 6 * 3600 * 1000, Scrape: "15s",
	}
	c.Expr += ` + rate(z[$__auto_interval_interval]) + rate(w[${__auto_interval_foo}])`
	r := Fill(c)
	want := `rate(x[1m0s]) + rate(y[15s]) + z[21600s] + 15000 + 21600 + rate(z[15s]) + rate(w[15s])`
	if r.Expr != want || r.StepMs != 15000 {
		t.Errorf("got %q step %d", r.Expr, r.StepMs)
	}
}

func TestTemplateValues(t *testing.T) {
	c := Case{Expr: `a{p="$p", q=~"${q}", r=~"[[r]]"}`, Scrape: "15s", ToMs: 3600000,
		Vars: map[string][]string{"p": {"one"}, "q": {"a.b", "c"}, "r": {"x"}}}
	if got := Fill(c).Expr; got != `a{p="one", q=~"(a\\.b|c)", r=~"x"}` {
		t.Errorf("got %s", got)
	}
	if got := Names(`a{p="$p", q=~"${q:regex}"} [$__interval] $__rate_interval [[r]]`); !reflect.DeepEqual(got, []string{"p", "q", "r"}) {
		t.Errorf("names %v", got)
	}
}
