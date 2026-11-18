package live

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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
		{"PUT", "/api/v1/query", false}, // a method that is neither GET nor POST on a query path
		{"PUT", "/select/logsql/query", false},
		{"PATCH", "/api/v1/query_range", false},
		{"DELETE", "/api/v1/query", false},
		{"HEAD", "/api/v1/query", false},
		{"OPTIONS", "/api/v1/query", false},
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
		http.Redirect(w, r, b.URL+"/api/v1/query?token=sekrit-location-token", http.StatusFound)
	}))
	defer a.Close()
	c := &Client{Headers: map[string]string{"X-Scope-OrgID": "acme"}}
	err := c.GetJSON(context.Background(), a.URL, "/api/v1/query", nil, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("a redirect to another host must be refused: %v", err)
	}
	if strings.Contains(err.Error(), "sekrit-location-token") || strings.Contains(err.Error(), "token=") {
		t.Fatalf("the refusal names the host at most, never the Location's query: %v", err)
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
	if err == nil || !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("a redirect to a path the guard refuses must be refused: %v", err)
	}
	if strings.Contains(err.Error(), "delete_series") || strings.Contains(err.Error(), "match") {
		t.Fatalf("the refusal must not print the redirect's path or query: %v", err)
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
		// the request-time check itself (not only the redirect-hop check that sees the same User)
		if (name == "credentials" || name == "user") && !strings.Contains(err.Error(), "user name or password") {
			t.Errorf("%s: want the base URL check's own refusal: %v", name, err)
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

// A Location net/http cannot parse is quoted whole in Go's own error (url.Error's inner error
// included): the diagnostic must say a redirect was refused and print nothing of the value.
func TestMalformedRedirectLocationIsNeverPrinted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://[::1%zz/api/v1/query?token=sekrit-malformed-token")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	err := (&Client{}).GetJSON(context.Background(), srv.URL, "/api/v1/query", nil, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("a malformed redirect must be refused: %v", err)
	}
	if strings.Contains(err.Error(), "sekrit-malformed-token") || strings.Contains(err.Error(), "token=") || strings.Contains(err.Error(), "%zz") {
		t.Fatalf("the refusal must not print the Location value: %v", err)
	}
}

// A response whose header block net/http cannot parse is quoted line by line in Go's own error
// (textproto "malformed MIME header line: ..."): obs prints none of it.
func TestMalformedResponseHeaderIsNeverPrinted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			conn.Read(buf)
			conn.Write([]byte("HTTP/1.1 302 Found\r\nLocation: http://x/api/v1/query?token=sekrit-header-token\x01\r\nContent-Length: 0\r\n\r\n"))
			conn.Close()
		}
	}()
	err = (&Client{}).GetJSON(context.Background(), "http://"+ln.Addr().String(), "/api/v1/query", nil, &struct{}{})
	if err == nil {
		t.Fatal("a malformed response must fail")
	}
	if strings.Contains(err.Error(), "sekrit-header-token") || strings.Contains(err.Error(), "token=") || strings.Contains(err.Error(), "Location http") {
		t.Fatalf("the error must not print the malformed header line: %v", err)
	}
}

// The allowlist must keep the errors a user needs on a real target readable: none of them carry
// response bytes, and "malformed HTTP" would send them looking in the wrong place.
func TestUsefulTransportErrorsStayReadable(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer tlsSrv.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer plain.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			conn.Read(buf)
			conn.Close() // closes without an answer
		}
	}()
	cases := []struct{ name, base, want string }{
		{"unknown CA", tlsSrv.URL, "authority this machine does not trust"},
		{"https to a plain-HTTP port", strings.Replace(plain.URL, "http://", "https://", 1), "answered plain HTTP"},
		{"connection closed", "http://" + ln.Addr().String(), "connection ended early"},
	}
	for _, c := range cases {
		err := (&Client{}).GetJSON(context.Background(), c.base, "/api/v1/query", nil, &struct{}{})
		if err == nil || strings.Contains(err.Error(), "malformed HTTP") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want a readable error naming %q, got %v", c.name, c.want, err)
		}
	}
}

