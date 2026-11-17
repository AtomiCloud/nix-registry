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

func TestEngineDependencyWithoutTagIsANote(t *testing.T) {
	d := t.TempDir()
	write(t, d, "Chart.yaml", "apiVersion: v2\nname: o\nversion: 1.0.0\ndependencies:\n  - name: grafana\n    version: 9.0.0\n")
	write(t, d, "values.yaml", "grafana:\n  replicas: 2\n")
	res, err := Check(d, pins.All())
	if err != nil || len(res.Findings) != 0 || len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "Grafana") {
		t.Fatalf("want a note naming Grafana, no drift: %v %+v", err, res)
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
