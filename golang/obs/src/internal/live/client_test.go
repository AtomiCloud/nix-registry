package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recorder is a fake store that logs every request it receives, so a test can assert that
// obs live never called anything but a query.
type recorder struct {
	mu   sync.Mutex
	reqs []string // "METHOD path"
	hdr  http.Header
}

func (r *recorder) record(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req.Method+" "+req.URL.Path)
	r.hdr = req.Header.Clone()
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reqs...)
}

// assertReadOnly fails when any recorded request is not an allowed read.
func assertReadOnly(t *testing.T, r *recorder) {
	t.Helper()
	for _, rq := range r.all() {
		m, p, _ := strings.Cut(rq, " ")
		if err := ReadOnly(m, p); err != nil {
			t.Errorf("the target received %q: %v", rq, err)
		}
		if m != http.MethodGet && m != http.MethodPost {
			t.Errorf("method %s reached the target", m)
		}
	}
}

func TestReadOnlyGuardRefusesEverythingThatIsNotAQuery(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		ok           bool
	}{
		{"GET", "/api/v1/query", true},
		{"POST", "/api/v1/query_range", true},
		{"GET", "/api/v1/series", true},
		{"GET", "/api/v1/status/tsdb", true},
		{"GET", "/select/logsql/stats_query", true},
		{"POST", "/select/logsql/query", true},
		{"GET", "/api/metrics/query_range", true},
		{"GET", "/api/search", true},
		{"GET", "/api/annotations", true},
		{"GET", "/api/health", true},
		{"GET", "/api/dashboards/uid/abc", true},
		{"GET", "/api/prometheus/grafana/api/v1/rules", true},
		{"DELETE", "/api/v1/series", false},
		{"PUT", "/api/search", false},
		{"PATCH", "/api/dashboards/uid/abc", false},
		{"POST", "/api/dashboards/db", false},
		{"POST", "/api/annotations", false},
		{"POST", "/api/admin/users", false},
		{"POST", "/api/v1/admin/tsdb/delete_series", false},
		{"GET", "/api/v1/admin/tsdb/delete_series", false},
		{"POST", "/api/v1/import", false},
		{"POST", "/insert/0/prometheus/api/v1/write", false},
		{"POST", "/insert/jsonline", false},
		{"POST", "/api/v1/write", false},
		{"GET", "/api/v1/query/../admin/tsdb/delete_series", false},
		{"GET", "/api/org", false},
		{"POST", "/api/ruler/grafana/api/v1/rules/ns", false},
		{"POST", "/api/search", false}, // Grafana's search is read through GET only; Tempo's POST is not used
	} {
		err := ReadOnly(tc.method, tc.path)
		if (err == nil) != tc.ok {
			t.Errorf("ReadOnly(%s %s) = %v, want ok=%v", tc.method, tc.path, err, tc.ok)
		}
		if err != nil && !strings.Contains(err.Error(), tc.path) {
			t.Errorf("the refusal must name the path %q: %v", tc.path, err)
		}
	}
}

func TestClientRefusesBeforeTheNetworkAndSendsHeaders(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()
	c := &Client{Headers: map[string]string{"Authorization": "Bearer abc"}}
	var out map[string]any
	if err := c.GetJSON(context.Background(), srv.URL, "/api/v1/query", map[string][]string{"query": {"up"}}, &out); err != nil {
		t.Fatal(err)
	}
	if rec.hdr.Get("Authorization") != "Bearer abc" {
		t.Fatalf("header not sent: %v", rec.hdr)
	}
	if err := c.GetJSON(context.Background(), srv.URL, "/api/v1/admin/tsdb/delete_series", nil, &out); err == nil {
		t.Fatal("a delete endpoint must be refused")
	}
	if got := rec.all(); len(got) != 1 {
		t.Fatalf("the refused call must not reach the server: %v", got)
	}
	assertReadOnly(t, rec)
}

func TestClientErrorNamesStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized: bad token", 401)
	}))
	defer srv.Close()
	c := &Client{}
	err := c.GetJSON(context.Background(), srv.URL, "/api/v1/query", nil, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad token") || !strings.Contains(err.Error(), "/api/v1/query") {
		t.Fatalf("%v", err)
	}
}
