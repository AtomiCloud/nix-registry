package chartcheck

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/pins"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// chart writes an umbrella chart whose image tags are the given versions.
func chart(t *testing.T, vm, vl, tempo, grafana, pyro string) string {
	t.Helper()
	d := t.TempDir()
	write(t, d, "Chart.yaml", `apiVersion: v2
name: observability
version: 0.1.0
dependencies:
  - name: victoria-metrics-cluster
    version: 0.99.0
    repository: https://victoriametrics.github.io/helm-charts
  - name: victoria-logs-single
    version: 0.50.0
    repository: https://victoriametrics.github.io/helm-charts
  - name: tempo
    version: 1.30.0
    repository: https://grafana.github.io/helm-charts
  - name: grafana
    version: 9.9.9
    repository: https://grafana.github.io/helm-charts
`)
	body := `victoria-metrics-cluster:
  vmstorage:
    image:
      repository: victoriametrics/vmstorage
      tag: ` + vm + `-cluster
  vmselect:
    image:
      repository: victoriametrics/vmselect
      tag: ` + vm + `-cluster
victoria-logs-single:
  server:
    image:
      repository: victoriametrics/victoria-logs
      tag: ` + vl + `
tempo:
  tempo:
    repository: grafana/tempo
    tag: "` + tempo + `"
grafana:
  image:
    repository: grafana/grafana
    tag: ` + grafana + `
`
	if pyro != "" {
		body += "pyroscope:\n  image: grafana/pyroscope:" + pyro + "\n"
	}
	write(t, d, "values.yaml", body)
	return d
}

func current() (vm, vl, tempo, grafana, pyro string) {
	p := pins.All()
	return p["VictoriaMetrics"].Version, p["VictoriaLogs"].Version, p["Tempo"].Version, p["Grafana"].Version, p["Pyroscope"].Version
}

func TestMatchingChartHasNoFinding(t *testing.T) {
	vm, vl, tempo, g, py := current()
	res, err := Check(chart(t, vm, vl, tempo, g, py), pins.All())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("matching chart drifted: %+v", res.Findings)
	}
	if len(res.Matches) < 5 {
		t.Fatalf("want every engine matched, got %v", res.Matches)
	}
}

func TestVPrefixIsNotDrift(t *testing.T) {
	vm, vl, tempo, g, py := current()
	res, err := Check(chart(t, vm, strings.TrimPrefix(vl, "v"), "v"+tempo, g, py), pins.All())
	if err != nil || len(res.Findings) != 0 {
		t.Fatalf("a leading v is not drift: %v %+v", err, res.Findings)
	}
}

func TestEachEngineDriftIsNamed(t *testing.T) {
	vm, vl, tempo, g, py := current()
	cases := []struct {
		name, engine, key string
		args              [5]string
	}{
		{"metrics", "VictoriaMetrics", "victoria-metrics-cluster.vmstorage.image.tag", [5]string{"v1.0.0", vl, tempo, g, py}},
		{"logs", "VictoriaLogs", "victoria-logs-single.server.image.tag", [5]string{vm, "v0.0.1", tempo, g, py}},
		{"tempo", "Tempo", "tempo.tempo.tag", [5]string{vm, vl, "2.0.0", g, py}},
		{"grafana", "Grafana", "grafana.image.tag", [5]string{vm, vl, tempo, "12.0.0", py}},
		{"pyroscope", "Pyroscope", "pyroscope.image", [5]string{vm, vl, tempo, g, "1.0.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Check(chart(t, c.args[0], c.args[1], c.args[2], c.args[3], c.args[4]), pins.All())
			if err != nil {
				t.Fatal(err)
			}
			var hit *Finding
			for i := range res.Findings {
				if hit == nil && res.Findings[i].Engine == c.engine {
					hit = &res.Findings[i]
				}
			}
			if hit == nil {
				t.Fatalf("no finding for %s: %+v", c.engine, res.Findings)
			}
			if hit.File != "values.yaml" || hit.Key != c.key || hit.Line == 0 || hit.PinValue != pins.All()[c.engine].Version {
				t.Fatalf("finding does not name file, key, line and pin: %+v", *hit)
			}
		})
	}
}

func TestChartAndDependencyVersionsAreNotAppVersions(t *testing.T) {
	// Chart.yaml dependency versions are chart versions, never compared with an engine pin.
	vm, vl, tempo, g, py := current()
	res, err := Check(chart(t, vm, vl, tempo, g, py), pins.All())
	if err != nil || len(res.Findings) != 0 {
		t.Fatalf("dependency version 9.9.9 must not drift: %v %+v", err, res.Findings)
	}
}

