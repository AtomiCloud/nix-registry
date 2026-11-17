package contract

import (
	"os"
	"strings"
	"testing"
)

const fixture = "../../../tests/fixtures/gen/good/observability/contract.yaml"

func TestFixtureIsValid(t *testing.T) {
	c, err := Load(fixture)
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
	base, err := os.ReadFile(fixture)
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
