package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const aDoc = "service: a\ntypes:\n  lane: { kind: enum, values: [x, y] }\nmetrics:\n  kube_pod_info:\n    source: ksm\n    kind: gauge\n    budget: 2\n    labels: { lane: lane }\n"
const bDoc = "service: b\ntypes:\n  lane: { kind: enum, values: [p, q, r] }\nmetrics:\n  box_thing:\n    source: x\n    kind: gauge\n    budget: 3\n    labels: { lane: lane }\n"

// Every observability/contract.yaml under the repository is another contract; test fixtures are not.
func TestDiscover(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	own := write(t, filepath.Join(root, "apps", "b", "observability", "contract.yaml"), bDoc)
	other := write(t, filepath.Join(root, "apps", "a", "observability", "contract.yaml"), aDoc)
	write(t, filepath.Join(root, "apps", "c", "docs", "contract.yaml"), "service: c\n")                       // not an observability folder
	write(t, filepath.Join(root, "tests", "fixtures", "z", "observability", "contract.yaml"), "service: z\n") // fixture
	write(t, filepath.Join(root, "node_modules", "p", "observability", "contract.yaml"), "service: nm\n")     // dependency
	got, err := Discover(filepath.Dir(own))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != other {
		t.Fatalf("Discover = %v, want [%s]", got, other)
	}
	// a fixture never goes looking for company
	fx := write(t, filepath.Join(root, "tests", "fixtures", "z", "observability", "contract.yaml"), "service: z\n")
	if got, err := Discover(filepath.Dir(fx)); err != nil || len(got) != 0 {
		t.Fatalf("fixture discovered %v, err %v", got, err)
	}
	// no repository: nothing to look at
	lone := write(t, filepath.Join(t.TempDir(), "observability", "contract.yaml"), aDoc)
	if got, err := Discover(filepath.Dir(lone)); err != nil || len(got) != 0 {
		t.Fatalf("no git root but discovered %v, err %v", got, err)
	}
}

func TestClashNamesBothContracts(t *testing.T) {
	a := parse(t, aDoc)
	a.File = "apps/a/observability/contract.yaml"
	clash := strings.Replace(bDoc, "box_thing", "kube_pod_info", 1)
	b, err := Parse("apps/b/observability/contract.yaml", []byte(clash))
	if err != nil {
		t.Fatal(err)
	}
	is := Clashes([]*Contract{b, a})
	if len(is) != 1 || is[0].Rule != "metric-clash" || !strings.Contains(is[0].Msg, "apps/a/observability/contract.yaml") || !strings.Contains(is[0].Msg, "apps/b/observability/contract.yaml") || !strings.Contains(is[0].Msg, "kube_pod_info") {
		t.Fatalf("clash: %v", is)
	}
	ok, _ := Parse("apps/b/observability/contract.yaml", []byte(bDoc))
	if is := Clashes([]*Contract{ok, a}); len(is) != 0 {
		t.Fatalf("no clash expected: %v", is)
	}
}

// The merged view answers name lookups for all contracts; each metric keeps its own contract's types.
func TestMergeLookup(t *testing.T) {
	a, _ := Parse("a.yaml", []byte(aDoc))
	b, _ := Parse("b.yaml", []byte(bDoc))
	m := Merge(b, []*Contract{a})
	if m.Service != "b" || m.Hash != b.Hash || m.File != "b.yaml" {
		t.Fatalf("merged identity %q %q", m.Service, m.File)
	}
	sm, ok := m.Lookup("kube_pod_info")
	if !ok || sm.Metric.Source != "ksm" {
		t.Fatalf("lookup of the other contract's metric: %v %v", sm, ok)
	}
	if lt, ok := m.LabelType(sm.Metric, "lane"); !ok || len(lt.Values) != 2 {
		t.Fatalf("label type of the other contract: %v %v", lt, ok)
	}
	if lt, ok := m.LabelType(m.Metrics["box_thing"], "lane"); !ok || len(lt.Values) != 3 {
		t.Fatalf("primary label type was replaced: %v %v", lt, ok)
	}
	if len(b.MetricOrder) != 1 {
		t.Fatal("Merge changed the primary")
	}
}

