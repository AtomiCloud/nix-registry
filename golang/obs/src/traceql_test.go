package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTraceQueriesAndLinks(t *testing.T) {
	dir := project(t, nil)
	seed := strings.Replace(seedDoc, "durian.cloud.platform: payments", "durian.cloud.platform: payments, k8s.pod.name: checkout-abc", 1) + `traces:
  - {from: {metric: checkout.charges}, server: POST /charge, route: /charge, client: charge, attributes: {amount: 0}}
`
	if err := os.WriteFile(filepath.Join(dir, "seed.yaml"), []byte(seed), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dashboards"), 0755); err != nil {
		t.Fatal(err)
	}
	write := func(q string) {
		t.Helper()
		body := `{"title":"Traces","time":{"from":"now-1h","to":"now"},"panels":[{"id":7,"title":"Trace search","datasource":{"type":"tempo","uid":"tempo"},"targets":[{"refId":"A","query":` + q + `}]}]}`
		if err := os.WriteFile(filepath.Join(dir, "dashboards/traces.json"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(`"{span.outcome=\"error\" && resource.service.name=\"checkout\"}"`)
	for _, mode := range []string{"--static", "--live"} {
		if rc, out := obs("query", mode, "--dir", dir, "--names"); rc != 0 {
			t.Fatalf("%s baseline: %d %s", mode, rc, out)
		}
	}
	if rc, out := obs("links", "--dir", dir); rc != 0 {
		t.Fatalf("links baseline: %d %s", rc, out)
	}
	write(`"{} | avg_over_time(span.amount)"`)
	if rc, out := obs("query", "--live", "--dir", dir); rc != 0 {
		t.Fatalf("valid zero metrics rejected: %d %s", rc, out)
	}
	write(`"{span.planted="`)
	if rc, out := obs("query", "--static", "--dir", dir); rc != 1 || !strings.Contains(out, "traceql-syntax") || !strings.Contains(out, "planted") || !strings.Contains(out, "panel 7") {
		t.Fatalf("syntax: %d %s", rc, out)
	}
	write(`"{span.drifted=\"error\"}"`)
	if rc, out := obs("query", "--live", "--dir", dir); rc != 1 || !strings.Contains(out, "attribute span.drifted") || !strings.Contains(out, "dashboards/traces.json") {
		t.Fatalf("drift: %d %s", rc, out)
	}
}
