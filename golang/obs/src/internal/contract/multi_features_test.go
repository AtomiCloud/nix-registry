package contract

import (
	"os"
	"os/exec"
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

// A repository that is itself an observability folder is recognised by what is at its top (a contract.yaml or an
// obs.yaml), never by the name of the folder it is checked out in: the same repository under two names gives one
// answer. Its feature folders are discovered and listed; an examples/ or docs/ folder counts only for a contract.
func TestStandaloneRepoIsRecognisedByContent(t *testing.T) {
	for _, name := range []string{"observability", "checkout"} {
		root := filepath.Join(t.TempDir(), name)
		write(t, filepath.Join(root, ".git", "HEAD"), "ref")
		write(t, filepath.Join(root, "obs.yaml"), "versions: {}\n")
		own := write(t, filepath.Join(root, "friend", "contract.yaml"), bDoc)
		other := write(t, filepath.Join(root, "nfctag", "contract.yaml"), aDoc)
		ex := write(t, filepath.Join(root, "examples", "demo", "contract.yaml"), aDoc)
		write(t, filepath.Join(root, "fixtures", "x", "contract.yaml"), "service: x\n")
		write(t, filepath.Join(root, "nfctag", "alerts", "s", "c.yaml"), "x: 1\n")
		write(t, filepath.Join(root, "docs", "alerts", "s", "c.yaml"), "x: 1\n") // alerts only: not counted
		write(t, filepath.Join(root, "alerts", "pods", "c.yaml"), "x: 1\n")      // the root's own alerts
		got, err := Discover(filepath.Dir(own))
		if err != nil || !reflect.DeepEqual(got, []string{ex, other}) {
			t.Fatalf("[%s] Discover = %v, %v; want [%s %s]", name, got, err, ex, other)
		}
		fs, err := Folders(filepath.Dir(own))
		want := []string{root, filepath.Dir(ex), filepath.Dir(own), filepath.Dir(other)}
		if err != nil || !reflect.DeepEqual(fs, want) {
			t.Fatalf("[%s] Folders = %v, %v; want %v", name, fs, err, want)
		}
	}
	// the same names with nothing at the top: a folder named observability is not enough, and nothing below counts
	root := filepath.Join(t.TempDir(), "observability")
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	own := write(t, filepath.Join(root, "friend", "contract.yaml"), bDoc)
	write(t, filepath.Join(root, "nfctag", "contract.yaml"), aDoc)
	if got, err := Discover(filepath.Dir(own)); err != nil || len(got) != 0 {
		t.Fatalf("a checkout named observability with nothing at its top: %v, %v", got, err)
	}
}

func TestBelowObservability(t *testing.T) {
	root := t.TempDir()
	at := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	cases := []struct {
		dir                  string
		rootObs, hasContract bool
		want                 bool
	}{
		{at("x", "observability"), false, false, true},
		{at("observability", "a", "b"), false, false, true},
		{at("docs"), false, true, false},
		{root, false, true, false},
		{root, true, false, true},
		{at("docs"), true, false, false},
		{at("docs"), true, true, true},
	}
	for _, c := range cases {
		if got := belowObservability(root, c.dir, c.rootObs, c.hasContract); got != c.want {
			t.Fatalf("belowObservability(%s, rootObs=%v, hasContract=%v) = %v", c.dir, c.rootObs, c.hasContract, got)
		}
	}
}

// Copies are not folders of their own: anything below a folder that holds a Chart.yaml (a chart's files/ copy of
// the alerts), and a folder git ignores, are skipped; dir itself is never skipped.
func TestFoldersSkipChartCopiesAndIgnored(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	a := filepath.Dir(write(t, filepath.Join(root, "observability", "nfctag", "contract.yaml"), aDoc))
	write(t, filepath.Join(root, "chart", "Chart.yaml"), "name: c\n")
	write(t, filepath.Join(root, "chart", "files", "observability", "nfctag", "alerts", "e", "w.yaml"), "x: 1\n")
	write(t, filepath.Join(root, "chart", "files", "observability", "nfctag", "contract.yaml"), aDoc)
	got, err := Folders(a)
	if err != nil || !reflect.DeepEqual(got, []string{a}) {
		t.Fatalf("Folders = %v, %v; want only %s", got, err, a)
	}
}

func TestFoldersSkipGitIgnoredFolders(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	a := filepath.Dir(write(t, filepath.Join(root, "observability", "nfctag", "contract.yaml"), aDoc))
	write(t, filepath.Join(root, "App", "Friend", "observability", "alerts", "e", "w.yaml"), "x: 1\n")
	b := filepath.Join(root, "App", "Friend", "observability")
	write(t, filepath.Join(root, "build", "observability", "nfctag", "alerts", "e", "w.yaml"), "x: 1\n")
	write(t, filepath.Join(root, ".gitignore"), "build/\n")
	got, err := Folders(a)
	want := []string{b, a}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Folders = %v, %v; want %v", got, err, want)
	}
}

// A symlinked --dir is the same contract as its target: it is not "another contract" of the repository.
func TestDiscoverExcludesTheSelectedFolderThroughASymlink(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	real := filepath.Dir(write(t, filepath.Join(root, "observability", "nfctag", "contract.yaml"), aDoc))
	other := write(t, filepath.Join(root, "observability", "friend", "contract.yaml"), bDoc)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skip("no symlinks here")
	}
	got, err := Discover(alias)
	if err != nil || !reflect.DeepEqual(got, []string{other}) {
		t.Fatalf("Discover(alias) = %v, %v; want [%s]", got, err, other)
	}
	if !SameFile(alias, real) || SameFile(alias, filepath.Dir(other)) {
		t.Fatal("SameFile")
	}
}

func TestNearMiss(t *testing.T) {
	declared := []string{"nfctag_scans_total", "nfctag_scans_seconds_bucket", "friend_invites_total", "a_reqs_total"}
	cases := map[string]string{
		"nfctag_scan_total":     "nfctag_scans_total", // one letter
		"nfctag_scanss_total":   "nfctag_scans_total", // one letter
		"nfctag.scans_total":    "nfctag_scans_total", // a dot for an underscore
		"nfctag_scans":          "nfctag_scans_total", // a missing kind suffix
		"nfctag_scans_bytes":    "nfctag_scans_total", // a wrong unit or kind suffix
		"b_reqs_total":          "",                   // another application's metric one letter from a declared one
		"up":                    "",                   // no namespace
		"kube_pod_status_phase": "",                   // nothing close
		"nfctag_other_thing":    "",                   // same namespace, far
		"friend_invites_total":  "",                   // declared itself
		"fr_invites_total":      "",                   // a different first word is not a typo of it
	}
	for in, want := range cases {
		if got := NearMiss(in, declared); got != want {
			t.Fatalf("NearMiss(%q) = %q, want %q", in, got, want)
		}
	}
}
