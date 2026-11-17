package alerts

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestPruneSnapshotRawInputsAndRecordingStages(t *testing.T) {
	t.Setenv("OBS_VERIFY_PRUNE_SNAPSHOT", "fixture")
	k := qKey{ds: "logs", expr: "fixture stats count()", run: "baseline"}
	points := map[int64]float64{2: math.Inf(1), 1: 10, 3: math.NaN()}
	d := &qData{res: []Result{{Labels: []Label{{"outcome", "error"}}, Points: points, Nulls: map[int64]bool{4: true}}}, perTick: map[int64][]Result{5: {{Points: map[int64]float64{5: math.Inf(-1)}}}}, tickErr: map[int64]error{6: fmt.Errorf("fixture error")}}
	c := &queryCache{m: map[qKey]*qEntry{k: {d: d}}}
	c.capturePruneSnapshot("record:r")
	points[1] = 11
	c.capturePruneSnapshot("alerts")
	if len(c.pruneHistory) != 2 {
		t.Fatalf("lost recording history: %+v", c.pruneHistory)
	}
	for key, s := range c.pruneHistory {
		want := jf(11)
		if strings.HasPrefix(key, "record:") {
			want = 10
		}
		if got := s.Series[0].Points[0][1]; got != want {
			t.Fatalf("%s input=%v want %v", key, got, want)
		}
		if s.Series[0].Nulls[0] != 4 || s.Ticks["6"].Error != "fixture error" {
			t.Fatalf("null/error state omitted: %+v", s)
		}
	}
	raw, err := json.Marshal(c.pruneHistory)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`"NaN"`, `"+Inf"`, `"-Inf"`, `"outcome":"error"`} {
		if !strings.Contains(string(raw), token) {
			t.Fatalf("snapshot missing %s: %s", token, raw)
		}
	}
	t.Setenv("OBS_VERIFY_PRUNE_SNAPSHOT", "")
	c.pruneHistory = nil
	c.capturePruneSnapshot("disabled")
	if c.pruneHistory != nil {
		t.Fatal("normal evaluation allocated debug history")
	}
}
