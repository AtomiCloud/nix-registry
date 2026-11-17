package assert

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
)

const testContract = `service: checkout
types:
  provider: { kind: enum, values: [stripe, adyen] }
  hour: { kind: time, unit: hour_of_day }
  size: { kind: int, min: 1, max: 3 }
metrics:
  checkout.charges:
    kind: counter
    budget: 20
    labels: { payment.provider: provider }
  checkout.slots:
    kind: counter
    budget: 100
    labels: { hour: hour, size: size }
  checkout.latency:
    kind: histogram
    unit: seconds
    budget: 40
    labels: { payment.provider: provider }
    buckets: 5
  checkout.queue:
    kind: gauge
    per_pod: true
    budget: 10
    labels: { payment.provider: provider }
platform_metrics:
  http.server.request.duration: [http.route]
`

func loadC(t *testing.T) *contract.Contract {
	t.Helper()
	c, err := contract.Parse("contract.yaml", []byte(testContract))
	if err != nil {
		t.Fatal(err)
	}
	if is := c.Validate(); len(is) > 0 {
		t.Fatalf("test contract invalid: %v", is)
	}
	return c
}

func sr(name string, kv ...string) Series {
	s := Series{Name: name, Labels: map[string]string{}}
	for i := 0; i+1 < len(kv); i += 2 {
		s.Labels[kv[i]] = kv[i+1]
	}
	return s
}

func find(r *Result, rule string) *Finding {
	for i := range r.Findings {
		if r.Findings[i].Rule == rule {
			return &r.Findings[i]
		}
	}
	return nil
}

func TestWithinWorstCasePasses(t *testing.T) {
	c := loadC(t)
	r := Check(c, []Series{
		sr("checkout_charges_total", "payment_provider", "stripe"),
		sr("checkout_charges_total", "payment_provider", "adyen"),
		sr("http_server_request_duration_seconds_count", "http_route", "/a"),
	}, Options{})
	if r.Failures() != 0 {
		t.Fatalf("%+v", r.Findings)
	}
	if len(r.Counts) != 2 || r.Counts[0].Series != 2 || r.Counts[0].Worst != 2 {
		t.Errorf("counts %+v", r.Counts)
	}
}

func TestOverWorstCaseNamesTheBadValue(t *testing.T) {
	c := loadC(t)
	r := Check(c, []Series{
		sr("checkout_charges_total", "payment_provider", "stripe"),
		sr("checkout_charges_total", "payment_provider", "adyen"),
		sr("checkout_charges_total", "payment_provider", "paypal"),
	}, Options{})
	if f := find(r, "series-over-worst-case"); f == nil || !strings.Contains(f.Msg, "3 series") || !strings.Contains(f.Msg, "worst case of 2") {
		t.Fatalf("%+v", r.Findings)
	}
	if f := find(r, "label-value-outside-type"); f == nil || !strings.Contains(f.Msg, `"paypal"`) || !strings.Contains(f.Msg, "stripe|adyen") {
		t.Errorf("%+v", r.Findings)
	}
}

func TestIntAndTimeValuesOutsideTheType(t *testing.T) {
	c := loadC(t)
	var ss []Series
	for h := 0; h < 24; h++ {
		for sz := 1; sz <= 3; sz++ {
			ss = append(ss, sr("checkout_slots_total", "hour", strconv.Itoa(h), "size", strconv.Itoa(sz)))
		}
	}
	if r := Check(c, ss, Options{}); r.Failures() != 0 {
		t.Fatalf("24 x 3 = 72 series are the worst case: %+v", r.Findings)
	}
	ss = append(ss, sr("checkout_slots_total", "hour", "24", "size", "7"))
	r := Check(c, ss, Options{})
	hf := find(r, "series-over-worst-case")
	if hf == nil {
		t.Fatal("73 series must fail")
	}
	var bad []string
	for _, f := range r.Findings {
		if f.Rule == "label-value-outside-type" {
			bad = append(bad, f.Msg)
		}
	}
	if len(bad) != 2 || !strings.Contains(strings.Join(bad, "|"), `"24"`) || !strings.Contains(strings.Join(bad, "|"), `"7"`) {
		t.Errorf("%v", bad)
	}
}

func TestHistogramCountsAllItsSeries(t *testing.T) {
	c := loadC(t)
	// buckets: 5 -> (5 + 2) x 2 providers = 14.
	mk := func(extra int) []Series {
		var ss []Series
		for _, p := range []string{"stripe", "adyen"} {
			for i := 0; i < 5+extra; i++ {
				ss = append(ss, sr("checkout_latency_seconds_bucket", "payment_provider", p, "le", string(rune('a'+i))))
			}
			ss = append(ss, sr("checkout_latency_seconds_sum", "payment_provider", p), sr("checkout_latency_seconds_count", "payment_provider", p))
		}
		return ss
	}
	if r := Check(c, mk(0), Options{}); r.Failures() != 0 || r.Counts[0].Series != 14 || r.Counts[0].Worst != 14 {
		t.Fatalf("%+v %+v", r.Counts, r.Findings)
	}
	if r := Check(c, mk(1), Options{}); find(r, "series-over-worst-case") == nil {
		t.Fatal("one bucket more must fail")
	}
}

