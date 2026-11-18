package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
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

// wireAllowed is the test's OWN list of what a fake store may ever receive: exact method and
// path, written out here and not derived from ReadOnly, so a mistake in the guard cannot also
// hide in the assertion.
var wireAllowed = map[string][]string{
	"GET": {
		"/api/v1/query", "/api/v1/query_range", "/api/v1/series", "/api/v1/labels", "/api/v1/status/buildinfo",
		"/select/logsql/stats_query", "/select/logsql/stats_query_range", "/select/logsql/query",
		"/api/metrics/query_range", "/api/search", "/api/annotations", "/api/health",
		"/api/prometheus/grafana/api/v1/rules", "/api/prometheus/grafana/api/v1/alerts",
	},
	"POST": {"/api/v1/query", "/api/v1/query_range", "/api/v1/series", "/select/logsql/query"},
}

var wireDashboard = regexp.MustCompile(`^/api/dashboards/uid/[A-Za-z0-9_-]+$`)

// assertReadOnly fails when any recorded request is not on the independent allowlist.
func assertReadOnly(t *testing.T, r *recorder) {
	t.Helper()
	for _, rq := range r.all() {
		m, p, _ := strings.Cut(rq, " ")
		ok := m == "GET" && wireDashboard.MatchString(p)
		for _, a := range wireAllowed[m] {
			if a == p {
				ok = true
			}
		}
		if !ok {
			t.Errorf("the target received %q, which is not on the test's own read-only allowlist", rq)
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

func TestRedirectToAnotherHostIsRefusedAndNothingIsForwarded(t *testing.T) {
	other := &recorder{}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other.record(r)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer b.Close()
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL+"/api/v1/query", http.StatusFound)
	}))
	defer a.Close()
	c := &Client{Headers: map[string]string{"X-Scope-OrgID": "acme"}}
	err := c.GetJSON(context.Background(), a.URL, "/api/v1/query", nil, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("a redirect to another host must be refused: %v", err)
	}
	if got := other.all(); len(got) != 0 {
		t.Fatalf("the other host must receive nothing (headers would travel with it): %v", got)
	}
}

func TestRedirectToAWritePathOnTheSameHostIsRefused(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.URL.Path == "/api/v1/query" {
			http.Redirect(w, r, "/api/v1/admin/tsdb/delete_series?match[]=up", http.StatusTemporaryRedirect)
			return
		}
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()
	err := (&Client{}).GetJSON(context.Background(), srv.URL, "/api/v1/query", nil, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "redirect") || !strings.Contains(err.Error(), "delete_series") {
		t.Fatalf("a redirect to a path the guard refuses must be refused, naming the path: %v", err)
	}
	for _, r := range rec.all() {
		if strings.Contains(r, "delete_series") {
			t.Fatalf("the hop reached the server: %v", rec.all())
		}
	}
}

func TestSameHostRedirectToAQueryPathIsFollowed(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.URL.Path == "/api/v1/query" {
			http.Redirect(w, r, "/api/v1/query_range", http.StatusFound)
			return
		}
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()
	var out map[string]any
	if err := (&Client{}).GetJSON(context.Background(), srv.URL, "/api/v1/query", nil, &out); err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, rec)
	if len(rec.all()) != 2 {
		t.Fatalf("want the query and its redirect target: %v", rec.all())
	}
}

func TestRedirectGuardAlsoCoversAClientTheCallerSupplied(t *testing.T) {
	// A caller-supplied http.Client with its own (nil) CheckRedirect must not switch the guard off.
	other := &recorder{}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { other.record(r) }))
	defer b.Close()
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL+"/api/search", http.StatusFound)
	}))
	defer a.Close()
	c := &Client{HTTP: &http.Client{}}
	if _, err := c.Get(context.Background(), a.URL, "/api/search", nil); err == nil {
		t.Fatal("redirect to another host must be refused")
	}
	if len(other.all()) != 0 {
		t.Fatalf("%v", other.all())
	}
}

func TestBaseURLWithCredentialsQueryOrFragmentIsRefusedBeforeTheNetwork(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rec.record(r) }))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	for name, base := range map[string]string{
		"credentials": "http://admin:hunter2secret@" + host,
		"user":        "http://hunter2secret@" + host,
		"query":       srv.URL + "?token=hunter2secret",
		"fragment":    srv.URL + "#hunter2secret",
		"scheme":      "file://" + host,
		"no host":     "http:///x",
	} {
		_, err := (&Client{}).Get(context.Background(), base, "/api/v1/query", nil)
		if err == nil {
			t.Errorf("%s: base %q must be refused", name, base)
			continue
		}
		if strings.Contains(err.Error(), "hunter2secret") {
			t.Errorf("%s: the error echoes the secret: %v", name, err)
		}
	}
	if len(rec.all()) != 0 {
		t.Fatalf("nothing may reach the server: %v", rec.all())
	}
}

func TestBaseURLWithAPathPrefixStillWorks(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " " + r.URL.Path
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()
	if _, err := (&Client{}).Get(context.Background(), srv.URL+"/grafana/", "/api/search", nil); err != nil {
		t.Fatal(err)
	}
	if got != "GET /grafana/api/search" {
		t.Fatalf("a Grafana behind a sub path: %q", got)
	}
}

func TestGuardRefusesQueryEncodedAndTraversalPaths(t *testing.T) {
	for _, p := range []string{
		"/api/v1/query?x=1", "/api/v1/query#x", "/api/v1/query%2f..%2fadmin", "/api/v1/query%00",
		"/api/v1/query\\..\\admin", "/api/v1/query ", "//api/v1/query", "/api/v1/query/",
	} {
		if err := ReadOnly("GET", p); err == nil {
			t.Errorf("ReadOnly(GET %q) must refuse", p)
		}
	}
}