func TestAppVersionOfAnEngineChartIsCompared(t *testing.T) {
	d := t.TempDir()
	write(t, d, "Chart.yaml", "apiVersion: v2\nname: tempo\nversion: 1.0.0\nappVersion: 2.0.0\n")
	res, err := Check(d, pins.All())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "Chart.yaml" || res.Findings[0].Key != "appVersion" || res.Findings[0].ChartValue != "2.0.0" {
		t.Fatalf("appVersion drift not named: %+v", res.Findings)
	}
}

func TestNoEngineIsAnInputError(t *testing.T) {
	d := t.TempDir()
	write(t, d, "Chart.yaml", "apiVersion: v2\nname: web\nversion: 1.0.0\ndependencies:\n  - name: redis\n    version: 1.0.0\n")
	write(t, d, "values.yaml", "image:\n  repository: nginx\n  tag: 1.2.3\n")
	if _, err := Check(d, pins.All()); !errors.Is(err, ErrNoEngines) {
		t.Fatalf("want ErrNoEngines, got %v", err)
	}
	if _, err := Check(t.TempDir(), pins.All()); !errors.Is(err, ErrNoEngines) {
		t.Fatalf("empty dir: want ErrNoEngines, got %v", err)
	}
	if _, err := Check(filepath.Join(d, "missing"), pins.All()); err == nil || errors.Is(err, ErrNoEngines) {
		t.Fatalf("missing dir is a different input error, got %v", err)
	}
}

func TestUnrelatedImageIsIgnored(t *testing.T) {
	vm, vl, tempo, g, py := current()
	d := chart(t, vm, vl, tempo, g, py)
	b, _ := os.ReadFile(filepath.Join(d, "values.yaml"))
	write(t, d, "values.yaml", string(b)+"renderer:\n  image:\n    repository: grafana/grafana-image-renderer\n    tag: 9.9.9\n")
	res, err := Check(d, pins.All())
	if err != nil || len(res.Findings) != 0 {
		t.Fatalf("grafana-image-renderer is not Grafana: %v %+v", err, res.Findings)
	}
}

const otherDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// digestChart is a chart whose only engine is Pyroscope, referenced as values.yaml says.
func digestChart(t *testing.T, values string) string {
	t.Helper()
	d := t.TempDir()
	write(t, d, "Chart.yaml", "apiVersion: v2\nname: o\nversion: 1.0.0\n")
	write(t, d, "values.yaml", values)
	return d
}

func TestDigestOnlyImageEqualToPinnedDigestIsOK(t *testing.T) {
	p := pins.All()["Pyroscope"]
	for name, values := range map[string]string{
		"string": "pyroscope:\n  image: grafana/pyroscope@" + p.Digest + "\n",
		"map":    "pyroscope:\n  image:\n    repository: grafana/pyroscope\n    digest: " + p.Digest + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			res, err := Check(digestChart(t, values), pins.All())
			if err != nil || len(res.Findings) != 0 || len(res.Matches) != 1 || !strings.Contains(res.Matches[0], p.Digest) {
				t.Fatalf("pinned digest must pass and be listed: %v %+v", err, res)
			}
		})
	}
}

func TestDigestOnlyImageDifferentFromPinnedDigestIsRed(t *testing.T) {
	p := pins.All()["Pyroscope"]
	for name, c := range map[string]struct{ values, key string }{
		"string": {"pyroscope:\n  image: grafana/pyroscope@" + otherDigest + "\n", "pyroscope.image"},
		"map":    {"pyroscope:\n  image:\n    repository: grafana/pyroscope\n    digest: " + otherDigest + "\n", "pyroscope.image.digest"},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := Check(digestChart(t, c.values), pins.All())
			if err != nil || len(res.Findings) != 1 {
				t.Fatalf("want one finding: %v %+v", err, res)
			}
			f := res.Findings[0]
			if f.File != "values.yaml" || f.Key != c.key || f.Line == 0 || f.ChartValue != otherDigest || f.PinValue != p.Digest {
				t.Fatalf("finding must name file, key, digest and pin: %+v", f)
			}
			if s := f.String(); !strings.Contains(s, otherDigest) || !strings.Contains(s, p.Digest) || !strings.Contains(s, "Pyroscope") {
				t.Fatalf("message lacks digest or pin: %s", s)
			}
		})
	}
}

