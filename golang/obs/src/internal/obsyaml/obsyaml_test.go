package obsyaml

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/pins"
	"github.com/AtomiCloud/obs/internal/profile"
)

const matching = `versions:
  grafana: 13.3.0
  victoriametrics: v1.156.0
  victorialogs: v1.54.0
  tempo: 3.1.1
  alloy: v1.21.3
  pyroscope: 2.4.3
  dashboard_schema: 42
`

func mustParse(t *testing.T, doc string) *File {
	t.Helper()
	f, err := Parse("observability/obs.yaml", []byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

func TestMatchingVersionsHaveNoMismatch(t *testing.T) {
	f := mustParse(t, matching)
	if ms := f.Mismatches(); len(ms) != 0 {
		t.Fatalf("matching obs.yaml: %v", ms)
	}
	if n := f.Notes(); len(n) != 0 {
		t.Fatalf("matching obs.yaml: notes %v", n)
	}
	// "v" or not is one release.
	g := mustParse(t, "versions:\n  victoriametrics: 1.156.0\n  tempo: v3.1.1\n")
	if ms := g.Mismatches(); len(ms) != 0 {
		t.Fatalf("spelling v1.156.0 as 1.156.0: %v", ms)
	}
}

func TestEachMismatchedVersionIsOneMismatchNamingKeyWantedAndEmbedded(t *testing.T) {
	for _, tc := range []struct{ key, line, want, embedded string }{
		{"versions.victoriametrics", "victoriametrics: v1.157.0", "v1.157.0", "v1.156.0"},
		{"versions.victorialogs", "victorialogs: v1.53.0", "v1.53.0", "v1.54.0"},
		{"versions.tempo", "tempo: 3.2.0", "3.2.0", "3.1.1"},
		{"versions.grafana", "grafana: 13.4.0", "13.4.0", "13.3.0"},
		{"versions.dashboard_schema", "dashboard_schema: 43", "43", "42"},
	} {
		f := mustParse(t, "versions:\n  "+tc.line+"\n")
		ms := f.Mismatches()
		if len(ms) != 1 {
			t.Errorf("%s: %d mismatches: %v", tc.key, len(ms), ms)
			continue
		}
		s := ms[0].String()
		for _, want := range []string{tc.key, "wants " + tc.want, "embeds " + tc.embedded} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: %q lacks %q", tc.key, s, want)
			}
		}
		var out bytes.Buffer
		if n := f.Gate(&out, "obs query"); n != 1 || !strings.Contains(out.String(), "FAIL observability/obs.yaml "+tc.key) {
			t.Errorf("%s: gate %d %q", tc.key, n, out.String())
		}
	}
}

func TestAlloyAndPyroscopeDifferencesAreNotesNotFailures(t *testing.T) {
	f := mustParse(t, "versions:\n  alloy: v1.22.0\n  pyroscope: 2.5.0\n")
	if len(f.Mismatches()) != 0 {
		t.Fatalf("alloy and pyroscope have no embedded evaluator: %v", f.Mismatches())
	}
	n := strings.Join(f.Notes(), "\n")
	for _, want := range []string{"versions.alloy", "v1.22.0", "v1.21.3", "versions.pyroscope", "2.5.0", "2.4.3"} {
		if !strings.Contains(n, want) {
			t.Errorf("notes lack %q: %s", want, n)
		}
	}
}

func TestUnknownKeyIsAnErrorNamingFileLineAndKey(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{"versions:\n  tempoo: 3.1.1\n", "observability/obs.yaml:2: unknown key versions.tempoo"},
		{"versions:\n  tempo: 3.1.1\nthresholds:\n  logs:\n    max_query_lenn: 1\n", "observability/obs.yaml:5: unknown key thresholds.logs.max_query_lenn"},
		{"threshold: {}\n", "observability/obs.yaml:1: unknown key threshold"},
		{"thresholds:\n  disk:\n    x: 1\n", "unknown key thresholds.disk"},
	} {
		_, err := Parse("observability/obs.yaml", []byte(tc.doc))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: want error containing %q, got %v", tc.doc, tc.want, err)
		}
	}
}

