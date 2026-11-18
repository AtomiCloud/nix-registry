package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testSeed = `length: 30m
random_seed: 7
resource:
  durian.cloud.landscape: fixture-dev
  durian.cloud.platform: payments
  durian.cloud.service: checkout
  durian.cloud.module: api
traffic: { requests: 2/s, shape: flat }
metrics:
  checkout.charges:
    type: counter
    series:
      - { where: { outcome: success }, share: 0.9 }
      - { where: { outcome: error }, share: 0.1 }
logs:
  - { event: checkout.charge.failed, level: error, line: charge failed, from: { metric: checkout.charges, where: { outcome: error } } }
`

const testDash = `{"uid":"payments-overview","title":"Payments overview","time":{"from":"now-3h","to":"now"},
"templating":{"list":[{"type":"datasource","name":"ds","query":"prometheus"}]},
"panels":[{"id":1,"title":"Charges","type":"timeseries","datasource":{"type":"prometheus","uid":"payments-metrics"},
"targets":[{"refId":"A","expr":"sum by (outcome) (rate(checkout_charges_total[5m]))"}]},
{"id":2,"title":"Errors","type":"logs","datasource":{"type":"victoriametrics-logs-datasource","uid":"payments-logs"},
"targets":[{"refId":"A","expr":"_time:5m error | stats count()"}]}]}`

// project writes a small observability folder and returns its path.
func project(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "observability")
	if err := os.MkdirAll(filepath.Join(dir, "dashboards"), 0o755); err != nil {
		t.Fatal(err)
	}
	for f, c := range map[string]string{"seed.yaml": testSeed, "dashboards/overview.json": testDash} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	ps    string
	// networkExists makes `network inspect` succeed.
	networkExists bool
	lists         map[string]string // first args ("network ls") -> output
	// imageVolumes is what `image inspect --format ...Volumes` answers (default: none declared).
	imageVolumes string
}

func (f *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	switch {
	case args[0] == "image" && args[1] == "inspect" && len(args) > 2 && args[2] == "--format":
		if strings.Contains(args[len(args)-1], "pyroscope") {
			return f.imageVolumes, nil
		}
		return "null", nil
	case args[0] == "ps":
		return f.ps, nil
	case args[0] == "network" && args[1] == "inspect":
		if f.networkExists {
			return "", nil
		}
		return "", fmt.Errorf("no such network")
	case args[0] == "network" && args[1] == "ls", args[0] == "volume" && args[1] == "ls":
		return f.lists[args[0]], nil
	}
	return "", nil
}

func (f *fakeRunner) has(prefix ...string) bool {
	for _, c := range f.calls {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			return true
		}
	}
	return false
}

func TestPinsAreTheConceptVersions(t *testing.T) {
	want := map[string]string{
		"grafana": "grafana/grafana:13.3.0", "vm": "victoriametrics/victoria-metrics:v1.156.0",
		"vl": "victoriametrics/victoria-logs:v1.54.0", "tempo": "grafana/tempo:3.1.1", "pyroscope": "grafana/pyroscope:2.4.3",
	}
	if len(Pins) != len(want) {
		t.Fatalf("%d pins, want %d", len(Pins), len(want))
	}
	for _, c := range Pins {
		if c.Ref() != want[c.Name] {
			t.Errorf("%s: %s, want %s", c.Name, c.Ref(), want[c.Name])
		}
	}
	if PluginID != "victoriametrics-logs-datasource" || PluginVersion != "0.34.0" {
		t.Errorf("plugin pin %s %s", PluginID, PluginVersion)
	}
}

