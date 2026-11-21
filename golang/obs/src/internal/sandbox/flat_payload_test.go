package sandbox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/files"
)

// A flat tier file's summary, description and panel link reach Grafana in the provisioned rule, as the
// standard's chart renders them. The fake Grafana records the payload.
func TestFlatRuleAnnotationsAreProvisioned(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"obs.yaml":                         "platform: tracker\n",
		"contract.yaml":                    "service: zinc\n",
		"alerts/retry-queue/critical.yaml": "title: Retry queue saturated\ndatasource: metrics\nexpr: 'queue_depth > 5000'\nfor: 10m\nsummary: 'Retry queue at {{ $values.A.Value }}'\ndescription: 'Check downstream health.'\npanel: 2\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := files.Load(dir)
	if err != nil || len(set.Problems) != 0 || len(set.Rules) != 1 {
		t.Fatalf("%v %v", err, set.Problems)
	}
	var got map[string]any
	hc := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/provisioning/alert-rules" {
			return reply(`[]`), nil
		}
		if r.Body != nil && strings.Contains(r.URL.Path, "/rule-groups/") {
			b, _ := io.ReadAll(r.Body)
			var v map[string]any
			_ = json.Unmarshal(b, &v)
			got = v["rules"].([]any)[0].(map[string]any)
		}
		return reply(`{}`), nil
	})}
	if err := ProvisionAlerts(context.Background(), hc, "http://grafana", "demo", set); err != nil {
		t.Fatal(err)
	}
	ann, _ := got["annotations"].(map[string]any)
	want := map[string]string{
		"summary": "Retry queue at {{ $values.A.Value }}", "description": "Check downstream health.",
		"__dashboardUid__": "tracker-zinc-overview", "__panelId__": "2",
	}
	for k, v := range want {
		if ann[k] != v {
			t.Errorf("annotation %s = %v, want %q (payload %v)", k, ann[k], v, got)
		}
	}
	if got["uid"] != "tracker-zinc-retry-queue-critical" {
		t.Errorf("uid %v", got["uid"])
	}
}