// obs prints only its own fixed texts: never a host the store named, never a trailer line, never
// the transport text a deadline error can carry.
func TestTransportErrorsAreFixedTexts(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer other.Close()
	otherHost := strings.TrimPrefix(other.URL, "http://")
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/v1/query", http.StatusFound)
	}))
	defer redirector.Close()
	err := (&Client{}).GetJSON(context.Background(), redirector.URL, "/api/v1/query", nil, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "redirect refused") || strings.Contains(err.Error(), otherHost) {
		t.Errorf("a refused redirect must not name the host the store sent: %v", err)
	}

	ln, lerr := net.Listen("tcp", "127.0.0.1:0")
	if lerr != nil {
		t.Fatal(lerr)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			conn.Read(buf)
			conn.Write([]byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\nX-Token: sekrit-trailer\x01\r\n\r\n"))
			conn.Close()
		}
	}()
	err = (&Client{}).GetJSON(context.Background(), "http://"+ln.Addr().String(), "/api/v1/query", nil, &struct{}{})
	if err == nil || strings.Contains(err.Error(), "sekrit-trailer") || strings.Contains(err.Error(), "X-Token") {
		t.Errorf("a malformed trailer must fail without printing it: %v", err)
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = (&Client{}).GetJSON(ctx, slow.URL, "/api/v1/query", nil, &struct{}{})
	if err == nil || !strings.HasSuffix(err.Error(), ": the request timed out") {
		t.Errorf("a deadline must print the fixed text only: %v", err)
	}

	closed, cerr := net.Listen("tcp", "127.0.0.1:0")
	if cerr != nil {
		t.Fatal(cerr)
	}
	addr := closed.Addr().String()
	closed.Close()
	err = (&Client{}).GetJSON(context.Background(), "http://"+addr, "/api/v1/query", nil, &struct{}{})
	if err == nil || !strings.HasSuffix(err.Error(), ": connection refused: nothing listens at the target address") {
		t.Errorf("connection refused must print the fixed text: %v", err)
	}
}

// A non-2xx body is printed (it is the store's own diagnostic) but cleaned: a gateway that echoes
// the credential it refused must not make obs print it, and a terminal escape must not reach the
// terminal. The query API's own error fields get the same cleaning.
func TestStoreTextHidesSentHeaderValuesAndControlBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" && r.URL.Query().Get("query") == "up" {
			w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"rejected Bearer sekrit-api-token-123\u001b[2J"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("bad token: " + r.Header.Get("Authorization") + "\x1b[2J for tenant " + r.Header.Get("X-Scope-OrgID")))
	}))
	defer srv.Close()
	c := &Client{Headers: map[string]string{"Authorization": "Bearer sekrit-api-token-123", "X-Scope-OrgID": "tenant-sekrit-42"}}
	_, err := c.Get(context.Background(), srv.URL, "/api/v1/labels", nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401: bad token:") {
		t.Fatalf("the store's own error text is still the diagnostic: %v", err)
	}
	for _, bad := range []string{"sekrit-api-token-123", "tenant-sekrit-42", "\x1b"} {
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("the body must not print %q: %q", bad, err.Error())
		}
	}
	_, err = c.Instant(context.Background(), srv.URL, "up", time.Unix(0, 0))
	if err == nil || strings.Contains(err.Error(), "sekrit-api-token-123") || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("the query API error must be cleaned too: %q", err)
	}
}

// Every fixed text names its own cause (pins the labels: swapping one turns this red).
func TestSafeDoErrorLabels(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{context.Canceled, "the request was cancelled"},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), "the request timed out"},
		{x509.UnknownAuthorityError{}, "authority this machine does not trust"},
		{x509.HostnameError{Certificate: &x509.Certificate{}, Host: "store.example"}, "not valid for the target's host name"},
		{x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.Expired}, "certificate is invalid"},
		{&tls.CertificateVerificationError{Err: errors.New("other")}, "could not be verified"},
		{tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, "answer is not TLS"},
		{fmt.Errorf("x: %w", http.ErrSchemeMismatch), "answered plain HTTP"},
		{fmt.Errorf("Get: %w: transport said sekrit-before", context.DeadlineExceeded), "the request timed out"},
		{&redirectRefused{"more than 5 redirects"}, "redirect refused: more than 5 redirects"},
		{errors.New("redirect refused: forged by the store"), "the request failed"},
		{io.ErrUnexpectedEOF, "connection ended early"},
		{&net.DNSError{Err: "no such host", Name: "store.invalid", IsNotFound: true}, "could not be resolved (DNS)"},
		{fmt.Errorf("dial: %w", syscall.ECONNREFUSED), "connection refused"},
		{fmt.Errorf("read: %w", syscall.ECONNRESET), "reset the connection"},
		{errors.New("something the store said"), "the request failed (obs prints no part of the underlying error)"},
	}
	for _, c := range cases {
		if got := safeDoError(c.err).Error(); !strings.Contains(got, c.want) {
			t.Errorf("%T %v: got %q, want it to contain %q", c.err, c.err, got, c.want)
		}
	}
}

