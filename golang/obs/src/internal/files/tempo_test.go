package files

import "testing"

func TestTempoQueryModels(t *testing.T) {
	s := &Set{}
	s.readDashboard("dashboards/traces.json", []byte(`{"panels":[{"id":7,"datasource":{"type":"tempo","uid":"tempo"},"targets":[{"refId":"A","queryType":"search","serviceName":"checkout"},{"refId":"B","queryType":"traceId","query":"01000000000000000000000000000000"},{"refId":"C","queryType":"traceql","query":"{}"}]}]}`))
	if len(s.Problems) > 0 || len(s.Queries) != 3 {
		t.Fatalf("legacy model dropped: %+v", s)
	}
	if s.Queries[0].QueryType != "search" || s.Queries[0].Expr != "" || s.Queries[1].QueryType != "traceId" || s.Queries[2].Expr != "{}" {
		t.Fatal(s.Queries)
	}
}
