package seed

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIncidentGenerationAndLazyLogs(t *testing.T) {
	sd := &Seed{Length: "10m", RandomSeed: 42, Traffic: Traffic{Requests: "3/s"}, Metrics: map[string]MetricDef{
		"latency": {Type: "histogram", Unit: "s", Buckets: []float64{0.1, 0.5}, Latency: &LatencyDef{Median: 0.1, Sigma: 0.5}, Series: []SeriesDef{{Where: map[string]string{"route": "fixture"}, Share: 1}}},
		"events":  {Type: "counter", Series: []SeriesDef{{Where: map[string]string{"outcome": "error"}, Share: 0}}},
	}, Logs: []LogDef{{Event: "fixture.request", From: LogSource{Metric: "latency"}}}}
	at := time.Unix(1700000000, 0)
	baseline, err := Build(sd, at)
	if err != nil {
		t.Fatal(err)
	}
	without := *sd
	without.Logs = nil
	noLogs, err := Build(&without, at)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline.Series, noLogs.Series) {
		t.Fatal("lazy log loading changed generated metric samples")
	}
	i := &Incident{Change: []Change{{Metric: "events", Rate: "2/s"}}}
	i.Window.Start = "2m"
	i.Window.Duration = "3m"
	changed, err := BuildIncident(sd, at, i)
	if err != nil {
		t.Fatal(err)
	}
	if !i.Contains(2*time.Minute) || i.Contains(5*time.Minute) {
		t.Fatal("incident window is not half open")
	}
	for k, s := range baseline.Series {
		isEvent := false
		for _, l := range s.Labels {
			isEvent = isEvent || (l.Name == "__name__" && l.Value == "events_total")
		}
		if !isEvent {
			if !reflect.DeepEqual(s, changed.Series[k]) {
				t.Fatal("unaffected series changed")
			}
			continue
		}
		if changed.Series[k].Vals[len(s.Vals)-1] <= 0 {
			t.Fatal("incident added no events")
		}
	}
}

func TestIncidentLoadRefusesBadWindowAndMetric(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "incidents"), 0o755); err != nil {
		t.Fatal(err)
	}
	sd := &Seed{Length: "10m", Incidents: []string{"spike"}, Metrics: map[string]MetricDef{"m": {Type: "counter", Series: []SeriesDef{{Where: map[string]string{"outcome": "error"}, Share: 1}}}}}
	for _, tc := range []struct {
		yaml string
		ok   bool
	}{
		{"window: {start: 2m, duration: 3m}\nchange: [{metric: m, rate: 2/s}]\nexpect: {fire: [fixture]}", true},
		{"window: {start: 9m, duration: 3m}\nchange: [{metric: m, rate: 2/s}]\nexpect: {fire: [fixture]}", false},
		{"window: {start: 2m, duration: 3m}\nchange: [{metric: typo, rate: 2/s}]\nexpect: {fire: [fixture]}", false},
		{"window: {start: 2m, duration: 3m}\nchange: [{metric: m, where: {outcome: typo}, rate: 2/s}]\nexpect: {fire: [fixture]}", false},
		{"window: {start: 2m, duration: 3m}\nchange: [{metric: m, rate: 2/s}]\nexpect: {fire: [fixture], fire_within: typo}", false},
	} {
		if err := os.WriteFile(filepath.Join(dir, "incidents/spike.yaml"), []byte(tc.yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadIncidents(sd, dir)
		if (err == nil) != tc.ok {
			t.Fatalf("load ok=%v want %v: %v", err == nil, tc.ok, err)
		}
	}
}

func disturbanceSeed(t *testing.T, incident string) (*Seed, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "incidents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "incidents", "blip.yaml"), []byte(incident), 0o644); err != nil {
		t.Fatal(err)
	}
	sd := &Seed{Length: "40m", RandomSeed: 1, Traffic: Traffic{Requests: "3/s"}, Metrics: map[string]MetricDef{
		"events": {Type: "counter", Series: []SeriesDef{{Where: map[string]string{"outcome": "error"}, Share: 0}}},
	}, Incidents: []string{"blip"}}
	return sd, dir
}

func TestLoadIncidentsReadsTheDisturbanceReason(t *testing.T) {
	sd, dir := disturbanceSeed(t, "story: s\nwindow: { start: 5m, duration: 5m }\nchange: [{ metric: events, rate: 1/s }]\nexpect: { quiet: [a] }\ndisturbance: proves a trickle stays quiet\n")
	incs, err := LoadIncidents(sd, dir)
	if err != nil || len(incs) != 1 || incs[0].Disturbance != "proves a trickle stays quiet" {
		t.Fatalf("got %+v, %v", incs, err)
	}
}

func TestLoadIncidentsEmptyDisturbanceNamesFileAndLine(t *testing.T) {
	for name, line := range map[string]string{"empty string": `disturbance: ""`, "blank": `disturbance: "   "`, "null": `disturbance:`} {
		t.Run(name, func(t *testing.T) {
			sd, dir := disturbanceSeed(t, "story: s\nwindow: { start: 5m, duration: 5m }\nchange: [{ metric: events, rate: 1/s }]\nexpect: { quiet: [a] }\n"+line+"\n")
			_, err := LoadIncidents(sd, dir)
			want := filepath.Join(dir, "incidents", "blip.yaml") + ":5"
			if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "disturbance") {
				t.Fatalf("want an error naming %s and disturbance, got %v", want, err)
			}
		})
	}
}
