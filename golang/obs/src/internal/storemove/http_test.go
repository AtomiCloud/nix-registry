package storemove

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func httpFixture(body string, check func(*http.Request)) HTTP {
	return HTTP{Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if check != nil {
			check(r)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
}
func TestCounters(t *testing.T) {
	h := httpFixture("# TYPE vl_rows_ingested_total counter\nvl_rows_ingested_total{type=\"json\"} 99\nvl_rows_ingested_total{type=\"opentelemetry_protobuf\",tenant=\"a\"} 4\nvl_rows_ingested_total{type=\"opentelemetry_protobuf\",tenant=\"b\"} 5\n", nil)
	v, err := h.Counter(context.Background(), "http://fixture/metrics", received("victorialogs"))
	if err != nil || v != 9 {
		t.Fatalf("%g %v", v, err)
	}
	for _, bad := range []string{"", "vm_rows_inserted_total NaN", "vm_rows_inserted_total -1", "vm_rows_inserted_total +Inf"} {
		if _, err := httpFixture(bad, nil).Counter(context.Background(), "http://fixture/metrics", "vm_rows_inserted_total"); err == nil {
			t.Fatalf("bad counter accepted: %q", bad)
		}
	}
}
func matrix(start time.Time, mutate int) string {
	var points []string
	for i := 0; i < 60; i++ {
		ts := start.Add(time.Duration(i) * time.Minute).Unix()
		if mutate == 1 && i == 17 {
			ts++
		}
		if mutate == 2 && i == 18 {
			continue
		}
		points = append(points, fmt.Sprintf(`[%d,"%d"]`, ts, i+1))
	}
	return `{"status":"success","data":{"resultType":"matrix","result":[{"values":[` + strings.Join(points, ",") + `]}]}}`
}
func TestStoreQueryAPIs(t *testing.T) {
	start := time.Unix(1700000040, 0)
	end := start.Add(59 * time.Minute)
	for _, store := range []string{"victorialogs", "victoriametrics"} {
		t.Run(store, func(t *testing.T) {
			h := httpFixture(matrix(start, 0), func(r *http.Request) {
				q := r.URL.Query()
				if q.Get("step") != "60" || q.Get("query") != "known fixture" || q.Get("start") != "1700000040" || r.Header.Get("X-Scope-OrgID") != "fixture" {
					t.Fatal(r.URL, r.Header)
				}
			})
			cfg := config()
			cfg.Query = "known fixture"
			cfg.Tenant = "fixture"
			v, err := h.Counts(context.Background(), store, cfg, end)
			if err != nil || len(v) != 60 || v[17] != 18 {
				t.Fatalf("%v %v", v, err)
			}
		})
	}
	for _, mutate := range []int{1, 2} {
		if _, err := matrixCounts([]byte(matrix(start, mutate)), start); err == nil {
			t.Fatalf("corrupt time series %d accepted", mutate)
		}
	}
	for _, bad := range []string{`{"status":"error"}`, `{"status":"success","data":{"resultType":"matrix","result":[]}}`, `{`} {
		if _, err := matrixCounts([]byte(bad), start); err == nil {
			t.Fatal("malformed query accepted")
		}
	}
}
func TestPyroscopeQueryAPI(t *testing.T) {
	start := time.Unix(1700000040, 0)
	var ps []string
	for i := 0; i < 60; i++ {
		ps = append(ps, fmt.Sprintf(`{"timestamp":"%d","value":%d}`, start.Add(time.Duration(i)*time.Minute).UnixMilli(), i+1))
	}
	body := `{"series":[{"points":[` + strings.Join(ps, ",") + `]}]}`
	h := httpFixture(body, func(r *http.Request) {
		if r.Method != "POST" {
			t.Fatal(r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"profileTypeID"`) {
			t.Fatal(string(b))
		}
	})
	v, err := h.Counts(context.Background(), "pyroscope", config(), start.Add(59*time.Minute))
	if err != nil || len(v) != 60 {
		t.Fatalf("%v %v", v, err)
	}
}
func TestKubernetesArguments(t *testing.T) {
	var calls []string
	k := Kubernetes{Command: func(_ context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		calls = append(calls, cmd)
		if strings.Contains(cmd, "jsonpath=") {
			return []byte("2"), nil
		}
		name := "new"
		if strings.Contains(cmd, "statefulset/single") {
			name = "single"
		}
		return []byte(fmt.Sprintf(`{"spec":{"replicas":2,"selector":{"matchLabels":{"app":"%s"}}},"status":{"readyReplicas":2}}`, name)), nil
	}}
	cfg := config()
	cfg.ScaledManifest = filepath.Join(t.TempDir(), "scaled.yaml")
	if err := os.WriteFile(cfg.ScaledManifest, []byte("kind: StatefulSet\nmetadata: {name: new}\nspec: {replicas: 2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, step := range []Step{Prepare, Route, Reconnect, "stop compaction", "stop single"} {
		if err := k.Execute(context.Background(), step, "tempo", cfg); err != nil {
			t.Fatal(err)
		}
	}
	var sourceTemplate, scale int = -1, -1
	for i, cmd := range calls {
		if !strings.HasPrefix(cmd, "--context fixture --namespace obs-test ") {
			t.Fatal(cmd)
		}
		if strings.Contains(cmd, "patch statefulset/single") {
			sourceTemplate = i
		}
		if strings.Contains(cmd, "scale statefulset/single --replicas=0") {
			scale = i
		}
	}
	if scale < 0 || sourceTemplate <= scale {
		t.Fatalf("old single template rolled before drain: %v", calls)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "exec statefulset/single -- control stop-compaction") {
		t.Fatal(calls)
	}
}
func TestAlloyReplicasRefused(t *testing.T) {
	calls := 0
	k := Kubernetes{Command: func(context.Context, ...string) ([]byte, error) { calls++; return []byte("1"), nil }}
	if err := k.Execute(context.Background(), Reconnect, "tempo", config()); err == nil {
		t.Fatal("one Alloy accepted")
	}
	if calls != 1 {
		t.Fatal("rolled deficient Alloy")
	}
}

func TestScaledManifestIsolation(t *testing.T) {
	cfg := config()
	cfg.ScaledManifest = filepath.Join(t.TempDir(), "scaled.yaml")
	good := "kind: StatefulSet\nmetadata: {name: new}\nspec: {replicas: 2}\n"
	if err := os.WriteFile(cfg.ScaledManifest, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateScaledManifest(cfg); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		good + "---\nkind: Service\nmetadata: {name: tempo-query}\n",
		good + "---\nkind: Deployment\nmetadata: {name: unlisted}\nspec: {replicas: 2}\n",
		strings.Replace(good, "replicas: 2", "replicas: 0", 1),
		strings.Replace(good, "name: new", "name: single", 1),
		good + "  template:\n    metadata:\n      labels: {obs.diene/tempo-read: true}\n",
	} {
		if err := os.WriteFile(cfg.ScaledManifest, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateScaledManifest(cfg); err == nil {
			t.Fatalf("unsafe scaled manifest accepted: %s", bad)
		}
	}
}

func TestRoutingCannotChangeServices(t *testing.T) {
	cfg := config()
	cfg.RoutingManifest = filepath.Join(t.TempDir(), "routing.yaml")
	if err := os.WriteFile(cfg.RoutingManifest, []byte("kind: Service\nmetadata: {name: vm-select}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	k := Kubernetes{Command: func(context.Context, ...string) ([]byte, error) { called = true; return nil, nil }}
	err := k.Execute(context.Background(), StorageRoute, "victoriametrics", cfg)
	if err == nil || !strings.Contains(err.Error(), "routing.yaml") || !strings.Contains(err.Error(), "Service") || called {
		t.Fatalf("stable Service changed: %v, command called=%v", err, called)
	}
}
func TestRolesNeverLabelKafkaOrMixComponents(t *testing.T) {
	cfg := config()
	cfg.Scaled = []string{"deployment/distributor", "deployment/query", "statefulset/kafka"}
	cfg.RoleWorkloads = map[string][]string{"deployment/distributor": {"obs.diene/tempo-write"}, "deployment/query": {"obs.diene/tempo-read"}}
	var calls []string
	k := Kubernetes{Command: func(_ context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		calls = append(calls, cmd)
		name := "single"
		for _, w := range cfg.Scaled {
			if strings.Contains(cmd, w) {
				name = strings.ReplaceAll(w, "/", "-")
			}
		}
		return []byte(fmt.Sprintf(`{"spec":{"replicas":2,"selector":{"matchLabels":{"component":"%s"}}},"status":{"readyReplicas":2}}`, name)), nil
	}}
	if err := k.roles(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	var distributor, query string
	for _, c := range calls {
		if strings.Contains(c, "statefulset/kafka") {
			t.Fatalf("Kafka role touched: %s", c)
		}
		if strings.Contains(c, "patch deployment/distributor") {
			distributor = c
		}
		if strings.Contains(c, "patch deployment/query") {
			query = c
		}
		if strings.Contains(c, "patch statefulset/single") {
			t.Fatal("old single rolled before drain")
		}
	}
	if !strings.Contains(distributor, `"obs.diene/tempo-write":"true"`) || !strings.Contains(distributor, `"obs.diene/tempo-read":"false"`) {
		t.Fatal(distributor)
	}
	if !strings.Contains(query, `"obs.diene/tempo-read":"true"`) || !strings.Contains(query, `"obs.diene/tempo-write":"false"`) {
		t.Fatal(query)
	}
}
func TestOverlappingSelectorsRefused(t *testing.T) {
	if disjointSelectors("app=tempo", "app=tempo,part=write") {
		t.Fatal("overlap accepted")
	}
	if !disjointSelectors("app=tempo,part=single", "app=tempo,part=write") {
		t.Fatal("distinct components refused")
	}
}
