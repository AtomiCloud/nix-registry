package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/AtomiCloud/obs/internal/seed"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
)

func TestIncidentAnnotationAndAlertProvisioningPayload(t *testing.T) {
	set, err := files.Load("../../../tests/fixtures/alerts/features")
	if err != nil {
		t.Fatal(err)
	}
	var rules, markers int
	hc := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Body == nil {
			return reply(`{}`), nil
		}
		b, _ := io.ReadAll(r.Body)
		var v map[string]any
		_ = json.Unmarshal(b, &v)
		switch {
		case strings.Contains(r.URL.Path, "/rule-groups/"):
			if r.Method != http.MethodPut || r.URL.EscapedPath() != "/api/v1/provisioning/folder/obs-demo/rule-groups/"+url.PathEscape(v["title"].(string)) {
				t.Errorf("wrong rule-group method or route: %s %s", r.Method, r.URL.EscapedPath())
			}
			rules += len(v["rules"].([]any))
			for _, raw := range v["rules"].([]any) {
				rule := raw.(map[string]any)
				if rule["folderUID"] != "obs-demo" || rule["condition"] == "" {
					t.Errorf("rule metadata %s", b)
				}
				if rule["uid"] == "keep" && rule["keep_firing_for"] != "3m" {
					t.Errorf("lost keepFiringFor: %s", b)
				}
				if record, ok := rule["record"].(map[string]any); ok && record["target_datasource_uid"] != "fixture-metrics" {
					t.Errorf("recording rule has no sandbox target datasource: %s", b)
				}
			}
		case r.URL.Path == "/api/annotations":
			markers++
			if v["dashboardUID"] != "payments-overview" || v["time"] != float64(1000) || v["timeEnd"] != float64(2000) || !strings.Contains(v["text"].(string), "spike") {
				t.Errorf("marker %s", b)
			}
		}
		return reply(`{}`), nil
	})}
	if err := ProvisionAlerts(context.Background(), hc, "http://grafana", "demo", set); err != nil {
		t.Fatal(err)
	}
	if err := AnnotateIncidents(context.Background(), hc, "http://grafana", []string{"payments-overview"}, []IncidentInfo{{Name: "spike", From: 1000, To: 2000, Fire: []string{"metric"}}}); err != nil {
		t.Fatal(err)
	}
	if rules != len(set.Rules) || markers != 1 {
		t.Fatalf("rules %d/%d markers %d", rules, len(set.Rules), markers)
	}
}

func TestLookSeedContainsIncidentData(t *testing.T) {
	s := newStackServer(t)
	cfg := s.cfg(t, &fakeRunner{})
	cfg.Dir = "../../../tests/fixtures/look-seeded"
	cfg.ApplyIncidents = true
	info, err := Up(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Incidents) != 1 || info.Incidents[0].Name != "payment-spike" || !info.TracesSeeded || !info.ProfilesSeeded {
		t.Errorf("info %+v", info)
	}
	if !strings.Contains(strings.Join(s.vlBodies, ""), `"durian.cloud/event":"checkout.failed"`) {
		t.Error("incident error logs missing; baseline has none")
	}
	for _, d := range s.dashes {
		raw := d["dashboard"].(map[string]any)
		if raw["annotations"] == nil {
			t.Error("dashboard has no annotation query; region cannot show")
		}
	}
	markers := 0
	for _, a := range s.annotations {
		tags, _ := a["tags"].([]any)
		for _, tag := range tags {
			if tag == "obs-incident" {
				markers++
				if a["dashboardUID"] != "look-payments" || int64(a["time"].(float64)) != info.From+int64(3*time.Minute/time.Millisecond) || int64(a["timeEnd"].(float64)) != info.From+int64(8*time.Minute/time.Millisecond) {
					t.Errorf("wrong applied marker: %v", a)
				}
			}
		}
	}
	if markers != 1 {
		t.Errorf("got %d incident markers, want one per dashboard", markers)
	}
	if len(s.groups) != 1 {
		t.Fatalf("got %d rule-group PUTs, want 1", len(s.groups))
	}
	if s.groupMethods[0] != http.MethodPut || s.groupRoutes[0] != "/api/v1/provisioning/folder/obs-demo/rule-groups/payment-errors" {
		t.Errorf("wrong Up rule-group request: %s %s", s.groupMethods[0], s.groupRoutes[0])
	}
	uids := map[string]bool{}
	for _, raw := range s.groups[0]["rules"].([]any) {
		uids[raw.(map[string]any)["uid"].(string)] = true
	}
	for _, uid := range []string{"payment-errors", "payment-safety", "payment-pending"} {
		if !uids[uid] {
			t.Errorf("rule-group missing %s", uid)
		}
	}
	before, after := -1.0, -1.0
	dec := json.NewDecoder(strings.NewReader(s.vmBody))
	for {
		var line vmLine
		if err := dec.Decode(&line); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if line.Metric["__name__"] != "checkout_requests_total" || line.Metric["outcome"] != "error" {
			continue
		}
		for j, ts := range line.Timestamps {
			if ts < info.From+int64(3*time.Minute/time.Millisecond) {
				before = line.Values[j]
			}
			if ts > info.From+int64(8*time.Minute/time.Millisecond) {
				after = line.Values[j]
			}
		}
	}
	if before != 0 || after < 500 || after > 700 {
		t.Errorf("incident counter upload before=%g after=%g, want 0 then ~600", before, after)
	}
	foundError := false
	for _, payload := range s.traces {
		for _, rr := range payload["resourceSpans"].([]any) {
			for _, scope := range rr.(map[string]any)["scopeSpans"].([]any) {
				for _, raw := range scope.(map[string]any)["spans"].([]any) {
					sp := raw.(map[string]any)
					ns, _ := strconv.ParseInt(sp["startTimeUnixNano"].(string), 10, 64)
					code := sp["status"].(map[string]any)["code"].(float64)
					if code == 2 && ns >= (info.From+int64(3*time.Minute/time.Millisecond))*1_000_000 && ns < (info.From+int64(8*time.Minute/time.Millisecond))*1_000_000 {
						foundError = true
					}
				}
			}
		}
	}
	if !foundError {
		t.Error("no ERROR OTLP span uploaded inside the incident window")
	}
}

