package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const cr = `apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata: { name: g }
spec:
  rules:
    - uid: r1
      data:
        - refId: A
          relativeTimeRange: { from: 600, to: 0 }
          datasourceUid: DS
          model: { expr: 'up', datasource: DSTYPE }
        - refId: B
          datasourceUid: __expr__
          model: { type: threshold, expression: A }
`

func TestAlertCR(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "alerts/a/critical.yaml", strings.NewReplacer("DSTYPE", "{}", "DS", "primordial-metrics").Replace(cr))
	write(t, dir, "alerts/b/warning.yaml", strings.NewReplacer("DSTYPE", "{ type: loki }", "DS", "x").Replace(cr))
	write(t, dir, "alerts/c/warning.yaml", cr+"---\nkind: Other\n")
	write(t, dir, "alerts/d/warning.yaml", "title: t\ndatasource: logs\nexpr: up\n")
	write(t, dir, "alerts/e/info.yaml", strings.NewReplacer("DSTYPE", "{}", "DS", "mystery").Replace(cr))
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]Kind{}
	probs := map[string]string{}
	for _, q := range s.Queries {
		kinds[q.File] = q.Kind
		if q.Problem != "" {
			probs[q.File] = q.Problem
		}
	}
	if kinds["alerts/a/critical.yaml"] != Metrics || kinds["alerts/b/warning.yaml"] != Loki {
		t.Errorf("kinds: %v", kinds)
	}
	if !strings.Contains(probs["alerts/e/info.yaml"], `"mystery"`) {
		t.Errorf("unknown datasource must name the uid: %v", probs)
	}
	// The expression query (refId B, __expr__) is not a query to parse.
	n := 0
	for _, q := range s.Queries {
		if q.File == "alerts/a/critical.yaml" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want 1 query in alert a, got %d", n)
	}
	pr := map[string]string{}
	for _, p := range s.Problems {
		pr[p.File] = p.Msg
	}
	if !strings.Contains(pr["alerts/c/warning.yaml"], "more than one YAML document") {
		t.Errorf("multi-doc: %v", pr)
	}
	if !strings.Contains(pr["alerts/d/warning.yaml"], "flat authoring form") {
		t.Errorf("flat form: %v", pr)
	}
}

func TestDashboard(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "dashboards/d.json", `{
	  "title": "T", "time": {"from": "now-1h", "to": "now"},
	  "templating": {"list": [
	    {"type": "datasource", "name": "ds", "query": "prometheus"},
	    {"type": "query", "name": "svc", "query": {"query": "label_values(up, svc)"}, "multi": true}]},
	  "panels": [
	    {"id": 1, "type": "timeseries", "title": "a", "datasource": {"type": "prometheus", "uid": "${ds}"},
	     "targets": [{"refId": "A", "expr": "up{svc=~\"$svc\"}"}]},
	    {"id": 2, "type": "row", "title": "r", "panels": [
	      {"id": 3, "type": "logs", "title": "l", "datasource": {"type": "victoriametrics-logs-datasource", "uid": "l"},
	       "targets": [{"refId": "A", "expr": "error"}]}]},
	    {"id": 4, "type": "text", "title": "no queries"}]}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Problems) != 0 {
		t.Fatal(s.Problems)
	}
	if len(s.Queries) != 2 || s.Queries[0].Kind != Metrics || s.Queries[1].Kind != Logs {
		t.Fatalf("queries: %+v", s.Queries)
	}
	if s.Queries[1].Where != `panel 3 "l" target A` {
		t.Errorf("where: %s", s.Queries[1].Where)
	}
	if v := s.Dashboards[0].Vars[1]; !v.Multi || v.Query != "label_values(up, svc)" {
		t.Errorf("var: %+v", v)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		typ, uid string
		want     Kind
		err      bool
	}{
		{"prometheus", "x", Metrics, false},
		{"victoriametrics-logs-datasource", "x", Logs, false},
		{"loki", "x", Loki, false},
		{"", "primordial-logs", Logs, false},
		{"", "primordial-metrics", Metrics, false},
		{"", "mimir", Metrics, false},
		{"", "loki", Loki, false},
		{"", "tempo", Traces, false},
		{"", "", Other, true},
		{"", "mystery", Other, true},
		{"weird", "x", Other, true},
	}
	for _, c := range cases {
		got, err := Classify(c.typ, c.uid)
		if got != c.want || (err != nil) != c.err {
			t.Errorf("Classify(%q,%q) = %v, %v", c.typ, c.uid, got, err)
		}
	}
}
