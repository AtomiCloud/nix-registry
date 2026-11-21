package contract

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

// A chart at the Git ROOT: its files/ and templates/ copies are skipped (contracts and folders), the repository's own
// observability folders stay. A chart at an app root keeps the observability folder beside its Chart.yaml.
func TestChartCopiesAtTheRootAndBesideAnApp(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	write(t, filepath.Join(root, "Chart.yaml"), "name: c\n")
	a := filepath.Dir(write(t, filepath.Join(root, "observability", "nfctag", "contract.yaml"), aDoc))
	friend := write(t, filepath.Join(root, "observability", "friend", "contract.yaml"), bDoc)
	write(t, filepath.Join(root, "observability", "nfctag", "alerts", "e", "w.yaml"), "x: 1\n")
	write(t, filepath.Join(root, "files", "observability", "nfctag", "contract.yaml"), aDoc)
	write(t, filepath.Join(root, "files", "observability", "nfctag", "alerts", "e", "w.yaml"), "x: 1\n")
	write(t, filepath.Join(root, "templates", "observability", "x", "contract.yaml"), aDoc)
	fs, err := Folders(a)
	if err != nil || !reflect.DeepEqual(fs, []string{filepath.Dir(friend), a}) {
		t.Fatalf("Folders = %v, %v; want the two source folders", fs, err)
	}
	got, err := Discover(a)
	if err != nil || !reflect.DeepEqual(got, []string{friend}) {
		t.Fatalf("Discover = %v, %v; want [%s]", got, err, friend)
	}
	// a chart beside an app's observability folder: that folder is still an observability folder
	write(t, filepath.Join(root, "apps", "x", "Chart.yaml"), "name: x\n")
	app := write(t, filepath.Join(root, "apps", "x", "observability", "contract.yaml"), aDoc)
	got, err = Discover(a)
	if err != nil || !reflect.DeepEqual(got, []string{app, friend}) {
		t.Fatalf("Discover with a chart beside an app folder = %v, %v", got, err)
	}
}

// Discover skips the same copies as Folders: a whole observability folder (contract included) under a chart's files/
// or under a folder git ignores is not a second contract.
func TestDiscoverSkipsChartAndIgnoredCopies(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	cmd := exec.Command("git", "-C", root, "init", "-q")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	a := filepath.Dir(write(t, filepath.Join(root, "observability", "nfctag", "contract.yaml"), aDoc))
	friend := write(t, filepath.Join(root, "observability", "friend", "contract.yaml"), bDoc)
	write(t, filepath.Join(root, "chart", "Chart.yaml"), "name: c\n")
	write(t, filepath.Join(root, "chart", "files", "observability", "nfctag", "contract.yaml"), aDoc)
	write(t, filepath.Join(root, "build", "observability", "nfctag", "contract.yaml"), aDoc)
	got, err := Discover(a)
	if err != nil || len(got) != 2 {
		t.Fatalf("Discover before the ignore rule = %v, %v (the control: the build copy counts)", got, err)
	}
	write(t, filepath.Join(root, ".gitignore"), "build/\n")
	got, err = Discover(a)
	if err != nil || !reflect.DeepEqual(got, []string{friend}) {
		t.Fatalf("Discover = %v, %v; want only [%s]", got, err, friend)
	}
}

// A top-level contract.yaml makes the repository a standalone observability repository only when it reads as an obs
// contract; an OpenAPI file leaves an ordinary repository, so its single-contract layout keeps its 0.5 result.
func TestRootIsObservabilityByContent(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  bool
	}{
		"obs.yaml":           {map[string]string{"obs.yaml": "versions: {}\n"}, true},
		"obs contract":       {map[string]string{"contract.yaml": aDoc}, true},
		"logs only contract": {map[string]string{"contract.yaml": "service: s\nlogs: {}\n"}, true},
		"openapi":            {map[string]string{"contract.yaml": "openapi: 3.0.0\ninfo: { title: x, version: '1' }\npaths: {}\n"}, false},
		"service only":       {map[string]string{"contract.yaml": "service: s\n"}, false},
		"not yaml":           {map[string]string{"contract.yaml": "a: [\n"}, false},
		"nothing":            {map[string]string{}, false},
	}
	for name, c := range cases {
		root := t.TempDir()
		for f, body := range c.files {
			write(t, filepath.Join(root, f), body)
		}
		if got := rootIsObservability(root); got != c.want {
			t.Fatalf("%s: rootIsObservability = %v, want %v", name, got, c.want)
		}
	}
	// the 0.5 layout with an unrelated top-level contract.yaml: the app's contract has no company
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	write(t, filepath.Join(root, "contract.yaml"), "openapi: 3.0.0\ninfo: { title: x, version: '1' }\n")
	own := write(t, filepath.Join(root, "observability", "contract.yaml"), aDoc)
	if got, err := Discover(filepath.Dir(own)); err != nil || len(got) != 0 {
		t.Fatalf("an OpenAPI contract.yaml at the top was discovered: %v, %v", got, err)
	}
	// and the standalone repository with an obs contract at its top does see its features
	write(t, filepath.Join(root, "contract.yaml"), bDoc)
	write(t, filepath.Join(root, "friend", "contract.yaml"), bDoc)
	if got, err := Discover(filepath.Dir(own)); err != nil || len(got) != 2 {
		t.Fatalf("a standalone repository with an obs contract at its top: %v, %v", got, err)
	}
}

