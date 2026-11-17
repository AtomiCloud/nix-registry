package pins

import (
	"os"
	"strings"
	"testing"
)

func TestEngineModulePins(t *testing.T) {
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VictoriaMetrics", "VictoriaLogs"} {
		want := "github.com/VictoriaMetrics/" + name + " " + All()[name].Version
		if !strings.Contains(string(b), want+"\n") {
			t.Fatalf("go.mod: %s pin differs: want %s", name, want)
		}
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "github.com/grafana/tempo" && !strings.HasSuffix(fields[1], "-"+All()["Tempo"].Commit) {
			t.Fatalf("go.mod: Tempo module %s differs from source commit %s", fields[1], All()["Tempo"].Commit)
		}
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
