package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A git that refuses to run inside a work tree (dubious ownership in a CI container) must stop
// ul with exit 2. Falling back to a walk would scan only the subfolder (a false green) or read
// ignored files (a false red).
func TestGitRefusalIsAnError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	writeTree(t, dir, map[string]string{
		"docs/UL.yaml":      ulYAML("ex.org/docs", "Doc"),
		"dist/copy/UL.yaml": ulYAML("ex.org/docs", "Doc"),
		"sub/readme.txt":    "x",
		".gitignore":        "dist/\n",
	})
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// Control: git works, the ignored copy is not read, check is clean.
	if code, out, errs := newEnv(t, dir).ul("check"); code != 0 {
		t.Fatalf("control: exit %d\n%s%s", code, out, errs)
	}
	t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "1")
	if probe := exec.Command("git", "rev-parse", "--show-toplevel"); true {
		probe.Dir = dir
		if probe.Run() == nil {
			t.Skip("this git does not honour GIT_TEST_ASSUME_DIFFERENT_OWNER")
		}
	}
	for _, cwd := range []string{dir, filepath.Join(dir, "sub")} {
		for _, cmd := range []string{"all", "check"} {
			code, _, errs := newEnv(t, cwd).ul(cmd)
			if code != 2 || !strings.Contains(errs, "git failed") {
				t.Fatalf("ul %s from %s with a refusing git: exit %d, stderr %q; want exit 2 naming git", cmd, cwd, code, errs)
			}
		}
	}
	// --root on the same tree is refused too: the walk would read the ignored copy.
	if code, _, errs := newEnv(t, dir).ul("check", "--root", dir); code != 2 || !strings.Contains(errs, "git failed") {
		t.Fatalf("--root with a refusing git: exit %d, %q", code, errs)
	}
}

// --from through a symlinked path ranks the same as through the real path (git reports the
// real top level; editors on macOS see /var for /private/var).
func TestFromThroughSymlink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	writeTree(t, repo, map[string]string{
		"a/UL.yaml": ulYAML("ex.org/a", "Item"),
		"b/UL.yaml": ulYAML("ex.org/b", "Item"),
		"b/x.go":    "package x\n",
	})
	gitIn(t, repo, "init", "-q")
	link := filepath.Join(base, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skip("no symlinks here")
	}
	want := []string{"ex.org/b#Item", "ex.org/a#Item"}
	for _, c := range []struct{ cwd, from string }{
		{repo, "b/x.go"},
		{link, "b/x.go"},
		{link, filepath.Join(link, "b", "x.go")},
		{repo, filepath.Join(link, "b", "missing.go")},
	} {
		if got := newEnv(t, c.cwd).lookupIDs("Item", "--from", c.from); !slices.Equal(got, want) {
			t.Fatalf("cwd %s --from %s: %v, want %v", c.cwd, c.from, got, want)
		}
	}
}

// One dangling or unreadable UL.yaml is a UL101 problem on that file; every other UL still loads.
func TestUnreadableFileIsAProblem(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"ok/UL.yaml": ulYAML("ex.org/ok", "Fine")})
	if err := os.MkdirAll(filepath.Join(dir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../nowhere/UL.yaml", filepath.Join(dir, "bad", "UL.yaml")); err != nil {
		t.Skip("no symlinks here")
	}
	te := newEnv(t, dir)
	code, out, errs := te.ul("check")
	if code != 1 || !strings.Contains(out, "bad/UL.yaml:1: UL101 cannot read the file") || strings.Contains(out, dir) {
		t.Fatalf("check: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	r := te.report("all")
	if ids := wordIDs(r); !slices.Equal(ids, []string{"ex.org/ok#Fine"}) {
		t.Fatalf("all must keep the readable UL: %v", ids)
	}
	if len(r.Problems) != 1 || r.Problems[0].Code != "UL101" {
		t.Fatalf("all problems: %+v", r.Problems)
	}
}

// git runs in the C locale: a translated "not a git repository" must still mean "walk", or a
// plain folder holding an empty .git dir becomes an error for a German-speaking user.
func TestGitMessageUnderAnotherLocale(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a/UL.yaml": ulYAML("ex.org/a", "Ay")})
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	probe := exec.Command("git", "rev-parse", "--show-toplevel")
	probe.Dir = dir
	out, _ := probe.CombinedOutput()
	if strings.Contains(string(out), "not a git repository") {
		t.Skip("git is not translated on this machine; the test would prove nothing")
	}
	if code, stdout, errs := newEnv(t, dir).ul("check"); code != 0 {
		t.Fatalf("plain folder under a German locale: exit %d\n%s%s", code, stdout, errs)
	}
}

// A relative --from from a symlinked cwd resolves ".." on the real path, as the kernel does.
func TestRelativeFromInSymlinkedCwd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	base := t.TempDir()
	g := filepath.Join(base, "g")
	writeTree(t, g, map[string]string{
		"a/UL.yaml":  ulYAML("ex.org/a", "A"),
		"b/UL.yaml":  ulYAML("ex.org/b", "A"),
		"zz/UL.yaml": ulYAML("ex.org/zz", "A"),
		"zz/x.go":    "package x\n",
	})
	gitIn(t, g, "init", "-q")
	if err := os.MkdirAll(filepath.Join(base, "else"), 0o755); err != nil {
		t.Fatal(err)
	}
	blink := filepath.Join(base, "else", "blink")
	if err := os.Symlink(filepath.Join(g, "b"), blink); err != nil {
		t.Skip("no symlinks here")
	}
	got := newEnv(t, blink).lookupIDs("A", "--from", "../zz/x.go")
	if len(got) == 0 || got[0] != "ex.org/zz#A" {
		t.Fatalf("--from ../zz/x.go from a symlinked b: %v, want ex.org/zz#A first", got)
	}
	// --root takes the same real-path join.
	r := newEnv(t, blink).report("all", "--root", "../zz")
	if ids := wordIDs(r); len(ids) != 1 || ids[0] != "ex.org/zz#A" {
		t.Fatalf("--root ../zz from a symlinked b: %v, want only ex.org/zz#A", ids)
	}
}
