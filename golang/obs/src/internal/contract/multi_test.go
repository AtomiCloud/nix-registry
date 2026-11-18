package contract

import (
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
