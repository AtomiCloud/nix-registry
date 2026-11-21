package check

import (
	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/VictoriaMetrics/metricsql"
	"testing"
)

func TestReleaseUnionRetainsExternalLabelScope(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte(nameContract))
	if err != nil {
		t.Fatal(err)
	}
	for _, expr := range []string{`sum by (external_label) (checkout_charges_total or external_requests_total)`, `sum by (external_label) (union(checkout_charges_total, external_requests_total))`, `sum by (external_label) (checkout_charges_total or external_requests_total or external_requests_total)`} {
		e, err := metricsql.Parse(expr)
		if err != nil {
			t.Fatal(err)
		}
		r := newReport()
		contractMetricsIn(r, &files.Query{File: "alerts/q/warning.yaml", Expr: expr}, e, c, []string{"observability/orders/contract.yaml"})
		if r.Failures() != 0 {
			t.Errorf("external union labels judged closed: %s: %v", expr, r.Findings)
		}
	}
	wantRed(t, "", `sum by (external_label) (checkout_charges_total)`, "contract-undeclared-label", "external_label")
}

func TestReviewUnlessKeepsLeftScope(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte(nameContract))
	if err != nil {
		t.Fatal(err)
	}
	for _, expr := range []string{`sum by (external_label) (checkout_charges_total unless external_requests_total)`, `sum by ("http.route") (checkout_charges_total unless http_server_request_duration_seconds_count)`} {
		e, err := metricsql.Parse(expr)
		if err != nil {
			t.Fatal(err)
		}
		r := newReport()
		contractMetricsIn(r, &files.Query{File: "alerts/q/warning.yaml", Expr: expr}, e, c, []string{"observability/orders/contract.yaml"})
		found := false
		for _, f := range r.Findings {
			if f.Rule == "contract-undeclared-label" {
				found = true
			}
		}
		if !found {
			t.Errorf("unless retained right-hand labels: %s: %v", expr, r.Findings)
		}
	}
	// Both operands are still validated even though only the left supplies output labels.
	wantRed(t, "", `checkout_charges_total unless http_server_request_duration_seconds_count{wrong="x"}`, "contract-undeclared-label", "wrong")
}
