package seed

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const doc = `length: 30m
random_seed: 1
resource: { durian.cloud.service: checkout, durian.cloud.platform: payments }
traffic: { requests: 5/s }
metrics:
  checkout.charges:
    type: counter
    series: [ { where: { outcome: error, payment.provider: stripe }, share: 0.5 } ]
  http.server.request.duration:
    type: histogram
    unit: s
    buckets: [0.1, 1]
    latency: { median: 0.1, sigma: 0.3 }
    series: [ { where: { http.route: /x }, share: 1 } ]
logs:
  - { event: checkout.charge.failed, level: error, line: boom, from: { metric: checkout.charges, where: { outcome: error } } }
`

func load(t *testing.T, body string) *Seed {
	t.Helper()
	p := filepath.Join(t.TempDir(), "seed.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoredShape(t *testing.T) {
	s := load(t, doc)
	start := time.Unix(1_700_000_000, 0)
	a, err := Build(s, start)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Build(s, start)
	if !reflect.DeepEqual(a.MetricNames(), b.MetricNames()) || len(a.Streams) != 1 {
		t.Fatalf("not deterministic or wrong streams")
	}
	// Metrics as the OTLP -> VictoriaMetrics path stores them: dots to underscores, counters get _total,
	// seconds histograms get _seconds_bucket / _sum / _count with le.
	want := []string{"checkout_charges_total", "http_server_request_duration_seconds_bucket", "http_server_request_duration_seconds_count", "http_server_request_duration_seconds_sum"}
	if got := a.MetricNames(); !reflect.DeepEqual(got, want) {
		t.Errorf("metric names %v", got)
	}
	if got := a.LabelValues("checkout_charges_total", "payment.provider", start.UnixMilli(), start.Add(time.Hour).UnixMilli()); !reflect.DeepEqual(got, []string{"stripe"}) {
		t.Errorf("label values %v", got)
	}
	// Logs as VictoriaLogs stores them: attribute keys unchanged, severity_text / severity_number, trace_id, span_id.
	for _, st := range a.Streams {
		var names []string
		for _, f := range st.Fields {
			names = append(names, f.Name)
		}
		if !reflect.DeepEqual(names, []string{"deployment.environment.name"}) && !strings.Contains(strings.Join(names, ","), "durian.cloud.service") {
			t.Errorf("stream fields %v", names)
		}
		if len(st.Rows) == 0 {
			t.Fatal("no rows")
		}
		got := map[string]string{}
		for _, f := range st.Rows[0].Fields {
			got[f.Name] = f.Value
		}
		for _, k := range []string{"durian.cloud/event", "severity_text", "severity_number", "trace_id", "span_id", "payment.provider"} {
			if got[k] == "" {
				t.Errorf("row lacks %s: %v", k, got)
			}
		}
		if got["severity_text"] != "ERROR" || got["severity_number"] != "17" || st.Rows[0].Msg != "boom" {
			t.Errorf("row: %v msg %q", got, st.Rows[0].Msg)
		}
	}
}

func TestLoadRefuses(t *testing.T) {
	for name, body := range map[string]string{
		"bad length":       strings.Replace(doc, "length: 30m", "length: soon", 1),
		"unknown key":      doc + "bogus: 1\n",
		"log from nothing": strings.Replace(doc, "from: { metric: checkout.charges", "from: { metric: nope", 1),
		"bad type":         strings.Replace(doc, "type: counter", "type: gauge", 1),
	} {
		p := filepath.Join(t.TempDir(), "seed.yaml")
		_ = os.WriteFile(p, []byte(body), 0o644)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Concept 2.1: VictoriaMetrics keeps a label's dots (metric NAMES use underscores). Every seeded
// series carries the resource and where labels with their dots, and none with an underscore twin.
func TestSeededMetricLabelsKeepDots(t *testing.T) {
	a, err := Build(load(t, doc), time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range a.Series {
		have := map[string]string{}
		for _, l := range s.Labels {
			have[l.Name] = l.Value
			if l.Name != "__name__" && l.Name != "le" && strings.Contains(l.Name, "_") && strings.Contains(l.Name, "durian") {
				t.Errorf("label %q has underscores: VictoriaMetrics keeps the dots", l.Name)
			}
		}
		if have["durian.cloud.service"] != "checkout" || have["durian.cloud.platform"] != "payments" {
			t.Errorf("series %v: want durian.cloud.* labels with dots", s.Labels)
		}
		if strings.HasPrefix(have["__name__"], "checkout_charges") && have["payment.provider"] != "stripe" {
			t.Errorf("series %v: want the where label payment.provider with its dot", s.Labels)
		}
	}
}
