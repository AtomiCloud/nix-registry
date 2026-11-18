package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/AtomiCloud/obs/internal/alerts"
	"github.com/AtomiCloud/obs/internal/seed"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacySandboxSpecAndLoaders(t *testing.T) {
	c := Config{Project: "demo", PortBase: 18300}
	c.defaults()
	var args [][]string
	for _, k := range Pins {
		args = append(args, c.createArgs(k))
	}
	b, _ := json.Marshal(args)
	if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != "5c0d8817b58b0b4a2b133798011768519f5d2a1301f5914754ee160b64f3b8fa" {
		t.Errorf("sandbox up container spec changed from 70b4358: %s", got)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(tempoConfig))); got != "c0c8993911c98a7b8adf9177ff3f0ce32a69031233b9535674097818c90d19a3" {
		t.Errorf("sandbox up Tempo config changed from 70b4358: %s", got)
	}
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	if err := os.WriteFile(filepath.Join(cfg.Dir, "dashboards", "profiles.json"), []byte(`{"uid":"profiles","panels":[{"id":1,"type":"flamegraph","datasource":{"type":"grafana-pyroscope-datasource","uid":"checkout-profiles"},"targets":[{"refId":"A"}]}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(s.traces) != 0 || s.profileLoads != 0 {
		t.Fatalf("legacy sandbox up called new loaders: traces=%d profiles=%d", len(s.traces), s.profileLoads)
	}
	var provisioned []string
	for _, ds := range s.dsPosts {
		provisioned = append(provisioned, ds["uid"].(string))
	}
	if strings.Join(provisioned, ",") != "payments-metrics,payments-logs,obs-traces,obs-profiles" {
		t.Errorf("legacy datasource provisioning changed: %v", provisioned)
	}
	if len(fr.tempoFiles) != 1 || fr.tempoFiles[0] != tempoConfig {
		t.Error("sandbox up wrote the look Tempo config instead of the original 0.4 file")
	}
}

func TestOverlappingIncidentsReplayLikeAlerts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "observability")
	if err := os.CopyFS(dir, os.DirFS("../../../tests/fixtures/look-seeded")); err != nil {
		t.Fatal(err)
	}
	sd, err := seed.Load(filepath.Join(dir, "seed.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sd.Incidents = append(sd.Incidents, "cancel")
	b, err := yaml.Marshal(sd)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.yaml"), b, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "incidents", "cancel.yaml"), []byte("story: cancels the simultaneous error stream\nwindow: {start: 3m, duration: 5m}\nchange: [{metric: checkout.requests, where: {outcome: error}, share_x: 0}]\nexpect: {quiet: [payment-errors, payment-pending, payment-safety]}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	from := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Hour)
	s := newStackServer(t)
	cfg := s.cfg(t, &fakeRunner{})
	cfg.Dir = dir
	cfg.ApplyIncidents = true
	cfg.SeedBase = from.Unix()
	cfg.Now = time.Now
	info, err := Up(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	steps := filepath.Join(t.TempDir(), "steps.json")
	code := alerts.Check(alerts.Options{Dir: dir, Base: from.Add(-sd.Duration() - 30*time.Minute).Unix(), Steps: steps, Workers: 1}, io.Discard, io.Discard)
	if code > 1 {
		t.Fatalf("obs alerts failed to set up: %d", code)
	}
	b, err = os.ReadFile(steps)
	if err != nil {
		t.Fatal(err)
	}
	var runs []alerts.AlertRun
	if err := json.Unmarshal(b, &runs); err != nil {
		t.Fatal(err)
	}
	want := int64(0)
	for _, run := range runs {
		if run.Run == "payment-spike" && run.Alert == "payment-errors" {
			want = run.FirstFireMs
		}
	}
	if want == 0 {
		t.Fatal("isolated obs alerts did not fire")
	}
	for _, tr := range info.Replay {
		if tr.Alert == "payment-errors" && tr.To == "Alerting" && tr.At == want {
			return
		}
	}
	t.Fatalf("overlapping combined timeline erased isolated replay fire at %d: %v", want, info.Replay)
}

func TestLookRejectsOldTempoReceiver(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{tempoBindings: `{}`}
	for _, k := range Pins {
		fr.ps += fmt.Sprintf("obs-sandbox-demo-%s|%s|running\n", k.Name, k.Ref())
	}
	cfg := s.cfg(t, fr)
	cfg.ApplyIncidents = true
	_, err := Up(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "stack started by obs sandbox up or an older obs") || !strings.Contains(err.Error(), "run `obs sandbox down` first") {
		t.Fatalf("want older-stack receiver diagnostic, got %v", err)
	}
}

func TestExpiredDefaultLookRefreshesAndHistoricalReuses(t *testing.T) {
	for _, historical := range []bool{false, true} {
		t.Run(fmt.Sprint(historical), func(t *testing.T) {
			s := newStackServer(t)
			fr := &fakeRunner{lists: map[string]string{"network": "obs-sandbox-demo"}}
			cfg := s.cfg(t, fr)
			cfg.Dir = "../../../tests/fixtures/look-seeded"
			cfg.ApplyIncidents = true
			cfg.LiveIncident = !historical
			clock := time.Now().UTC().Truncate(time.Second)
			cfg.Now = func() time.Time { return clock }
			var output strings.Builder
			cfg.Out = &output
			if historical {
				cfg.SeedBase = clock.Add(-time.Hour).Unix()
			}
			first, err := Up(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range Pins {
				fr.ps += fmt.Sprintf("obs-sandbox-demo-%s|%s|running\n", k.Name, k.Ref())
			}
			fr.networkExists = true
			fr.ps += "obs-sandbox-other-vm|other-image|running\n"
			fr.calls = nil
			clock = clock.Add(time.Hour)
			second, err := Up(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if historical {
				if second.From != first.From || fr.has("rm") {
					t.Error("explicit history was refreshed")
				}
			} else {
				if second.From <= first.From || second.LiveIncident == "" || !fr.has("rm") {
					t.Errorf("expired default look reused old snapshot: first=%+v second=%+v", first, second)
				}
				if !strings.Contains(output.String(), "recreated the old look stack") {
					t.Error("refresh did not say the old stack was recreated")
				}
				if fr.has("rm", "-f", "-v", "obs-sandbox-other-vm") || !strings.Contains(fr.ps, "obs-sandbox-other-vm") {
					t.Error("refresh touched another project")
				}
			}
		})
	}
}
