package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

func TestReviewerGeneratedLabelGrouping(t *testing.T) {
	const expr = `sum by (payment_provider) (label_set(checkout_charges_total, "payment_provider", "review"))`
	p := filepath.Join(t.TempDir(), "seed.yaml")
	if err := os.WriteFile(p, []byte(dottedSeed), 0600); err != nil {
		t.Fatal(err)
	}
	sd, err := seed.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := seed.Build(sd, time.Now().UTC().Truncate(time.Minute).Add(-sd.Duration()-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	e, err := vm.New(ds)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := e.Instant(expr, ds.End)
	if err != nil || len(rows) != 1 {
		t.Fatalf("engine control: rows=%+v err=%v", rows, err)
	}
	t.Logf("real embedded engine accepts the generated payment_provider label and returns rows: %+v", rows)
	e.Close()
	r := dottedLive(t, expr)
	if r.Failures() != 0 {
		t.Fatalf("valid query-generated label must pass live; got %+v", r.Findings)
	}
}

// Labels a query creates are not stored labels: reading them back is fine in any spelling, in a grouping, a
// join modifier or a label function. A read of a stored dotted label's underscore twin the query did NOT
// create is still wrong, even beside a created label.
func TestLiveGeneratedLabelsAreNotStoredLabels(t *testing.T) {
	for _, expr := range []string{
		`sum by (payment_provider) (label_set(checkout_charges_total, "payment_provider", "review"))`,
		`sum by (provider_copy) (label_copy(checkout_charges_total, "payment.provider", "provider_copy"))`,
		`sum by (durian_cloud_x) (label_replace(checkout_charges_total, "durian_cloud_x", "$1", "outcome", "(.*)"))`,
		`sum by (payment_provider) (label_join(checkout_charges_total, "payment_provider", "-", "outcome", "payment.provider"))`,
		`sum by (payment_provider) (label_set(checkout_charges_total, "payment_provider", "a")) / on (payment_provider) group_left sum by (payment_provider) (label_set(checkout_charges_total, "payment_provider", "a"))`,
		`count_values("durian_cloud_v", checkout_charges_total)`,
	} {
		if r := dottedLive(t, expr); r.Failures() != 0 {
			t.Errorf("%s: a query-created label must pass live, got %+v", expr, r.Findings)
		}
	}
	for expr, want := range map[string]string{
		// created payment_provider on one side; the other side reads the stored label's underscore twin
		`sum by (payment_provider) (label_set(checkout_charges_total, "payment_provider", "a")) + sum by (payment_provider) (checkout_charges_total)`: `write "payment.provider"`,
		// created durian_cloud_x, but the grouping also names the stored label's underscore twin
		`sum by (durian_cloud_x, durian_cloud_service) (label_replace(checkout_charges_total, "durian_cloud_x", "$1", "outcome", "(.*)"))`: `write "durian.cloud.service"`,
		// label_replace reads a label the series do not have under that spelling
		`label_replace(checkout_charges_total, "x", "$1", "durian_cloud_platform", "(.*)")`: `write "durian.cloud.platform"`,
	} {
		r := dottedLive(t, expr)
		found := false
		for _, f := range r.Findings {
			if f.Rule == "label-spelling" && strings.Contains(f.Msg, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want label-spelling naming %s, got %+v", expr, want, r.Findings)
		}
	}
}

func TestParseLabelValues(t *testing.T) {
	for _, tc := range []struct{ q, metric, label string }{
		{`label_values(payment.provider)`, "", "payment.provider"},
		{`label_values(checkout_charges_total, payment.provider)`, "checkout_charges_total", "payment.provider"},
		{`label_values(checkout_charges_total, "payment.provider")`, "checkout_charges_total", "payment.provider"},
		{`label_values(checkout_charges_total{outcome="error"}, payment.provider)`, "checkout_charges_total", "payment.provider"},
		{`label_values(checkout_charges_total{outcome="error","payment.provider"="stripe"}, payment.provider)`, "checkout_charges_total", "payment.provider"},
		{`label_values(checkout_charges_total{outcome=~"error,declined", "payment.provider"="a,b"}, "payment.provider")`, "checkout_charges_total", "payment.provider"},
		{`  label_values( checkout_charges_total{outcome="a)b"} , payment.provider )  `, "checkout_charges_total", "payment.provider"},
	} {
		m, l, ok := parseLabelValues(tc.q)
		if !ok || m != tc.metric || l != tc.label {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q)", tc.q, m, l, ok, tc.metric, tc.label)
		}
	}
	for _, q := range []string{`label_values()`, `label_values(a, b, c)`, `label_values(m{a="b}, l)`, `query_result(x)`} {
		if _, _, ok := parseLabelValues(q); ok {
			t.Errorf("%s: must not parse", q)
		}
	}
}

func TestStaticLabelValuesSelectorWithMatchers(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte(nameContract))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`label_values(checkout_charges_total{outcome="error"}, payment.provider)`,
		`label_values(checkout_charges_total{outcome="error","payment.provider"="stripe"}, payment.provider)`,
		`label_values(checkout_charges_total{outcome=~"error,declined"}, "payment.provider")`,
	} {
		r := newReport()
		contractVariables(r, &files.Dashboard{File: "dashboards/overview.json", Vars: []files.Variable{{Name: "provider", Type: "query", Query: q}}}, c)
		if len(r.Findings) != 0 {
			t.Errorf("%s: %+v", q, r.Findings)
		}
	}
}