func TestDigestOnlyImageWithNoRecordedDigestFails(t *testing.T) {
	pin := pins.All()
	py := pin["Pyroscope"]
	py.Digest = ""
	pin["Pyroscope"] = py
	res, err := Check(digestChart(t, "pyroscope:\n  image: grafana/pyroscope@"+otherDigest+"\n"), pin)
	if err != nil || len(res.Findings) != 1 {
		t.Fatalf("a digest-only image with no recorded digest must fail, never pass: %v %+v", err, res)
	}
	s := res.Findings[0].String()
	for _, want := range []string{"values.yaml:", "pyroscope.image", otherDigest, "pinned by digest only; cannot prove version " + py.Version} {
		if !strings.Contains(s, want) {
			t.Fatalf("message lacks %q: %s", want, s)
		}
	}
}

func TestTagWithDigestComparesBoth(t *testing.T) {
	p := pins.All()["Pyroscope"]
	// Tag right, digest wrong: the runtime pulls the digest, so this is drift.
	res, err := Check(digestChart(t, "pyroscope:\n  image: grafana/pyroscope:"+p.Version+"@"+otherDigest+"\n"), pins.All())
	if err != nil || len(res.Findings) != 1 || res.Findings[0].ChartValue != otherDigest {
		t.Fatalf("wrong digest behind the right tag: %v %+v", err, res)
	}
	// Tag wrong, digest right: still drift on the tag.
	res, err = Check(digestChart(t, "pyroscope:\n  image:\n    repository: grafana/pyroscope\n    tag: 1.0.0\n    digest: "+p.Digest+"\n"), pins.All())
	if err != nil || len(res.Findings) != 1 || res.Findings[0].ChartValue != "1.0.0" {
		t.Fatalf("wrong tag beside the right digest: %v %+v", err, res)
	}
	// Both right.
	res, err = Check(digestChart(t, "pyroscope:\n  image: grafana/pyroscope:"+p.Version+"@"+p.Digest+"\n"), pins.All())
	if err != nil || len(res.Findings) != 0 {
		t.Fatalf("tag and digest both right: %v %+v", err, res)
	}
}

func TestDigestOnlyEngineIsPresentNotIgnored(t *testing.T) {
	// A chart whose only engine is digest-pinned is a chart with an engine (not exit 2).
	if _, err := Check(digestChart(t, "pyroscope:\n  image: grafana/pyroscope@"+otherDigest+"\n"), pins.All()); errors.Is(err, ErrNoEngines) {
		t.Fatal("digest-only engine image was not recognised")
	}
}

// ---- round 2: the common chart shapes ----------------------------------------------------

func depChart(t *testing.T, deps, values string) string {
	t.Helper()
	d := t.TempDir()
	write(t, d, "Chart.yaml", "apiVersion: v2\nname: o\nversion: 1.0.0\ndependencies:\n"+deps)
	write(t, d, "values.yaml", values)
	return d
}

func TestTagWithNoRepositoryBesideItIsTiedToItsDependency(t *testing.T) {
	// The common upstream shape: victoria-logs-single.server.image.tag, no repository.
	p := pins.All()["VictoriaLogs"]
	dep := "  - name: victoria-logs-single\n    version: 0.50.0\n"
	res, err := Check(depChart(t, dep, "victoria-logs-single:\n  server:\n    image:\n      tag: v1.50.0\n"), pins.All())
	if err != nil || len(res.Findings) != 1 {
		t.Fatalf("want one drift: %v %+v", err, res)
	}
	f := res.Findings[0]
	if f.Engine != "VictoriaLogs" || f.Key != "victoria-logs-single.server.image.tag" || f.ChartValue != "v1.50.0" || f.PinValue != p.Version {
		t.Fatalf("drift not named: %+v", f)
	}
	res, err = Check(depChart(t, dep, "victoria-logs-single:\n  server:\n    image:\n      tag: "+p.Version+"\n"), pins.All())
	if err != nil || len(res.Findings) != 0 || len(res.Matches) != 1 {
		t.Fatalf("matching tag: %v %+v", err, res)
	}
}

func TestDependencyAliasKeysTheValues(t *testing.T) {
	dep := "  - name: grafana\n    alias: dashboards\n    version: 9.0.0\n"
	res, err := Check(depChart(t, dep, "dashboards:\n  image:\n    tag: 12.0.0\n"), pins.All())
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Engine != "Grafana" || res.Findings[0].Key != "dashboards.image.tag" {
		t.Fatalf("alias values must belong to Grafana: %v %+v", err, res)
	}
}

func TestTagWithNoRepositoryOutsideAnEngineDependencyIsIgnored(t *testing.T) {
	dep := "  - name: grafana\n    version: 9.0.0\n  - name: redis\n    version: 1.0.0\n"
	g := pins.All()["Grafana"].Version
	res, err := Check(depChart(t, dep, "grafana:\n  image:\n    tag: "+g+"\nredis:\n  image:\n    tag: 7.0.0\n"), pins.All())
	if err != nil || len(res.Findings) != 0 {
		t.Fatalf("redis tag is not an engine: %v %+v", err, res)
	}
}

