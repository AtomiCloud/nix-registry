package contract

import (
	"os"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/testfix"
)

func TestFixtureIsValid(t *testing.T) {
	c, err := Load(testfix.GoodContract(t))
	if err != nil {
		t.Fatal(err)
	}
	if is := c.Validate(); len(is) > 0 {
		t.Fatalf("fixture contract has issues: %v", is)
	}
	if len(c.Hash) != 64 {
		t.Fatalf("hash %q", c.Hash)
	}
	if w, ok := c.WorstCase("checkout.charges"); !ok || w != 3456 {
		t.Fatalf("worst case %d %v, want 3456", w, ok)
	}
	if w, _ := c.WorstCase("checkout.charge.duration"); w != 126 {
		t.Fatalf("histogram worst case %d, want 126", w)
	}
	if got := c.MetricOrder[0]; got != "checkout.charges" {
		t.Fatalf("file order lost: %v", c.MetricOrder)
	}
	if got := c.StreamCount(3); got != 6 {
		t.Fatalf("stream count %d, want 6 (3 landscapes x 2 modules)", got)
	}
}

func TestStoredNames(t *testing.T) {
	got := StoredNames("checkout.charge.duration", Metric{Kind: MetricHistogram, Unit: "seconds"})
	if strings.Join(got, ",") != "checkout_charge_duration_seconds_bucket,checkout_charge_duration_seconds_sum,checkout_charge_duration_seconds_count" {
		t.Fatal(got)
	}
	if got := StoredNames("checkout.charges", Metric{Kind: MetricCounter}); got[0] != "checkout_charges_total" {
		t.Fatal(got)
	}
}

// each failure of concept 11.4 is a rule that names the path and the bad token
func TestFailures(t *testing.T) {
	base, err := os.ReadFile(testfix.GoodContract(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, from, to, rule, token string
	}{
		{"free metric label", "labels: { provider: provider, outcome: outcome, shard: shard, hour: hour }", "labels: { provider: provider, user_id: user_id }", "free-metric-label", "user_id"},
		{"free stream label", "ChargeFailed: { event: checkout.charge.failed, level: error }", "ChargeFailed: { event: checkout.charge.failed, level: error, stream_labels: { who: user_id } }", "free-stream-label", "who"},
		{"date stream label", "ChargeFailed: { event: checkout.charge.failed, level: error }", "ChargeFailed: { event: checkout.charge.failed, level: error, stream_labels: { d: billing_day } }", "date-stream-label", "billing_day"},
		{"date no window", "billing_day: { kind: date, window_days: 30 }", "billing_day: { kind: date }", "date-no-window", "billing_day"},
		{"over budget", "budget: 5000", "budget: 3000", "over-budget", "3456"},
		{"gauge neither", "kind: gauge\n    across_pods: max\n    unit: seconds", "kind: gauge\n    unit: seconds", "gauge-pods", "checkout.worker.lag"},
		{"no kind", "checkout.config.generation:\n    kind: gauge", "checkout.config.generation:\n    unit: bytes", "metric-kind", "checkout.config.generation"},
		{"unknown type", "labels: { lane: lane }", "labels: { lane: nolane }", "label-type", "nolane"},
	}
	for _, tc := range cases {
		s := string(base)
		if !strings.Contains(s, tc.from) {
			t.Fatalf("%s: fixture lacks %q", tc.name, tc.from)
		}
		c, err := Parse("c.yaml", []byte(strings.Replace(s, tc.from, tc.to, 1)))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		found := false
		for _, is := range c.Validate() {
			if is.Rule == tc.rule && strings.Contains(is.Msg+is.Path, tc.token) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want rule %s naming %q, got %v", tc.name, tc.rule, tc.token, c.Validate())
		}
	}
}

func TestUnknownKeyFails(t *testing.T) {
	if _, err := Parse("c.yaml", []byte("service: x\nmetricz: {}\n")); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func parse(t *testing.T, doc string) *Contract {
	t.Helper()
	c, err := Parse("c.yaml", []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func hasRule(is []Issue, rule, token string) bool {
	for _, i := range is {
		if i.Rule == rule && strings.Contains(i.Path+i.Msg, token) {
			return true
		}
	}
	return false
}

// Review of PR 111, finding 2: a log event declares no stream field of its own (concept 3.3).
func TestBoundedStreamLabelIsRefused(t *testing.T) {
	c := parse(t, `
service: s
types: { provider: { kind: enum, values: [stripe, adyen] } }
logs: { Failed: { event: a.failed, level: error, stream_labels: { provider: provider } } }
`)
	if !hasRule(c.Validate(), "stream-label-declared", "provider") {
		t.Fatalf("a bounded stream label passed: %v", c.Validate())
	}
	if c.StreamFields()["provider"] {
		t.Fatal("StreamFields lists a per-event stream label")
	}
	if got := c.StreamCount(2); got != 2 {
		t.Fatalf("stream count %d, want landscapes x modules = 2", got)
	}
}

// Review finding 8: a product that does not fit in 64 bits is over any budget, not 0.
func TestOverflowIsOverBudget(t *testing.T) {
	c := parse(t, `
service: s
types: { big: { kind: int, min: 0, max: 2147483647 } }
metrics: { m: { kind: counter, budget: 1, labels: { a: big, b: big, c: big } } }
`)
	if !hasRule(c.Validate(), "over-budget", "m") {
		t.Fatalf("overflowing worst case passed: %v", c.Validate())
	}
	if w, _ := c.WorstCase("m"); w != MaxWorst {
		t.Fatalf("worst case %d, want saturated %d", w, MaxWorst)
	}
	c2 := parse(t, "service: s\ntypes: { huge: { kind: int, min: -9223372036854775808, max: 9223372036854775807 } }\n")
	if got := c2.Types["huge"].Size(); got != int(MaxWorst) {
		t.Fatalf("size of the whole int range %d, want saturated", got)
	}
}

// Review finding 10: the concept's own example writes `free` without declaring it.
func TestIntrinsicFreeInSpanAttributes(t *testing.T) {
	c := parse(t, `
service: checkout
types: { provider: { kind: enum, values: [stripe] } }
spans:
  checkout.charge: { attributes: { user_id: free, provider: provider } }
`)
	if is := c.Validate(); len(is) > 0 {
		t.Fatalf("the concept 3.1 example is refused: %v", is)
	}
	if c.Types["free"].Kind != KindFree {
		t.Fatal("free was not made")
	}
	// ... but still never a metric label.
	m := parse(t, `
service: s
metrics: { m: { kind: counter, budget: 5, labels: { user_id: free } } }
`)
	if !hasRule(m.Validate(), "free-metric-label", "user_id") {
		t.Fatalf("free metric label passed: %v", m.Validate())
	}
}
