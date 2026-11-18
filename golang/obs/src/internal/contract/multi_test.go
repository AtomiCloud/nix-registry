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
	got := Discover(filepath.Dir(own))
	if len(got) != 1 || got[0] != other {
		t.Fatalf("Discover = %v, want [%s]", got, other)
	}
	// a fixture never goes looking for company
	fx := write(t, filepath.Join(root, "tests", "fixtures", "z", "observability", "contract.yaml"), "service: z\n")
	if got := Discover(filepath.Dir(fx)); len(got) != 0 {
		t.Fatalf("fixture discovered %v", got)
	}
	// no repository: nothing to look at
	lone := write(t, filepath.Join(t.TempDir(), "observability", "contract.yaml"), aDoc)
	if got := Discover(filepath.Dir(lone)); len(got) != 0 {
		t.Fatalf("no git root but discovered %v", got)
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
