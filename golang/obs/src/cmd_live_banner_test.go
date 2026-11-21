package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The read-only banner and the usage text must say what the client really sends: only GET.
// The test runs a real obs live command against a fake store that records every method.
func TestLiveBannerAndUsageSayOnlyGET(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()
		switch r.URL.Path {
		case "/api/annotations":
			fmt.Fprint(w, "[]")
		default:
			http.Error(w, "unexpected", 500)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "obs.yaml"), []byte("targets:\n  prod:\n    grafana: "+srv.URL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	runLiveIncidents([]string{"--dir", dir, "--target", "prod", "--window", "-2h..-1h", "--json"}, &out, &stderr)
	mu.Lock()
	defer mu.Unlock()
	if len(methods) == 0 {
		t.Fatalf("the command sent no request: %s", stderr.String())
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Fatalf("obs live sent %s; the client sends only GET", m)
		}
	}
	banner := stderr.String()
	if !strings.Contains(banner, "read-only: only GET to query endpoints") {
		t.Fatalf("banner must say only GET:\n%s", banner)
	}
	for name, text := range map[string]string{"banner": banner, "usage": liveUsage} {
		if strings.Contains(text, "POST") {
			t.Fatalf("%s says POST although the client sends only GET:\n%s", name, text)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(liveUsage), " "), "GET to the stores' query endpoints") {
		t.Fatalf("usage must say GET to the stores' query endpoints:\n%s", liveUsage)
	}
}
