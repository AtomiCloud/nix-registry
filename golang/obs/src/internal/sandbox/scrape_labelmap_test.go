package sandbox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/promrelabel"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/prometheus"

	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/scrape"
	"github.com/AtomiCloud/obs/internal/seed"
)

// scrapedInto runs the real `obs scrape` code against a fixture endpoint and plays what the sandbox
// VictoriaMetrics does with the write: extra_label labels are added, then the -relabelConfig is applied
// (the same VictoriaMetrics relabel library the server uses). It returns an in-process VictoriaMetrics
// holding the result.
func scrapedInto(t *testing.T, relabelConfig string) *vm.Engine {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "app_up 1\napp_requests_total{route=\"/x\"} 3\n")
	}))
	defer target.Close()
	var body, query string
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body, query = string(b), r.URL.RawQuery
		w.WriteHeader(204)
	}))
	defer store.Close()
	err := scrape.Run(context.Background(), scrape.Options{
		Targets: []string{target.URL}, VM: store.URL, Job: "obs-scrape", Count: 1,
		Labels: map[string]string{"durian_cloud_service": "checkout", "durian_cloud_landscape": "local", "durian_cloud_platform": "payments", "durian_cloud_module": "api", "durian_cloud_extra": "x", "cluster": "c1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(query)
	var extra []prompb.Label
	for _, kv := range q["extra_label"] {
		k, v, _ := splitKV(kv)
		extra = append(extra, prompb.Label{Name: k, Value: v})
	}
	var pcs *promrelabel.ParsedConfigs
	if relabelConfig != "" {
		if pcs, err = promrelabel.ParseRelabelConfigsData([]byte(relabelConfig)); err != nil {
			t.Fatal(err)
		}
	}
	var rows prometheus.Rows
	rows.UnmarshalWithErrLogger(body, func(s string) { t.Fatal(s) })
	at := time.Now().Add(-time.Minute).UnixMilli()
	var series []*seed.Series
	for _, row := range rows.Rows {
		ls := []prompb.Label{{Name: "__name__", Value: row.Metric}}
		for _, tg := range row.Tags {
			ls = append(ls, prompb.Label{Name: tg.Key, Value: tg.Value})
		}
		ls = append(ls, extra...)
		if pcs != nil {
			ls = pcs.Apply(ls, 0)
		}
		s := &seed.Series{Ts: []int64{at}, Vals: []float64{row.Value}}
		for _, l := range ls {
			s.Labels = append(s.Labels, seed.Label{Name: l.Name, Value: l.Value})
		}
		series = append(series, s)
	}
	e, err := vm.New(&seed.Dataset{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Append(series); err != nil {
		t.Fatal(err)
	}
	return e
}

func splitKV(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func rowsOf(t *testing.T, e *vm.Engine, q string) int {
	t.Helper()
	rs, err := e.Instant(q, time.Now())
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return len(rs)
}

// The sandbox VictoriaMetrics mirrors the real vminsert: obs scrape sends durian_cloud_* and the stored
// labels are durian.cloud.*, so the dotted, quoted selector of the standard returns rows (concept 2.1).
func TestScrapedSamplesAreStoredWithDottedLabels(t *testing.T) {
	e := scrapedInto(t, VMRelabelConfig)
	defer e.Close()
	if n := rowsOf(t, e, `app_up{"durian.cloud.service"="checkout", "durian.cloud.landscape"="local"}`); n != 1 {
		t.Errorf("dotted selector: want 1 row, got %d", n)
	}
	// all four named where-from labels map
	if n := rowsOf(t, e, `app_up{"durian.cloud.platform"="payments", "durian.cloud.module"="api"}`); n != 1 {
		t.Errorf("platform and module: want 1 row, got %d", n)
	}
	if n := rowsOf(t, e, `sum by ("durian.cloud.service") (app_requests_total{route="/x"})`); n != 1 {
		t.Errorf("dotted grouping: want 1 row, got %d", n)
	}
	// the underscore originals are not stored: the wrong spelling selects nothing, as in production
	if n := rowsOf(t, e, `app_up{durian_cloud_service="checkout"}`); n != 0 {
		t.Errorf("underscore selector: want 0 rows, got %d", n)
	}
	// Primordial's rules, exactly: `cluster` is dropped, and only the four where-from labels are mapped,
	// so any other durian_cloud_* label stays as sent.
	if n := rowsOf(t, e, `app_up{cluster="c1"}`); n != 0 {
		t.Errorf("cluster label: want it dropped, got %d rows", n)
	}
	if n := rowsOf(t, e, `app_up{durian_cloud_extra="x"}`); n != 1 {
		t.Errorf("an unmapped durian_cloud_* label stays as sent: want 1 row, got %d", n)
	}
	if n := rowsOf(t, e, `app_up{"durian.cloud.extra"="x"}`); n != 0 {
		t.Errorf("an unmapped durian_cloud_* label is not renamed: got %d rows", n)
	}
}

// Control: without the labelmap the dotted selector finds nothing, so the labelmap is what makes it work.
func TestScrapedSamplesWithoutLabelmapHaveNoDottedLabels(t *testing.T) {
	e := scrapedInto(t, "")
	defer e.Close()
	if n := rowsOf(t, e, `app_up{"durian.cloud.service"="checkout"}`); n != 0 {
		t.Errorf("no labelmap: want 0 rows, got %d", n)
	}
	if n := rowsOf(t, e, `app_up{durian_cloud_service="checkout"}`); n != 1 {
		t.Errorf("no labelmap, underscore: want 1 row, got %d", n)
	}
}
