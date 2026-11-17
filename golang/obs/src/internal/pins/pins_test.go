package pins

import (
	"os"
	"strings"
	"testing"
)

func TestVictoriaMetricsModulePin(t *testing.T) {
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	want := "github.com/VictoriaMetrics/VictoriaMetrics " + All()["VictoriaMetrics"].Version
	if !strings.Contains(string(b), want+"\n") {
		t.Fatalf("go.mod: VictoriaMetrics pin differs: want %s", want)
	}
}

func TestPinsAreComplete(t *testing.T) {
	p := All()
	for _, name := range []string{"VictoriaMetrics", "VictoriaLogs", "Tempo", "Grafana", "Pyroscope", "VictoriaLogsDatasource"} {
		if p[name].Version == "" {
			t.Fatalf("pins.json: missing %s version", name)
		}
	}
	for _, name := range []string{"VictoriaLogs", "Tempo", "Grafana"} {
		if len(p[name].Commit) < 8 {
			t.Fatalf("pins.json: missing %s source commit", name)
		}
	}
}
