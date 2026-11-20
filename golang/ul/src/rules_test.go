package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var allRuleCodes = []string{"UL101", "UL102", "UL103", "UL104", "UL105", "UL106", "UL107", "UL108", "UL109",
	"UL110", "UL201", "UL202", "UL203", "UL204", "UL301",
	"UL401", "UL402", "UL403", "UL407", "UL408", "UL409", "UL501", "UL502", "UL503"}

// Every planted-failure fixture breaks only the rule its directory names: it is refused with
// that code and no other.
func TestPlantedFailures(t *testing.T) {
	dirs, err := filepath.Glob("testdata/bad/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no planted failures found: %v", err)
	}
	covered := map[string]int{}
	for _, d := range dirs {
		name := filepath.Base(d)
		want := name[:5]
		covered[want]++
		t.Run(name, func(t *testing.T) {
			dir := fixture(t, filepath.Join("bad", name))
			te := newEnv(t, dir)
			code, out, errs := te.ul("check", "--root", dir, "--format", "json")
			if code != 1 {
				t.Fatalf("check exit %d, want 1\n%s%s", code, out, errs)
			}
			var res problemsOut
			if err := jsonUnmarshal(out, &res); err != nil {
				t.Fatal(err)
			}
			if len(res.Problems) == 0 {
				t.Fatal("no problems")
			}
			for _, p := range res.Problems {
				if p.Code != want {
					t.Errorf("refused with %s, want only %s: %+v", p.Code, want, p)
				}
				if p.Line < 1 || p.File == "" || p.Message == "" {
					t.Errorf("incomplete problem %+v", p)
				}
			}
			// The text form is `file:line: ULnnn message`.
			_, text, _ := te.ul("check", "--root", dir)
			for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
				parts := strings.SplitN(line, " ", 3)
				if len(parts) < 3 || parts[1] != want || !strings.Contains(parts[0], ".") || strings.Count(parts[0], ":") != 2 {
					t.Errorf("bad text line %q", line)
				}
			}
		})
	}
	for _, c := range allRuleCodes {
		if covered[c] == 0 {
			t.Errorf("rule %s has no planted-failure fixture", c)
		}
	}
}

func TestCleanFixturePasses(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	code, out, errs := te.ul("check", "--root", dir)
	if code != 0 || out != "" {
		t.Fatalf("check exit %d\n%s%s", code, out, errs)
	}
	var res problemsOut
	if err := jsonUnmarshal(te.mustOK("check", "--root", dir, "--format", "json"), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("problems on the clean fixture: %+v", res.Problems)
	}
	if code, out, _ := te.ul("gen", "--check", "--root", dir); code != 0 {
		t.Fatalf("gen --check on the clean fixture: exit %d\n%s", code, out)
	}
	r := te.report("all", "--root", dir)
	if len(r.Problems) != 0 || len(r.Words) != 9 || len(r.Namespaces) != 3 {
		t.Fatalf("clean fixture: %d words, %d namespaces, problems %+v", len(r.Words), len(r.Namespaces), r.Problems)
	}
}

// A problem on one file never hides a problem on another, and `all` still lists the words
// of every readable file.
func TestAllReportsProblemsAndKeepsGoing(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a/UL.yaml": "namespace: ex.org/a\npremise: A\nlanguage:\n  One:\n    desc: One.\n    see: [Missing]\n",
		"b/UL.yaml": "namespace: ex.org/b\npremise: B\nlanguage:\n  Two:\n    desc: Two.\n",
		"c/UL.yaml": "namespace: ex.org/a\npremise: A again\nlanguage:\n  Three:\n    desc: Three.\n",
	})
	te := newEnv(t, dir)
	r := te.report("all", "--root", dir)
	if got := wordIDs(r); len(got) != 2 || got[0] != "ex.org/a#One" || got[1] != "ex.org/b#Two" {
		t.Fatalf("words %v: the first file by path must win UL103", got)
	}
	if len(r.Problems) != 2 || r.Problems[0].Code != "UL201" || r.Problems[1].Code != "UL103" || r.Problems[1].File != "c/UL.yaml" {
		t.Fatalf("problems %+v", r.Problems)
	}
	if one := findWord(t, r, "ex.org/a#One"); len(one.See) != 0 {
		t.Fatalf("an unresolved reference must be left out: %v", one.See)
	}
}

