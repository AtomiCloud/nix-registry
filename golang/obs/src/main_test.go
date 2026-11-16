package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const seedDoc = `length: 1h
random_seed: 3
resource: { durian.cloud.service: checkout, durian.cloud.platform: payments }
traffic: { requests: 5/s }
metrics:
  checkout.charges:
    type: counter
    series: [ { where: { outcome: error }, share: 0.5 } ]
logs:
  - { event: checkout.charge.failed, level: error, from: { metric: checkout.charges, where: { outcome: error } } }
`

func alert(dsUID, expr, qt string) string {
	return `apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata: { name: g }
spec:
  rules:
    - uid: r1
      data:
        - refId: A
          relativeTimeRange: { from: 600, to: 0 }
          datasourceUid: ` + dsUID + `
          model:
            expr: '` + expr + `'
            queryType: ` + qt + "\n"
}

func project(t *testing.T, alerts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "seed.yaml"), []byte(seedDoc), 0o644)
	for rel, body := range alerts {
		p := filepath.Join(dir, "alerts", rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(body), 0o644)
	}
	return dir
}

func obs(args ...string) (int, string) {
	var out, errb bytes.Buffer
	rc := run(args, &out, &errb)
	return rc, out.String() + errb.String()
}

func TestEndToEnd(t *testing.T) {
	good := project(t, map[string]string{
		"a/critical.yaml": alert("primordial-metrics", `sum(rate(checkout_charges_total{outcome="error"}[5m])) > 1`, "instant"),
		"b/warning.yaml":  alert("primordial-logs", `_time:5m {durian.cloud.service="checkout"} "durian.cloud/event":="checkout.charge.failed" | stats by (outcome) count() n`, "stats"),
	})
	for _, mode := range []string{"--static", "--live"} {
		if rc, out := obs("query", mode, "--dir", good); rc != 0 {
			t.Errorf("good %s: rc %d\n%s", mode, rc, out)
		}
	}
	// Planted failures: each is red and names the file, the rule and the bad token.
	cases := []struct {
		name, mode string
		alerts     map[string]string
		want       []string
	}{
		{"syntax", "--static", map[string]string{"x/critical.yaml": alert("primordial-logs", `_time:5m {a="b" | stats count() c`, "stats")}, []string{"x/critical.yaml", "rule r1 query A", "[logsql-syntax]"}},
		{"no stats", "--static", map[string]string{"x/critical.yaml": alert("primordial-logs", `_time:5m error`, "stats")}, []string{"[logsql-alert-needs-stats]"}},
		{"loki", "--static", map[string]string{"x/critical.yaml": alert("loki", `{a="b"}`, "range")}, []string{"[loki-datasource]", `"loki"`}},
		{"prom-only", "--static", map[string]string{"x/critical.yaml": alert("primordial-metrics", `double_exponential_smoothing(up[5m], 0.5, 0.5)`, "instant")}, []string{"[metricsql-unsupported-function]", `"double_exponential_smoothing"`}},
		{"no rows", "--live", map[string]string{"x/critical.yaml": alert("primordial-metrics", `sum(rate(nope_total[5m]))`, "instant")}, []string{"[no-rows]", "metric nope_total"}},
		{"drifted field", "--live", map[string]string{"x/critical.yaml": alert("primordial-logs", `_time:5m severity:ERROR | stats count() n`, "stats")}, []string{"[no-rows]", "log field severity"}},
	}
	for _, c := range cases {
		rc, out := obs("query", c.mode, "--dir", project(t, c.alerts))
		if rc != 1 {
			t.Errorf("%s: rc %d, want 1\n%s", c.name, rc, out)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: output lacks %q\n%s", c.name, w, out)
			}
		}
	}
	if rc, _ := obs("query"); rc != 2 {
		t.Errorf("no mode: rc %d", rc)
	}
	if rc, out := obs("version"); rc != 0 || !strings.Contains(out, "VictoriaMetrics v1.156.0") || !strings.Contains(out, "VictoriaLogs v1.54.0") {
		t.Errorf("version: rc %d\n%s", rc, out)
	}
}
