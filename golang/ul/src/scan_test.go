package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func ulYAML(ns, word string) string {
	return "namespace: " + ns + "\npremise: P\nlanguage:\n  " + word + ":\n    desc: " + word + ".\n"
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func scanned(t *testing.T, root string) []string {
	t.Helper()
	res, err := scanRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	return res.Yaml
}

// In git the scan is `git ls-files -co --exclude-standard`: tracked and untracked files, not
// ignored ones, not files deleted from the work tree.
func TestScanInGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	writeTree(t, dir, map[string]string{
		"z/UL.yaml":                ulYAML("ex.org/z", "Zed"),
		"a/UL.yaml":                ulYAML("ex.org/a", "Ay"),
		"UL.yaml":                  ulYAML("ex.org/root", "Root"),
		"ignored/UL.yaml":          ulYAML("ex.org/ignored", "Ig"),
		"gone/UL.yaml":             ulYAML("ex.org/gone", "Gone"),
		"notes/UL.yaml.bak":        "x",
		"notes/MYUL.yaml":          "x",
		"node_modules/pkg/UL.yaml": ulYAML("ex.org/nm", "Nm"),
		".gitignore":               "ignored/\n",
	})
	gitIn(t, dir, "add", "z/UL.yaml", "gone/UL.yaml", "node_modules/pkg/UL.yaml", ".gitignore")
	os.RemoveAll(filepath.Join(dir, "gone"))
	got := scanned(t, dir)
	want := []string{"UL.yaml", "a/UL.yaml", "node_modules/pkg/UL.yaml", "z/UL.yaml"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("in git: %v, want %v (tracked + untracked, sorted; ignored and deleted files out; only the exact name)", got, want)
	}
	// --root inside the repo scans only that folder, with paths relative to it.
	sub := filepath.Join(dir, "z")
	if got := scanned(t, sub); strings.Join(got, " ") != "UL.yaml" {
		t.Fatalf("sub-root: %v", got)
	}
	// The default root is the git top level, found from any folder below it.
	top, err := resolveRoot("", sub)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(dir); top != want && top != dir {
		t.Fatalf("root %s, want %s", top, dir)
	}
	te := newEnv(t, sub)
	r := te.report("all")
	if len(r.Words) != 4 {
		t.Fatalf("all from a sub-folder must scan the whole repo: %v", wordIDs(r))
	}
}

// Outside git the scan walks the tree, skipping .git, node_modules, .direnv and vendor.
func TestScanWalk(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"b/UL.yaml":              ulYAML("ex.org/b", "Bee"),
		"a/deep/er/UL.yaml":      ulYAML("ex.org/a", "Ay"),
		"UL.yaml":                ulYAML("ex.org/root", "Root"),
		"node_modules/UL.yaml":   ulYAML("ex.org/nm", "Nm"),
		"x/node_modules/UL.yaml": ulYAML("ex.org/nm2", "Nm2"),
		".direnv/UL.yaml":        ulYAML("ex.org/direnv", "De"),
		"vendor/UL.yaml":         ulYAML("ex.org/vendor", "Ve"),
		".git/UL.yaml":           ulYAML("ex.org/git", "Gi"),
		"a/UL.yml":               "x",
		"a/ul.yaml":              "x",
	})
	got := scanned(t, dir)
	want := []string{"UL.yaml", "a/deep/er/UL.yaml", "b/UL.yaml"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("walk: %v, want %v", got, want)
	}
	// Outside git, the root is the current directory unless --root says otherwise.
	te := newEnv(t, filepath.Join(dir, "b"))
	if root, _ := resolveRoot("", te.cwd); root != te.cwd && !strings.HasSuffix(te.cwd, "b") {
		t.Fatal(root)
	}
	if r := te.report("all", "--root", dir); len(r.Words) != 3 {
		t.Fatalf("--root: %v", wordIDs(r))
	}
	if code, _, errs := te.ul("all", "--root", filepath.Join(dir, "missing")); code != 2 || errs == "" {
		t.Fatalf("a missing root: exit %d", code)
	}
}

func TestParseEdges(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"empty/UL.yaml":       "",
		"multi/UL.yaml":       ulYAML("ex.org/m", "Em") + "---\n" + ulYAML("ex.org/n", "En"),
		"comment/UL.yaml":     "# only a comment\n",
		"anchor/UL.yaml":      "namespace: ex.org/anchor\npremise: &p One line.\nlanguage:\n  Aa:\n    desc: *p\n  Bb:\n    desc: |\n      Block scalar, one line.\n",
		"nullentry/UL.yaml":   "namespace: ex.org/nullentry\npremise: P\nlanguage:\n  Aa:\n",
		"scalarentry/UL.yaml": "namespace: ex.org/scalarentry\npremise: P\nlanguage:\n  Aa: just text\n",
		"listroot/UL.yaml":    "- a\n- b\n",
		"seelist/UL.yaml":     "namespace: ex.org/seelist\npremise: P\nlanguage:\n  Aa:\n    desc: D.\n    see: Bb\n",
	})
	te := newEnv(t, dir)
	r := te.report("all", "--root", dir)
	by := map[string][]string{}
	for _, p := range r.Problems {
		by[filepath.Dir(p.File)] = append(by[filepath.Dir(p.File)], p.Code)
	}
	for dirName, want := range map[string]string{
		"empty": "UL101", "multi": "UL101", "comment": "UL101", "listroot": "UL101", "nullentry": "UL106",
		"scalarentry": "UL105", "seelist": "UL101",
	} {
		if got := by[dirName]; len(got) == 0 || got[0] != want {
			t.Errorf("%s: %v, want %s", dirName, got, want)
		}
	}
	if len(by["anchor"]) != 0 {
		t.Errorf("YAML anchors and block scalars are fine: %v", by["anchor"])
	}
	if w := findWord(t, r, "ex.org/anchor#Aa"); w.Desc != "One line." {
		t.Errorf("alias: %q", w.Desc)
	}
	if w := findWord(t, r, "ex.org/anchor#Bb"); w.Desc != "Block scalar, one line." {
		t.Errorf("block scalar: %q", w.Desc)
	}
	// The first document of a multi-document repo file is still loaded.
	findWord(t, r, "ex.org/m#Em")
}

func TestCLI(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	if got := te.mustOK("--version"); got != "ul dev\n" {
		t.Errorf("version: %q", got)
	}
	if got := te.mustOK("version"); got != "ul dev\n" {
		t.Errorf("version: %q", got)
	}
	for _, args := range [][]string{{}, {"nope"}, {"all", "--bogus"}, {"all", "--format", "xml", "--root", dir}, {"all", "extra", "--root", dir},
		{"check", "--format", "md", "--root", dir}, {"lookup", "a", "b", "--root", dir}} {
		if code, _, _ := te.ul(args...); code != 2 {
			t.Errorf("ul %v: exit %d, want 2", args, code)
		}
	}
	// Global flags work before and after the command.
	a := te.mustOK("--root", dir, "--format", "md", "all")
	b := te.mustOK("all", "--format", "md", "--root", dir)
	if a != b {
		t.Error("global flags before the command")
	}
	if code, out, _ := te.ul("help"); code != 0 || !strings.Contains(out, "Usage: ul") {
		t.Errorf("help: %d", code)
	}
}
