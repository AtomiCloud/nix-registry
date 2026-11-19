package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/VictoriaMetrics/metricsql"
	"github.com/prometheus/prometheus/promql/parser"
)

// checkout.charges: 2 providers x 10 shards = 20 worst case series, budget 24; one sample every
// 30 s, so 2 points a minute a series: 40 points/min worst case, 48 budget.
const usageContract = `
service: checkout
types:
  provider: { kind: enum, values: [stripe, adyen] }
  shard: { kind: int, min: 0, max: 9 }
metrics:
  checkout.charges:
    kind: counter
    budget: 24
    labels: { payment.provider: provider, shard: shard }
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

func TestUsageSeriesParserErrorRedactsEchoedCredentials(t *testing.T) {
	for _, message := range []string{"bad token: Bearer test-sent-header-123\x1b[2J", "bad token: Bearer test-sent-header-123 empty"} {
		t.Run(message, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]string{"status": "error", "error": message})
			}))
			defer srv.Close()
			_, err := Usage(context.Background(), &Client{Headers: map[string]string{"Authorization": "Bearer test-sent-header-123"}}, &Target{Metrics: srv.URL}, testContract(t), UsageOptions{})
			if err == nil || strings.Contains(err.Error(), "test-sent-header-123") || strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "active series") {
				t.Fatalf("series API failure must stay an error and pass through storeText: %v", err)
			}
		})
	}
}

// validPromQL parses q with the Prometheus parser AND with MetricsQL: a query obs sends to a
// target must run on a Prometheus and on a VictoriaMetrics store (targets: dev, stage and prod
// may be either).
func validPromQL(q string) error {
	if _, err := parser.NewParser(parser.Options{}).ParseExpr(q); err != nil {
		return fmt.Errorf("Prometheus rejects %q: %v", q, err)
	}
	if _, err := metricsql.Parse(q); err != nil {
		return fmt.Errorf("MetricsQL rejects %q: %v", q, err)
	}
	return nil
}

// fakeStores serves answers shaped like VictoriaMetrics, VictoriaLogs, Tempo and Grafana on one
// server and records every request. Every metrics query must parse in both engines.
type fakeStores struct {
	rec     *recorder
	srv     *httptest.Server
	series  []map[string]string           // the /api/v1/series answer
	dpm     map[string]map[string]float64 // stored name -> landscape -> points per minute
	dpmFail map[string]bool               // stored name -> the query answers an error
	vlStat  map[string]float64
	vlLand  map[string]map[string]float64 // landscape -> lines / msg_bytes / streams
	tempo   []float64                     // nil: the metrics endpoint answers 404
	self    map[string]float64
	matches []string // every match[] received
}

func newFake(t *testing.T) *fakeStores {
	f := &fakeStores{rec: &recorder{}, dpm: map[string]map[string]float64{}, dpmFail: map[string]bool{}, vlStat: map[string]float64{}, self: map[string]float64{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// add stores n series of one metric name in one landscape ("" for none). Series i has provider
// stripe/adyen alternating and shard i/2, so up to 20 are inside the contract's types and more are
// outside them (a hole).
func (f *fakeStores) add(name, land string, n int) {
	for i := 0; i < n; i++ {
		m := map[string]string{"__name__": name, "durian.cloud.service": "checkout"}
		if land != "" {
			m["durian.cloud.landscape"] = land
		}
		switch name {
		case "checkout_charges_total":
			m["payment.provider"] = []string{"stripe", "adyen"}[i%2]
			m["shard"] = strconv.Itoa(i / 2)
		case "http_server_request_duration_seconds_bucket":
			m["http.route"] = "/r" + strconv.Itoa(i)
		default:
			m["id"] = strconv.Itoa(i)
		}
		f.series = append(f.series, m)
	}
}

func (f *fakeStores) setDPM(name, land string, perMin float64) {
	if f.dpm[name] == nil {
		f.dpm[name] = map[string]float64{}
	}
	f.dpm[name][land] = perMin
}

func vector(m map[string]float64, label string) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf(`{"metric":{%q:%q},"value":[1700000000,"%v"]}`, label, k, v))
	}
	sort.Strings(parts)
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(parts, ",") + `]}}`
}

var nameInQuery = regexp.MustCompile(`__name__="([^"]+)"`)

