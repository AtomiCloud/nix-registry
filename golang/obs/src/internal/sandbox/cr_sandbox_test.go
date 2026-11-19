package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
)

// Review of PR 131 (CodeRabbit, threads on the look sandbox): one test per finding, each red before the fix.

const lookFixture = "../../../tests/fixtures/look-seeded"

func runningStack(fr *fakeRunner) {
	fr.ps = ""
	for _, k := range Pins {
		fr.ps += fmt.Sprintf("obs-sandbox-demo-%s|%s|running\n", k.Name, k.Ref())
	}
	fr.networkExists = true
}

// 3: a retained look stack: groups obs owns in its folder that the checkout no longer has are removed.
func TestLookRemovesRuleGroupsTheCheckoutNoLongerHas(t *testing.T) {
	s := newStackServer(t)
	s.existingRules = []map[string]any{
		{"uid": "payment-errors", "folderUID": "obs-demo", "ruleGroup": "payment-errors"},
		{"uid": "gone-1", "folderUID": "obs-demo", "ruleGroup": "old group"},
		{"uid": "gone-2", "folderUID": "obs-demo", "ruleGroup": "old group"},
		{"uid": "elsewhere", "folderUID": "someone-elses-folder", "ruleGroup": "old group"},
	}
	cfg := s.cfg(t, &fakeRunner{})
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	if _, err := Up(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	want := []string{"/api/v1/provisioning/folder/obs-demo/rule-groups/old%20group"}
	if fmt.Sprint(s.deletes) != fmt.Sprint(want) {
		t.Fatalf("deleted %v, want exactly %v (the current group and other folders stay)", s.deletes, want)
	}
}

func TestLookRemovesEveryOwnedGroupWhenTheRuleSetIsEmpty(t *testing.T) {
	s := newStackServer(t)
	s.existingRules = []map[string]any{
		{"uid": "a", "folderUID": "obs-demo", "ruleGroup": "a"},
		{"uid": "b", "folderUID": "obs-demo", "ruleGroup": "b"},
		{"uid": "c", "folderUID": "other", "ruleGroup": "c"},
	}
	if err := ProvisionAlerts(context.Background(), http.DefaultClient, s.URL, "demo", &files.Set{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/api/v1/provisioning/folder/obs-demo/rule-groups/a", "/api/v1/provisioning/folder/obs-demo/rule-groups/b"}
	if fmt.Sprint(s.deletes) != fmt.Sprint(want) {
		t.Fatalf("deleted %v, want %v", s.deletes, want)
	}
}

// 4: a seed that failed part way is recorded as incomplete; the next look recreates the stores, a complete one is reused.
func TestLookRecreatesStoresAfterAPartialSeedLoad(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	s.failTraces = true
	if _, err := Up(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "loading traces") {
		t.Fatalf("want the Tempo failure, got %v", err)
	}
	if st := ReadState(cfg); st == nil || !st.SeedIncomplete {
		t.Fatalf("a part-loaded seed must be recorded as incomplete: %+v", st)
	}
	s.failTraces = false
	runningStack(fr)
	fr.calls = nil
	info, err := Up(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !fr.has("rm", "-f", "-v", "obs-sandbox-demo-vm") || !fr.has("rm", "-f", "-v", "obs-sandbox-demo-vl") || !fr.has("rm", "-f", "-v", "obs-sandbox-demo-tempo") {
		t.Errorf("the next look must remove the part-loaded stores before loading again: %v", fr.calls)
	}
	if info.Reused || info.From == 0 {
		t.Errorf("recreated stack loaded no fresh seed: %+v", info)
	}
	if st := ReadState(cfg); st == nil || st.SeedIncomplete {
		t.Errorf("a completed load must clear the incomplete mark: %+v", st)
	}
	// a completed, compatible snapshot is reused
	runningStack(fr)
	fr.calls = nil
	again, err := Up(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fr.has("rm") || !again.Reused || again.From != info.From {
		t.Errorf("a complete snapshot must be reused: rm=%v %+v", fr.has("rm"), again)
	}
}

// 5: the live window is checked again once setup is done.
func TestLookDoesNotPresentAWindowThatExpiredDuringSetup(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(fmt.Sprint("setup-slow=", slow), func(t *testing.T) {
			s := newStackServer(t)
			var now atomic.Int64
			now.Store(time.Date(2026, 11, 17, 10, 0, 30, 0, time.UTC).UnixMilli())
			cfg := s.cfg(t, &fakeRunner{})
			cfg.Dir = lookFixture
			cfg.ApplyIncidents = true
			cfg.LiveIncident = true
			cfg.Now = func() time.Time { return time.UnixMilli(now.Load()).UTC() }
			if slow {
				// provisioning takes longer than the incident's whole remaining window
				s.onGroup = func() { now.Add(int64(10 * time.Minute / time.Millisecond)) }
			}
			info, err := Up(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(info.Incidents) != 1 {
				t.Fatalf("historical guidance (the incident list) must stay: %+v", info.Incidents)
			}
			if !slow {
				if info.LiveIncident != "payment-spike" || len(info.LiveRules) == 0 {
					t.Fatalf("an open window stays current: %+v", info)
				}
				return
			}
			if info.LiveIncident != "" || len(info.LiveRules) != 0 {
				t.Errorf("the window ended during setup: live %q rules %v", info.LiveIncident, info.LiveRules)
			}
			if st := ReadState(cfg); st == nil || st.LiveIncident != "" {
				t.Errorf("the remembered state still calls the expired window current: %+v", st)
			}
		})
	}
}
