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

// An unreadable flat tier (bad YAML, a second document) stops the command with file and finding; a CR
// file that does not parse keeps 0.5's behaviour.
func TestLiveIncidentsUnreadableFlatTierExitsTwo(t *testing.T) {
	for name, c := range map[string]struct{ body, rule string }{
		"unclosed value":                   {flatLiveTier + "summary2: 'unclosed\n", "[flat-alert-yaml]"},
		"comment header, marker, bad tier": {"# yaml-language-server: $schema=x\n---\n" + flatLiveTier + "summary2: 'unclosed\n", "[flat-alert-yaml]"},
		"two documents":                    {flatLiveTier + "---\ntitle: again\n", "[flat-alert-documents]"},
		"a trailing ConfigMap":             {flatLiveTier + "---\napiVersion: v1\nkind: ConfigMap\nmetadata: { name: x }\n", "[flat-alert-documents]"},
	} {
		_, run := flatLiveDir(t, c.body, nil)
		rc, out, stderr := run()
		if rc != 2 || !strings.Contains(stderr, "alerts/retry-queue/critical.yaml") || !strings.Contains(stderr, c.rule) {
			t.Errorf("%s: exit %d, want 2 naming the file and %s:\n%s\n%s", name, rc, c.rule, stderr, out)
		}
	}
	_, run := flatLiveDir(t, "apiVersion: v1\nkind: GrafanaAlertRuleGroup\nspec: 'unclosed\n", nil)
	if rc, _, stderr := run(); rc != 0 {
		t.Errorf("a CR that does not parse keeps 0.5's behaviour (exit 0), got %d: %s", rc, stderr)
	}
}
