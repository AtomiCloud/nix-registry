package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/contract"
)

const usageContract = `
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

func testContract(t *testing.T) *contract.Contract {
	t.Helper()
	c, err := contract.Parse("contract.yaml", []byte(usageContract))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fakeStores serves recorded-shape answers of VictoriaMetrics, VictoriaLogs, Tempo and Grafana
// on one server and records every request.
type fakeStores struct {
	rec    *recorder
	srv    *httptest.Server
	ats    map[string]float64 // stored name -> series
	dpm    map[string]float64
	vlStat map[string]float64
	tempo  []float64 // nil: the metrics endpoint answers 404
	self   map[string]float64
}

func newFake(t *testing.T) *fakeStores {
	f := &fakeStores{rec: &recorder{}, ats: map[string]float64{}, dpm: map[string]float64{}, vlStat: map[string]float64{}, self: map[string]float64{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func vector(m map[string]float64, label string) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf(`{"metric":{%q:%q},"value":[1700000000,"%v"]}`, label, k, v))
	}
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(parts, ",") + `]}}`
}

func (f *fakeStores) serve(w http.ResponseWriter, r *http.Request) {
	f.rec.record(r)
	q := r.URL.Query().Get("query")
	switch {
	case r.URL.Path == "/api/v1/query" && strings.HasPrefix(q, "count by (__name__)"):
		w.Write([]byte(vector(f.ats, "__name__")))
	case r.URL.Path == "/api/v1/query" && strings.Contains(q, "count_over_time"):
		if !strings.Contains(q, "keep_metric_names") { // found on a real VictoriaMetrics: a rollup drops __name__ without it
			w.Write([]byte(vector(map[string]float64{"": 1}, "__name__")))
			return
		}
		w.Write([]byte(vector(f.dpm, "__name__")))
	case r.URL.Path == "/api/v1/query" && strings.Contains(q, "tempo_distributor"):
		for k, v := range f.self {
			if strings.Contains(q, k) {
				w.Write([]byte(fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"%v"]}]}}`, v)))
				return
			}
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	case r.URL.Path == "/select/logsql/stats_query":
		w.Write([]byte(vector(f.vlStat, "__name__")))
	case r.URL.Path == "/api/metrics/query_range":
		if f.tempo == nil {
			http.Error(w, "metrics generator not enabled", 404)
			return
		}
		var ss []string
		for _, v := range f.tempo {
			ss = append(ss, fmt.Sprintf(`{"timestampMs":"1700000000000","value":%v}`, v))
		}
		w.Write([]byte(`{"series":[{"labels":[{"key":"__name__","value":{"stringValue":"rate"}}],"samples":[` + strings.Join(ss, ",") + `]}]}`))
	default:
		http.Error(w, "unexpected "+r.URL.Path, 500)
	}
}

func (f *fakeStores) target() *Target {
	return &Target{Name: "fake", Metrics: f.srv.URL, Logs: f.srv.URL, Traces: f.srv.URL, Grafana: f.srv.URL, Headers: map[string]string{}}
}

func TestUsageFindsTheMetricOverBudgetAndSortsByDistance(t *testing.T) {
	f := newFake(t)
	// worst case of checkout.charges = 2 providers x 3 outcomes = 6; budget 20.
	f.ats["checkout_charges_total"] = 25                      // over budget (20)
	f.ats["http_server_request_duration_seconds_bucket"] = 40 // platform: no limit
	f.ats["mystery_total"] = 3                                // not in the contract
	f.dpm["checkout_charges_total"] = 100
	f.vlStat["lines"], f.vlStat["msg_bytes"], f.vlStat["streams"] = 3000, 600000, 1
	f.tempo = []float64{10, 20}
	f.self["tempo_distributor_bytes_received_total"] = 2.5
	res, err := Usage(context.Background(), &Client{}, f.target(), testContract(t), UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn", Now: func() time.Time { return time.Unix(1700000000, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OverBudget() {
		t.Fatal("25 series against a budget of 20 must be over budget")
	}
	if len(res.Metrics) != 3 {
		t.Fatalf("rows: %+v", res.Metrics)
	}
	first := res.Metrics[0]
	if first.Metric != "checkout.charges" || first.Status != StatusOverBudget || first.Series != 25 || first.Worst != 6 || first.Budget != 20 {
		t.Fatalf("the metric over budget must come first: %+v", first)
	}
	if res.Metrics[1].Status != StatusNotInContract || res.Metrics[1].Metric != "mystery_total" {
		t.Fatalf("an undeclared metric ranks second: %+v", res.Metrics[1])
	}
	if first.DPM != 20 { // 100 points over 5 minutes
		t.Fatalf("data points per minute: %v", first.DPM)
	}
	// 6 series x 2 samples a minute (30s interval) = 12 allowed
	if first.MaxDPM != 12 || !first.DPMOver {
		t.Fatalf("dpm limit: %+v", first)
	}
	if res.Logs.LinesPerMin != 600 || res.Logs.BytesPerMin != 120000 || res.Logs.Streams != 1 {
		t.Fatalf("logs: %+v", res.Logs)
	}
	if res.Logs.StreamWorst != 1 {
		t.Fatalf("stream worst case: %+v", res.Logs)
	}
	if res.Traces.SpansPerMin != 900 || res.Traces.SpansSource != "tempo metrics" { // mean 15/s x 60
		t.Fatalf("traces: %+v", res.Traces)
	}
	if res.Traces.BytesPerMin != 150 || !strings.Contains(res.Traces.BytesSource, "tempo_distributor_bytes_received_total") {
		t.Fatalf("trace bytes: %+v", res.Traces)
	}
	assertReadOnly(t, f.rec)
}

