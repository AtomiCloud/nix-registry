package pins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestVictoriaLogsSourceMarker(t *testing.T) {
	var marker struct{ Version, Commit, ManifestSHA256 string }
	b, err := os.ReadFile("../../third_party/VictoriaLogs.source.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &marker); err != nil {
		t.Fatal(err)
	}
	p := All()["VictoriaLogs"]
	if marker.Version != p.Version || marker.Commit != p.Commit {
		t.Fatalf("VictoriaLogs source marker differs from pins.json: %+v vs %+v", marker, p)
	}
	b, err = os.ReadFile("../../third_party/VictoriaLogs.sha256")
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != marker.ManifestSHA256 {
		t.Fatal("VictoriaLogs.sha256 differs from pinned source marker")
	}
}

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

func TestGrafanaPortProvenance(t *testing.T) {
	b, err := os.ReadFile("../alerts/gfexpr/PROVENANCE.md")
	if os.IsNotExist(err) {
		t.Skip("Grafana alert port has not landed")
	}
	if err != nil {
		t.Fatal(err)
	}
	p := All()["Grafana"]
	if !strings.Contains(string(b), p.Version) || !strings.Contains(string(b), p.Commit) {
		t.Fatalf("alerts/gfexpr/PROVENANCE.md differs from Grafana source pin %s (%s)", p.Version, p.Commit)
	}
}

func TestEveryEngineImageHasADigest(t *testing.T) {
	for _, name := range []string{"VictoriaMetrics", "VictoriaLogs", "Tempo", "Grafana", "Pyroscope"} {
		d := All()[name].Digest
		if len(d) != len("sha256:")+64 || !strings.HasPrefix(d, "sha256:") {
			t.Fatalf("pins.json: %s digest %q is not sha256:<64 hex>", name, d)
		}
	}
}