// Git refusing the repository for its ownership must not fail silently: one note says ignored folders are not skipped,
// but only when there is a folder besides the selected one that the skipping could have changed.
func TestGitOwnershipRefusalIsNotedOnlyWhenItMatters(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "git")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'fatal: detected dubious ownership in repository at /x' >&2\nexit 128\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var buf bytes.Buffer
	old := NoteWriter
	NoteWriter = &buf
	defer func() { NoteWriter = old }()
	// a repository with one observability folder: nothing else is listed, so no note (a 0.5 repo prints nothing new)
	one := t.TempDir()
	write(t, filepath.Join(one, ".git", "HEAD"), "ref")
	only := filepath.Dir(write(t, filepath.Join(one, "observability", "contract.yaml"), aDoc))
	write(t, filepath.Join(one, "observability", "alerts", "e", "w.yaml"), "x: 1\n")
	gitNote = sync.Once{}
	for i := 0; i < 2; i++ {
		if got, err := Discover(only); err != nil || len(got) != 0 {
			t.Fatalf("Discover = %v, %v", got, err)
		}
		if got, err := Folders(only); err != nil || !reflect.DeepEqual(got, []string{only}) {
			t.Fatalf("Folders = %v, %v", got, err)
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("a one-folder repository printed a note: %q", buf.String())
	}
	// two folders: the skipping could matter, so one note, however often the walks run
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	a := filepath.Dir(write(t, filepath.Join(root, "observability", "nfctag", "contract.yaml"), aDoc))
	write(t, filepath.Join(root, "observability", "friend", "contract.yaml"), bDoc)
	gitNote = sync.Once{}
	for i := 0; i < 3; i++ {
		if _, err := Discover(a); err != nil {
			t.Fatal(err)
		}
		if _, err := Folders(a); err != nil {
			t.Fatal(err)
		}
	}
	out := buf.String()
	if strings.Count(out, "note: git refused to read") != 1 || !strings.Contains(out, "dubious ownership") || !strings.Contains(out, "safe.directory") {
		t.Fatalf("want exactly one note naming the cause and the fix, got %q", out)
	}
	// any other git failure (not a repository: a fake .git) is quiet, as before
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'fatal: not a git repository' >&2\nexit 128\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	gitNote = sync.Once{}
	if _, err := Discover(a); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("a repository that is not one printed a note: %q", buf.String())
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// An ignored folder is pruned on entry, never read: one that cannot be read does not fail the walk, in discovery or
// in the folder scan.
func TestUnreadableIgnoredFolderIsNeverRead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	root := t.TempDir()
	gitIn(t, root, "init", "-q")
	a := filepath.Dir(write(t, filepath.Join(root, "observability", "nfctag", "contract.yaml"), aDoc))
	friend := write(t, filepath.Join(root, "observability", "friend", "contract.yaml"), bDoc)
	write(t, filepath.Join(root, "build", "observability", "nfctag", "contract.yaml"), aDoc)
	write(t, filepath.Join(root, ".gitignore"), "build/\n")
	if err := os.Chmod(filepath.Join(root, "build"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(root, "build"), 0o755)
	got, err := Discover(a)
	if err != nil || !reflect.DeepEqual(got, []string{friend}) {
		t.Fatalf("Discover = %v, %v; want [%s] and no error for the unreadable ignored folder", got, err, friend)
	}
	fs, err := Folders(a)
	if err != nil || !reflect.DeepEqual(fs, []string{filepath.Dir(friend), a}) {
		t.Fatalf("Folders = %v, %v", fs, err)
	}
	// the control: the same folder, not ignored, is an error naming it
	write(t, filepath.Join(root, ".gitignore"), "")
	if _, err := Discover(a); err == nil || !strings.Contains(err.Error(), "build") {
		t.Fatalf("an unreadable folder that is not ignored must be an error naming it: %v", err)
	}
}

// A nested Git repository and a populated submodule are their own boundary: the walk does not enter them, and a copy
// of an observability folder inside one is not a second contract.
func TestNestedRepositoriesAndSubmodulesAreBoundaries(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	gitIn(t, root, "init", "-q")
	a := filepath.Dir(write(t, filepath.Join(root, "observability", "nfctag", "contract.yaml"), aDoc))
	friend := write(t, filepath.Join(root, "observability", "friend", "contract.yaml"), bDoc)
	// an untracked nested repository holding a copy
	nested := filepath.Join(root, "tools", "other")
	gitIn(t, root, "init", "-q", nested)
	write(t, filepath.Join(nested, "observability", "nfctag", "contract.yaml"), aDoc)
	// a real submodule holding a copy
	sub := t.TempDir()
	gitIn(t, sub, "init", "-q")
	write(t, filepath.Join(sub, "observability", "nfctag", "contract.yaml"), aDoc)
	gitIn(t, sub, "add", "-A")
	gitIn(t, sub, "commit", "-q", "-m", "x")
	gitIn(t, root, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "vendor2/sub")
	if !hasGit(filepath.Join(root, "vendor2", "sub")) {
		t.Fatal("the submodule is not populated")
	}
	if _, err := os.Stat(filepath.Join(root, "vendor2", "sub", "observability", "nfctag", "contract.yaml")); err != nil {
		t.Fatalf("the submodule copy is not there: %v", err)
	}
	got, err := Discover(a)
	if err != nil || !reflect.DeepEqual(got, []string{friend}) {
		t.Fatalf("Discover = %v, %v; want only [%s]", got, err, friend)
	}
	fs, err := Folders(a)
	if err != nil || !reflect.DeepEqual(fs, []string{filepath.Dir(friend), a}) {
		t.Fatalf("Folders = %v, %v", fs, err)
	}
	// a --dir inside the nested repository is its own repository: it sees its own contracts, not the outer ones
	other := filepath.Join(nested, "observability", "nfctag")
	got, err = Discover(other)
	if err != nil || len(got) != 0 {
		t.Fatalf("Discover inside a nested repository = %v, %v", got, err)
	}
}