func TestExtraLabelAndPodLabel(t *testing.T) {
	c := loadC(t)
	r := Check(c, []Series{
		sr("checkout_charges_total", "payment_provider", "stripe", "user_id", "u1"),
		sr("checkout_charges_total", "payment_provider", "stripe", "user_id", "u2"),
		sr("checkout_charges_total", "payment_provider", "adyen", "user_id", "u1"),
	}, Options{})
	if f := find(r, "label-not-in-contract"); f == nil || !strings.Contains(f.Msg, `"user_id"`) {
		t.Errorf("%+v", r.Findings)
	}
	r = Check(c, []Series{
		sr("checkout_charges_total", "payment_provider", "stripe", "k8s_pod_name", "a"),
		sr("checkout_charges_total", "payment_provider", "stripe", "k8s_pod_name", "b"),
		sr("checkout_charges_total", "payment_provider", "adyen", "k8s_pod_name", "a"),
	}, Options{})
	if f := find(r, "pod-label-on-business-metric"); f == nil || !strings.Contains(f.Msg, `"k8s_pod_name"`) {
		t.Errorf("%+v", r.Findings)
	}
	// Dotted spelling in the export is the same label.
	r = Check(c, []Series{
		sr("checkout_charges_total", "payment.provider", "stripe", "k8s.pod.name", "a"),
		sr("checkout_charges_total", "payment.provider", "stripe", "k8s.pod.name", "b"),
		sr("checkout_charges_total", "payment.provider", "adyen"),
	}, Options{})
	if find(r, "pod-label-on-business-metric") == nil {
		t.Errorf("dotted pod label: %+v", r.Findings)
	}
}

func TestPerPodMultiplierAndLandscapes(t *testing.T) {
	c := loadC(t)
	var q []Series
	for _, pod := range []string{"a", "b", "c"} {
		for _, p := range []string{"stripe", "adyen"} {
			q = append(q, sr("checkout_queue", "payment_provider", p, "k8s_pod_name", pod))
		}
	}
	if r := Check(c, q, Options{Pods: 3}); r.Failures() != 0 {
		t.Errorf("3 pods x 2: %+v", r.Findings)
	}
	if r := Check(c, q, Options{Pods: 2}); find(r, "series-over-worst-case") == nil {
		t.Error("3 pods with --pods 2 must fail")
	}
	// Two landscapes are counted separately.
	var ss []Series
	for _, l := range []string{"lab", "prod"} {
		for _, p := range []string{"stripe", "adyen"} {
			ss = append(ss, sr("checkout_charges_total", "payment_provider", p, "durian_cloud_landscape", l))
		}
	}
	if r := Check(c, ss, Options{}); r.Failures() != 0 || len(r.Counts) != 2 {
		t.Errorf("%+v %+v", r.Counts, r.Findings)
	}
}

func TestUnknownMetric(t *testing.T) {
	c := loadC(t)
	ss := []Series{sr("checkout_secret_total", "who", "x"), sr("go_goroutines")}
	if r := Check(c, ss, Options{}); r.Failures() != 0 || find(r, "metric-not-in-contract") == nil {
		t.Errorf("warn: %+v", r.Findings)
	}
	if r := Check(c, ss, Options{Unknown: "fail"}); r.Failures() != 2 {
		t.Errorf("fail: %+v", r.Findings)
	}
	if r := Check(c, ss, Options{Unknown: "ignore"}); len(r.Findings) != 0 {
		t.Errorf("ignore: %+v", r.Findings)
	}
}

func TestReadSeriesBothForms(t *testing.T) {
	dir := t.TempDir()
	w := func(n, body string) string {
		p := filepath.Join(dir, n)
		_ = os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	got, err := ReadSeries(w("s.json", `{"status":"success","data":[{"__name__":"m","a":"1"},{"__name__":"m","a":"1"},{"__name__":"m","a":"2"}]}`))
	if err != nil || len(got) != 2 {
		t.Fatalf("series form: %v %+v", err, got)
	}
	got, err = ReadSeries(w("e.jsonl", `{"metric":{"__name__":"m","a":"1"},"values":[1],"timestamps":[1]}`+"\n"+`{"metric":{"__name__":"m","a":"1"},"values":[2],"timestamps":[2]}`+"\n"+`{"metric":{"__name__":"m","a":"2"},"values":[2],"timestamps":[2]}`+"\n"))
	if err != nil || len(got) != 2 {
		t.Fatalf("export form (a repeated series counts once): %v %+v", err, got)
	}
	for name, body := range map[string]string{
		"junk.json":   `not json`,
		"other.json":  `{"hello":1}`,
		"failed.json": `{"status":"error","error":"bad"}`,
		"empty.json":  ``,
		"noname.json": `{"metric":{"a":"1"}}`,
	} {
		if _, err := ReadSeries(w(name, body)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, err := ReadSeries(w("none.json", `{"status":"success","data":[]}`)); err != nil || len(got) != 0 {
		t.Errorf("an empty answer is valid: %v %v", err, got)
	}
}
