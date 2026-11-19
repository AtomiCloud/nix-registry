package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const searchBody = `[
 {"id":1,"uid":"ckout01","title":"Checkout overview","uri":"db/checkout-overview","url":"/d/ckout01/checkout-overview","type":"dash-db","tags":["checkout"],"folderTitle":"Checkout"},
 {"id":2,"uid":"vm0001","title":"VictoriaMetrics","uri":"db/vm","url":"/d/vm0001/victoriametrics","type":"dash-db","tags":[],"folderTitle":"Platform"}
]`

func lookServer(t *testing.T, rec *recorder) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch r.URL.Path {
		case "/api/health":
			w.Write([]byte(`{"database":"ok","version":"13.3.0","commit":"abc"}`))
		case "/api/search":
			w.Write([]byte(searchBody))
		default:
			http.Error(w, "unexpected", 500)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func checklistFile(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "index.md")
	os.WriteFile(p, []byte("# Dashboards\n\n## Visual checklist\n\n- No panel says No data\n- Units are shown\n\n## Other\n"), 0o644)
	return p
}

func TestLookListsWhatTheClusterHasWithAddressesAndChecklist(t *testing.T) {
	rec := &recorder{}
	srv := lookServer(t, rec)
	res := Look(context.Background(), &Client{}, &Target{Name: "stage", Grafana: srv.URL}, LookOptions{Checklist: checklistFile(t), Out: "obs-look"})
	if !res.OK || len(res.Dashboards) != 2 {
		t.Fatalf("%+v", res)
	}
	d := res.Dashboards[0]
	if d.URL != srv.URL+"/d/ckout01/checkout-overview?from=now-6h&to=now" || d.Folder != "Checkout" {
		t.Fatalf("address: %+v", d)
	}
	if res.GrafanaVersion != "13.3.0" || len(res.Checklist) != 2 {
		t.Fatalf("%+v", res)
	}
	for _, want := range []string{"installs nothing", "platform chart", "No panel says No data", srv.URL + "/d/ckout01/checkout-overview", "obs look"} {
		if !strings.Contains(res.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, res.Prompt)
		}
	}
	assertReadOnly(t, rec)
}

func TestLookMatchFilter(t *testing.T) {
	rec := &recorder{}
	srv := lookServer(t, rec)
	res := Look(context.Background(), &Client{}, &Target{Name: "stage", Grafana: srv.URL}, LookOptions{Checklist: checklistFile(t), Match: "victoria"})
	if len(res.Dashboards) != 1 || res.Dashboards[0].UID != "vm0001" {
		t.Fatalf("%+v", res.Dashboards)
	}
}

func TestLookNeverFailsAndSaysWhyWhenGrafanaIsDown(t *testing.T) {
	res := Look(context.Background(), &Client{}, &Target{Name: "prod", Grafana: "http://127.0.0.1:1"}, LookOptions{Checklist: "/nonexistent"})
	if res.OK || !strings.Contains(res.Prompt, "127.0.0.1:1") || !strings.Contains(res.Prompt, "exits 0") {
		t.Fatalf("%+v", res)
	}
	res = Look(context.Background(), &Client{}, &Target{Name: "prod"}, LookOptions{Checklist: "/nonexistent"})
	if res.OK || !strings.Contains(res.Prompt, "grafana") {
		t.Fatalf("%+v", res)
	}
}

func TestLookMissingChecklistIsANoteNotAFailure(t *testing.T) {
	rec := &recorder{}
	srv := lookServer(t, rec)
	res := Look(context.Background(), &Client{}, &Target{Name: "stage", Grafana: srv.URL}, LookOptions{Checklist: "/nonexistent/index.md"})
	if !res.OK || len(res.Warnings) == 0 {
		t.Fatalf("%+v", res)
	}
}