// Go's JSON decode errors quote the value they could not decode (an out-of-range time from
// Grafana, any string the store sent): obs prints a fixed text instead.
func TestJSONDecodeErrorsPrintNothingOfTheAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status": sekrit-decode-value}`))
	}))
	defer srv.Close()
	c := &Client{}
	var out struct {
		Status string `json:"status"`
	}
	errs := map[string]error{
		"GetJSON": c.GetJSON(context.Background(), srv.URL, "/api/v1/query", nil, &out),
	}
	_, errs["Instant"] = c.Instant(context.Background(), srv.URL, "up", time.Unix(0, 0))
	_, errs["TempoRange"] = c.TempoRange(context.Background(), srv.URL, "{} | rate()", time.Unix(0, 0), time.Unix(600, 0), time.Minute)
	for name, err := range errs {
		if err == nil {
			t.Errorf("%s: a malformed answer must fail", name)
			continue
		}
		for _, bad := range []string{"invalid character", "sekrit", "looking for"} {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("%s: a decode error must print a fixed text only, got %q", name, err.Error())
			}
		}
	}
}

// vmauth (v1.156.0) quotes the token it refused with %q and rewrites a legacy "Token x" to
// "Bearer x": the quoted (backslash-escaped) form and a short token are hidden too. The text is
// capped at 200 runes including the ellipsis.
func TestStoreTextHidesGatewayEchoForms(t *testing.T) {
	c := &Client{Headers: map[string]string{"Authorization": `Token ab"c\9`}}
	echo := `cannot authorize request with auth tokens ["Bearer ab\"c\\9"]`
	got := c.storeText(echo)
	if strings.Contains(got, `ab\"c\\9`) || strings.Contains(got, `ab"c\9`) || !strings.Contains(got, "cannot authorize request") {
		t.Fatalf("the quoted and rewritten token must be hidden, the diagnostic kept: %q", got)
	}
	long := strings.Repeat("é", 201)
	if n := len([]rune((&Client{}).storeText(long))); n != 200 {
		t.Fatalf("store text is at most 200 runes including the ellipsis, got %d", n)
	}
}

// Forms are found in the original text and merged, so a token that starts with its own scheme
// word leaves nothing behind; vmauth's bracketed token list is hidden whatever the token length;
// a short tenant id stays readable.
func TestStoreTextMergesOverlapsAndHidesVmauthTokenList(t *testing.T) {
	c := &Client{Headers: map[string]string{"Authorization": "Token Tokenabc123"}}
	got := c.storeText(`cannot authorize: Bearer Tokenabc123 refused`)
	if strings.Contains(got, "abc123") {
		t.Fatalf("an overlapping scheme word must not leave the token suffix: %q", got)
	}
	c = &Client{Headers: map[string]string{"X-Custom": "k9z"}}
	got = c.storeText(`cannot authorize request with auth tokens ["k9z"]`)
	if strings.Contains(got, "k9z") || !strings.Contains(got, "cannot authorize request") {
		t.Fatalf("vmauth's token list must be hidden, the diagnostic kept: %q", got)
	}
	c = &Client{Headers: map[string]string{"X-Api-Key": "ab1"}}
	if got = c.storeText(`key ab1 is not valid`); strings.Contains(got, "ab1") {
		t.Fatalf("a short value on a credential header must be hidden: %q", got)
	}
	c = &Client{Headers: map[string]string{"X-Scope-OrgID": "1"}}
	if got = c.storeText(`tenant 1 over limit 10`); got != `tenant 1 over limit 10` {
		t.Fatalf("a short tenant id is no secret and must not garble the text: %q", got)
	}
}

// vmauth's real 401 (v1.156.0): the token list is read with Go's quoting, so a token holding ]
// does not end it early, and it is found before any other hiding, so a short secret that also
// occurs in the marker text cannot hide the marker and let the list through.
func TestStoreTextVmauthListQuotingAndOrder(t *testing.T) {
	c := &Client{Headers: map[string]string{"X-Custom": "a]b"}}
	got := c.storeText(`cannot authorize request with auth tokens ["a]b" "zq1"]: forbidden`)
	if strings.Contains(got, "zq1") || strings.Contains(got, "a]b") || !strings.HasSuffix(got, ": forbidden") {
		t.Fatalf("a ] inside a quoted token must not end the list: %q", got)
	}
	c = &Client{Headers: map[string]string{"X-Api-Key": "tok"}}
	got = c.storeText(`cannot authorize request with auth tokens ["zq2"]`)
	if strings.Contains(got, "zq2") {
		t.Fatalf("a short key matching the marker must not let the list through: %q", got)
	}
}
