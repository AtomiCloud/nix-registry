package check

import (
	"strings"
	"testing"

	"github.com/VictoriaMetrics/metricsql"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/files"
)

// repoNames runs the lenient lookup of a folder with no contract of its own.
func repoNames(t *testing.T, expr string) []Finding {
	t.Helper()
	c, err := contract.Parse("c.yaml", []byte(nameContract))
	if err != nil {
		t.Fatal(err)
	}
	e, err := metricsql.Parse(expr)
	if err != nil {
		t.Fatal(err)
	}
	r := newReport()
	contractMetricsIn(r, &files.Query{File: "f", Where: "w", Expr: expr}, e, c, []string{"apps/a/observability/contract.yaml"})
	return r.Findings
}

// A metric in no contract is a note; a near miss of a declared name fails and names the contract files; a selector
// with no metric name judges nothing; a declared metric keeps its label rules.
func TestRepoLookupIsLenient(t *testing.T) {
	for _, q := range []string{`up{job="x"}`, `kube_pod_status_phase{phase="Running"}`, `sum(rate(other_app_reqs_total[1m]))`, `{job="x"}`, `{__name__=~"nothing_.*"}`} {
		for _, f := range repoNames(t, q) {
			if f.Level == Fail {
				t.Fatalf("%s: a metric in no contract failed: %+v", q, f)
			}
		}
	}
	fs := repoNames(t, `sum(rate(checkout_charge_total[1m]))`)
	if len(fs) != 1 || fs[0].Level != Fail || fs[0].Rule != "contract-undeclared-metric" ||
		!strings.Contains(fs[0].Msg, "apps/a/observability/contract.yaml") || !strings.Contains(fs[0].Msg, `did you mean "checkout_charges_total"?`) ||
		strings.Contains(fs[0].Msg, "(observability/contract.yaml)") {
		t.Fatalf("near miss: %+v", fs)
	}
	fs = repoNames(t, `checkout_charges_total{nothere="x"}`)
	if len(fs) != 1 || fs[0].Rule != "contract-undeclared-label" || fs[0].Level != Fail {
		t.Fatalf("a declared metric keeps its label rules: %+v", fs)
	}
}