// An aggregation keeps only the labels it keeps: a query-made label that by() leaves out or without() drops is
// gone from its output, so an outer reader of it reads nothing and the underscore twin of the stored label it
// meant is still a wrong spelling (review 120, jayci). The reviewer's regression plus by/without/on/ignoring.
func TestLiveAggregationsResetTheGeneratedLabelScope(t *testing.T) {
	const made = `label_set(checkout_charges_total, "payment_provider", "review")`
	// one series only, so a binary match has no duplicates
	const one = `label_set(checkout_charges_total{outcome="error"}, "payment_provider", "review")`
	for _, expr := range []string{
		`sum by (payment_provider) (sum by (outcome) (` + made + `))`,
		`sum by (payment_provider) (sum without (payment_provider) (` + made + `))`,
		`sum by (payment_provider) (sum(` + made + `))`,
		`sum by (payment_provider) (count(` + made + `))`,
		`sum by (payment_provider) (count_values("v", ` + made + `))`,
		`sum by (payment_provider) (` + one + ` / on (outcome) ` + one + `)`,
		`sum by (payment_provider) (` + one + ` / ignoring (payment_provider) ` + one + `)`,
	} {
		r := dottedLive(t, expr)
		if r.Failures() != 2 {
			t.Errorf("a label the inner aggregation or match dropped must not exempt the outer grouping: %s: %+v", expr, r.Findings)
		}
		for _, f := range r.Findings {
			if f.Level == Fail && (f.Rule != "label-spelling" || !strings.Contains(f.Msg, `write "payment.provider"`)) {
				t.Errorf("%s: wrong diagnostic %+v", expr, f)
			}
		}
	}
	// ... while a label the aggregation keeps stays in scope
	for _, expr := range []string{
		`sum by (payment_provider) (sum by (payment_provider, outcome) (` + made + `))`,
		`sum by (payment_provider) (sum without (outcome) (` + made + `))`,
		`sum by (payment_provider) (topk(2, ` + made + `))`,
		`sum by (payment_provider) (` + one + ` / on (payment_provider) ` + one + `)`,
		`sum by (payment_provider) (` + one + ` / ignoring (outcome) ` + one + `)`,
		`sum by (payment_provider) (` + one + ` / on (outcome) group_left ` + one + `)`,
		`sum by (v) (count_values("v", checkout_charges_total))`,
		`sum by (payment_provider) (sum by (payment_provider) (` + made + `))`,
	} {
		if r := dottedLive(t, expr); r.Failures() != 0 {
			t.Errorf("%s: want it to pass, got %+v", expr, r.Findings)
		}
	}
}
