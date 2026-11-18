package alerts

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/seed"
)

func TestReplayEqualsAlertsFireTimes(t *testing.T) {
	dir := "../../../tests/fixtures/alerts/features"
	p, err := loadProject(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(-6 * time.Hour)
	runs := planRuns(p, base)
	ds, err := seed.BuildIncident(&p.Seed, runs[1].Start, runs[1].Incident)
	if err != nil {
		t.Fatal(err)
	}
	replay, recorded, err := ReplayDataset(dir, "", ds)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) == 0 {
		t.Fatal("recording samples missing")
	}
	steps := filepath.Join(t.TempDir(), "steps.json")
	code := Check(Options{Dir: dir, Base: base.Unix(), Steps: steps, Workers: 1}, io.Discard, io.Discard)
	if code > 1 {
		t.Fatalf("alerts setup failed: %d", code)
	}
	b, err := os.ReadFile(steps)
	if err != nil {
		t.Fatal(err)
	}
	var got []AlertRun
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, r := range replay {
		found := false
		for _, a := range got {
			if a.Run == runs[1].Name && a.Alert == r.Alert {
				found = true
				if !reflect.DeepEqual(r.FiringTimes, a.FiringTimes) || r.FirstFireMs != a.FirstFireMs {
					t.Errorf("rule %s replay fire times %v != obs alerts %v", r.Alert, r.FiringTimes, a.FiringTimes)
				}
			}
		}
		if !found {
			t.Errorf("rule %s missing from alerts", r.Alert)
		}
	}
}