func TestReplayAnnotationIsExplicitlyComputed(t *testing.T) {
	var got []byte
	hc := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { got, _ = io.ReadAll(r.Body); return reply(`{}`), nil })}
	if err := AnnotateReplay(context.Background(), hc, "http://grafana", []string{"board"}, []ReplayTransition{{Alert: "errors", At: 1000, From: "Normal", To: "Alerting"}}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("replay (computed by obs)")) || !bytes.Contains(got, []byte(`"dashboardUID":"board"`)) {
		t.Errorf("unlabelled replay payload %s", got)
	}
}

func TestLiveIncidentPlacementAndNoFutureData(t *testing.T) {
	sd, err := seed.Load("../../../tests/fixtures/look-seeded/seed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	incs, err := seed.LoadIncidents(sd, "../../../tests/fixtures/look-seeded")
	if err != nil {
		t.Fatal(err)
	}
	set, err := files.Load("../../../tests/fixtures/look-seeded")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC)
	from, to, live, err := seedWindow(sd, incs, set, now, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if live != "payment-spike" || !to.Equal(now) || !incs[0].Contains(now.Sub(from)) {
		t.Fatalf("from=%v to=%v live=%s", from, to, live)
	}
	ds, err := seed.BuildIncidents(sd, from, incs)
	if err != nil {
		t.Fatal(err)
	}
	clipDataset(ds, to)
	for _, sr := range ds.Series {
		for _, ts := range sr.Ts {
			if ts > now.UnixMilli() {
				t.Fatal("future metric")
			}
		}
	}
	for _, stream := range ds.Streams {
		for _, row := range stream.Rows {
			if row.TsNs > now.UnixNano() {
				t.Fatal("future log")
			}
		}
	}
	for _, tr := range ds.Traces {
		for _, sp := range tr.Spans {
			if sp.StartNs+sp.DurNs > uint64(now.UnixNano()) {
				t.Fatal("future trace")
			}
		}
	}
}

func TestLiveStatePanelPayload(t *testing.T) {
	dashboard := map[string]any{"panels": []any{map[string]any{"id": float64(2), "gridPos": map[string]any{"y": float64(8), "h": float64(8)}}}}
	id := addAlertStatePanel(dashboard, "demo")
	panel := dashboard["panels"].([]any)[1].(map[string]any)
	options := panel["options"].(map[string]any)
	if id != 3 || panel["type"] != "alertlist" || options["showInactiveAlerts"] != true || options["folder"].(map[string]any)["uid"] != "obs-demo" {
		t.Errorf("native live-state panel %v", panel)
	}
}

func TestNativeForecastWindow(t *testing.T) {
	dir := "../../../tests/fixtures/look-seeded"
	sd, err := seed.Load(dir + "/seed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	incs, err := seed.LoadIncidents(sd, dir)
	if err != nil {
		t.Fatal(err)
	}
	set, err := files.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	from, to, _, err := seedWindow(sd, incs, set, now, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := seed.BuildIncidents(sd, from, incs)
	if err != nil {
		t.Fatal(err)
	}
	clipDataset(ds, to)
	rules, err := nativeForecast(dir, dir+"/seed.yaml", set, ds, now, incs[0].Expect.Fire)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if rule.UID == "payment-pending" {
			if rule.FiringAt != now.Add(70*time.Second).UnixMilli() || rule.HoldUntil <= rule.FiringAt || !rule.Supported {
				t.Fatalf("forecast %+v", rule)
			}
			return
		}
	}
	t.Fatal("pending rule forecast absent")
}