func TestUsageWithinBudgetIsNotFailing(t *testing.T) {
	f := newFake(t)
	f.ats["checkout_charges_total"] = 6
	f.dpm["checkout_charges_total"] = 10
	res, err := Usage(context.Background(), &Client{}, f.target(), testContract(t), UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OverBudget() {
		t.Fatalf("within budget must not fail: %+v", res.Metrics)
	}
	if res.Metrics[0].Status != StatusOK {
		t.Fatalf("%+v", res.Metrics[0])
	}
}

func TestUsageAboveWorstCaseButInsideBudgetWarnsAndExitsZero(t *testing.T) {
	f := newFake(t)
	f.ats["checkout_charges_total"] = 10 // worst 6, budget 20
	res, _ := Usage(context.Background(), &Client{}, f.target(), testContract(t), UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn"})
	if res.OverBudget() || res.Metrics[0].Status != StatusAboveWorst {
		t.Fatalf("%+v", res.Metrics[0])
	}
}

func TestUsageTopOverOnlyAndUnknownFail(t *testing.T) {
	f := newFake(t)
	f.ats["checkout_charges_total"] = 6
	f.ats["a_total"], f.ats["b_total"] = 1, 2
	c := testContract(t)
	res, _ := Usage(context.Background(), &Client{}, f.target(), c, UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn", Top: 1})
	if len(res.Metrics) != 1 {
		t.Fatalf("--top 1: %+v", res.Metrics)
	}
	res, _ = Usage(context.Background(), &Client{}, f.target(), c, UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn", OverOnly: true})
	for _, m := range res.Metrics {
		if m.Status == StatusOK {
			t.Fatalf("--over-only kept an ok row: %+v", m)
		}
	}
	res, _ = Usage(context.Background(), &Client{}, f.target(), c, UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "fail"})
	if !res.OverBudget() {
		t.Fatal("--unknown fail must fail on an undeclared metric")
	}
	res, _ = Usage(context.Background(), &Client{}, f.target(), c, UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "ignore"})
	for _, m := range res.Metrics {
		if m.Status == StatusNotInContract {
			t.Fatalf("--unknown ignore kept %+v", m)
		}
	}
}

func TestUsageTracesFallBackAndSayWhyWhenUnavailable(t *testing.T) {
	f := newFake(t) // tempo nil -> 404, no self metrics
	res, err := Usage(context.Background(), &Client{}, f.target(), testContract(t), UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Traces.SpansAvailable || !strings.Contains(res.Traces.SpansNote, "404") {
		t.Fatalf("unavailable spans must say why: %+v", res.Traces)
	}
	f.self["tempo_distributor_spans_received_total"] = 5
	res, _ = Usage(context.Background(), &Client{}, f.target(), testContract(t), UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn"})
	if !res.Traces.SpansAvailable || res.Traces.SpansPerMin != 300 || !strings.Contains(res.Traces.SpansSource, "tempo_distributor_spans_received_total") {
		t.Fatalf("fallback to Tempo's own metric: %+v", res.Traces)
	}
}

func TestUsageEstimatesAreCompared(t *testing.T) {
	f := newFake(t)
	f.vlStat["lines"], f.vlStat["msg_bytes"], f.vlStat["streams"] = 3000, 600000, 1
	var e Estimates
	e.Logs.LinesPerMin = 100
	res, _ := Usage(context.Background(), &Client{}, f.target(), testContract(t), UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn", Estimates: e})
	if res.Logs.LinesEstimate != 100 || res.Logs.LinesRatio != 6 {
		t.Fatalf("lines %v vs estimate 100: %+v", res.Logs.LinesPerMin, res.Logs)
	}
	if res.Logs.BytesEstimate != 0 {
		t.Fatalf("no estimate stays empty: %+v", res.Logs)
	}
}

func TestUsageTextAndJSON(t *testing.T) {
	f := newFake(t)
	f.ats["checkout_charges_total"] = 25
	res, _ := Usage(context.Background(), &Client{}, f.target(), testContract(t), UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn"})
	var sb strings.Builder
	res.Print(&sb)
	out := sb.String()
	for _, want := range []string{"checkout.charges", "OVER BUDGET", "25", "budget 20", "exit 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
	b, err := json.Marshal(res)
	if err != nil || !strings.Contains(string(b), `"over_budget":true`) {
		t.Fatalf("json: %v %s", err, b)
	}
}

func TestUsageUnreachableTargetIsAnError(t *testing.T) {
	_, err := Usage(context.Background(), &Client{}, &Target{Name: "x", Metrics: "http://127.0.0.1:1"}, testContract(t), UsageOptions{Window: time.Minute, Pods: 1, Unknown: "warn"})
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("%v", err)
	}
}
