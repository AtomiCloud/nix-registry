package live

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/AtomiCloud/obs/internal/sandbox"
	"gopkg.in/yaml.v3"
)

// The recorded fixture's obs.yaml says where its local sandbox listens: `obs look --port-base
// 24000` puts every component at base + its offset (internal/sandbox pins), so the addresses
// must be those, not the stores' own ports with a prefix.
func TestRecordedFixtureTargetsMatchTheSandboxPortLayout(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(liveFixtureDir, "obs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Targets map[string]map[string]string `yaml:"targets"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	const base = 24000
	for key, comp := range map[string]string{"metrics": "vm", "logs": "vl", "traces": "tempo", "grafana": "grafana"} {
		c, ok := sandbox.ByName(comp)
		if !ok {
			t.Fatalf("no sandbox component %q", comp)
		}
		want := fmt.Sprintf("http://127.0.0.1:%d", base+c.Offset)
		if got := doc.Targets["local"][key]; got != want {
			t.Errorf("targets.local.%s = %q, want %q (port base %d + offset %d of %s)", key, got, want, base, c.Offset, comp)
		}
	}
}
