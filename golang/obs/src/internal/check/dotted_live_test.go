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

const dottedSeed = `length: 30m
random_seed: 7
resource: { durian.cloud.service: checkout, durian.cloud.platform: payments }
traffic: { requests: 5/s }
metrics:
  checkout.charges:
    type: counter
    series: [ { where: { outcome: error, payment.provider: stripe }, share: 0.5 }, { where: { outcome: success, payment.provider: adyen }, share: 0.5 } ]
`

// dottedLive runs one alert and one panel query live on the embedded VictoriaMetrics.
func dottedLive(t *testing.T, expr string) *Report {
	t.Helper()
	p := filepath.Join(t.TempDir(), "seed.yaml")
	if err := os.WriteFile(p, []byte(dottedSeed), 0o644); err != nil {
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
	defer e.Close()
	set := &files.Set{
		Queries: []files.Query{
			{File: "alerts/where-from/critical.yaml", Where: "rule where-from-critical query A", Kind: files.Metrics, IsAlert: true, Expr: expr},
			{File: "dashboards/where-from.json", Where: "panel 1 target A", Kind: files.Metrics, Dash: &files.Dashboard{}, Expr: expr},
		},
		Dashboards: []*files.Dashboard{{}},
	}
	return runLive(set, Options{}, ds, e, nil)
}

// Concept 2.1: VictoriaMetrics keeps a label's dots and a query quotes them. A dashboard panel and an
// alert written to the standard return rows from an obs seed.
func TestLiveQuotedDottedLabelReturnsRows(t *testing.T) {
	for _, expr := range []string{
		`sum(rate(checkout_charges_total{"durian.cloud.service"="checkout"}[5m]))`,
		`sum by ("payment.provider") (rate(checkout_charges_total{"durian.cloud.service"="checkout", "payment.provider"="stripe"}[5m]))`,
		`sum by (outcome, "durian.cloud.platform") (checkout_charges_total{"durian.cloud.platform"=~"pay.*"})`,
	} {
		if r := dottedLive(t, expr); r.Failures() != 0 {
			t.Errorf("%s: want rows, got %+v", expr, r.Findings)
		}
	}
}

// The underscore form selects nothing: the live run fails it once per query with label-spelling, naming the
// stored (dotted) spelling, instead of running it to a bare no-rows.
func TestLiveUnderscoreSpellingFailsNamingTheDottedOne(t *testing.T) {
	r := dottedLive(t, `sum(rate(checkout_charges_total{durian_cloud_service="checkout"}[5m]))`)
	if r.Failures() != 2 {
		t.Fatalf("want a failure for the alert and the panel, got %+v", r.Findings)
	}
	for _, f := range r.Findings {
		if f.Level == Fail && (f.Rule != "label-spelling" || !strings.Contains(f.Msg, `write "durian.cloud.service"`)) {
			t.Errorf("want label-spelling naming the dotted label, got %+v", f)
		}
	}
}

// Opus 2 (live): label_values(metric, payment_provider) lists nothing; the run fails naming the stored label.
func TestLiveLabelValuesUnderscoreVariableNamesTheDottedLabel(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seed.yaml")
	if err := os.WriteFile(p, []byte(dottedSeed), 0o644); err != nil {
		t.Fatal(err)
	}
	sd, _ := seed.Load(p)
	ds, err := seed.Build(sd, time.Now().UTC().Truncate(time.Minute).Add(-sd.Duration()-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	e, err := vm.New(ds)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	run := func(label string) *Report {
		d := &files.Dashboard{File: "dashboards/overview.json", Vars: []files.Variable{{Name: "provider", Type: "query", Query: "label_values(checkout_charges_total, " + label + ")"}}}
		q := files.Query{File: d.File, Where: "panel 1 target A", Kind: files.Metrics, Dash: d, Expr: `sum(rate(checkout_charges_total{"payment.provider"=~"$provider"}[5m]))`}
		return runLive(&files.Set{Queries: []files.Query{q}, Dashboards: []*files.Dashboard{d}}, Options{}, ds, e, nil)
	}
	for _, label := range []string{"payment.provider", `"payment.provider"`} {
		if r := run(label); r.Failures() != 0 {
			t.Errorf("label_values(..., %s): want values on the seed, got %+v", label, r.Findings)
		}
	}
	r := run("payment_provider")
	found := false
	for _, f := range r.Findings {
		if f.Rule == "label-spelling" && strings.Contains(f.Msg, `write "payment.provider"`) && strings.Contains(f.Where, "panel 1") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a label-spelling failure naming payment.provider, got %+v", r.Findings)
	}
}

// Tempo keeps attribute names as sent (concept 2.1): a TraceQL attribute the contract spells dotted must be
// written dotted. The underscore form fails and the finding names the right spelling.
func TestTraceQLUnderscoreSpellingOfDottedAttributeFails(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte(`
service: checkout
types: { provider: { kind: enum, values: [stripe, adyen] } }
spans: { checkout.charge: { attributes: { payment.provider: provider } } }
`))
	if err != nil {
		t.Fatal(err)
	}
	run := func(expr string) *Report {
		r := newReport()
		q := &files.Query{File: "dashboards/traces.json", Where: "panel 7 target A", Kind: files.Traces, Expr: expr}
		staticTraceQL(r, q, Options{SpanAttributes: c}, expr)
		return r
	}
	for _, expr := range []string{
		`{span.payment.provider = "stripe"}`,
		`{resource.durian.cloud.service = "checkout"}`,
		`{.payment.provider = "stripe"}`,
	} {
		if r := run(expr); r.Failures() != 0 {
			t.Errorf("%s: %+v", expr, r.Findings)
		}
	}
	for expr, want := range map[string]string{
		`{span.payment_provider = "stripe"}`:           `write "payment.provider"`,
		`{resource.durian_cloud_service = "checkout"}`: `write "durian.cloud.service"`,
		`{.payment_provider = "stripe"}`:               `write "payment.provider"`,
	} {
		r := run(expr)
		if r.Failures() != 1 || !strings.Contains(r.Findings[0].Msg, want) || !strings.Contains(r.Findings[0].Msg, "undeclared attribute") {
			t.Errorf("%s: want one failure naming %s, got %+v", expr, want, r.Findings)
		}
	}
}

// Opus 2: a dashboard variable label_values(metric, label) is judged like a selector label.
func TestStaticLabelValuesVariableSpelling(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte(nameContract))
	if err != nil {
		t.Fatal(err)
	}
	run := func(q string) *Report {
		r := newReport()
		contractVariables(r, &files.Dashboard{File: "dashboards/overview.json", Vars: []files.Variable{{Name: "provider", Type: "query", Query: q}}}, c)
		return r
	}
	for _, q := range []string{
		`label_values(checkout_charges_total, payment.provider)`,
		`label_values(checkout_charges_total, "payment.provider")`,
		`label_values(checkout_charges_total{outcome="error"}, outcome)`,
	} {
		if r := run(q); len(r.Findings) != 0 {
			t.Errorf("%s: %+v", q, r.Findings)
		}
	}
	for q, want := range map[string]string{
		`label_values(checkout_charges_total, payment_provider)`: `write "payment.provider"`,
		`label_values(payment_provider)`:                         `write "payment.provider"`,
	} {
		r := run(q)
		if r.Failures() != 1 || !strings.Contains(r.Findings[0].Msg, want) || r.Findings[0].Where != "variable $provider" {
			t.Errorf("%s: want one failure naming %s, got %+v", q, want, r.Findings)
		}
	}
}

// Opus 3: with no contract.yaml, a grouping-only underscore query is judged against the seed's label
// names, and a label_values variable with the underscore spelling names the stored one.
func TestLiveGroupingOnlyUnderscoreSpellingFails(t *testing.T) {
	r := dottedLive(t, `sum by (durian_cloud_service) (rate(checkout_charges_total[5m])) > 0.0001`)
	if r.Failures() != 2 {
		t.Fatalf("want the alert and the panel to fail, got %+v", r.Findings)
	}
	for _, f := range r.Findings {
		if f.Level == Fail && (f.Rule != "label-spelling" || !strings.Contains(f.Msg, `write "durian.cloud.service"`)) {
			t.Errorf("want label-spelling naming the dotted label, got %+v", f)
		}
	}
	if r := dottedLive(t, `sum by ("durian.cloud.service") (rate(checkout_charges_total[5m]))`); r.Failures() != 0 {
		t.Errorf("dotted grouping must pass: %+v", r.Findings)
	}
}

// A live run also applies the static rules. A wrong label spelling the contract check already flagged is
// not reported a second time by the live label-spelling check: each wrong label fails once.
func TestAppendLiveReportsAWrongSpellingOnce(t *testing.T) {
	static := newReport()
	static.add(Fail, "alerts/a.yaml", "rule a query A", "contract-undeclared-label", `label "durian_cloud_service" is not a label of metric "m" in the contract; VictoriaMetrics stores this label with its dots, write "durian.cloud.service"`)
	static.add(Fail, "dashboards/d.json", "variable $provider", "contract-undeclared-label", `label_values reads label "payment_provider", which is not a label ...`)
	live := newReport()
	live.add(Fail, "alerts/a.yaml", "rule a query A", "label-spelling", `VictoriaMetrics keeps a label's dots: label "durian_cloud_service" is stored as "durian.cloud.service": write "durian.cloud.service"`)
	live.add(Fail, "alerts/a.yaml", "rule a query A", "label-spelling", `VictoriaMetrics keeps a label's dots: label "durian_cloud_platform" is stored as "durian.cloud.platform": write "durian.cloud.platform"`)
	live.add(Fail, "dashboards/d.json", "panel 1", "label-spelling", `variable $provider: label_values(m, payment_provider) reads label "payment_provider", which VictoriaMetrics stores as "payment.provider": write "payment.provider"`)
	live.add(Fail, "dashboards/d.json", "panel 1", "no-rows", "unrelated")
	static.AppendLive(live)
	var got []string
	for _, f := range static.Findings {
		got = append(got, f.Rule+"|"+f.Where)
	}
	want := []string{"contract-undeclared-label|rule a query A", "contract-undeclared-label|variable $provider", "label-spelling|rule a query A", "no-rows|panel 1"}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Errorf("got %v, want %v", got, want)
	}
	if !strings.Contains(static.Findings[2].Msg, "durian_cloud_platform") {
		t.Errorf("the unflagged label must still be reported: %+v", static.Findings[2])
	}
}
