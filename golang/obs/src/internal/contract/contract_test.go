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

// Round 2, finding 8: buckets + 2 saturates before it is multiplied.
func TestHistogramBucketsOverflow(t *testing.T) {
	c := parse(t, `
service: s
metrics: { h: { kind: histogram, buckets: 9223372036854775807, budget: 1 } }
`)
	if w, _ := c.WorstCase("h"); w != MaxWorst {
		t.Fatalf("worst case %d, want saturated %d", w, MaxWorst)
	}
	if !hasRule(c.Validate(), "over-budget", "h") {
		t.Fatalf("a histogram with MaxInt64 buckets passed: %v", c.Validate())
	}
}

// Round 2, finding 10: the stored series names must not intersect, not only the dotted base names.
func TestStoredNamesDoNotIntersect(t *testing.T) {
	for name, doc := range map[string]string{
		"counter + gauge": `
service: s
metrics:
  requests: { kind: counter }
  requests_total: { kind: gauge, per_pod: true }
`,
		"histogram + unit gauge": `
service: s
metrics:
  d: { kind: histogram, unit: seconds }
  d_seconds_bucket: { kind: gauge, across_pods: max }
`,
		"histogram sum": `
service: s
metrics:
  d: { kind: histogram }
  d_sum: { kind: gauge, across_pods: max }
`,
	} {
		if is := parse(t, doc).Validate(); !hasRule(is, "metric-duplicate", "stored as the series") {
			t.Errorf("%s: colliding stored names passed: %v", name, is)
		}
	}
	// distinct stored names stay fine
	if is := parse(t, "service: s\nmetrics:\n  a: { kind: counter }\n  b: { kind: gauge, across_pods: max }\n").Validate(); len(is) > 0 {
		t.Fatalf("distinct metrics refused: %v", is)
	}
}

// Opus 4 and GPT (c): a trace resource carries the pod keys; an unscoped attribute is a span OR a resource attribute.
func TestHasSpanAttributeScopes(t *testing.T) {
	c := parse(t, `
service: s
types: { t: { kind: enum, values: [x] } }
spans: { a.b: { attributes: { provider: t } } }
`)
	for _, tc := range []struct {
		scope, name string
		want        bool
	}{
		{"resource", "k8s.pod.name", true},
		{"resource", "k8s.pod.uid", true},
		{"resource", "service.name", true},
		{"resource", "provider", false},
		{"span", "provider", true},
		{"span", "service.name", false},
		{"", "service.name", true},
		{"", "provider", true},
		{"", "k8s.pod.name", true},
		{"", "nonsense", false},
	} {
		if got := c.HasSpanAttribute(tc.scope, tc.name); got != tc.want {
			t.Errorf("HasSpanAttribute(%q, %q) = %v, want %v", tc.scope, tc.name, got, tc.want)
		}
	}
}

// Opus 2: a unit that does not change the stored name the way the OTLP path does is refused.
func TestUnknownUnitRefused(t *testing.T) {
	c := parse(t, "service: s\nmetrics: { lat: { kind: histogram, unit: milliseconds } }\n")
	if !hasRule(c.Validate(), "metric-unit", "milliseconds") {
		t.Fatalf("unit milliseconds passed: %v", c.Validate())
	}
	for _, u := range []string{"s", "seconds", "By", "bytes", ""} {
		c := parse(t, "service: s\nmetrics: { lat: { kind: histogram, unit: '"+u+"' } }\n")
		if hasRule(c.Validate(), "metric-unit", "") {
			t.Errorf("unit %q refused", u)
		}
	}
}
