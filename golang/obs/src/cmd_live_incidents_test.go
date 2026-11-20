package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLiveIncidentsQuietGrafanaJSONIsUnknownAndExitZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/annotations":
			fmt.Fprint(w, "[]")
		case "/api/prometheus/grafana/api/v1/rules":
			t.Error("incident history must not infer a miss from a current-state read")
			fmt.Fprint(w, `{"status":"success","data":{"groups":[]}}`)
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
	rc := runLiveIncidents([]string{"--dir", dir, "--target", "prod", "--window", "-2h..-1h", "--json"}, &out, &stderr)
	if rc != 0 {
		t.Fatalf("observational incident report exits zero, got %d: %s", rc, stderr.String())
	}
	var got struct {
		Windows []struct {
			Unknown string `json:"unknown"`
			Status  string `json:"status"`
		} `json:"windows"`
		Missed      []json.RawMessage `json:"incident_not_alerted"`
		Suggestions []string          `json:"suggested_incident_entries"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Windows) != 1 || got.Windows[0].Status != "not_seen" || got.Windows[0].Unknown == "" || len(got.Missed) != 0 || len(got.Suggestions) != 0 {
		t.Fatalf("quiet Grafana must not signal a miss through JSON: %s", out.String())
	}
}
