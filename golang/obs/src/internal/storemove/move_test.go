package storemove

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeCluster struct {
	calls    []Step
	fail     Step
	onStep   func(Step) // runs when a step is executed, before any planted failure
	resume   int        // what Check reports
	checkErr error
	checked  bool
}

func (f *fakeCluster) Check(context.Context, string, Config, bool) (int, error) {
	f.checked = true
	return f.resume, f.checkErr
}

func (f *fakeCluster) Execute(_ context.Context, s Step, _ string, _ Config) error {
	f.calls = append(f.calls, s)
	if f.onStep != nil {
		f.onStep(s)
	}
	if f.fail == s {
		return errors.New("planted readiness failure")
	}
	return nil
}

type fakeMetrics struct {
	counters map[string][]float64
	last     map[string]float64 // value most recently returned per counter
	calls    []string
	counts   int
	corrupt  bool
	end      time.Time
}

func (f *fakeMetrics) Counter(_ context.Context, _ string, name string) (float64, error) {
	f.calls = append(f.calls, name)
	vs := f.counters[name]
	if len(vs) == 0 {
		return 0, fmt.Errorf("missing fake metric %s", name)
	}
	v := vs[0]
	if len(vs) > 1 {
		f.counters[name] = vs[1:]
	}
	if f.last == nil {
		f.last = map[string]float64{}
	}
	f.last[name] = v
	return v, nil
}
func (f *fakeMetrics) Counts(_ context.Context, _ string, _ Config, end time.Time) ([]float64, error) {
	if !f.end.IsZero() && f.end != end {
		return nil, errors.New("verification window shifted")
	}
	f.end = end
	f.counts++
	v := make([]float64, 60)
	for i := range v {
		v[i] = float64(i + 1)
	}
	if f.corrupt && f.counts > 1 {
		v[17]++
	}
	return v, nil
}
func runFake(c *fakeCluster, m *fakeMetrics) Runner {
	return Runner{Cluster: c, Metrics: m, Pause: func(context.Context, time.Duration) error { return nil }, Now: func() time.Time { return time.Unix(1700000000, 0) }}
}
func TestMoves(t *testing.T) {
	for _, store := range []string{"tempo", "pyroscope", "victorialogs", "victoriametrics"} {
		t.Run(store, func(t *testing.T) {
			c := &fakeCluster{}
			m := &fakeMetrics{counters: map[string][]float64{received(store): {12, 12}, "tempo_live_store_blocks_completed_total": {5, 6}, "tempo_live_store_local_blocks_flushed_total": {5, 6}, "vl_insert_remote_send_errors_total": {0, 0}}}
			if storage(store) {
				m.counters[received(store)] = []float64{12, 13}
			}
			cfg := storeConfig(store)
			if err := runFake(c, m).Run(context.Background(), store, cfg, false, false); err != nil {
				t.Fatal(err)
			}
			if m.counts != 2 {
				t.Fatalf("baseline and verify not both run: %d", m.counts)
			}
			want := []Step{Prepare, Route, Reconnect, "stop compaction", "stop single"}
			if storage(store) {
				want = []Step{AddNodes, StorageRoute}
			}
			if store == "pyroscope" {
				want = []Step{Prepare, Route, Reconnect, "retain single as metastore"}
			}
			if !reflect.DeepEqual(c.calls, want) {
				t.Fatalf("steps %v, want %v", c.calls, want)
			}
		})
	}
}
func TestFailureNeverSkips(t *testing.T) {
	for _, failure := range []Step{Prepare, Route, Reconnect, "stop compaction", "stop single"} {
		t.Run(string(failure), func(t *testing.T) {
			c := &fakeCluster{fail: failure}
			m := &fakeMetrics{counters: map[string][]float64{received("tempo"): {5, 5}, "tempo_live_store_blocks_completed_total": {5, 6}, "tempo_live_store_local_blocks_flushed_total": {5, 6}}}
			err := runFake(c, m).Run(context.Background(), "tempo", config(), false, false)
			if err == nil || !strings.Contains(err.Error(), "planted readiness failure") {
				t.Fatalf("failure skipped: %v", err)
			}
			if c.calls[len(c.calls)-1] != failure {
				t.Fatalf("ran past %s: %v", failure, c.calls)
			}
			if m.counts != 1 {
				t.Fatal("verification ran after failed step")
			}
		})
	}
}
func TestMetricsFailures(t *testing.T) {
	cases := []struct {
		name, store, metric string
		values              []float64
	}{
		{"single still receives", "tempo", received("tempo"), []float64{2, 3}},
		{"counter reset", "tempo", received("tempo"), []float64{2, 0}},
		{"writes stopped", "victorialogs", received("victorialogs"), []float64{2, 2}},
		{"send errors", "victorialogs", "vl_insert_remote_send_errors_total", []float64{0, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeCluster{}
			m := &fakeMetrics{counters: map[string][]float64{received(tc.store): {1, 2}, "vl_insert_remote_send_errors_total": {0, 0}}}
			m.counters[tc.metric] = tc.values
			err := runFake(c, m).Run(context.Background(), tc.store, storeConfig(tc.store), false, false)
			if err == nil || !strings.Contains(err.Error(), tc.metric) {
				t.Fatalf("bad counter accepted: %v", err)
			}
			for _, call := range c.calls {
				if call == Step("stop single") {
					t.Fatal("stopped while receiving")
				}
			}
		})
	}
}
func TestDrainWaitsForAllFlushes(t *testing.T) {
	c := &fakeCluster{}
	m := &fakeMetrics{counters: map[string][]float64{received("tempo"): {1, 1}, "tempo_live_store_blocks_completed_total": {5, 7, 7}, "tempo_live_store_local_blocks_flushed_total": {5, 6, 7}}}
	// The proof is the state at the moment the single is stopped: the last flushed
	// value read must already have caught up to the last completed value read.
	// (Round 2: the old assertion looked at how many fake values were left, which
	// stays true when the g>=b check is deleted.)
	stopped := false
	c.onStep = func(s Step) {
		if s != Step("stop single") {
			return
		}
		stopped = true
		const completed, flushed = "tempo_live_store_blocks_completed_total", "tempo_live_store_local_blocks_flushed_total"
		if m.last[flushed] < m.last[completed] || m.last[flushed] != 7 {
			t.Errorf("single stopped before all completed blocks flushed: completed %g flushed %g", m.last[completed], m.last[flushed])
		}
	}
	if err := runFake(c, m).Run(context.Background(), "tempo", config(), false, false); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("single never stopped")
	}
}
func TestNoFlushDoesNotStop(t *testing.T) {
	c := &fakeCluster{}
	m := &fakeMetrics{counters: map[string][]float64{received("tempo"): {1, 1}, "tempo_live_store_blocks_completed_total": {5, 6}, "tempo_live_store_local_blocks_flushed_total": {5, 5}}}
	r := runFake(c, m)
	n := 0
	r.Pause = func(context.Context, time.Duration) error {
		n++
		if n > 3 {
			return context.DeadlineExceeded
		}
		return nil
	}
	if err := r.Run(context.Background(), "tempo", config(), false, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing flush accepted: %v", err)
	}
	for _, s := range c.calls {
		if s == Step("stop single") {
			t.Fatal("undrained single stopped")
		}
	}
}
func TestKnownDataMismatch(t *testing.T) {
	c := &fakeCluster{}
	m := &fakeMetrics{counters: map[string][]float64{received("victoriametrics"): {1, 2}}, corrupt: true}
	err := runFake(c, m).Run(context.Background(), "victoriametrics", storeConfig("victoriametrics"), false, false)
	if err == nil || !strings.Contains(err.Error(), "minute 17") {
		t.Fatalf("bad known data accepted: %v", err)
	}
}
func TestDryRunHasNoEffects(t *testing.T) {
	var b bytes.Buffer
	r := Runner{Out: &b}
	if err := r.Run(context.Background(), "tempo", config(), false, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "completed and flushed") {
		t.Fatal(b.String())
	}
}
func TestRollbackRetainsStorage(t *testing.T) {
	for _, store := range []string{"tempo", "victorialogs"} {
		c := &fakeCluster{}
		m := &fakeMetrics{counters: map[string][]float64{received(store): {5, 5}}}
		var b bytes.Buffer
		r := runFake(c, m)
		r.Out = &b
		if err := r.Run(context.Background(), store, storeConfig(store), true, false); err != nil {
			t.Fatal(err)
		}
		want := Restore
		if storage(store) {
			want = RestoreRoute
			if !strings.Contains(b.String(), "unreadable") {
				t.Fatal("lost-data warning missing")
			}
		}
		expected := []Step{want}
		if !storage(store) {
			expected = []Step{Restore, ReverseRoles, ReverseReconnect}
		}
		if !reflect.DeepEqual(c.calls, expected) {
			t.Fatal(c.calls)
		}
	}
}
func TestConfigRefusals(t *testing.T) {
	cfg := config()
	cfg.StopCompaction = nil
	if err := cfg.Validate("tempo", false); err == nil {
		t.Fatal("missing compaction control accepted")
	}
	cfg = config()
	cfg.Compactor = cfg.Single
	if err := cfg.Validate("tempo", false); err == nil {
		t.Fatal("undrained single accepted as compactor")
	}
	cfg = config()
	cfg.RoleLabels = []string{"app"}
	if err := cfg.Validate("tempo", false); err == nil {
		t.Fatal("arbitrary role label accepted")
	}
}