func TestUnrelatedImageInsideAnEngineDependencyIsIgnored(t *testing.T) {
	dep := "  - name: grafana\n    version: 9.0.0\n"
	g := pins.All()["Grafana"].Version
	values := "grafana:\n  image:\n    tag: " + g + "\n  sidecar:\n    image:\n      repository: quay.io/kiwigrid/k8s-sidecar\n      tag: 1.30.0\n"
	res, err := Check(depChart(t, dep, values), pins.All())
	if err != nil || len(res.Findings) != 0 {
		t.Fatalf("a sidecar image with its own repository is not Grafana: %v %+v", err, res)
	}
}

func TestEnginePresentButVersionNotFoundIsRed(t *testing.T) {
	cases := map[string]struct{ deps, values, key string }{
		"dependency with no image at all": {"  - name: grafana\n    version: 9.0.0\n", "grafana:\n  replicas: 2\n", "grafana"},
		"empty tag (chart default)":       {"  - name: grafana\n    version: 9.0.0\n", "grafana:\n  image:\n    tag: \"\"\n", "grafana.image"},
		"no tag key (chart default)":      {"  - name: grafana\n    version: 9.0.0\n", "grafana:\n  image:\n    repository: grafana/grafana\n", "grafana.image"},
		"image string with no tag":        {"  - name: grafana\n    version: 9.0.0\n", "grafana:\n  image: grafana/grafana\n", "grafana.image"},
		"no values.yaml":                  {"  - name: tempo\n    version: 1.0.0\n", "", "tempo"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := depChart(t, c.deps, c.values)
			if c.values == "" {
				os.Remove(filepath.Join(d, "values.yaml"))
			}
			res, err := Check(d, pins.All())
			if err != nil || len(res.Findings) != 1 {
				t.Fatalf("want one finding, not a note: %v %+v", err, res)
			}
			f := res.Findings[0]
			s := f.String()
			if f.Kind != KindMissing || f.Key != c.key || f.Line == 0 || !strings.Contains(s, "cannot prove version "+pins.All()[f.Engine].Version) || !strings.Contains(s, "[engine-pin-missing]") {
				t.Fatalf("message must name file, key and pin: %+v %s", f, s)
			}
		})
	}
}

func TestEngineChartOwnNameWithoutAppVersionIsRed(t *testing.T) {
	d := t.TempDir()
	write(t, d, "Chart.yaml", "apiVersion: v2\nname: tempo\nversion: 1.0.0\n")
	res, err := Check(d, pins.All())
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Kind != KindMissing || res.Findings[0].File != "Chart.yaml" {
		t.Fatalf("engine chart with no appVersion and no image: %v %+v", err, res)
	}
}

// ---- round 2: image string parsing ---------------------------------------------------------

func TestImageStringWithRegistryPort(t *testing.T) {
	p := pins.All()["Pyroscope"]
	res, err := Check(digestChart(t, "pyroscope:\n  image: myreg.io:5000/grafana/pyroscope:2.4.0\n"), pins.All())
	if err != nil || len(res.Findings) != 1 || res.Findings[0].ChartValue != "2.4.0" || res.Findings[0].PinValue != p.Version {
		t.Fatalf("tag must be cut after the last slash (not at the port): %v %+v", err, res)
	}
	res, err = Check(digestChart(t, "pyroscope:\n  image: myreg.io:5000/grafana/pyroscope:"+p.Version+"\n"), pins.All())
	if err != nil || len(res.Findings) != 0 || len(res.Matches) != 1 {
		t.Fatalf("matching tag behind a port registry: %v %+v", err, res)
	}
	// A registry with a port and no tag has no tag: it must not read the port as one.
	res, err = Check(digestChart(t, "pyroscope:\n  image: myreg.io:5000/grafana/pyroscope\n"), pins.All())
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Kind != KindMissing {
		t.Fatalf("no tag behind a port registry is a missing version: %v %+v", err, res)
	}
}

func TestTagAndDigestStringIsNotDriftWhenBothMatch(t *testing.T) {
	p := pins.All()["Pyroscope"]
	res, err := Check(digestChart(t, "pyroscope:\n  image: myreg.io:5000/grafana/pyroscope:"+p.Version+"@"+p.Digest+"\n"), pins.All())
	if err != nil || len(res.Findings) != 0 || len(res.Matches) != 2 {
		t.Fatalf("repo:tag@digest equal to both pins: %v %+v", err, res)
	}
}
