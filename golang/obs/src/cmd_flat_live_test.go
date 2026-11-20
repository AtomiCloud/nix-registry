package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const flatLiveTier = "title: Retry queue saturated\ndatasource: metrics\nexpr: 'queue_depth > 5000'\nfor: 10m\nsummary: s\ndescription: d\n"

func flatLiveDir(t *testing.T, tier string, extra map[string]string) (dir string, run func() (int, string, string)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/annotations" {
			fmt.Fprint(w, "[]")
			return
		}
		http.Error(w, "unexpected", 500)
	}))
	t.Cleanup(srv.Close)
	dir = t.TempDir()
	files := map[string]string{
		"obs.yaml":                         "platform: tracker\ntargets:\n  prod:\n    grafana: " + srv.URL + "\n",
		"contract.yaml":                    "service: zinc\n",
		"alerts/retry-queue/critical.yaml": tier,
	}
	for k, v := range extra {
		files[k] = v
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, func() (int, string, string) {
		var out, stderr bytes.Buffer
		rc := runLiveIncidents([]string{"--dir", dir, "--target", "prod", "--window", "-2h..-1h", "--json"}, &out, &stderr)
		return rc, out.String(), stderr.String()
	}
}

// A flat tier file that cannot be read as a rule must say so: it is not skipped with exit 0.
func TestLiveIncidentsSurfacesABadFlatFile(t *testing.T) {
	bad := strings.Replace(flatLiveTier, "expr: 'queue_depth > 5000'\n", "", 1)
	_, run := flatLiveDir(t, bad, nil)
	rc, out, stderr := run()
	if rc == 0 {
		t.Fatalf("a flat file with no expr exited 0:\n%s", out)
	}
	for _, want := range []string{"alerts/retry-queue/critical.yaml", "[flat-alert-field-missing]", `"expr"`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

// The standard's runbook is the sibling alerts/<slug>/runbook.md: with it, a flat rule is not
// reported as an alert without a runbook.
func TestLiveIncidentsSiblingRunbookCountsForAFlatRule(t *testing.T) {
	noRunbook := func(extra map[string]string) []string {
		_, run := flatLiveDir(t, flatLiveTier, extra)
		rc, out, stderr := run()
		if rc != 0 {
			t.Fatalf("exit %d: %s", rc, stderr)
		}
		var got struct {
			NoRunbook []struct {
				ID string `json:"id"`
			} `json:"alert_without_runbook"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range got.NoRunbook {
			ids = append(ids, r.ID)
		}
		return ids
	}
	if ids := noRunbook(nil); len(ids) != 1 || ids[0] != "tracker-zinc-retry-queue-critical" {
		t.Fatalf("without a runbook.md the rule is listed: %v", ids)
	}
	if ids := noRunbook(map[string]string{"alerts/retry-queue/runbook.md": "# Retry queue\n"}); len(ids) != 0 {
		t.Fatalf("with alerts/retry-queue/runbook.md the rule has a runbook: %v", ids)
	}
}
