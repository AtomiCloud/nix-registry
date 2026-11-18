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

// The underscore form selects nothing, and the no-rows finding names the stored (dotted) spelling.
func TestLiveUnderscoreSpellingHasNoRowsAndNamesTheDottedOne(t *testing.T) {
	r := dottedLive(t, `sum(rate(checkout_charges_total{durian_cloud_service="checkout"}[5m]))`)
	if r.Failures() != 2 {
		t.Fatalf("want a no-rows failure for the alert and the panel, got %+v", r.Findings)
	}
	for _, f := range r.Findings {
		if f.Rule != "no-rows" && f.Rule != "threshold-removed" {
			t.Errorf("unexpected finding %+v", f)
		}
		if f.Rule == "no-rows" && !strings.Contains(f.Msg, `stored as "durian.cloud.service"`) {
			t.Errorf("no-rows must name the dotted spelling: %s", f.Msg)
		}
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
