package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lookProject writes a folder with one dashboard and a checklist and returns both paths.
func lookProject(t *testing.T) (dir, checklist string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "observability")
	if err := os.MkdirAll(filepath.Join(dir, "dashboards"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "dashboards", "overview.json"), []byte(`{"uid":"x-overview","title":"X","panels":[]}`), 0o644)
	checklist = filepath.Join(root, "index.md")
	_ = os.WriteFile(checklist, []byte("## Visual checklist\n\n- No \"No data\" panels\n"), 0o644)
	return dir, checklist
}

func TestVersionPrintsTheSandboxPins(t *testing.T) {
	rc, out := obs("version")
	if rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	for _, want := range []string{
		"sandbox image grafana/grafana:13.3.0", "sandbox image victoriametrics/victoria-metrics:v1.156.0", "sandbox image victoriametrics/victoria-logs:v1.54.0",
		"sandbox image grafana/tempo:3.1.1", "sandbox image grafana/pyroscope:2.4.3", "sandbox Grafana plugin victoriametrics-logs-datasource v0.34.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("version lacks %q:\n%s", want, out)
		}
	}
}

// obs look exits 0 when the setup fails, and says what failed.
func TestLookExitsZeroWhenSetupFails(t *testing.T) {
	dir, cl := lookProject(t)
	t.Setenv("OBS_CONTAINER_RUNTIME", "nerdctl")
	rc, out := obs("look", "--dir", dir, "--checklist", cl, "--file", "dashboards/overview.json")
	if rc != 0 || !strings.Contains(out, "setup failed, nothing to review") || !strings.Contains(out, `container runtime "nerdctl"`) {
		t.Errorf("rc=%d out=%s", rc, out)
	}
	// Not a git repository, no --file: the changed set cannot be told. Still exit 0.
	rc, out = obs("look", "--dir", dir, "--checklist", cl, "--base", "origin/none")
	if rc != 0 || !strings.Contains(out, "cannot tell which dashboards changed") {
		t.Errorf("git failure: rc=%d out=%s", rc, out)
	}
	// --json: the same, as JSON an agent can read.
	rc, out = obs("look", "--json", "--dir", dir, "--checklist", cl, "--file", "dashboards/overview.json")
	var j struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Prompt string `json:"prompt"`
	}
	if rc != 0 || json.Unmarshal([]byte(out), &j) != nil || j.OK || j.Error == "" || !strings.Contains(j.Prompt, "setup failed") {
		t.Errorf("json: rc=%d out=%s", rc, out)
	}
	// A bad flag, a missing flag value and --help do not fail CI either: exit 0 with the usage error.
	rc, out = obs("look", "--nope")
	if rc != 0 || !strings.Contains(out, "usage error") || !strings.Contains(out, "flag provided but not defined: -nope") || !strings.Contains(out, "obs look exits 0 on purpose") {
		t.Errorf("bad flag rc=%d out=%s", rc, out)
	}
	rc, out = obs("look", "--width", "wide")
	if rc != 0 || !strings.Contains(out, "usage error") || !strings.Contains(out, "width") {
		t.Errorf("bad flag value rc=%d out=%s", rc, out)
	}
	rc, out = obs("look", "--help")
	if rc != 0 || !strings.Contains(out, "-base") || !strings.Contains(out, "-checklist") {
		t.Errorf("--help rc=%d out=%s", rc, out)
	}
	// With --json the usage error is an ok:false object, wherever --json sits in the line.
	for _, args := range [][]string{{"look", "--json", "--nope"}, {"look", "--nope", "--json"}} {
		rc, out = obs(args...)
		if rc != 0 || json.Unmarshal([]byte(out), &j) != nil || j.OK || !strings.Contains(j.Error, "flag provided but not defined: -nope") || !strings.Contains(j.Prompt, "usage error") {
			t.Errorf("%v: rc=%d out=%s", args, rc, out)
		}
	}
}

// obs look never reads an LLM key and never talks to a pull request: with the usual
// variables set to a server that fails the test on any request, nothing is contacted.
func TestLookContactsNoServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("obs look contacted %s %s", r.Method, r.URL)
	}))
	defer srv.Close()
	for _, k := range []string{"OBS_LLM_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GITHUB_TOKEN", "GH_TOKEN"} {
		t.Setenv(k, "secret-"+k)
	}
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("OBS_CONTAINER_RUNTIME", "nerdctl")
	dir, cl := lookProject(t)
	rc, out := obs("look", "--dir", dir, "--checklist", cl, "--file", "dashboards/overview.json")
	if rc != 0 || strings.Contains(out, "secret-") {
		t.Errorf("rc=%d out=%s", rc, out)
	}
}

func TestSandboxUsageErrors(t *testing.T) {
	if rc, _ := obs("sandbox"); rc != 2 {
		t.Errorf("no subcommand rc=%d", rc)
	}
	if rc, out := obs("sandbox", "frobnicate"); rc != 2 || !strings.Contains(out, "unknown subcommand") {
		t.Errorf("unknown rc=%d %s", rc, out)
	}
	t.Setenv("OBS_CONTAINER_RUNTIME", "nerdctl")
	if rc, out := obs("sandbox", "up", "--dir", t.TempDir()); rc != 2 || !strings.Contains(out, "want docker or podman") {
		t.Errorf("runtime rc=%d %s", rc, out)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if rc, out := obs("sandbox", "status", "--project", "none"); rc != 1 || !strings.Contains(out, "no sandbox state") {
		t.Errorf("status rc=%d %s", rc, out)
	}
}

func TestScrapeCommand(t *testing.T) {
	if rc, _ := obs("scrape"); rc != 2 {
		t.Errorf("no target rc=%d", rc)
	}
	if rc, out := obs("scrape", "--target", "http://127.0.0.1:1/m", "--label", "nope"); rc != 2 || !strings.Contains(out, "name=value") {
		t.Errorf("bad label rc=%d %s", rc, out)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if rc, out := obs("scrape", "--target", "http://127.0.0.1:1/m", "--once"); rc != 1 || !strings.Contains(out, "no running sandbox") {
		t.Errorf("no sandbox rc=%d %s", rc, out)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "app_up 1\n") }))
	defer target.Close()
	var q string
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { q = r.URL.RawQuery; w.WriteHeader(204) }))
	defer vm.Close()
	rc, out := obs("scrape", "--once", "--target", target.URL, "--vm-url", vm.URL, "--label", "durian_cloud_landscape=local")
	if rc != 0 || !strings.Contains(out, "1 samples") || !strings.Contains(q, "durian_cloud_landscape%3Dlocal") {
		t.Errorf("rc=%d out=%s q=%s", rc, out, q)
	}
	if rc, _ := obs("scrape", "--once", "--target", "http://127.0.0.1:1/m", "--vm-url", vm.URL); rc != 1 {
		t.Errorf("unreachable target rc=%d", rc)
	}
}
