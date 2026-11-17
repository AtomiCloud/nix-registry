package logevent

import (
	"github.com/VictoriaMetrics/VictoriaLogs/lib/logstorage"
	"strings"
	"testing"
	"time"
)

func TestSubqueryPolicyCannotBeBypassed(t *testing.T) {
	for _, tc := range []struct{ name, expr, token string }{
		{"union unknown", `"durian.cloud/event":=checkout.failed | union ("durian.cloud/event":=checkout.typo) | stats count()`, `event "checkout.typo" is not in the contract`},
		{"nested union unknown", `"durian.cloud/event":=checkout.failed | union ("durian.cloud/event":=checkout.ok | union ("durian.cloud/event":=checkout.typo)) | stats count()`, `event "checkout.typo" is not in the contract`},
		{"join unknown", `"durian.cloud/event":=checkout.failed | join by (trace_id) ("durian.cloud/event":=checkout.typo) | stats count()`, `event "checkout.typo" is not in the contract`},
		{"in subquery unknown", `"durian.cloud/event":=checkout.failed user:in("durian.cloud/event":=checkout.typo | fields user) | stats count()`, `event "checkout.typo" is not in the contract`},
		{"event in subquery unknown", `"durian.cloud/event":in("durian.cloud/event":=checkout.typo | fields "durian.cloud/event") | stats count()`, `event "checkout.typo" is not in the contract`},
		{"nested stored stats", `"durian.cloud/event":=checkout.failed | union ("durian.cloud/event":=checkout.ok | stats sum(amount) as n) | stats count()`, "sum(amount)"},
		{"conditional pipe unknown", `"durian.cloud/event":=checkout.failed | unpack_json if ("durian.cloud/event":=checkout.typo) | stats count()`, `event "checkout.typo" is not in the contract`},
		{"inline rows", `"durian.cloud/event":=checkout.failed | union rows({"durian.cloud/event":"checkout.typo"}) | stats count()`, "pipe form not checkable"},
		{"stream context", `"durian.cloud/event":=checkout.failed | stream_context before 1 after 1 | stats count()`, "pipe form not checkable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := logstorage.ParseQueryAtTimestamp(tc.expr, time.Now().UnixNano()); err != nil {
				t.Fatalf("invalid test syntax: %v", err)
			}
			if _, err := Analyze(tc.expr, events{"checkout.failed", "checkout.ok"}); err == nil || !strings.Contains(err.Error(), tc.token) {
				t.Fatalf("%s bypassed nested policy: got %v, want %s", tc.expr, err, tc.token)
			}
		})
	}
}

func TestNestedAssumptionsAreStripped(t *testing.T) {
	expr := `"durian.cloud/event":=checkout.failed | union ("durian.cloud/event":=checkout.ok customer_tier:=gold) | stats count()`
	a, err := Analyze(expr, events{"checkout.failed", "checkout.ok"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.Expr, "customer_tier") || len(a.Assumed) != 1 {
		t.Fatalf("nested assumed field retained: %+v", a)
	}
	if strings.Join(a.Events, ",") != "checkout.failed,checkout.ok" {
		t.Fatalf("nested events absent: %+v", a)
	}
}

func TestConditionalPipeAssumptionsAreStripped(t *testing.T) {
	a, err := Analyze(`"durian.cloud/event":=checkout.failed | unpack_json if (customer_tier:=gold) | stats count()`, events{"checkout.failed"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.Expr, "customer_tier") || len(a.Assumed) != 1 {
		t.Fatalf("conditional pipe escaped assumption policy: %+v", a)
	}
}
