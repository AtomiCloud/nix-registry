package contract

import (
	"path/filepath"
	"reflect"
	"testing"
)

// One contract per feature: observability/<feature>/contract.yaml and <feature>/observability/contract.yaml
// are both other contracts of the repository, at any depth below an observability folder.
func TestDiscoverFeatureLayouts(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	own := write(t, filepath.Join(root, "observability", "friend", "contract.yaml"), bDoc)
	byFeature := write(t, filepath.Join(root, "observability", "nfctag", "contract.yaml"), aDoc)
	deeper := write(t, filepath.Join(root, "observability", "billing", "payments", "contract.yaml"), aDoc)
	inFeature := write(t, filepath.Join(root, "App", "Modules", "Zed", "observability", "contract.yaml"), aDoc)
	rootOne := write(t, filepath.Join(root, "observability", "contract.yaml"), aDoc)
	write(t, filepath.Join(root, "observability", "fixtures", "x", "contract.yaml"), "service: x\n")
	write(t, filepath.Join(root, "observability", "testdata", "contract.yaml"), "service: x\n")
	write(t, filepath.Join(root, "App", "docs", "contract.yaml"), "service: docs\n") // not below an observability folder
	got, err := Discover(filepath.Dir(own))
	if err != nil {
		t.Fatal(err)
	}
	// sorted by path: App/... before observability/..., observability/contract.yaml before observability/nfctag/...
	want := []string{inFeature, deeper, rootOne, byFeature}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Discover = %v, want %v", got, want)
	}
}

func TestFoldersHoldContractsAndAlerts(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	a := filepath.Dir(write(t, filepath.Join(root, "observability", "a", "contract.yaml"), aDoc))
	b := filepath.Dir(filepath.Dir(filepath.Dir(write(t, filepath.Join(root, "App", "B", "observability", "alerts", "s", "critical.yaml"), "x: 1\n"))))
	top := filepath.Dir(filepath.Dir(filepath.Dir(write(t, filepath.Join(root, "observability", "alerts", "pods", "critical.yaml"), "x: 1\n"))))
	write(t, filepath.Join(root, "observability", "fixtures", "z", "contract.yaml"), aDoc)
	write(t, filepath.Join(root, "App", "docs", "alerts", "x.yaml"), "x: 1\n") // alerts, but not in an observability folder
	got, err := Folders(a)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{b, top, a} // App/B/observability, observability, observability/a: path order
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Folders = %v, want %v", got, want)
	}
	// no repository, or a test folder: none
	lone := write(t, filepath.Join(t.TempDir(), "observability", "contract.yaml"), aDoc)
	if got, err := Folders(filepath.Dir(lone)); err != nil || len(got) != 0 {
		t.Fatalf("no git root but folders %v, err %v", got, err)
	}
}

// A service's log streams are landscapes x the distinct modules of its contracts; one contract is StreamCount.
func TestServiceStreamCount(t *testing.T) {
	mk := func(mods ...string) *Contract { return &Contract{Service: "s", Modules: mods} }
	if got := ServiceStreamCount([]*Contract{mk("a", "b")}, 3); got != (mk("a", "b")).StreamCount(3) {
		t.Fatalf("one contract: %d, want StreamCount", got)
	}
	if got := ServiceStreamCount([]*Contract{mk("a"), mk("b")}, 3); got != 6 {
		t.Fatalf("two modules in two contracts, 3 landscapes: %d, want 6", got)
	}
	if got := ServiceStreamCount([]*Contract{mk("a"), mk("a")}, 2); got != 2 {
		t.Fatalf("the same module twice: %d, want 2", got)
	}
	if got := ServiceStreamCount([]*Contract{mk(), mk()}, 2); got != 2 {
		t.Fatalf("no modules in both: %d, want 2 (one unnamed module)", got)
	}
	if got := ServiceStreamCount([]*Contract{mk(), mk("a")}, 1); got != 2 {
		t.Fatalf("one with no module, one with a: %d, want 2", got)
	}
}

// A Git root that is itself named observability: every folder below it is below an observability folder, so its
// feature children are discovered and listed (the shared helper both Discover and Folders use).
func TestRootNamedObservability(t *testing.T) {
	root := filepath.Join(t.TempDir(), "observability")
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	own := write(t, filepath.Join(root, "friend", "contract.yaml"), bDoc)
	other := write(t, filepath.Join(root, "nfctag", "contract.yaml"), aDoc)
	write(t, filepath.Join(root, "fixtures", "x", "contract.yaml"), "service: x\n")
	write(t, filepath.Join(root, "nfctag", "alerts", "s", "c.yaml"), "x: 1\n")
	got, err := Discover(filepath.Dir(own))
	if err != nil || !reflect.DeepEqual(got, []string{other}) {
		t.Fatalf("Discover = %v, %v; want [%s]", got, err, other)
	}
	fs, err := Folders(filepath.Dir(own))
	if err != nil || !reflect.DeepEqual(fs, []string{filepath.Dir(own), filepath.Dir(other)}) {
		t.Fatalf("Folders = %v, %v", fs, err)
	}
	for dir, want := range map[string]bool{
		root:                          true,
		filepath.Join(root, "a", "b"): true,
	} {
		if belowObservability(root, dir) != want {
			t.Fatalf("belowObservability(%s) != %v", dir, want)
		}
	}
	// a root with another name: a folder outside observability/ is not below one
	plain := t.TempDir()
	if belowObservability(plain, filepath.Join(plain, "docs")) || !belowObservability(plain, filepath.Join(plain, "x", "observability")) {
		t.Fatal("belowObservability on a root with another name")
	}
}
