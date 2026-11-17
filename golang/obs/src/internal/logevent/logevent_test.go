package logevent

import (
	"strings"
	"testing"
	"time"
)

type events []string

func (e events) EventNames() []string { return []string(e) }

func TestEventSelectorsAndAssumptions(t *testing.T) {
	names := events{"checkout.failed", "checkout.ok", "refund.failed"}
	cases := []struct {
		name, expr string
		want       []string
		assumed    int
	}{
		{"exact", `"durian.cloud/event":=checkout.failed | stats count() as hits`, []string{"checkout.failed"}, 0},
		{"negative", `-"durian.cloud/event":=checkout.failed | stats count()`, []string{"checkout.ok", "refund.failed"}, 0},
		{"regex", `"durian.cloud/event":~"checkout\\..*" | stats count()`, []string{"checkout.failed", "checkout.ok"}, 0},
		{"in", `"durian.cloud/event":in(checkout.failed, typo) | stats count()`, []string{"checkout.failed"}, 0},
		{"fields", `_stream:{"service.name"="checkout"} "durian.cloud/event":=checkout.failed user_tier:=gold amount:>100 "timeout" | stats by ("service.name") count()`, []string{"checkout.failed"}, 3},
		{"negated fields", `"durian.cloud/event":=checkout.failed !(user_tier:=gold or amount:>100) | stats count()`, []string{"checkout.failed"}, 1},
		{"pre-stats filter", `* | filter "durian.cloud/event":=checkout.failed amount:>100 | stats count() | filter "count()":>2`, []string{"checkout.failed"}, 1},
		{"conditional", `"durian.cloud/event":=checkout.failed | stats count() if (user_tier:=gold) as hits`, []string{"checkout.failed"}, 1},
		{"global", `options(global_filter=("durian.cloud/event":=checkout.failed user_tier:=gold)) * | stats rate()`, []string{"checkout.failed"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := AnalyzeAt(tc.expr, names, time.Unix(1700000000, 0))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(a.Events, ",") != strings.Join(tc.want, ",") {
				t.Errorf("events %v want %v", a.Events, tc.want)
			}
			if len(a.Assumed) != tc.assumed {
				t.Errorf("assumed %v want count %d", a.Assumed, tc.assumed)
			}
			if strings.Contains(a.Expr, "user_tier") || strings.Contains(a.Expr, "amount") || strings.Contains(a.Expr, "timeout") {
				t.Errorf("assumed values retained: %s", a.Expr)
			}
			if tc.name == "pre-stats filter" && !strings.Contains(a.Expr, ">2") {
				t.Errorf("computed threshold was stripped: %s", a.Expr)
			}
			b, err := AnalyzeAt(a.Expr, names, time.Unix(1700000000, 0))
			if err != nil {
				t.Fatal("rewritten query does not parse:", a.Expr, err)
			}
			if len(b.Assumed) > 0 {
				t.Errorf("rewrite not idempotent: %s => %v", a.Expr, b.Assumed)
			}
		})
	}
}

func TestEventRefusals(t *testing.T) {
	names := events{"checkout.failed", "checkout.ok"}
	cases := []struct{ expr, want string }{
		{`user_tier:=gold | stats count()`, `must filter on the event`},
		{`_stream:{"durian.cloud/event"="checkout.failed"} | stats count()`, `inside _stream`},
		{`("durian.cloud/event":=checkout.failed or severity_text:=ERROR) | stats count()`, `inside an or`},
		{`!("durian.cloud/event":=checkout.failed or severity_text:=ERROR) | stats count()`, `inside an or`},
		{`"durian.cloud/event":=checkout.faile | stats count()`, `did you mean "checkout.failed"`},
		{`-"durian.cloud/event":=checkout.faile | stats count()`, `did you mean "checkout.failed"`},
		{`"durian.cloud/event":~nomatch | stats count()`, `matches no contract event`},
		{`"durian.cloud/event":in(nope, typo) | stats count()`, `matches no contract event`},
		{`"durian.cloud/event":checkout.failed | stats count()`, `must use :=`},
		{`"durian.cloud/event":=checkout.failed | stats sum(amount)`, `sum(amount)`},
		{`"durian.cloud/event":=checkout.failed | stats quantile(0.9, latency)`, `latency`},
		{`"durian.cloud/event":=checkout.failed | stats count(amount)`, `count(amount)`},
	}
	for _, tc := range cases {
		t.Run(tc.want+tc.expr, func(t *testing.T) {
			_, err := Analyze(tc.expr, names)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s: got %v want %s", tc.expr, err, tc.want)
			}
		})
	}
}

func TestNoContractAndSeed(t *testing.T) {
	q := `severity_text:=ERROR "timeout" | stats count()`
	a, err := Analyze(q, nil)
	if err != nil || !a.Skipped || a.Expr != q {
		t.Fatalf("no contract: %+v %v", a, err)
	}
	if err := ValidateSeed([]string{"checkout.failed"}, events{"checkout.failed"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSeed([]string{"checkout.faile"}, events{"checkout.failed"}); err == nil || !strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("bad seed passed: %v", err)
	}
}
