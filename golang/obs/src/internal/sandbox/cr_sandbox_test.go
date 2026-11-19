package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
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

// GPT review of PR 133, finding 1: the incomplete mark is written before any upload (the look fails when it
// cannot be written) and stays until the stores are really gone.
func TestLookFailsBeforeUploadingWhenTheMarkCannotBeWritten(t *testing.T) {
	s := newStackServer(t)
	cfg := s.cfg(t, &fakeRunner{})
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	// The state file cannot be replaced (a directory sits where it goes); the lock next to it still works.
	cfg.StateDir = t.TempDir()
	c := cfg
	c.defaults()
	if err := os.MkdirAll(filepath.Join(stateFile(c), "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "seed load") {
		t.Fatalf("want an error naming the seed load record, got %v", err)
	}
	if s.vmBody != "" || len(s.vlBodies) != 0 {
		t.Errorf("data was uploaded without a record that a load had begun: vm %d bytes, vl %d bodies", len(s.vmBody), len(s.vlBodies))
	}
}

func TestIncompleteMarkSurvivesAFailedStoreRemoval(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{lists: map[string]string{"volume": "obs-sandbox-demo-vm\nobs-sandbox-demo-vl", "network": "obs-sandbox-demo"}}
	cfg := s.cfg(t, fr)
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	s.failTraces = true
	if _, err := Up(context.Background(), cfg); err == nil {
		t.Fatal("want the Tempo failure")
	}
	s.failTraces = false
	runningStack(fr)
	fr.failVolumeRm = true
	if _, err := Up(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "part-loaded seed") {
		t.Fatalf("a store that cannot be removed must stop the look: %v", err)
	}
	if st := ReadState(cfg); st == nil || !st.SeedIncomplete {
		t.Fatalf("the mark must stay until cleanup succeeded: %+v", st)
	}
	fr.failVolumeRm = false
	runningStack(fr)
	fr.calls = nil
	if _, err := Up(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !fr.has("rm", "-f", "-v", "obs-sandbox-demo-vm") {
		t.Errorf("after the removal error the next look must clear the stores: %v", fr.calls)
	}
}

// finding 2: a retained look whose checkout changed a rule does not reuse the old live forecast.
func TestRetainedLookDropsTheForecastWhenTheRulesChanged(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	dir := filepath.Join(t.TempDir(), "observability")
	if err := os.CopyFS(dir, os.DirFS(lookFixture)); err != nil {
		t.Fatal(err)
	}
	cfg.Dir = dir
	cfg.ApplyIncidents = true
	cfg.LiveIncident = true
	first, err := Up(context.Background(), cfg)
	if err != nil || len(first.LiveRules) == 0 {
		t.Fatalf("first look: %v %+v", err, first.LiveRules)
	}
	runningStack(fr)
	same, err := Up(context.Background(), cfg)
	if err != nil || !same.Reused || len(same.LiveRules) != len(first.LiveRules) {
		t.Fatalf("an unchanged checkout keeps its forecast: %v reused=%v %+v", err, same.Reused, same.LiveRules)
	}
	alert := filepath.Join(dir, "alerts", "errors", "warning.yaml")
	b, err := os.ReadFile(alert)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alert, []byte(strings.Replace(string(b), "for: 1m", "for: 2m", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	runningStack(fr)
	changed, err := Up(context.Background(), cfg)
	if err != nil || !changed.Reused {
		t.Fatalf("changed look: %v reused=%v", err, changed.Reused)
	}
	for _, r := range changed.LiveRules {
		if r.UID == "payment-pending" && r.For == "1m0s" {
			t.Fatalf("the forecast of the old rule set was reused: %+v", changed.LiveRules)
		}
	}
}

// finding 3: a retained window that ended before the early reuse check clears the incident and its forecast together.
func TestRetainedExpiredWindowClearsIncidentAndForecastTogether(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	cfg.LiveIncident = true
	var now atomic.Int64
	now.Store(time.Date(2026, 11, 17, 10, 0, 30, 0, time.UTC).UnixMilli())
	cfg.Now = func() time.Time { return time.UnixMilli(now.Load()).UTC() }
	first, err := Up(context.Background(), cfg)
	if err != nil || first.LiveIncident == "" || len(first.LiveRules) == 0 {
		t.Fatalf("first look: %v %+v", err, first)
	}
	runningStack(fr)
	now.Add(int64(time.Hour / time.Millisecond))
	cfg.LiveIncident = false // a historical look reuses the snapshot instead of refreshing it
	again, err := Up(context.Background(), cfg)
	if err != nil || !again.Reused {
		t.Fatalf("second look: %v reused=%v", err, again.Reused)
	}
	if again.LiveIncident != "" || len(again.LiveRules) != 0 || len(again.Incidents) != 1 {
		t.Errorf("expired window: live %q rules %v incidents %d (history stays, forecast goes)", again.LiveIncident, again.LiveRules, len(again.Incidents))
	}
}

// A historical reuse without a forecast leaves the public Info as it was: no fingerprint, no forecast.
func TestHistoricalReuseJSONHasNoFingerprint(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	cfg.SeedBase = time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC).Unix()
	if _, err := Up(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	runningStack(fr)
	again, err := Up(context.Background(), cfg)
	if err != nil || !again.Reused {
		t.Fatalf("%v reused=%v", err, again.Reused)
	}
	b, _ := json.Marshal(again)
	if strings.Contains(string(b), "live_rules") {
		t.Errorf("a historical reuse with no forecast prints forecast fields: %s", b)
	}
}

// A second setup (or sandbox down) of the same project while one is running must not tear down the stores
// the running one is loading: the incomplete mark is also present during a load.
func TestSecondSetupOfTheSameProjectDoesNotTearDownARunningLoad(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	s.failTraces = true
	if _, err := Up(context.Background(), cfg); err == nil {
		t.Fatal("want the Tempo failure that leaves the incomplete mark")
	}
	s.failTraces = false
	// The running setup holds the project lock, as Up does from its state read to its last state write.
	c := cfg
	c.defaults()
	lf, err := os.OpenFile(lockFile(c), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	runningStack(fr)
	fr.calls = nil
	if _, err := Up(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("a second setup of a busy project must refuse, got %v", err)
	}
	if err := Down(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("sandbox down of a busy project must refuse, got %v", err)
	}
	if fr.has("rm") {
		t.Fatalf("the running setup's stores were removed: %v", fr.calls)
	}
	if st := ReadState(cfg); st == nil || !st.SeedIncomplete {
		t.Fatalf("the running setup's state must be left alone: %+v", st)
	}
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	if _, err := Up(context.Background(), cfg); err != nil {
		t.Fatalf("once the project is free, a failed load is cleared and reloaded: %v", err)
	}
	if !fr.has("rm", "-f", "-v", "obs-sandbox-demo-vm") {
		t.Errorf("the part-loaded stores must be removed once the project is free: %v", fr.calls)
	}
}

// A state folder that cannot be made: the lock beside it cannot be taken, so the setup stops before anything
// is uploaded or any container is touched (fail closed).
func TestLookFailsBeforeUploadingWhenTheStateFolderCannotBeMade(t *testing.T) {
	s := newStackServer(t)
	cfg := s.cfg(t, &fakeRunner{})
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = filepath.Join(blocker, "state") // a directory cannot be made under a file
	if _, err := Up(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "sandbox lock") {
		t.Fatalf("want the lock error, got %v", err)
	}
	if s.vmBody != "" || len(s.vlBodies) != 0 {
		t.Errorf("data was uploaded: vm %d bytes, vl %d bodies", len(s.vmBody), len(s.vlBodies))
	}
}

// A lock file that cannot be opened for writing (left by another user) still locks: a second setup of a
// busy project is refused and removes nothing.
func TestAReadOnlyLockFileStillLocksTheProject(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	s.failTraces = true
	if _, err := Up(context.Background(), cfg); err == nil {
		t.Fatal("want the Tempo failure that leaves the incomplete mark")
	}
	s.failTraces = false
	c := cfg
	c.defaults()
	name := lockFile(c)
	if err := os.Chmod(name, 0o444); err != nil {
		t.Fatal(err)
	}
	if f, err := os.OpenFile(name, os.O_RDWR, 0); err == nil {
		f.Close()
		t.Skip("running as a user that can write a 0444 file (root): the case cannot be made here")
	}
	held, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	runningStack(fr)
	fr.calls = nil
	if _, err := Up(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("a busy project with a read-only lock file must refuse, got %v", err)
	}
	if fr.has("rm") {
		t.Fatalf("the running setup's stores were removed: %v", fr.calls)
	}
}

// A lock file that cannot be opened at all: refuse rather than run unlocked (with or without state).
func TestAnUnopenableLockFileRefuses(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	cfg.Dir = lookFixture
	cfg.ApplyIncidents = true
	s.failTraces = true
	if _, err := Up(context.Background(), cfg); err == nil {
		t.Fatal("want the Tempo failure that leaves the incomplete mark")
	}
	s.failTraces = false
	c := cfg
	c.defaults()
	name := lockFile(c)
	if err := os.Chmod(name, 0o000); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(name); err == nil {
		f.Close()
		t.Skip("running as a user that can read a 0000 file (root): the case cannot be made here")
	}
	runningStack(fr)
	fr.calls = nil
	if _, err := Up(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "touches no container without it") {
		t.Fatalf("want a refusal naming the lock, got %v", err)
	}
	if fr.has("rm") {
		t.Fatalf("stores were removed without the lock: %v", fr.calls)
	}
}

// With no state file at all, an unopenable lock still refuses: a running setup holds the lock before it
// writes any state, so "no state" does not mean "nothing running".
func TestAnUnopenableLockFileRefusesWithoutState(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	c := cfg
	c.defaults()
	name := lockFile(c)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, nil, 0o000); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(name); err == nil {
		f.Close()
		t.Skip("running as a user that can read a 0000 file (root): the case cannot be made here")
	}
	runningStack(fr)
	if err := Down(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "touches no container without it") {
		t.Fatalf("want a refusal naming the lock, got %v", err)
	}
	if fr.has("rm") {
		t.Fatalf("containers were removed without the lock: %v", fr.calls)
	}
}

// The default lock depends on no environment variable: invocations with different XDG_CACHE_HOME or TMPDIR
// that address the same project share it.
func TestDefaultLockIsSharedAcrossCacheFolders(t *testing.T) {
	cfg := Config{Project: "demo"}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	a := lockFile(cfg)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	if b := lockFile(cfg); a != b || a != "/tmp/obs-sandbox-demo.lock" {
		t.Fatalf("lock path must be one machine-wide place per project: %q vs %q", a, b)
	}
}

// The lock is never taken through a symlink or on anything but a regular file, and a project name cannot
// reach outside the lock folder.
func TestLockRefusesSymlinksOtherFilesAndBadNames(t *testing.T) {
	old := lockDir
	lockDir = t.TempDir()
	defer func() { lockDir = old }()
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(lockDir, "obs-sandbox-link.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := lockProject(Config{Project: "link"}); err == nil {
		t.Fatal("a symlinked lock file must be refused")
	}
	if err := os.Mkdir(filepath.Join(lockDir, "obs-sandbox-dir.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := lockProject(Config{Project: "dir"}); err == nil || !strings.Contains(err.Error(), "touches no container") {
		t.Fatalf("a directory in the lock's place must be refused, got %v", err)
	}
	for _, p := range []string{"../x", "a/b", "", "-x"} {
		if _, err := lockProject(Config{Project: p}); err == nil {
			t.Errorf("project %q must be refused", p)
		}
	}
	unlock, err := lockProject(Config{Project: "ok-1.a_b"})
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if fi, err := os.Lstat(filepath.Join(lockDir, "obs-sandbox-ok-1.a_b.lock")); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the lock must be a regular file in the lock folder: %v %v", fi, err)
	}
}

// A FIFO in the lock's place is rejected at once: no open may wait for a writer before the regular-file check.
func TestLockRejectsAFIFOPromptly(t *testing.T) {
	old := lockDir
	lockDir = t.TempDir()
	defer func() { lockDir = old }()
	// 0444: the read-write open fails, so the read-only fallback is the open that could block.
	if err := syscall.Mkfifo(filepath.Join(lockDir, "obs-sandbox-fifo.lock"), 0o444); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := lockProject(Config{Project: "fifo"})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("a FIFO must be refused as not a regular file, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("opening a FIFO lock blocked")
	}
}
