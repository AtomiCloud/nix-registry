package check

import (
	"strings"
	"testing"

	"github.com/VictoriaMetrics/metricsql"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/files"
)

const nameContract = `
service: checkout
types:
  provider: { kind: enum, values: [stripe, adyen] }
  outcome: { kind: enum, values: [success, declined, error] }
metrics:
  checkout.charges:
    kind: counter
    budget: 20
    labels: { payment.provider: provider, outcome: outcome }
platform_metrics:
  http.server.request.duration: [http.route]
`

// names runs the contract name check on an expression and returns the rules that fired, with
// the unfilled text (`raw`, where a dashboard variable still is) and the filled text it parses.
func names(t *testing.T, raw, filled string) []string {
	t.Helper()
	c, err := contract.Parse("c.yaml", []byte(nameContract))
	if err != nil {
		t.Fatal(err)
	}
	if is := c.Validate(); len(is) > 0 {
		t.Fatal(is)
	}
	e, err := metricsql.Parse(filled)
	if err != nil {
		t.Fatalf("%s: %v", filled, err)
	}
	r := newReport()
	contractMetrics(r, &files.Query{File: "f", Where: "w", Expr: raw}, e, c)
	var out []string
	for _, f := range r.Findings {
		out = append(out, "["+f.Rule+"] "+f.Msg)
	}
	return out
}

func wantRed(t *testing.T, raw, filled, rule, token string) {
	t.Helper()
	for _, f := range names(t, raw, filled) {
		if strings.HasPrefix(f, "["+rule+"]") && strings.Contains(f, token) {
			return
		}
	}
	t.Errorf("%s: want [%s] naming %q, got %v", filled, rule, token, names(t, raw, filled))
}

func wantGreen(t *testing.T, raw, filled string) {
	t.Helper()
	if got := names(t, raw, filled); len(got) > 0 {
		t.Errorf("%s: want no findings, got %v", filled, got)
	}
}

// Review of PR 111, finding 4: label functions read labels that must exist; labels they make are
// visible to the functions above them only.
func TestLabelFunctionsReadRealLabels(t *testing.T) {
	wantRed(t, "", `label_replace(checkout_charges_total, "legend", "foo", "outcom", "(.*)")`, "contract-undeclared-label", "outcom")
	wantRed(t, "", `label_replace(checkout_charges_total{legend="x"}, "legend", "foo", "outcome", "(.*)")`, "contract-undeclared-label", "legend")
	wantRed(t, "", `label_join(checkout_charges_total, "both", "-", "outcome", "nope")`, "contract-undeclared-label", "nope")
	wantGreen(t, "", `label_replace(checkout_charges_total, "legend", "foo", "outcome", "(.*)")`)
	wantGreen(t, "", `sum by (legend) (label_replace(checkout_charges_total, "legend", "foo", "outcome", "(.*)"))`)
	// a made label is visible to the function above it, not to a selector inside
	wantGreen(t, "", `label_replace(label_replace(checkout_charges_total, "a", "x", "outcome", "(.*)"), "b", "y", "a", "(.*)")`)
	wantRed(t, "", `sum by (legend) (checkout_charges_total)`, "contract-undeclared-label", "legend")
}

// Review finding 5: a quoted label name with a dashboard variable is not judged on the placeholder.
func TestQuotedLabelWithVariable(t *testing.T) {
	wantGreen(t, `checkout_charges_total{"payment.provider"=~"$provider"}`, `checkout_charges_total{"payment.provider"=~"x"}`)
	wantGreen(t, `checkout_charges_total{payment_provider=~"$provider"}`, `checkout_charges_total{payment_provider=~"x"}`)
	wantGreen(t, `checkout_charges_total{"payment.provider"="${provider}"}`, `checkout_charges_total{"payment.provider"="x"}`)
	// ... while a fixed bad value on the same quoted label is still red
	wantRed(t, `checkout_charges_total{"payment.provider"="visa"}`, `checkout_charges_total{"payment.provider"="visa"}`, "contract-bad-enum-value", "visa")
}

// Review finding 6: a regex value is matched, not read as literal text.
func TestRegexValues(t *testing.T) {
	wantGreen(t, "", `checkout_charges_total{outcome=~"succe.."}`)
	wantGreen(t, "", `checkout_charges_total{outcome=~"success|error"}`)
	wantGreen(t, "", `checkout_charges_total{outcome!~"zzz"}`)
	wantRed(t, "", `checkout_charges_total{outcome=~"zzz.*"}`, "contract-bad-enum-value", "matches no allowed value")
	wantRed(t, "", `checkout_charges_total{outcome=~"success|refunded"}`, "contract-bad-enum-value", "refunded")
	wantRed(t, "", `checkout_charges_total{outcome=~"succes"}`, "contract-bad-enum-value", "succes")
}

// Review finding 7: a regex metric name is resolved against the known names.
func TestRegexMetricName(t *testing.T) {
	wantRed(t, "", `{__name__=~"checkout_chargez_total"}`, "contract-undeclared-metric", "checkout_chargez_total")
	wantGreen(t, "", `{__name__=~"checkout_charges_total"}`)
	wantGreen(t, "", `{__name__=~"checkout_.*", outcome="error"}`)
	wantRed(t, "", `{__name__=~"checkout_.*", result="error"}`, "contract-undeclared-label", "result")
	wantGreen(t, "", `{__name__=~"http_server_request_duration_seconds_.*", http_route="/x"}`)
}
