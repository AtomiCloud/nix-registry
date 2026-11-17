package links

import (
	"fmt"
	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/seed"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
	"time"
)

const fixture = `length: 3m
random_seed: 42
resource: {durian.cloud.service: checkout, durian.cloud.platform: payments, k8s.pod.name: checkout-abc}
traffic: {requests: 2/s}
metrics:
  requests:
    type: counter
    series:
      - {where: {provider: stripe}, share: 0.5}
      - {where: {provider: adyen}, share: 0.5}
logs:
  - {event: charged, from: {metric: requests}}
traces:
  - {from: {metric: requests}, server: POST /charge, client: charge, route: /charge, attributes: {http.response.status_code: 200, cached: true}}
`

func data(t *testing.T) *seed.Dataset {
	t.Helper()
	var s seed.Seed
	if err := yaml.Unmarshal([]byte(fixture), &s); err != nil {
		t.Fatal(err)
	}
	d, err := seed.Build(&s, time.Now().UTC().Truncate(time.Minute).Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func check(t *testing.T, d *seed.Dataset) *Report {
	t.Helper()
	l, err := vl.New(d)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return Check(d, l, tempo.NewTempoEngine(d.Traces))
}

func TestLinksBaselineAndPlantedFailures(t *testing.T) {
	r := check(t, data(t))
	if len(r.Findings) != 0 {
		t.Fatalf("baseline: %v", r.Findings)
	}
	if r.Checked["trace-to-logs"] != 2 || r.Checked["metric-to-traces"] != 2 || r.Checked["span-to-profile"] != 2 {
		t.Fatalf("counts %v", r.Checked)
	}
	for _, kind := range []string{"trace-to-logs", "metric-to-traces", "span-to-profile"} {
		t.Run(kind, func(t *testing.T) {
			d := data(t)
			switch kind {
			case "trace-to-logs":
				for _, st := range d.Streams {
					for i := range st.Rows {
						for j := range st.Rows[i].Fields {
							if st.Rows[i].Fields[j].Name == "trace_id" {
								st.Rows[i].Fields[j].Value = "deadbeef"
							}
						}
					}
				}
			case "metric-to-traces":
				for _, s := range d.Series {
					for i := range s.Labels {
						if s.Labels[i].Name == "provider" {
							s.Labels[i].Value = "drifted"
						}
					}
				}
			case "span-to-profile":
				for _, tr := range d.Traces {
					for _, s := range tr.Spans {
						delete(s.Resource, "k8s.pod.name")
					}
				}
			}
			r := check(t, d)
			found := false
			for _, f := range r.Findings {
				if f.Kind == kind {
					found = true
					if f.Subject == "" || f.Error == "" {
						t.Fatal(f)
					}
				}
			}
			if !found {
				t.Fatalf("planted %s passed: %+v", kind, r)
			}
		})
	}
}

func TestTraceToLogsRangeAndQuotes(t *testing.T) {
	s := &seed.TSpan{Service: `a"b\c`, StartNs: 1800000000000123456, DurNs: 100}
	q, start, end := TraceToLogs(s, strings.Repeat("a", 32))
	if end.Sub(start) != time.Millisecond {
		t.Fatalf("range %s to %s", start, end)
	}
	if _, err := vl.Parse(q, end); err != nil {
		t.Fatal(q, err)
	}
	if !strings.Contains(q, `service.name:="a\"b\\c"`) {
		t.Fatal(q)
	}
	if _, err := metricQuery([]seed.Label{{Name: "planted", Value: "x"}}, map[string]attr{}); err == nil || !strings.Contains(err.Error(), "planted") {
		t.Fatal(err)
	}
	q, err := metricQuery([]seed.Label{{Name: "code", Value: "200"}}, map[string]attr{"code": {"span", "http.response.status_code", 1}})
	if err != nil || !strings.Contains(q, " = 200") {
		t.Fatal(q, err)
	}
	if _, err := tempo.Parse(q); err != nil {
		t.Fatal(q, err)
	}
}

type corruptLogs struct{ inner *vl.Engine }

func (c corruptLogs) Rows(q string, s, e time.Time) ([]vl.Result, error) {
	rs, err := c.inner.Rows(q, s, e)
	if len(rs) > 0 {
		rs[0].Labels = append(rs[0].Labels, seed.Label{Name: "_msg", Value: "planted"})
		for i := range rs[0].Labels {
			if rs[0].Labels[i].Name == "trace_id" {
				rs[0].Labels[i].Value = "another-trace"
			}
		}
	}
	return rs, err
}
func TestRejectWrongTraceRows(t *testing.T) {
	d := data(t)
	l, err := vl.New(d)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	r := Check(d, corruptLogs{l}, tempo.NewTempoEngine(d.Traces))
	if !strings.Contains(fmt.Sprint(r.Findings), "another trace/service") {
		t.Fatalf("wrong rows passed: %v", r.Findings)
	}
}