func TestBadValuesAreErrorsNamingTheKey(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{"versions:\n  tempo: latest\n", "versions.tempo"},
		{"versions:\n  grafana: 13.3\n", "versions.grafana"},
		{"versions:\n  dashboard_schema: forty\n", "dashboard_schema"},
		{"versions:\n  dashboard_schema: 0\n", "versions.dashboard_schema: 0"},
		{"thresholds:\n  logs:\n    max_query_duration: soon\n", "thresholds.logs.max_query_duration"},
		{"thresholds:\n  logs:\n    max_query_len: \"16 KiB\"\n", "thresholds.logs.max_query_len"},
		{"thresholds:\n  logs:\n    max_concurrent_requests: -1\n", "thresholds.logs.max_concurrent_requests"},
		{"thresholds:\n  metrics:\n    max_points_per_series: 0\n", "thresholds.metrics.max_points_per_series"},
		{"thresholds:\n  traces:\n    query_end_cutoff: nope\n", "thresholds.traces.query_end_cutoff"},
		{"thresholds:\n  traces:\n    metrics_max_intervals: -4\n", "thresholds.traces.metrics_max_intervals"},
		{"thresholds:\n  profiles:\n    tolerance_total: 15\n", "thresholds.profiles.tolerance_total"},
		{"versions: 5\n", "versions"},
		{"- a\n- b\n", "want a mapping"},
		{"versions: {}\n---\nversions: {}\n", "exactly one YAML document"},
	} {
		_, err := Parse("observability/obs.yaml", []byte(tc.doc))
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "observability/obs.yaml") {
			t.Errorf("%q: want error naming the file and %q, got %v", tc.doc, tc.want, err)
		}
	}
}

func TestEmptyAndMissingFilesNameNothing(t *testing.T) {
	f := mustParse(t, "# nothing yet\n")
	if f.Logs != nil || f.Frontend != nil || f.MaxPoints != 0 || len(f.Mismatches()) != 0 || len(f.Applied()) != 0 {
		t.Fatalf("an empty file must name nothing: %+v", f)
	}
	got, err := Load(t.TempDir())
	if got != nil || err != nil {
		t.Fatalf("a missing file is (nil, nil), got %v %v", got, err)
	}
	var nilFile *File
	if nilFile.Mismatches() != nil || nilFile.Notes() != nil || nilFile.Applied() != nil || nilFile.MetricsMaxPoints() != 1_000_000 {
		t.Fatal("a nil File must behave as no file")
	}
}

func TestLoadReadsTheFileInTheFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(matching), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(dir)
	if err != nil || f == nil || f.Path != filepath.Join(dir, FileName) || f.Versions.Tempo != "3.1.1" {
		t.Fatalf("Load: %+v %v", f, err)
	}
}

