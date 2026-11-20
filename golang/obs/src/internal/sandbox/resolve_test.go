package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

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

// A refusal starts nothing: checkExisting compares the running containers with the stack
// Resolve returned (not with the pins) and is the first thing Up does after listing them.
// It is tested directly, so the test never starts a container, never waits on a port and
// never meets a service on the machine that runs it.
func TestCheckExistingComparesRunningContainersWithTheResolvedStack(t *testing.T) {
	got, err := Resolve(map[string]string{"pyroscope": "2.4.2"}, knownWithOlderPyroscope())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Project: "demo", Components: got}
	name := containerName("demo", Component{Name: "pyroscope"})
	// 2.4.2 runs and 2.4.2 is wanted: accepted. The pin (2.4.3) is not what is wanted.
	if err := checkExisting(cfg, map[string]existing{name: {Image: "grafana/pyroscope:2.4.2", State: "running"}}); err != nil {
		t.Fatalf("a running image that matches the resolved stack was refused: %v", err)
	}
	// docker.io/ in front is how podman reports the same image.
	if err := checkExisting(cfg, map[string]existing{name: {Image: "docker.io/grafana/pyroscope:2.4.2", State: "exited"}}); err != nil {
		t.Fatalf("docker.io/ spelling was refused: %v", err)
	}
	// 2.4.3 runs but 2.4.2 is wanted: refused, naming the container, the running image and the wanted tag.
	err = checkExisting(cfg, map[string]existing{name: {Image: "grafana/pyroscope:2.4.3", State: "running"}})
	if err == nil {
		t.Fatal("2.4.3 running while 2.4.2 is wanted was accepted")
	}
	for _, want := range []string{"refusing", name, "grafana/pyroscope:2.4.3", "grafana/pyroscope:2.4.2", "obs sandbox down"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
	// No container yet: nothing to refuse.
	if err := checkExisting(cfg, nil); err != nil {
		t.Fatalf("an empty sandbox: %v", err)
	}
}

// Up reaches checkExisting before it runs anything but `ps`: the refusal starts nothing.
// The Runner is a fake and the whole test is bounded, so nothing can wait on a
// service of the machine running the test.
func TestUpRefusesBeforeStartingAnything(t *testing.T) {
	got, err := Resolve(map[string]string{"pyroscope": "2.4.2"}, knownWithOlderPyroscope())
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{ps: "obs-sandbox-demo-pyroscope|grafana/pyroscope:2.4.3|running\n"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	started := time.Now()
	_, err = Up(ctx, Config{Project: "demo", Dir: project(t), Runner: fr, Components: got, StateDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "refusing") || !strings.Contains(err.Error(), "2.4.2") {
		t.Fatalf("want a refusal naming the wanted tag, got %v", err)
	}
	if time.Since(started) > 10*time.Second {
		t.Errorf("a refusal waited %s: it must return before any start-up wait", time.Since(started))
	}
	if len(fr.calls) != 1 || fr.calls[0][0] != "ps" {
		t.Errorf("only ps may run before the refusal: %v", fr.calls)
	}
}

// An Also entry counts only with a valid sha256 digest: absent or malformed is refused by
// name, with the reason, never run.
func TestResolveRefusesAnAlsoImageWithoutAValidDigest(t *testing.T) {
	for name, digest := range map[string]string{
		"absent":     "",
		"short":      "sha256:abc",
		"no prefix":  strings.Repeat("a", 64),
		"upper case": "sha256:" + strings.Repeat("A", 64),
		"other hash": "sha1:" + strings.Repeat("a", 40),
		"padded":     " sha256:" + strings.Repeat("a", 64),
	} {
		k := pins.All()
		p := k["Pyroscope"]
		p.Also = []pins.Image{{Version: "2.4.2", Digest: digest}}
		k["Pyroscope"] = p
		_, err := Resolve(map[string]string{"pyroscope": "2.4.2"}, k)
		if err == nil {
			t.Errorf("%s: an image with digest %q was accepted", name, digest)
			continue
		}
		for _, want := range []string{"versions.pyroscope", "2.4.2", "digest", "sha256:"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error lacks %q: %v", name, want, err)
			}
		}
	}
	// The same entry with a valid digest runs.
	if _, err := Resolve(map[string]string{"pyroscope": "2.4.2"}, knownWithOlderPyroscope()); err != nil {
		t.Fatalf("valid digest: %v", err)
	}
}