func parseAt(t *testing.T, file, doc string) *Contract {
	t.Helper()
	c, err := Parse(file, []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Two contracts may name the same service: their types must not meet in the merged view.
func TestMergeSameServiceDoesNotOverwriteTypes(t *testing.T) {
	doc := func(state, metric string) string {
		return "service: same\ntypes:\n  state: { kind: enum, values: [" + state + "] }\nmetrics:\n  " + metric + ":\n    source: x\n    kind: gauge\n    budget: 1\n    labels: { state: state }\n"
	}
	a := parseAt(t, "apps/a/observability/contract.yaml", doc("alpha", "alpha_info"))
	b := parseAt(t, "apps/b/observability/contract.yaml", doc("beta", "beta_info"))
	prim := parseAt(t, "apps/p/observability/contract.yaml", "service: p\n")
	m := Merge(prim, []*Contract{a, b})
	for metric, want := range map[string]string{"alpha_info": "alpha", "beta_info": "beta"} {
		sm, ok := m.Lookup(metric)
		if !ok {
			t.Fatalf("%s not found", metric)
		}
		lt, ok := m.LabelType(sm.Metric, "state")
		if !ok || len(lt.Values) != 1 || lt.Values[0] != want {
			t.Errorf("%s: state type %v, want [%s] (the type of its own contract)", metric, lt.Values, want)
		}
	}
}

const platDoc = "service: %s\nplatform_metrics:\n  http.server.request.duration: [%s]\n"

// A platform metric several contracts list is shared, not a clash; the merged view has every label.
func TestSharedPlatformMetricIsNoClashAndLabelsMerge(t *testing.T) {
	a := parseAt(t, "apps/a/observability/contract.yaml", fmt.Sprintf(platDoc, "a", "http.route, http.request.method"))
	b := parseAt(t, "apps/b/observability/contract.yaml", fmt.Sprintf(platDoc, "b", "http.route, http.response.status_code"))
	if is := Clashes([]*Contract{a, b}); len(is) != 0 {
		t.Fatalf("shared platform metric is a clash: %v", is)
	}
	m := Merge(a, []*Contract{b})
	got := strings.Join(m.Platform["http.server.request.duration"], ",")
	if got != "http.route,http.request.method,http.response.status_code" {
		t.Fatalf("merged labels %q", got)
	}
	if len(a.Platform["http.server.request.duration"]) != 2 {
		t.Fatal("Merge changed the primary's labels")
	}
	sm, ok := m.Lookup("http_server_request_duration_seconds_bucket")
	if !ok || !sm.Labels["http.response.status_code"] || !sm.Labels["http.request.method"] {
		t.Fatalf("lookup misses a merged label: %v %v", ok, sm.Labels)
	}
}

// A platform metric's stored name (a suffixed one included) against a metric of another contract is a clash.
func TestPlatformNameAgainstMetricOfAnotherContractClashes(t *testing.T) {
	plat := parseAt(t, "apps/b/observability/contract.yaml", "service: b\nplatform_metrics:\n  alpha.info: [x]\n  etcd.request.duration: [x]\n  process.cpu.utilization: [x]\n")
	cases := map[string]string{
		"source gauge": "service: a\nmetrics:\n  alpha_info:\n    source: ksm\n    kind: gauge\n    budget: 1\n",
		"own counter":  "service: a\nmetrics:\n  alpha.info:\n    kind: counter\n    budget: 1\n", // alpha_info_total
		"own histogram parts vs platform duration": "service: a\nmetrics:\n  etcd.request.duration:\n    kind: histogram\n    unit: seconds\n    budget: 4\n",
		"source ratio gauge":                       "service: a\nmetrics:\n  process_cpu_utilization_ratio:\n    source: x\n    kind: gauge\n    budget: 1\n",
	}
	for name, doc := range cases {
		other := parseAt(t, "apps/a/observability/contract.yaml", doc)
		for _, order := range [][]*Contract{{other, plat}, {plat, other}} {
			is := Clashes(order)
			if len(is) != 1 || is[0].Rule != "metric-clash" || !strings.Contains(is[0].Msg, "apps/a/observability/contract.yaml") || !strings.Contains(is[0].Msg, "apps/b/observability/contract.yaml") {
				t.Errorf("%s: want one clash naming both files, got %v", name, is)
			}
		}
	}
	// nothing of the same name: no clash
	free := parseAt(t, "apps/a/observability/contract.yaml", "service: a\nmetrics:\n  beta_info:\n    source: ksm\n    kind: gauge\n    budget: 1\n")
	if is := Clashes([]*Contract{free, plat}); len(is) != 0 {
		t.Fatalf("no clash expected: %v", is)
	}
}

// own against source, source against source, own against own: still clashes
func TestMetricAgainstMetricStillClashes(t *testing.T) {
	src := func(n string) string {
		return "service: s\nmetrics:\n  " + n + ":\n    source: x\n    kind: gauge\n    budget: 1\n"
	}
	own := func(n string) string { return "service: o\nmetrics:\n  " + n + ":\n    kind: counter\n    budget: 1\n" }
	for name, pair := range map[string][2]string{
		"source/source": {src("q_total"), src("q_total")},
		"own/source":    {own("q"), src("q_total")},
		"own/own":       {own("q"), own("q.total")},
	} {
		a := parseAt(t, "apps/a/observability/contract.yaml", pair[0])
		b := parseAt(t, "apps/b/observability/contract.yaml", pair[1])
		is := Clashes([]*Contract{a, b})
		if len(is) != 1 || !strings.Contains(is[0].Msg, "apps/a/observability/contract.yaml") || !strings.Contains(is[0].Msg, "apps/b/observability/contract.yaml") {
			t.Errorf("%s: %v", name, is)
		}
	}
}

// A message names the other contract relative to the Git root, never by its absolute path.
func TestClashNamesFilesRelativeToGitRoot(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	pa := write(t, filepath.Join(root, "apps", "a", "observability", "contract.yaml"), aDoc)
	pb := write(t, filepath.Join(root, "apps", "b", "observability", "contract.yaml"), strings.Replace(bDoc, "box_thing", "kube_pod_info", 1))
	a, err := Load(pa)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(pb)
	if err != nil {
		t.Fatal(err)
	}
	is := Clashes([]*Contract{b, a})
	if len(is) != 1 {
		t.Fatalf("clashes: %v", is)
	}
	if strings.Contains(is[0].Msg, root) {
		t.Errorf("message holds an absolute path: %s", is[0].Msg)
	}
	for _, want := range []string{"apps/a/observability/contract.yaml:", "apps/b/observability/contract.yaml:"} {
		if !strings.Contains(is[0].Msg, want) {
			t.Errorf("message lacks %s: %s", want, is[0].Msg)
		}
	}
}

// Review of PR 125, thread 3: a directory Discover cannot read is a failure, not a quiet skip.
func TestDiscoverReportsAWalkError(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	own := write(t, filepath.Join(root, "apps", "b", "observability", "contract.yaml"), bDoc)
	write(t, filepath.Join(root, "apps", "a", "observability", "contract.yaml"), aDoc)
	locked := filepath.Join(root, "apps", "locked")
	write(t, filepath.Join(locked, "observability", "contract.yaml"), aDoc)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("the directory is readable anyway (running as root)")
	}
	got, err := Discover(filepath.Dir(own))
	if err == nil {
		t.Fatalf("Discover hid the unreadable directory: %v", got)
	}
	if !strings.Contains(err.Error(), "apps/locked") {
		t.Errorf("the error does not name the directory: %v", err)
	}
	if !strings.Contains(err.Error(), "contract discovery") {
		t.Errorf("the error does not say what failed: %v", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Errorf("the error holds an absolute path: %v", err)
	}
}

// Review of PR 125, thread 4: a source gauge and a source histogram with one YAML key in two contracts
// store different series, so the merged view keeps both; a real stored-name collision stays a clash.
func TestMergeKeepsSameKeyWithDistinctStoredNames(t *testing.T) {
	gauge := "service: g\nmetrics:\n  queue_depth:\n    source: x\n    kind: gauge\n    budget: 1\n"
	hist := "service: h\nmetrics:\n  queue_depth:\n    source: y\n    kind: histogram\n    budget: 1\n"
	for name, order := range map[string][2]string{"gauge primary": {gauge, hist}, "histogram primary": {hist, gauge}} {
		a := parseAt(t, "apps/a/observability/contract.yaml", order[0])
		b := parseAt(t, "apps/b/observability/contract.yaml", order[1])
		if is := Clashes([]*Contract{a, b}); len(is) != 0 {
			t.Fatalf("%s: distinct stored names are not a clash: %v", name, is)
		}
		m := Merge(a, []*Contract{b})
		for _, stored := range []string{"queue_depth", "queue_depth_bucket", "queue_depth_sum", "queue_depth_count"} {
			if _, ok := m.Lookup(stored); !ok {
				t.Errorf("%s: merged view cannot resolve %s", name, stored)
			}
		}
		if len(m.MetricOrder) != 2 || len(m.Metrics) != 2 {
			t.Errorf("%s: merged view has %d ordered and %d keyed metrics, want 2 and 2", name, len(m.MetricOrder), len(m.Metrics))
		}
		if len(a.MetricOrder) != 1 || len(b.MetricOrder) != 1 {
			t.Errorf("%s: Merge changed an input", name)
		}
	}
	// three contracts with the same key: all three stay
	c := parseAt(t, "apps/c/observability/contract.yaml", "service: c\nmetrics:\n  queue_depth:\n    kind: histogram\n    unit: seconds\n    budget: 1\n")
	a := parseAt(t, "apps/a/observability/contract.yaml", gauge)
	b := parseAt(t, "apps/b/observability/contract.yaml", hist)
	if is := Clashes([]*Contract{a, b, c}); len(is) != 0 {
		t.Fatalf("clashes: %v", is)
	}
	m := Merge(a, []*Contract{b, c})
	for _, stored := range []string{"queue_depth", "queue_depth_bucket", "queue_depth_seconds_bucket"} {
		if _, ok := m.Lookup(stored); !ok {
			t.Errorf("three contracts: merged view cannot resolve %s", stored)
		}
	}
	// a real collision (the same stored name twice) is still reported by Clashes
	g2 := parseAt(t, "apps/d/observability/contract.yaml", gauge)
	if is := Clashes([]*Contract{a, g2}); len(is) != 1 || is[0].Rule != "metric-clash" {
		t.Fatalf("a real stored-name collision must stay a clash: %v", is)
	}
}
