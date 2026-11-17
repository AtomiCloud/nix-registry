package check

import (
	"strings"
	"testing"
	"time"

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

// Round 2, finding 1: a regex name selects known metrics, whose label values are judged too.
func TestRegexMetricNameValues(t *testing.T) {
	wantRed(t, "", `{__name__=~"checkout_charges_total", outcome="erorr"}`, "contract-bad-enum-value", "erorr")
	wantRed(t, "", `{__name__=~"checkout_.*", outcome=~"success|refunded"}`, "contract-bad-enum-value", "refunded")
	wantGreen(t, "", `{__name__=~"checkout_charges_total", outcome="error"}`)
	wantGreen(t, "", `{__name__=~"checkout_charges.*", outcome=~"succe.."}`)
}

// Round 2, finding 2: a variable on one selector excuses only that selector's matcher.
func TestVariableDoesNotExcuseFixedTypo(t *testing.T) {
	wantRed(t, `checkout_charges_total{outcome=~"$outcome"} + checkout_charges_total{outcome="erorr"}`,
		`checkout_charges_total{outcome=~"x"} + checkout_charges_total{outcome="erorr"}`, "contract-bad-enum-value", "erorr")
	wantRed(t, `checkout_charges_total{outcome="erorr"} + checkout_charges_total{outcome=~"$outcome"}`,
		`checkout_charges_total{outcome="erorr"} + checkout_charges_total{outcome=~"x"}`, "contract-bad-enum-value", "erorr")
	wantGreen(t, `checkout_charges_total{outcome=~"$outcome"} + checkout_charges_total{outcome="error"}`,
		`checkout_charges_total{outcome=~"x"} + checkout_charges_total{outcome="error"}`)
	wantGreen(t, `checkout_charges_total{outcome=~"$outcome"}`, `checkout_charges_total{outcome=~"x"}`)
}

const bigContract = `
service: s
types: { n: { kind: int, min: 0, max: 100001 }, top: { kind: int, min: 9223372036854775807, max: 9223372036854775807 } }
metrics:
  m: { kind: counter, budget: 200000, labels: { n: n } }
  t: { kind: counter, budget: 1, labels: { n: top } }
`

func namesFor(t *testing.T, doc, filled string) []string {
	t.Helper()
	c, err := contract.Parse("c.yaml", []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	e, err := metricsql.Parse(filled)
	if err != nil {
		t.Fatal(err)
	}
	r := newReport()
	contractMetrics(r, &files.Query{File: "f", Where: "w", Expr: filled}, e, c)
	var out []string
	for _, f := range r.Findings {
		out = append(out, "["+f.Rule+"] "+f.Msg)
	}
	return out
}

// Round 2, finding 3: a large bounded number is judged without listing it.
func TestLargeIntDomainLiteral(t *testing.T) {
	if got := namesFor(t, bigContract, `m_total{n="100002"}`); len(got) != 1 || !strings.Contains(got[0], "100002") {
		t.Fatalf("out-of-range literal on a large domain: %v", got)
	}
	if got := namesFor(t, bigContract, `m_total{n=~"5|100003"}`); len(got) != 1 || !strings.Contains(got[0], "100003") {
		t.Fatalf("out-of-range alternative on a large domain: %v", got)
	}
	if got := namesFor(t, bigContract, `m_total{n="100001"}`); len(got) != 0 {
		t.Fatalf("in-range literal refused: %v", got)
	}
	if got := namesFor(t, bigContract, `m_total{n="007"}`); len(got) != 1 {
		t.Fatalf("non-canonical literal accepted: %v", got)
	}
}

// Round 2, finding 9: a singleton range at MaxInt64 finishes at once (no i <= max; i++ wrap).
func TestSingletonMaxIntFinishes(t *testing.T) {
	done := make(chan []string, 1)
	go func() {
		a := namesFor(t, bigContract, `t_total{n="9223372036854775807"}`)
		b := namesFor(t, bigContract, `t_total{n=~"9223372036854775.*"}`)
		done <- append(a, b...)
	}()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("the one allowed value was refused: %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the check did not finish on a singleton range at MaxInt64")
	}
	if got := namesFor(t, bigContract, `t_total{n="1"}`); len(got) != 1 {
		t.Fatalf("a wrong value on the singleton: %v", got)
	}
}

// Round 3 (GPT verifier): an unrelated platform metric in a regex selection does not switch off value checks.
func TestRegexWithPlatformMetricStillChecksValues(t *testing.T) {
	wantRed(t, "", `{__name__=~"checkout_charges_total|http_server_request_duration_seconds_count", outcome="erorr"}`, "contract-bad-enum-value", "erorr")
	wantGreen(t, "", `{__name__=~"checkout_charges_total|http_server_request_duration_seconds_count", outcome="error"}`)
}

// Round 3 (GPT verifier): an escaped single-quoted fixed matcher is a fixed matcher, not the variable's.
func TestEscapedSingleQuotedFixedMatcher(t *testing.T) {
	wantRed(t, `checkout_charges_total{outcome=~'$outcome'} + checkout_charges_total{outcome='er\'ror'}`,
		`checkout_charges_total{outcome=~'x'} + checkout_charges_total{outcome='er\'ror'}`, "contract-bad-enum-value", "er'ror")
}

// Round 3 (GPT verifier, finding 2): a fixed single-quoted matcher with an escape decodes the way MetricsQL does.
func TestEscapedSingleQuotedHexSibling(t *testing.T) {
	wantRed(t, `checkout_charges_total{outcome=~"$outcome"} + checkout_charges_total{outcome=~'erro\x72s'}`,
		`checkout_charges_total{outcome=~"x"} + checkout_charges_total{outcome=~'erro\x72s'}`, "contract-bad-enum-value", "errors")
	wantRed(t, `checkout_charges_total{outcome=~"$outcome"} + checkout_charges_total{outcome='erro\x72s'}`,
		`checkout_charges_total{outcome=~"x"} + checkout_charges_total{outcome='erro\x72s'}`, "contract-bad-enum-value", "errors")
}

// Round 4 (GPT re-check): a bare $ is a regex end anchor, not a dashboard variable.
func TestAnchoredTypoIsNotAVariable(t *testing.T) {
	wantRed(t, `checkout_charges_total{outcome=~"erorr$"}`, `checkout_charges_total{outcome=~"erorr$"}`, "contract-bad-enum-value", "erorr")
	wantGreen(t, `checkout_charges_total{outcome=~"error$"}`, `checkout_charges_total{outcome=~"error$"}`)
	wantGreen(t, `checkout_charges_total{outcome=~"${outcome:regex}"}`, `checkout_charges_total{outcome=~"x"}`)
	wantGreen(t, `checkout_charges_total{outcome=~"[[outcome]]"}`, `checkout_charges_total{outcome=~"x"}`)
}