func TestProjectName(t *testing.T) {
	for in, want := range map[string]string{"My Repo": "my-repo", "fixture-app": "fixture-app", "": "default", "A_b.c": "a-b-c", strings.Repeat("x", 50): strings.Repeat("x", 30)} {
		if got := ProjectName(in); got != want {
			t.Errorf("ProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEveryCreateIsLabelledNamedAndPinned(t *testing.T) {
	cfg := Config{Project: "demo", PortBase: 18300}
	cfg.defaults()
	for _, k := range Pins {
		a := strings.Join(cfg.createArgs(k), " ")
		for _, want := range []string{
			"--name obs-sandbox-demo-" + k.Name, "--label obs.sandbox=demo", "--network obs-sandbox-demo",
			"-v obs-sandbox-demo-" + k.Name + ":" + k.Data, " " + k.Ref(),
			fmt.Sprintf("-p 127.0.0.1:%d:%d", 18300+k.Offset, k.Port),
		} {
			if !strings.Contains(a, want) {
				t.Errorf("%s: create args lack %q: %s", k.Name, want, a)
			}
		}
	}
	// Defaults keep the components' own ports.
	d := Config{Project: "demo"}
	d.defaults()
	if a := strings.Join(d.createArgs(Pins[0]), " "); !strings.Contains(a, "-p 127.0.0.1:3000:3000") {
		t.Errorf("default Grafana port: %s", a)
	}
}

func TestUpRefusesAContainerWithAnotherImageTag(t *testing.T) {
	fr := &fakeRunner{ps: "obs-sandbox-demo-vm|victoriametrics/victoria-metrics:v1.111.0|running\n"}
	_, err := Up(context.Background(), Config{Project: "demo", Dir: project(t), Runner: fr})
	if err == nil || !strings.Contains(err.Error(), "refusing") || !strings.Contains(err.Error(), "v1.111.0") || !strings.Contains(err.Error(), "v1.156.0") {
		t.Fatalf("want a refusal naming both tags, got %v", err)
	}
	for _, c := range fr.calls {
		if c[0] != "ps" {
			t.Errorf("a command ran after the refusal: %v", c)
		}
	}
}

func TestDownRemovesOnlyItsOwnAndNeverPrunes(t *testing.T) {
	fr := &fakeRunner{
		// The runtime lists a container that carries the label but another name: it must stay.
		ps:    "",
		lists: map[string]string{"network": "obs-sandbox-demo\nother-net\n", "volume": "obs-sandbox-demo-vm\nobs-sandbox-demo-grafana\nteam-volume\n"},
	}
	// ps is used for containers, with a different format per call: return names for the --format {{.Names}} call.
	fr.ps = "obs-sandbox-demo-vm\nobs-sandbox-demo-grafana\nreels-worker\n"
	var out strings.Builder
	if err := Down(context.Background(), Config{Project: "demo", Runner: fr, Out: &out, StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{
		{"rm", "-f", "-v", "obs-sandbox-demo-grafana"}, {"rm", "-f", "-v", "obs-sandbox-demo-vm"},
		{"network", "rm", "obs-sandbox-demo"}, {"volume", "rm", "obs-sandbox-demo-vm"}, {"volume", "rm", "obs-sandbox-demo-grafana"},
	} {
		if !fr.has(want...) {
			t.Errorf("missing call %v; calls: %v", want, fr.calls)
		}
	}
	for _, c := range fr.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "prune") {
			t.Errorf("a prune ran: %s", j)
		}
		for _, other := range []string{"reels-worker", "other-net", "team-volume"} {
			if strings.Contains(j, "rm") && strings.Contains(j, other) {
				t.Errorf("removed a resource that is not this sandbox's: %s", j)
			}
		}
	}
	if !strings.Contains(out.String(), "skipping container reels-worker") {
		t.Errorf("the skipped container is not reported: %s", out.String())
	}
	// Every listing is filtered by the label.
	for _, c := range fr.calls {
		if (c[0] == "ps" || c[1] == "ls") && !strings.Contains(strings.Join(c, " "), "label=obs.sandbox=demo") {
			t.Errorf("listing without the label filter: %v", c)
		}
	}
}

// stackServer answers every endpoint of the five components on one server.
type stackServer struct {
	*httptest.Server
	mu       sync.Mutex
	vmBody   string
	vlBodies []string
	vlQuery  string
	dsPosts  []map[string]any
	dashes   []map[string]any
	versions map[string]string // overrides: vm, vl, grafana, plugin, tempo
}

func newStackServer(t *testing.T) *stackServer {
	s := &stackServer{versions: map[string]string{"vm": "v1.156.0", "vl": "v1.54.0", "grafana": "13.3.0", "plugin": "0.34.0", "tempo": "v3.1.1"}}
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "OK") }
	mux.HandleFunc("/health", ok)
	mux.HandleFunc("/ready", ok)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"version":%q}`, s.versions["grafana"])
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "vm_log_messages_total{app_version=\"victoria-metrics-20261106-175016-tags-%s-0-g0c1bf87a0c\", level=\"info\"} 1\n", s.versions["vm"])
	})
	// VictoriaLogs answers under /vl on the same server.
	mux.HandleFunc("/vl/metrics", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "vm_log_messages_total{app_version=\"victoria-logs-20261113-102924-tags-%s-0-g6433dae586\", level=\"info\"} 1\n", s.versions["vl"])
	})
	mux.HandleFunc("/vl/health", ok)
	mux.HandleFunc("/api/plugins/"+PluginID+"/settings", func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "admin" || p != "admin" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprintf(w, `{"info":{"version":%q}}`, s.versions["plugin"])
	})
	mux.HandleFunc("/api/status/buildinfo", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"version":%q}`, s.versions["tempo"])
	})
	mux.HandleFunc("/api/datasources/uid/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, "{}")
	})
	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		s.mu.Lock()
		s.dsPosts = append(s.dsPosts, m)
		s.mu.Unlock()
		fmt.Fprint(w, "{}")
	})
	mux.HandleFunc("/api/dashboards/db", func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		s.mu.Lock()
		s.dashes = append(s.dashes, m)
		s.mu.Unlock()
		fmt.Fprint(w, "{}")
	})
	mux.HandleFunc("/api/v1/import", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.vmBody += string(b)
		s.mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/internal/force_flush", ok)
	mux.HandleFunc("/vl/insert/jsonline", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.vlBodies = append(s.vlBodies, r.URL.RawQuery+"\n"+string(b))
		s.mu.Unlock()
		w.WriteHeader(200)
	})
	mux.HandleFunc("/vl/select/logsql/query", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"_msg":"x"}`+"\n") })
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *stackServer) cfg(t *testing.T, fr *fakeRunner) Config {
	return Config{
		Project: "demo", Dir: project(t), Runner: fr, StateDir: t.TempDir(), Out: io.Discard,
		HostURL: func(c Component) string {
			if c.Name == "vl" {
				return s.URL + "/vl"
			}
			return s.URL
		},
		Now: func() time.Time { return time.Date(2026, 11, 17, 10, 0, 30, 0, time.UTC) },
	}
}

func TestUpStartsLoadsAndRemembersTheSeedWindow(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	cfg := s.cfg(t, fr)
	info, err := Up(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Every component was created once with the pinned image, and the network and volumes are labelled.
	for _, k := range Pins {
		if !fr.has("start", "obs-sandbox-demo-"+k.Name) {
			t.Errorf("%s was not started", k.Name)
		}
		var created bool
		for _, c := range fr.calls {
			if c[0] == "create" && strings.Contains(strings.Join(c, " "), " "+k.Ref()) && strings.Contains(strings.Join(c, " "), "--label obs.sandbox=demo") {
				created = true
			}
		}
		if !created {
			t.Errorf("%s: no labelled create with the pinned image", k.Name)
		}
	}
	if !fr.has("network", "create", "--label", "obs.sandbox=demo", "obs-sandbox-demo") {
		t.Errorf("network not created with the label: %v", fr.calls)
	}
	if !fr.has("volume", "create", "--label", "obs.sandbox=demo", "obs-sandbox-demo-vm") {
		t.Errorf("volume not created with the label")
	}
	if !fr.has("cp") {
		t.Errorf("tempo config not copied in")
	}
	for _, c := range fr.calls {
		if strings.Contains(strings.Join(c, " "), "prune") {
			t.Errorf("prune: %v", c)
		}
	}
	// The seed ends at the minute before now and is 30 m long.
	wantTo := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	if info.To != wantTo.UnixMilli() || info.From != wantTo.Add(-30*time.Minute).UnixMilli() {
		t.Errorf("window from=%d to=%d", info.From, info.To)
	}
	if !strings.Contains(s.vmBody, `"__name__":"checkout_charges_total"`) {
		t.Errorf("VictoriaMetrics got no checkout_charges_total: %.200s", s.vmBody)
	}
	if len(s.vlBodies) == 0 || !strings.Contains(s.vlBodies[0], "_stream_fields=") || !strings.Contains(strings.Join(s.vlBodies, ""), `"durian.cloud/event":"checkout.charge.failed"`) {
		t.Errorf("VictoriaLogs got no event rows: %v", s.vlBodies)
	}
	// Datasources: the uids the dashboard names, not invented ones; plus default traces and profiles.
	var uids []string
	for _, m := range s.dsPosts {
		uids = append(uids, fmt.Sprint(m["uid"]))
	}
	if got := strings.Join(uids, ","); got != "payments-metrics,payments-logs,obs-traces,obs-profiles" {
		t.Errorf("datasource uids %s", got)
	}
	if len(s.dashes) != 1 || s.dashes[0]["overwrite"] != true || s.dashes[0]["dashboard"].(map[string]any)["uid"] != "payments-overview" {
		t.Errorf("dashboard import: %v", s.dashes)
	}
	// A second up reuses: nothing is created again and the seed is not loaded again.
	fr.ps = ""
	for _, k := range Pins {
		fr.ps += fmt.Sprintf("obs-sandbox-demo-%s|%s|running\n", k.Name, k.Ref())
	}
	fr.networkExists = true
	before := len(s.vlBodies)
	fr.calls = nil
	info2, err := Up(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !info2.Reused || info2.From != info.From || len(s.vlBodies) != before {
		t.Errorf("second up: reused=%v from=%d (want %d), vl bodies %d -> %d", info2.Reused, info2.From, info.From, before, len(s.vlBodies))
	}
	for _, c := range fr.calls {
		if c[0] == "create" || c[0] == "start" {
			t.Errorf("second up ran %v", c)
		}
	}
}

func TestUpRefusesWhenAComponentRunsAnotherVersion(t *testing.T) {
	for comp, bad := range map[string]string{"vm": "v1.111.0", "vl": "v1.50.0", "grafana": "12.1.0", "plugin": "0.30.0", "tempo": "2.8.0"} {
		s := newStackServer(t)
		s.versions[comp] = bad
		_, err := Up(context.Background(), s.cfg(t, &fakeRunner{}))
		if err == nil || !strings.Contains(err.Error(), "refusing") || !strings.Contains(err.Error(), bad) {
			t.Errorf("%s=%s: want a refusal naming the version, got %v", comp, bad, err)
		}
		if len(s.dashes) != 0 || s.vmBody != "" {
			t.Errorf("%s: data was loaded into a stack that does not match the pins", comp)
		}
	}
}

func TestDetectRuntimeRejectsOthers(t *testing.T) {
	if _, err := DetectRuntime("nerdctl"); err == nil {
		t.Error("nerdctl accepted")
	}
}

// Every VOLUME an image declares must be mounted from a labelled named volume; a declared
// VOLUME left alone becomes an anonymous volume that `down` cannot find by label (Pyroscope
// 2.4.3 declares /data, /data-compactor and /data-metastore; MEASURED with docker image inspect).
func TestEveryDeclaredImageVolumeIsMountedFromANamedVolume(t *testing.T) {
	cfg := Config{Project: "demo"}
	cfg.defaults()
	for _, k := range Pins {
		a := cfg.createArgs(k)
		mounts := map[string]string{} // container path -> volume name
		for i, x := range a {
			if x == "-v" && i+1 < len(a) {
				p := strings.SplitN(a[i+1], ":", 2)
				mounts[p[1]] = p[0]
			}
		}
		if mounts[k.Data] == "" {
			t.Errorf("%s: data dir %s is not mounted: %v", k.Name, k.Data, a)
		}
		for _, v := range k.Declared {
			name := mounts[v]
			if name == "" {
				t.Errorf("%s: image VOLUME %s is not mounted: %v", k.Name, v, a)
			} else if name != Prefix("demo") && !strings.HasPrefix(name, Prefix("demo")+"-") {
				t.Errorf("%s: VOLUME %s mounts %q, not a name under %s", k.Name, v, name, Prefix("demo"))
			}
		}
		for p, name := range mounts {
			if strings.HasPrefix(name, "/") || !strings.HasPrefix(name, Prefix("demo")+"-") {
				t.Errorf("%s: mount of %s is not a named obs volume: %q", k.Name, p, name)
			}
		}
	}
	py, _ := ByName("pyroscope")
	want := []string{"/data", "/data-compactor", "/data-metastore"}
	if strings.Join(py.Declared, ",") != strings.Join(want, ",") {
		t.Errorf("pyroscope declared volumes %v, want %v", py.Declared, want)
	}
}

func TestUpCreatesEveryLabelledVolumeAndDownRemovesWithDashV(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{}
	if _, err := Up(context.Background(), s.cfg(t, fr)); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"obs-sandbox-demo-pyroscope", "obs-sandbox-demo-pyroscope-data-compactor", "obs-sandbox-demo-pyroscope-data-metastore"} {
		if !fr.has("volume", "create", "--label", "obs.sandbox=demo", v) {
			t.Errorf("volume %s not created with the label: %v", v, fr.calls)
		}
	}
	// Down removes the container together with its volumes.
	fr2 := &fakeRunner{ps: "obs-sandbox-demo-pyroscope\n", lists: map[string]string{}}
	if err := Down(context.Background(), Config{Project: "demo", Runner: fr2, StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if !fr2.has("rm", "-f", "-v", "obs-sandbox-demo-pyroscope") {
		t.Errorf("container removal lacks -v: %v", fr2.calls)
	}
}

// §10.4 single-instance settings that change what data and queries return.
func TestCreateArgsCarryTheSingleInstanceSettings(t *testing.T) {
	cfg := Config{Project: "demo"}
	cfg.defaults()
	want := map[string][]string{
		"vm": {"-retentionPeriod=30d", "-maxLabelsPerTimeseries=30", "-search.maxConcurrentRequests=4", "-memory.allowedPercent=60"},
		"vl": {"-retentionPeriod=30d", "-retention.maxDiskSpaceUsageBytes=17179869184", "-search.maxConcurrentRequests=4",
			"-search.maxQueueDuration=10s", "-search.maxQueryDuration=30s", "-memory.allowedPercent=60"},
	}
	for name, flags := range want {
		k, _ := ByName(name)
		args := cfg.createArgs(k)
		for _, f := range flags {
			found := false
			for _, a := range args {
				if a == f {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: create args lack %s: %v", name, f, args)
			}
		}
		for _, a := range args {
			if a == "-retentionPeriod=1y" {
				t.Errorf("%s keeps the 1y retention: %v", name, args)
			}
		}
	}
	// A 16 GiB cap is 16 * 1024^3 bytes.
	if LaptopVLDiskCap != 16*1024*1024*1024 {
		t.Errorf("laptop VL cap %d", LaptopVLDiskCap)
	}
	for _, want := range []string{"max_attribute_bytes: 16384", "max_block_duration: 5m"} {
		if !strings.Contains(tempoConfig, want) {
			t.Errorf("tempo config lacks %q:\n%s", want, tempoConfig)
		}
	}
}

func TestUpRefusesAnImageThatDeclaresAnUnknownVolume(t *testing.T) {
	s := newStackServer(t)
	fr := &fakeRunner{imageVolumes: `{"/data":{},"/data-compactor":{},"/data-metastore":{},"/data-new":{}}`}
	_, err := Up(context.Background(), s.cfg(t, fr))
	if err == nil || !strings.Contains(err.Error(), "/data-new") || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("want a refusal naming /data-new, got %v", err)
	}
	// A declared set the pins know passes.
	fr = &fakeRunner{imageVolumes: `{"/data":{},"/data-compactor":{},"/data-metastore":{}}`}
	s2 := newStackServer(t)
	if _, err := Up(context.Background(), s2.cfg(t, fr)); err != nil {
		t.Fatalf("known volumes refused: %v", err)
	}
}