func TestLogsThresholdsLayerUnderTheSettingsFile(t *testing.T) {
	f := mustParse(t, "thresholds:\n  logs:\n    max_query_len: 100\n    max_query_duration: 5s\n")
	dir := t.TempDir()
	// No settings file: obs.yaml over the pinned defaults, and the note says so.
	l, note, err := f.VLLimits(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if l.MaxQueryLen != 100 || l.MaxQueryDuration != 5*time.Second {
		t.Fatalf("obs.yaml limits not applied: %+v", l)
	}
	if l.MaxQueueDuration != 10*time.Second {
		t.Fatalf("a key obs.yaml does not name keeps the pinned default: %+v", l)
	}
	if !strings.Contains(note, "obs.yaml thresholds.logs") {
		t.Fatalf("note: %q", note)
	}
	// A settings file overrides obs.yaml key by key.
	settings := filepath.Join(dir, "victorialogs-settings.yaml")
	if err := os.WriteFile(settings, []byte("- -search.maxQueryLen=500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, note, err = f.VLLimits(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if l.MaxQueryLen != 500 || l.MaxQueryDuration != 5*time.Second || note != "" {
		t.Fatalf("settings file must override only the keys it names: %+v note=%q", l, note)
	}
	// --vl-settings (explicit path) overrides too.
	other := filepath.Join(dir, "other.yaml")
	if err := os.WriteFile(other, []byte("search.maxQueryDuration: 7s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, _, err = f.VLLimits(dir, other)
	if err != nil || l.MaxQueryLen != 100 || l.MaxQueryDuration != 7*time.Second {
		t.Fatalf("--vl-settings: %+v %v", l, err)
	}
	// No obs.yaml: exactly the old behaviour.
	var none *File
	l, note, err = none.VLLimits(t.TempDir(), "")
	if err != nil || l.MaxQueryLen != 16*1024 || !strings.Contains(note, "VictoriaLogs v1.54.0 query defaults") {
		t.Fatalf("nil file: %+v %q %v", l, note, err)
	}
}

func TestTracesThresholdsLayerUnderTempoConfig(t *testing.T) {
	f := mustParse(t, "thresholds:\n  traces:\n    query_end_cutoff: 1m\n    metrics_max_intervals: 500\n")
	fe, err := f.TempoFrontend("")
	if err != nil || fe == nil || fe.QueryEndCutoff != time.Minute || fe.MaxIntervals != 500 || fe.MaxDuration != 24*time.Hour {
		t.Fatalf("obs.yaml traces: %+v %v", fe, err)
	}
	cfg := filepath.Join(t.TempDir(), "tempo.yaml")
	if err := os.WriteFile(cfg, []byte("query_frontend:\n  query_end_cutoff: 2m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fe, err = f.TempoFrontend(cfg)
	if err != nil || fe.QueryEndCutoff != 2*time.Minute || fe.MaxIntervals != 500 {
		t.Fatalf("--tempo-config must override only the keys it names: %+v %v", fe, err)
	}
	if _, err := f.TempoFrontend(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "--tempo-config") {
		t.Fatalf("missing --tempo-config: %v", err)
	}
	// Nothing named anywhere: nil, the engine keeps Tempo's defaults.
	var none *File
	if fe, err := none.TempoFrontend(""); fe != nil || err != nil {
		t.Fatalf("no obs.yaml, no config: %v %v", fe, err)
	}
	if fe, err := mustParse(t, matching).TempoFrontend(""); fe != nil || err != nil {
		t.Fatalf("versions only names no traces threshold: %v %v", fe, err)
	}
}

func TestOtherThresholds(t *testing.T) {
	f := mustParse(t, "thresholds:\n  metrics:\n    max_points_per_series: 5000\n  profiles:\n    tolerance_function: 0.1\n    tolerance_dotnet_trace: 0.4\n")
	if f.MetricsMaxPoints() != 5000 {
		t.Errorf("max points %d", f.MetricsMaxPoints())
	}
	if f.Profiles != (profile.Tolerances{Function: 0.1, DotnetTrace: 0.4}) {
		t.Errorf("profiles %+v", f.Profiles)
	}
	if got := strings.Join(f.Applied(), ","); got != "metrics,profiles" {
		t.Errorf("applied %q", got)
	}
}

func TestPrintEmbeddedNamesEveryEvaluatorAndPin(t *testing.T) {
	var out bytes.Buffer
	PrintEmbedded(&out)
	p := pins.All()
	for _, want := range []string{
		"MetricsQL", "VictoriaMetrics " + p["VictoriaMetrics"].Version,
		"LogsQL", "VictoriaLogs " + p["VictoriaLogs"].Version,
		"TraceQL", "Tempo " + p["Tempo"].Version,
		"Grafana alert engine", "Grafana " + p["Grafana"].Version,
		"dashboard schema", "schemaVersion 42",
		"Alloy " + p["Alloy"].Version, "Pyroscope " + p["Pyroscope"].Version,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("embedded versions lack %q:\n%s", want, out.String())
		}
	}
}

// DashboardSchema was read from Grafana's source at one tag. When the pin moves, the person
// bumping it must read the schemaVersion again and change both numbers together.
func TestDashboardSchemaMatchesGrafanaPin(t *testing.T) {
	if got := pins.All()["Grafana"].Version; got != grafanaForDashboardSchema {
		t.Fatalf("Grafana pin is %s but DashboardSchema=%d was read from Grafana %s: read DASHBOARD_SCHEMA_VERSION in public/app/features/dashboard/state/DashboardMigrator.ts at the new tag and update obsyaml.DashboardSchema and grafanaForDashboardSchema",
			got, DashboardSchema, grafanaForDashboardSchema)
	}
}

func TestEveryPinnedEngineAndReferenceIsListed(t *testing.T) {
	p := pins.All()
	for _, name := range []string{"Alloy", "Pyroscope"} {
		if p[name].Version == "" || !strings.HasPrefix(p[name].Digest, "sha256:") {
			t.Errorf("pins.json: %s needs a version and a digest", name)
		}
	}
	for _, e := range Embedded() {
		if e.Embedded == "" {
			t.Errorf("%s has no embedded version", e.Role)
		}
	}
}