func TestGenRefusesWhileProblemsExist(t *testing.T) {
	dir := fixture(t, "bad/UL201-see-unresolved")
	te := newEnv(t, dir)
	code, _, errs := te.ul("gen", "--root", dir)
	if code != 1 || !strings.Contains(errs, "UL201") {
		t.Fatalf("gen exit %d: %s", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "UL.md")); err == nil {
		t.Fatal("gen wrote UL.md although the repo has problems")
	}
}

func TestGenDriftAndRewrite(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	md := filepath.Join(dir, "docs/standard/kitchen/UL.md")
	orig, _ := os.ReadFile(md)
	if err := os.WriteFile(md, append(orig, []byte("drift\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := te.ul("gen", "--check", "--root", dir)
	if code != 1 || !strings.Contains(out, "docs/standard/kitchen/UL.md:1: UL301") {
		t.Fatalf("gen --check exit %d: %s", code, out)
	}
	if code, out, _ := te.ul("check", "--root", dir); code != 1 || !strings.Contains(out, "UL301") {
		t.Fatalf("check must report UL301: exit %d: %s", code, out)
	}
	te.mustOK("gen", "--root", dir)
	if got, _ := os.ReadFile(md); string(got) != string(orig) {
		t.Fatal("gen did not restore the file")
	}
	// A missing UL.md is drift for gen --check, but not a UL301 problem for check.
	os.Remove(md)
	if code, out, _ := te.ul("gen", "--check", "--root", dir); code != 1 || !strings.Contains(out, "missing") {
		t.Fatalf("gen --check on a missing UL.md: exit %d: %s", code, out)
	}
	if code, out, _ := te.ul("check", "--root", dir); code != 0 {
		t.Fatalf("check on a missing UL.md: exit %d: %s", code, out)
	}
}

func TestGenIsDeterministic(t *testing.T) {
	a, b := fixture(t, "clean"), fixture(t, "clean")
	for _, d := range []string{a, b} {
		os.Remove(filepath.Join(d, "recipes/bread/UL.md"))
		newEnv(t, d).mustOK("gen", "--root", d)
	}
	x, _ := os.ReadFile(filepath.Join(a, "recipes/bread/UL.md"))
	y, _ := os.ReadFile(filepath.Join(b, "recipes/bread/UL.md"))
	if string(x) != string(y) || len(x) == 0 {
		t.Fatal("same input must give the same bytes")
	}
}

func TestReferenceResolution(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a/UL.yaml": "namespace: ex.org/a\npremise: A\nlanguage:\n  Widget:\n    desc: A widget.\n    aliases: [Gizmo]\n  User:\n    desc: Uses things.\n    see: [Widget, ex.org/b#Widget, Doohickey, ex.org/b#Doohickey]\n",
		"b/UL.yaml": "namespace: ex.org/b\npremise: B\nlanguage:\n  Widget:\n    desc: Another widget.\n  Doohickey:\n    desc: Only in b.\n    aliases: [Thing]\n",
	})
	te := newEnv(t, dir)
	r := te.report("all", "--root", dir)
	user := findWord(t, r, "ex.org/a#User")
	want := []string{"ex.org/a#Widget", "ex.org/b#Widget", "ex.org/b#Doohickey"}
	if strings.Join(user.See, ",") != strings.Join(want, ",") {
		t.Fatalf("see = %v, want %v (same namespace first, then the one namespace that has it; duplicates folded)", user.See, want)
	}
	if len(r.Problems) != 0 {
		t.Fatalf("unexpected problems %+v", r.Problems)
	}
}
