package ruleids

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func alertDoc(uid string) string {
	return "apiVersion: grafana.integreatly.org/v1beta1\nkind: GrafanaAlertRuleGroup\nmetadata: { name: g-" + uid + " }\nspec:\n  interval: 1m\n  rules:\n    - uid: " + uid +
		"\n      title: t\n      condition: C\n      for: 1m\n      data:\n        - { refId: C, datasourceUid: __expr__, model: { type: math, expression: '1 > 0' } }\n"
}

func put(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repo(t *testing.T, aUID, bUID string) (a, b string) {
	root := t.TempDir()
	put(t, filepath.Join(root, ".git", "HEAD"), "ref")
	a = filepath.Join(root, "observability", "nfctag")
	b = filepath.Join(root, "App", "Friend", "observability")
	put(t, filepath.Join(a, "alerts", "e", "warning.yaml"), alertDoc(aUID))
	put(t, filepath.Join(b, "alerts", "e", "warning.yaml"), alertDoc(bUID))
	return a, b
}

func TestAllFindsTheSameUidInTwoFolders(t *testing.T) {
	a, _ := repo(t, "errors-warning", "errors-warning")
	cs, err := All(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 {
		t.Fatalf("clashes = %v, want one", cs)
	}
	got := cs[0].String()
	for _, want := range []string{"[rule-id-clash]", `"errors-warning"`, "App/Friend/observability/alerts/e/warning.yaml", "observability/nfctag/alerts/e/warning.yaml"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q is missing %q", got, want)
		}
	}
}

func TestDistinctUidsAndOneFolderAreQuiet(t *testing.T) {
	a, _ := repo(t, "a-warning", "b-warning")
	if cs, err := All(a); err != nil || len(cs) != 0 {
		t.Fatalf("distinct uids: %v %v", cs, err)
	}
	// a repository with one folder: nothing to compare (the per-folder check owns duplicates inside it)
	root := t.TempDir()
	put(t, filepath.Join(root, ".git", "HEAD"), "ref")
	one := filepath.Join(root, "observability")
	put(t, filepath.Join(one, "alerts", "e", "warning.yaml"), alertDoc("x"))
	put(t, filepath.Join(one, "alerts", "f", "warning.yaml"), alertDoc("x"))
	if cs, err := All(one); err != nil || len(cs) != 0 {
		t.Fatalf("one folder: %v %v", cs, err)
	}
	// no repository: nothing
	lone := filepath.Join(t.TempDir(), "observability")
	put(t, filepath.Join(lone, "alerts", "e", "warning.yaml"), alertDoc("x"))
	if cs, err := All(lone); err != nil || len(cs) != 0 {
		t.Fatalf("no git root: %v %v", cs, err)
	}
}

func TestFindLimitsToMyFiles(t *testing.T) {
	rules := []Rule{{"x", "a.yaml"}, {"x", "b.yaml"}, {"y", "c.yaml"}, {"y", "d.yaml"}, {"z", "e.yaml"}, {"z", "e.yaml"}}
	if got := Find(rules, nil); len(got) != 2 {
		t.Fatalf("all: %v, want x and y (a rule twice in one file is not this check's)", got)
	}
	got := Find(rules, map[string]bool{"d.yaml": true})
	if len(got) != 1 || got[0].ID != "y" || got[0].Also != "d.yaml" || got[0].First != "c.yaml" {
		t.Fatalf("mine=d.yaml: %v", got)
	}
}

// A standalone observability repository (obs.yaml at its top) has its feature folders scanned, whatever its
// checkout folder is called.
func TestAllInAStandaloneRepo(t *testing.T) {
	for _, name := range []string{"observability", "checkout"} {
		root := filepath.Join(t.TempDir(), name)
		put(t, filepath.Join(root, ".git", "HEAD"), "ref")
		put(t, filepath.Join(root, "obs.yaml"), "versions: {}\n")
		a := filepath.Join(root, "nfctag")
		put(t, filepath.Join(a, "contract.yaml"), "service: a\n")
		put(t, filepath.Join(a, "alerts", "e", "warning.yaml"), alertDoc("errors-warning"))
		put(t, filepath.Join(root, "friend", "contract.yaml"), "service: b\n")
		put(t, filepath.Join(root, "friend", "alerts", "e", "warning.yaml"), alertDoc("errors-warning"))
		cs, err := All(a)
		if err != nil || len(cs) != 1 {
			t.Fatalf("[%s] clashes = %v, %v; want one", name, cs, err)
		}
	}
}

// A symlinked --dir is its target: its own rules are not clashes with themselves.
func TestOthersSkipsTheSelectedFolderThroughASymlink(t *testing.T) {
	a, b := repo(t, "errors-warning", "other-warning")
	alias := filepath.Join(filepath.Dir(filepath.Dir(a)), "alias")
	if err := os.Symlink(a, alias); err != nil {
		t.Skip("no symlinks here")
	}
	others, err := Others(alias)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range others {
		if strings.Contains(r.File, "observability/nfctag/") {
			t.Fatalf("the selected folder came back as another folder: %v", others)
		}
	}
	if len(others) != 1 || !strings.Contains(others[0].File, "Friend") {
		t.Fatalf("others = %v, want only Friend's rule (%s)", others, b)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
}

// A generated copy INSIDE a discovered folder's alerts/ (git-ignored, or a nested repository) is not a second
// definition of its uids: the rule reader prunes with the discovery's rule.
func TestCopiesInsideAFoldersAlertsAreNotClashes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	gitInit(t, root)
	a := filepath.Join(root, "observability", "nfctag")
	b := filepath.Join(root, "observability", "friend")
	put(t, filepath.Join(a, "contract.yaml"), "service: a\n")
	put(t, filepath.Join(a, "alerts", "e", "warning.yaml"), alertDoc("a-errors"))
	put(t, filepath.Join(b, "contract.yaml"), "service: b\n")
	put(t, filepath.Join(b, "alerts", "e", "warning.yaml"), alertDoc("b-errors"))
	put(t, filepath.Join(b, "alerts", "build", "e", "warning.yaml"), alertDoc("b-errors"))
	cs, err := All(a)
	if err != nil || len(cs) != 1 {
		t.Fatalf("the control (not ignored): clashes = %v, %v; want the copy to clash", cs, err)
	}
	put(t, filepath.Join(root, ".gitignore"), "build/\n")
	if cs, err := All(a); err != nil || len(cs) != 0 {
		t.Fatalf("an ignored copy inside alerts/: %v, %v", cs, err)
	}
	put(t, filepath.Join(root, ".gitignore"), "")
	if err := os.RemoveAll(filepath.Join(b, "alerts", "build")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(b, "alerts", "vendored", "e", "warning.yaml"), alertDoc("b-errors"))
	gitInit(t, filepath.Join(b, "alerts", "vendored"))
	if cs, err := All(a); err != nil || len(cs) != 0 {
		t.Fatalf("a nested repository inside alerts/: %v, %v", cs, err)
	}
	if others, err := Others(a); err != nil || len(others) != 1 {
		t.Fatalf("Others = %v, %v; want friend's one rule", others, err)
	}
}

// An unreadable ignored copy inside alerts/ is never read, so it cannot stop the walk before a real duplicate in a later
// folder (a false green); an unreadable folder that is NOT ignored is an error naming it.
func TestUnreadableFolderInAlerts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	root := t.TempDir()
	gitInit(t, root)
	a := filepath.Join(root, "observability", "nfctag")
	b := filepath.Join(root, "observability", "friend")
	put(t, filepath.Join(a, "contract.yaml"), "service: a\n")
	put(t, filepath.Join(a, "alerts", "e", "warning.yaml"), alertDoc("shared-errors"))
	put(t, filepath.Join(b, "contract.yaml"), "service: b\n")
	put(t, filepath.Join(b, "alerts", "build", "x", "warning.yaml"), alertDoc("other"))
	put(t, filepath.Join(b, "alerts", "z-source", "warning.yaml"), alertDoc("shared-errors")) // a real duplicate, after build/
	put(t, filepath.Join(root, ".gitignore"), "build/\n")
	build := filepath.Join(b, "alerts", "build")
	if err := os.Chmod(build, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(build, 0o755)
	cs, err := All(a)
	if err != nil || len(cs) != 1 || !strings.Contains(cs[0].String(), "z-source") {
		t.Fatalf("unreadable ignored build/ must not hide the real clash: %v, %v", cs, err)
	}
	// the same folder, not ignored: an error naming it
	put(t, filepath.Join(root, ".gitignore"), "")
	if _, err := All(a); err == nil || !strings.Contains(err.Error(), "build") {
		t.Fatalf("an unreadable folder that is not ignored must be an error naming it: %v", err)
	}
	if _, err := Others(a); err == nil || !strings.Contains(err.Error(), "build") {
		t.Fatalf("Others: an unreadable folder that is not ignored must be an error naming it: %v", err)
	}
}

// Below an observability folder only the ignored / nested-repository / chart-copy pruning applies, never the name
// lists: an alert slug folder called vendor is a definition, so one uid under alerts/vendor/ in two features clashes.
func TestSlugFolderNamedVendorIsNotPruned(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	gitInit(t, root)
	a := filepath.Join(root, "observability", "nfctag")
	b := filepath.Join(root, "observability", "friend")
	put(t, filepath.Join(a, "contract.yaml"), "service: a\n")
	put(t, filepath.Join(a, "alerts", "vendor", "warning.yaml"), alertDoc("shared-errors"))
	put(t, filepath.Join(b, "contract.yaml"), "service: b\n")
	put(t, filepath.Join(b, "alerts", "vendor", "warning.yaml"), alertDoc("shared-errors"))
	cs, err := All(a)
	if err != nil || len(cs) != 1 {
		t.Fatalf("alerts/vendor/ in two features: %v, %v; want one clash", cs, err)
	}
}
