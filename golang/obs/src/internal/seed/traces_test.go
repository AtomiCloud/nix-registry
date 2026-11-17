package seed

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
	"time"
)

func TestTraceSeedStorageShapeAndIDs(t *testing.T) {
	const doc = `length: 2m
random_seed: 9
resource: {durian.cloud.service: checkout, durian.cloud.platform: payments, k8s.pod.name: checkout-abc}
traffic: {requests: 1/s}
metrics:
  requests:
    series: [{where: {outcome: error, http.response.status_code: "502"}, share: 1}]
logs: [{event: failed, from: {metric: requests}}]
traces:
  - {from: {metric: requests}, server: POST /charge, client: charge, route: /charge, error_when: {outcome: error}, attributes: {cached: true, amount: 12.5}}
`
	var s Seed
	if err := yaml.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatal(err)
	}
	d, err := Build(&s, time.Unix(1800000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Traces) == 0 {
		t.Fatal("no traces")
	}
	traces := map[string]*TTrace{}
	for _, tr := range d.Traces {
		id := fmt.Sprintf("%x", tr.TraceID)
		if traces[id] != nil {
			t.Fatal("duplicate trace ID", id)
		}
		traces[id] = tr
		if len(tr.Spans) != 2 || tr.Spans[1].ParentID != tr.Spans[0].SpanID {
			t.Fatalf("invalid span hierarchy %+v", tr)
		}
		sp := tr.Spans[0]
		if sp.Service != "checkout" || sp.Resource["service.namespace"].S != "payments" || sp.Status != 2 {
			t.Fatal(sp)
		}
		if sp.Attrs["http.response.status_code"].Kind != 1 || sp.Attrs["http.response.status_code"].I != 502 || sp.Attrs["cached"].Kind != 3 || sp.Attrs["amount"].Kind != 2 {
			t.Fatal(sp.Attrs)
		}
	}
	n := 0
	for _, st := range d.Streams {
		for _, row := range st.Rows {
			id, sid := "", ""
			for _, f := range row.Fields {
				if f.Name == "trace_id" {
					id = f.Value
				}
				if f.Name == "span_id" {
					sid = f.Value
				}
			}
			tr := traces[id]
			if tr == nil || sid != fmt.Sprintf("%x", tr.Spans[0].SpanID) || uint64(row.TsNs) != tr.Spans[0].StartNs {
				t.Fatalf("uncorrelated log %v", row)
			}
			n++
		}
	}
	if n != len(d.Traces) {
		t.Fatalf("logs=%d traces=%d", n, len(d.Traces))
	}
	d2, err := Build(&s, d.Start)
	if err != nil || fmt.Sprint(d.Traces[0].TraceID) != fmt.Sprint(d2.Traces[0].TraceID) {
		t.Fatal("seed is not deterministic", err)
	}
	for _, planted := range []struct{ old, new, token string }{
		{"server: POST /charge", "unknown: planted", "unknown"},
		{"from: {metric: requests}, server", "from: {metric: missing}, server", "missing"},
		{"cached: true", "cached: [planted]", "cached"},
	} {
		var bad Seed
		if err := yaml.Unmarshal([]byte(strings.Replace(doc, planted.old, planted.new, 1)), &bad); err != nil {
			t.Fatal(err)
		}
		if _, err := Build(&bad, d.Start); err == nil || !strings.Contains(err.Error(), planted.token) {
			t.Fatalf("planted %s: %v", planted.token, err)
		}
	}
}
