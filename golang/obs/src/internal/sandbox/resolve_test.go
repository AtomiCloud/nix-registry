package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/pins"
)

// A table with one extra, older Pyroscope image whose digest is known.
func knownWithOlderPyroscope() map[string]pins.Pin {
	k := pins.All()
	p := k["Pyroscope"]
	p.Also = []pins.Image{{Version: "2.4.2", Digest: "sha256:" + strings.Repeat("a", 64)}}
	k["Pyroscope"] = p
	return k
}

func TestResolveWithNothingNamedIsThePins(t *testing.T) {
	got, err := Resolve(nil, pins.All())
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range got {
		if c.Ref() != Pins[i].Ref() {
			t.Errorf("%s: %s, the pin is %s", c.Name, c.Ref(), Pins[i].Ref())
		}
	}
}

func TestResolveSpellingIsTolerantAndTheTagIsTheRegistrys(t *testing.T) {
	got, err := Resolve(map[string]string{"victoriametrics": "1.156.0", "pyroscope": "v2.4.3"}, pins.All())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.Name == "vm" && c.Tag != "v1.156.0" {
			t.Errorf("vm tag %q: docker tags are spelled as the pin spells them", c.Tag)
		}
		if c.Name == "pyroscope" && c.Tag != "2.4.3" {
			t.Errorf("pyroscope tag %q", c.Tag)
		}
	}
}

func TestResolveRunsAKnownOlderImageAndNamesItInTheCreate(t *testing.T) {
	got, err := Resolve(map[string]string{"pyroscope": "2.4.2"}, knownWithOlderPyroscope())
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Project: "demo", Components: got}
	cfg.defaults()
	var ref string
	for _, c := range cfg.comps() {
		if c.Name == "pyroscope" {
			ref = c.Ref()
			if a := strings.Join(cfg.createArgs(c), " "); !strings.HasSuffix(a, " grafana/pyroscope:2.4.2") && !strings.Contains(a, " grafana/pyroscope:2.4.2 ") {
				t.Errorf("create args do not run 2.4.2: %s", a)
			}
		}
	}
	if ref != "grafana/pyroscope:2.4.2" {
		t.Fatalf("resolved pyroscope ref %q", ref)
	}
	// The package-level pins are untouched.
	for _, c := range Pins {
		if c.Name == "pyroscope" && c.Tag != "2.4.3" {
			t.Errorf("Resolve changed the pins: %s", c.Ref())
		}
	}
}

func TestResolveRefusesAVersionWithNoKnownDigestAndNamesTheKey(t *testing.T) {
	for _, tc := range []struct{ key, version, image string }{
		{"pyroscope", "2.4.2", "grafana/pyroscope"},
		{"grafana", "13.2.0", "grafana/grafana"},
		{"victoriametrics", "v1.155.0", "victoriametrics/victoria-metrics"},
		{"victorialogs", "v1.53.0", "victoriametrics/victoria-logs"},
		{"tempo", "3.1.0", "grafana/tempo"},
	} {
		_, err := Resolve(map[string]string{tc.key: tc.version}, pins.All())
		if err == nil {
			t.Errorf("%s %s: accepted without a known digest", tc.key, tc.version)
			continue
		}
		for _, want := range []string{"versions." + tc.key, tc.version, "no known image digest", tc.image} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s %s: error lacks %q: %v", tc.key, tc.version, want, err)
			}
		}
	}
}

func TestResolveRefusesAPinWithoutADigest(t *testing.T) {
	k := pins.All()
	p := k["Tempo"]
	p.Digest = ""
	k["Tempo"] = p
	if _, err := Resolve(map[string]string{"tempo": "3.1.1"}, k); err == nil || !strings.Contains(err.Error(), "versions.tempo") {
		t.Fatalf("a version whose pin has no digest must be refused, got %v", err)
	}
}

// A refusal starts nothing: Up with resolved components runs only what Resolve returned,
// and checkExisting compares running containers with that stack, not with the pins.
func TestUpChecksRunningContainersAgainstTheResolvedStack(t *testing.T) {
	got, err := Resolve(map[string]string{"pyroscope": "2.4.2"}, knownWithOlderPyroscope())
	if err != nil {
		t.Fatal(err)
	}
	// 2.4.2 runs and 2.4.2 is wanted: no refusal. The pin (2.4.3) is not what is wanted.
	fr := &fakeRunner{ps: "obs-sandbox-demo-pyroscope|grafana/pyroscope:2.4.2|running\n"}
	_, err = Up(context.Background(), Config{Project: "demo", Dir: project(t), Runner: fr, Components: got})
	if err != nil && strings.Contains(err.Error(), "image tags differ from the pins") {
		t.Fatalf("a running image that matches the resolved stack was refused: %v", err)
	}
	// 2.4.3 runs but 2.4.2 is wanted: refused, nothing but ps ran.
	fr = &fakeRunner{ps: "obs-sandbox-demo-pyroscope|grafana/pyroscope:2.4.3|running\n"}
	_, err = Up(context.Background(), Config{Project: "demo", Dir: project(t), Runner: fr, Components: got})
	if err == nil || !strings.Contains(err.Error(), "refusing") || !strings.Contains(err.Error(), "2.4.2") {
		t.Fatalf("want a refusal naming the wanted tag, got %v", err)
	}
	for _, c := range fr.calls {
		if c[0] != "ps" {
			t.Errorf("a command ran after the refusal: %v", c)
		}
	}
}
