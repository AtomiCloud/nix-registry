package scrape

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const metricsText = "# HELP app_requests_total requests\n# TYPE app_requests_total counter\napp_requests_total{route=\"/a\"} 3\napp_requests_total{route=\"/b\"} 5\n\napp_up 1\n"

func TestScrapeWritesTheTargetsMetricsWithLabels(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, metricsText) }))
	defer target.Close()
	var gotQuery url.Values
	var gotBody string
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/api/v1/import/prometheus" {
			t.Errorf("path %s", r.URL.Path)
		}
		gotQuery, gotBody = r.URL.Query(), string(b)
		w.WriteHeader(204)
	}))
	defer vm.Close()
	var out bytes.Buffer
	err := Run(context.Background(), Options{
		Targets: []string{target.URL + "/metrics"}, VM: vm.URL, Count: 1, Out: &out,
		Labels: map[string]string{"durian_cloud_landscape": "local", "durian_cloud_service": "checkout"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotBody != metricsText {
		t.Errorf("body not passed through: %q", gotBody)
	}
	host := strings.TrimPrefix(target.URL, "http://")
	want := []string{"durian_cloud_landscape=local", "durian_cloud_service=checkout", "instance=" + host, "job=obs-scrape"}
	if got := gotQuery["extra_label"]; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("extra_label %v, want %v", got, want)
	}
	if !strings.Contains(out.String(), "3 samples") {
		t.Errorf("output %q", out.String())
	}
}

func TestScrapeFailuresAreReported(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	var out bytes.Buffer
	err := Run(context.Background(), Options{Targets: []string{bad.URL}, VM: "http://127.0.0.1:1", Count: 1, Out: &out})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(out.String(), "scrape "+bad.URL) {
		t.Errorf("err=%v out=%q", err, out.String())
	}
	// VictoriaMetrics refusing the data is an error too.
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, metricsText) }))
	defer good.Close()
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400); io.WriteString(w, "bad line") }))
	defer vm.Close()
	err = Run(context.Background(), Options{Targets: []string{good.URL}, VM: vm.URL, Count: 1})
	if err == nil || !strings.Contains(err.Error(), "VictoriaMetrics HTTP 400") {
		t.Errorf("err=%v", err)
	}
}

func TestScrapeRejectsBadTargets(t *testing.T) {
	for _, tg := range []string{"localhost:9100", "file:///etc/passwd", "ftp://x/y", ""} {
		if err := Run(context.Background(), Options{Targets: []string{tg}, VM: "http://x", Count: 1}); err == nil {
			t.Errorf("target %q accepted", tg)
		}
	}
	if err := Run(context.Background(), Options{VM: "http://x"}); err == nil {
		t.Error("no target accepted")
	}
}

func TestScrapeRepeatsUntilCancelled(t *testing.T) {
	n := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { n++; io.WriteString(w, "a 1\n") }))
	defer target.Close()
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer vm.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if err := Run(ctx, Options{Targets: []string{target.URL}, VM: vm.URL, Interval: 100 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Errorf("only %d scrapes in 400 ms at 100 ms", n)
	}
}