func (f *fakeStores) serve(w http.ResponseWriter, r *http.Request) {
	f.rec.record(r)
	q := r.URL.Query().Get("query")
	if q != "" && r.URL.Path == "/api/v1/query" {
		if err := validPromQL(q); err != nil {
			http.Error(w, `{"status":"error","errorType":"bad_data","error":"`+strings.ReplaceAll(err.Error(), `"`, `'`)+`"}`, 400)
			return
		}
	}
	switch {
	case r.URL.Path == "/api/v1/series":
		f.matches = append(f.matches, r.URL.Query()["match[]"]...)
		b, _ := json.Marshal(map[string]any{"status": "success", "data": f.series})
		w.Write(b)
	case r.URL.Path == "/api/v1/query" && strings.Contains(q, "count_over_time"):
		m := nameInQuery.FindStringSubmatch(q)
		if m == nil {
			http.Error(w, "no name", 400)
			return
		}
		if f.dpmFail[m[1]] {
			http.Error(w, `{"status":"error","errorType":"execution","error":"boom"}`, 422)
			return
		}
		var parts []string
		var lands []string
		for l := range f.dpm[m[1]] {
			lands = append(lands, l)
		}
		sort.Strings(lands)
		for _, l := range lands {
			lbl := "{}"
			if l != "" {
				lbl = fmt.Sprintf(`{"durian.cloud.landscape":%q}`, l)
			}
			parts = append(parts, fmt.Sprintf(`{"metric":%s,"value":[1700000000,"%v"]}`, lbl, f.dpm[m[1]][l]*5)) // a 5 minute window
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(parts, ",") + `]}}`))
	case r.URL.Path == "/api/v1/query" && strings.Contains(q, "tempo_distributor"):
		for k, v := range f.self {
			if strings.Contains(q, k) {
				w.Write([]byte(fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"%v"]}]}}`, v)))
				return
			}
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	case r.URL.Path == "/select/logsql/stats_query":
		// one vector entry per stat, and per landscape when the query groups by it
		var parts []string
		for k, v := range f.vlStat {
			parts = append(parts, fmt.Sprintf(`{"metric":{"__name__":%q},"value":[1700000000,"%v"]}`, k, v))
		}
		for land, st := range f.vlLand {
			if !strings.Contains(q, "by (") {
				continue // an ungrouped query cannot tell the landscapes apart: it sums them
			}
			for k, v := range st {
				parts = append(parts, fmt.Sprintf(`{"metric":{"__name__":%q,"durian.cloud.landscape":%q},"value":[1700000000,"%v"]}`, k, land, v))
			}
		}
		sort.Strings(parts)
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(parts, ",") + `]}}`))
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

func at() time.Time { return time.Unix(1700000000, 0) }

func usage(t *testing.T, f *fakeStores, o UsageOptions) *UsageResult {
	t.Helper()
	if o.Window == 0 {
		o.Window = 5 * time.Minute
	}
	o.Now = at
	res, err := Usage(context.Background(), &Client{}, f.target(), testContract(t), o)
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, f.rec)
	return res
}

func row(t *testing.T, res *UsageResult, metric string) MetricRow {
	t.Helper()
	for _, m := range res.Metrics {
		if m.Metric == metric {
			return m
		}
	}
	t.Fatalf("no row %q in %+v", metric, res.Metrics)
	return MetricRow{}
}

func TestUsageFindsTheMetricOverBudgetAndSortsByDistance(t *testing.T) {
	f := newFake(t)
	f.add("checkout_charges_total", "", 30) // over the budget of 24 (and 10 values outside the shard type)
	f.add("http_server_request_duration_seconds_bucket", "", 40)
	f.add("mystery_total", "", 3)
	f.setDPM("checkout_charges_total", "", 20)
	f.vlStat["lines"], f.vlStat["msg_bytes"], f.vlStat["streams"] = 3000, 600000, 1
	f.tempo = []float64{10, 20}
	res := usage(t, f, UsageOptions{Pods: 1, Unknown: "warn"})
	if !res.OverBudget() {
		t.Fatal("30 series against a budget of 24 must be over budget")
	}
	if len(res.Metrics) != 3 {
		t.Fatalf("rows: %+v", res.Metrics)
	}
	first := res.Metrics[0]
	if first.Metric != "checkout.charges" || first.Status != StatusOverBudget || first.Series != 30 || first.Worst != 20 || first.Budget != 24 {
		t.Fatalf("the metric over budget must come first: %+v", first)
	}
	if res.Metrics[1].Status != StatusNotInContract || res.Metrics[1].Metric != "mystery_total" {
		t.Fatalf("an undeclared metric ranks second: %+v", res.Metrics[1])
	}
	if res.Metrics[2].Status != StatusPlatform {
		t.Fatalf("platform last: %+v", res.Metrics[2])
	}
	if first.DPM != 20 || first.WorstDPM != 40 || first.BudgetDPM != 48 {
		t.Fatalf("data points per minute and both thresholds: %+v", first)
	}
	if res.Logs.LinesPerMin != 600 || res.Logs.BytesPerMin != 120000 || res.Logs.Streams != 1 || res.Logs.StreamWorst != 1 {
		t.Fatalf("logs: %+v", res.Logs)
	}
	if res.Traces.SpansPerMin != 900 || res.Traces.SpansSource != "tempo metrics" { // mean 15/s x 60
		t.Fatalf("traces: %+v", res.Traces)
	}
}

func TestUsageQueriesParseInPrometheusAndMetricsQL(t *testing.T) {
	// The data point query used keep_metric_names, which Prometheus rejects: usage exited 2 on a
	// Prometheus target. The fake store parses every query with both parsers and answers 400.
	f := newFake(t)
	f.add("checkout_charges_total", "", 4)
	f.setDPM("checkout_charges_total", "", 8)
	res := usage(t, f, UsageOptions{Pods: 1, Unknown: "warn"})
	if row(t, res, "checkout.charges").DPM != 8 {
		t.Fatalf("%+v", res.Metrics)
	}
	q := dataPointsQuery("checkout_charges_total", `,"durian.cloud.service"="checkout"`, 5*time.Minute)
	if strings.Contains(q, "keep_metric_names") {
		t.Fatal(q)
	}
	if err := validPromQL(q); err != nil {
		t.Fatal(err)
	}
	if err := validPromQL(`sum by (__name__) (count_over_time({__name__=~".+"}[300s]) keep_metric_names)`); err == nil {
		t.Fatal("the old query must be rejected by the Prometheus parser (the red of this test)")
	}
}

func TestUsageWithinBudgetIsNotFailing(t *testing.T) {
	f := newFake(t)
	f.add("checkout_charges_total", "", 20)
	f.setDPM("checkout_charges_total", "", 30)
	res := usage(t, f, UsageOptions{Pods: 1})
	if res.OverBudget() {
		t.Fatalf("within budget must not fail: %+v %+v", res.Metrics, res.Findings)
	}
	if row(t, res, "checkout.charges").Status != StatusOK {
		t.Fatalf("%+v", res.Metrics[0])
	}
}

func TestUsageDPMAboveWorstCaseButInsideBudgetWarnsAndExitsZero(t *testing.T) {
	f := newFake(t)
	f.add("checkout_charges_total", "", 20)
	f.setDPM("checkout_charges_total", "", 44) // worst 40, budget 48
	res := usage(t, f, UsageOptions{Pods: 1})
	m := row(t, res, "checkout.charges")
	if res.OverBudget() || m.Status != StatusAboveWorst || !m.DPMOver {
		t.Fatalf("%+v", m)
	}
}

func TestUsageDPMOverBudgetFailsEvenWhenSeriesAreFine(t *testing.T) {
	// 100 points/min against a worst case of 40 and a budget of 48 was "ok" while only the worst
	// case was compared and only the series decided the exit.
	f := newFake(t)
	f.add("checkout_charges_total", "", 8)
	f.setDPM("checkout_charges_total", "", 100)
	res := usage(t, f, UsageOptions{Pods: 1})
	m := row(t, res, "checkout.charges")
	if !res.OverBudget() || m.Status != StatusOverBudget || !strings.Contains(m.Why, "points/min") {
		t.Fatalf("%+v", m)
	}
	var sb strings.Builder
	res.Print(&sb)
	for _, want := range []string{"40", "48", "100", "points/min", "OVER BUDGET"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, sb.String())
		}
	}
}

func TestUsageRanksByTheLargerOfSeriesAndDPM(t *testing.T) {
	// Both inside their limits: A has few series and a hot data rate, B many series and a cold one.
	// Ranking by series alone put B first.
	refunds := "  checkout.refunds:\n    kind: counter\n    budget: 24\n    labels: { payment.provider: provider, shard: shard }\n"
	c, err := contract.Parse("contract.yaml", []byte(strings.Replace(usageContract, "platform_metrics:", refunds+"platform_metrics:", 1)))
	if err != nil {
		t.Fatal(err)
	}
	f := newFake(t)
	f.add("checkout_charges_total", "", 10) // series 10/24 = 0.42, dpm 45/48 = 0.94
	f.setDPM("checkout_charges_total", "", 45)
	for i := 0; i < 20; i++ { // series 20/24 = 0.83, dpm 4/48
		f.series = append(f.series, map[string]string{"__name__": "checkout_refunds_total", "durian.cloud.service": "checkout", "payment.provider": []string{"stripe", "adyen"}[i%2], "shard": strconv.Itoa(i / 2)})
	}
	f.setDPM("checkout_refunds_total", "", 4)
	res, err := Usage(context.Background(), &Client{}, f.target(), c, UsageOptions{Window: 5 * time.Minute, Pods: 1, Now: at})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Metrics) != 2 || res.Metrics[0].Metric != "checkout.charges" {
		t.Fatalf("rank by the larger deviation: %+v", res.Metrics)
	}
	if d := res.Metrics[0].Deviation; d < 0.93 || d > 0.95 {
		t.Fatalf("deviation: %v", d)
	}
}

func TestUsageTwoLandscapesAreCountedOneByOne(t *testing.T) {
	// A store holding two landscapes doubled the series of a metric and reported a false OVER
	// BUDGET: 2 x 20 = 40 against a budget of 24.
	f := newFake(t)
	f.add("checkout_charges_total", "prod-a", 20)
	f.add("checkout_charges_total", "prod-b", 20)
	f.setDPM("checkout_charges_total", "prod-a", 30)
	f.setDPM("checkout_charges_total", "prod-b", 35)
	res := usage(t, f, UsageOptions{Pods: 1})
	m := row(t, res, "checkout.charges")
	if res.OverBudget() || m.Status != StatusOK {
		t.Fatalf("two landscapes of 20 series each are within a budget of 24 each: %+v %+v", m, res.Findings)
	}
	if m.Series != 20 || len(m.Landscapes) != 2 || m.DPM != 35 {
		t.Fatalf("the row shows the fullest landscape and lists both: %+v", m)
	}
	f.add("checkout_charges_total", "prod-a", 0)
	f.series = f.series[:0]
	f.add("checkout_charges_total", "prod-a", 30)
	f.add("checkout_charges_total", "prod-b", 5)
	res = usage(t, f, UsageOptions{Pods: 1})
	m = row(t, res, "checkout.charges")
	if !res.OverBudget() || m.Series != 30 || m.Status != StatusOverBudget {
		t.Fatalf("one landscape over budget fails: %+v", m)
	}
}

func TestUsageKeepsAssertsLabelChecks(t *testing.T) {
	label := func(mut func(m map[string]string)) *fakeStores {
		f := newFake(t)
		f.add("checkout_charges_total", "", 4)
		mut(f.series[0])
		return f
	}
	for name, tc := range map[string]struct {
		f    *fakeStores
		rule string
	}{
		"label not in the contract (varying)": {label(func(m map[string]string) { m["instance"] = "a" }), "label-not-in-contract"},
		"pod label on a non per_pod metric":   {label(func(m map[string]string) { m["service.instance.id"] = "pod-1" }), "pod-label-on-business-metric"},
		"value outside the type":              {label(func(m map[string]string) { m["payment.provider"] = "paypal" }), "label-value-outside-type"},
	} {
		if name == "label not in the contract (varying)" {
			tc.f.series[1]["instance"] = "b" // a label that varies splits series: FAIL
		}
		res := usage(t, tc.f, UsageOptions{Pods: 1})
		found := false
		for _, fd := range res.Findings {
			found = found || (fd.Rule == tc.rule && fd.Level == "FAIL")
		}
		if !found || !res.OverBudget() {
			t.Errorf("%s: want a FAIL finding %q and exit 1: %+v", name, tc.rule, res.Findings)
		}
	}
}

func TestUsageTopOverOnlyAndUnknownModes(t *testing.T) {
	f := newFake(t)
	f.add("checkout_charges_total", "", 6)
	f.add("a_total", "", 1)
	f.add("b_total", "", 2)
	res := usage(t, f, UsageOptions{Pods: 1, Unknown: "warn", Top: 1})
	if len(res.Metrics) != 1 {
		t.Fatalf("--top 1: %+v", res.Metrics)
	}
	res = usage(t, f, UsageOptions{Pods: 1, Unknown: "warn", OverOnly: true})
	for _, m := range res.Metrics {
		if m.Status == StatusOK {
			t.Fatalf("--over-only kept an ok row: %+v", m)
		}
	}
	if res.OverBudget() {
		t.Fatal("--unknown warn shows an undeclared metric without failing")
	}
	// the default: an undeclared metric has no budget, so it fails the run
	res = usage(t, f, UsageOptions{Pods: 1})
	if !res.OverBudget() {
		t.Fatal("an undeclared metric must exit 1 by default")
	}
	if m := row(t, res, "a_total"); m.Status != StatusOverBudget || !strings.Contains(m.Why, "not in the contract") {
		t.Fatalf("%+v", m)
	}
	res = usage(t, f, UsageOptions{Pods: 1, Unknown: "ignore"})
	for _, m := range res.Metrics {
		if m.Status == StatusNotInContract || strings.HasSuffix(m.Metric, "_total") {
			t.Fatalf("--unknown ignore kept %+v", m)
		}
	}
	if res.OverBudget() {
		t.Fatal("--unknown ignore must not fail")
	}
}

func TestUsageOneFailingNameIsNotedNotHidden(t *testing.T) {
	f := newFake(t)
	f.add("checkout_charges_total", "", 4)
	f.add("a_total", "", 1)
	f.setDPM("a_total", "", 3)
	f.dpmFail["checkout_charges_total"] = true
	res := usage(t, f, UsageOptions{Pods: 1, Unknown: "warn"})
	if m := row(t, res, "checkout.charges"); !strings.Contains(m.DPMNote, "checkout_charges_total") || !strings.Contains(m.DPMNote, "boom") {
		t.Fatalf("a points/min query that failed must say so on the row: %+v", m)
	}
	f.dpmFail["a_total"] = true
	if _, err := Usage(context.Background(), &Client{}, f.target(), testContract(t), UsageOptions{Window: 5 * time.Minute, Now: at}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("when every query fails the command is an error: %v", err)
	}
}

func TestUsageReadsOnlyTheServiceUnlessAllServices(t *testing.T) {
	f := newFake(t)
	f.add("checkout_charges_total", "", 2)
	usage(t, f, UsageOptions{Pods: 1})
	if len(f.matches) != 1 || !strings.Contains(f.matches[0], `"durian.cloud.service"="checkout"`) {
		t.Fatalf("scope: %v", f.matches)
	}
	f.matches = nil
	usage(t, f, UsageOptions{Pods: 1, AllServices: true})
	if len(f.matches) != 1 || strings.Contains(f.matches[0], "durian.cloud.service") {
		t.Fatalf("--all-services: %v", f.matches)
	}
}

func TestUsageTracesNeverDivideTheWholeTempoByOneServicesEstimate(t *testing.T) {
	f := newFake(t) // tempo nil -> 404
	f.self["tempo_distributor_spans_received_total"] = 5
	f.self["tempo_distributor_bytes_received_total"] = 2.5
	var e Estimates
	e.Traces.SpansPerMin, e.Traces.BytesPerMin = 100, 100
	res := usage(t, f, UsageOptions{Pods: 1, Estimates: e})
	tr := res.Traces
	if tr.SpansAvailable || tr.SpansPerMin != 0 || tr.SpansRatio != 0 || !strings.Contains(tr.SpansNote, "404") || !strings.Contains(tr.SpansNote, "not available for one service") {
		t.Fatalf("a service-scoped read must not fall back to Tempo's whole-Tempo counter: %+v", tr)
	}
	if tr.BytesAvailable || tr.BytesPerMin != 0 || tr.BytesRatio != 0 || !strings.Contains(tr.BytesNote, "not available for one service") {
		t.Fatalf("Tempo has no per-service byte figure: %+v", tr)
	}
	var sb strings.Builder
	res.Print(&sb)
	if !strings.Contains(sb.String(), "not available") || strings.Contains(sb.String(), "300") {
		t.Fatalf("the text must say not available and print no whole-Tempo number:\n%s", sb.String())
	}
	// the whole target asked for: the counters are the right scope, and are not compared with an estimate
	res = usage(t, f, UsageOptions{Pods: 1, AllServices: true, Estimates: e})
	tr = res.Traces
	if !tr.SpansAvailable || tr.SpansPerMin != 300 || tr.SpansRatio != 0 || !tr.BytesAvailable || tr.BytesPerMin != 150 || tr.BytesRatio != 0 {
		t.Fatalf("--all-services reads the whole Tempo and compares nothing: %+v", tr)
	}
}

func TestUsageTempoMetricsForOneServiceAreCompared(t *testing.T) {
	f := newFake(t)
	f.tempo = []float64{10, 20}
	var e Estimates
	e.Traces.SpansPerMin = 450
	tr := usage(t, f, UsageOptions{Pods: 1, Estimates: e}).Traces
	if !tr.SpansAvailable || tr.SpansPerMin != 900 || tr.SpansRatio != 2 {
		t.Fatalf("%+v", tr)
	}
}

func TestUsageLogEstimatesAreComparedOnlyAtTheServicesScope(t *testing.T) {
	f := newFake(t)
	f.vlStat["lines"], f.vlStat["msg_bytes"], f.vlStat["streams"] = 3000, 600000, 5
	var e Estimates
	e.Logs.LinesPerMin = 100
	res := usage(t, f, UsageOptions{Pods: 1, Estimates: e})
	if res.Logs.LinesEstimate != 100 || res.Logs.LinesRatio != 6 || res.Logs.BytesEstimate != 0 || !res.Logs.StreamsOver || !res.OverBudget() {
		t.Fatalf("lines %v vs estimate 100: %+v", res.Logs.LinesPerMin, res.Logs)
	}
	res = usage(t, f, UsageOptions{Pods: 1, Estimates: e, AllServices: true})
	if res.Logs.Compared || res.Logs.LinesRatio != 0 || res.Logs.StreamsOver || res.OverBudget() {
		t.Fatalf("the whole target is not compared with one service's estimates or stream worst case: %+v", res.Logs)
	}
}

func TestUsageTextAndJSON(t *testing.T) {
	f := newFake(t)
	f.add("checkout_charges_total", "", 30)
	res := usage(t, f, UsageOptions{Pods: 1, Unknown: "warn"})
	var sb strings.Builder
	res.Print(&sb)
	out := sb.String()
	for _, want := range []string{"checkout.charges", "OVER BUDGET", "30", "budget 24", "exit 1", "FINDINGS"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
	b, err := json.Marshal(res)
	if err != nil || !strings.Contains(string(b), `"over_budget":true`) || !strings.Contains(string(b), `"findings"`) {
		t.Fatalf("json: %v %s", err, b)
	}
}

func TestUsageUnreachableTargetIsAnError(t *testing.T) {
	_, err := Usage(context.Background(), &Client{}, &Target{Name: "x", Metrics: "http://127.0.0.1:1"}, testContract(t), UsageOptions{Window: time.Minute, Pods: 1, Unknown: "warn"})
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("%v", err)
	}
}

func TestUsageSeriesAboveTheWorstCaseButInsideTheBudgetExitsZeroAndSaysSo(t *testing.T) {
	// 21 series, worst case 20, budget 24 (per-pod series from 21 pods are no hole in a label):
	// the worst-case finding is reported, the exit stays 0 because only the budget and the label checks fail the run.
	c, err := contract.Parse("contract.yaml", []byte(strings.Replace(usageContract, "    kind: counter\n", "    kind: counter\n    per_pod: true\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	f := newFake(t)
	for i := 0; i < 21; i++ {
		f.series = append(f.series, map[string]string{"__name__": "checkout_charges_total", "durian.cloud.service": "checkout", "payment.provider": "stripe", "shard": "0", "service.instance.id": "pod-" + strconv.Itoa(i)})
	}
	res, err := Usage(context.Background(), &Client{}, f.target(), c, UsageOptions{Window: 5 * time.Minute, Pods: 1, Now: at})
	if err != nil {
		t.Fatal(err)
	}
	m := row(t, res, "checkout.charges")
	if m.Series != 21 || m.Worst != 20 || m.Budget != 24 || m.Status != StatusAboveWorst {
		t.Fatalf("%+v", m)
	}
	if res.OverBudget() {
		t.Fatalf("21 series is inside the budget of 24: exit 0: %+v", res.Findings)
	}
	found := false
	for _, fd := range res.Findings {
		found = found || fd.Rule == "series-over-worst-case"
	}
	var sb strings.Builder
	res.Print(&sb)
	if !found || !strings.Contains(sb.String(), "series-over-worst-case") || !strings.Contains(sb.String(), "exit 0") {
		t.Fatalf("the worst-case finding must still be reported:\n%s", sb.String())
	}
}

func TestUsageLogStreamsAreComparedPerLandscape(t *testing.T) {
	// Two landscapes each hold one valid stream (the worst case is one per landscape): 1 + 1 = 2
	// streams was compared with the single landscape's worst case of 1 and exited 1.
	f := newFake(t)
	f.vlLand = map[string]map[string]float64{
		"prod-a": {"lines": 1500, "msg_bytes": 300000, "streams": 1},
		"prod-b": {"lines": 1500, "msg_bytes": 300000, "streams": 1},
	}
	res := usage(t, f, UsageOptions{Pods: 1, Unknown: "warn"})
	l := res.Logs
	if res.OverBudget() || l.StreamsOver || l.Streams != 1 || len(l.Landscapes) != 2 {
		t.Fatalf("one stream in each of two landscapes is within the worst case of 1 per landscape: %+v", l)
	}
	if l.LinesPerMin != 600 || l.BytesPerMin != 120000 {
		t.Fatalf("lines and bytes are the sum of the landscapes: %+v", l)
	}
	f.vlLand["prod-b"]["streams"] = 3
	res = usage(t, f, UsageOptions{Pods: 1, Unknown: "warn"})
	if !res.OverBudget() || !res.Logs.StreamsOver || res.Logs.Streams != 3 {
		t.Fatalf("a landscape over its own worst case fails: %+v", res.Logs)
	}
	var sb strings.Builder
	res.Print(&sb)
	if !strings.Contains(sb.String(), "prod-b: 3") {
		t.Fatalf("the text lists each landscape:\n%s", sb.String())
	}
}

func TestTempoRangeSkipsAStepWithNoValue(t *testing.T) {
	// A real Tempo answers "value": null for a step with no spans yet. It is no sample, not a zero:
	// counting it as 0 lowered the mean (193 spans/min instead of 232 on the recorded sandbox).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"series":[{"samples":[{"timestampMs":"1","value":null},{"timestampMs":"2","value":3},{"timestampMs":"3","value":5}]}]}`))
	}))
	defer srv.Close()
	vals, err := (&Client{}).TempoRange(context.Background(), srv.URL, "{ } | rate()", at().Add(-time.Minute), at(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 2 || vals[0] != 3 || vals[1] != 5 {
		t.Fatalf("%v", vals)
	}
}
