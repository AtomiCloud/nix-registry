package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file; run go test -update and review the diff\n--- got ---\n%s", name, got)
	}
}

func TestGoldenAll(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	js := strings.ReplaceAll(te.mustOK("all", "--root", dir), dir, "/ROOT")
	golden(t, "all.json", js)
	golden(t, "all.md", te.mustOK("all", "--root", dir, "--format", "md"))
	golden(t, "all-details.md", te.mustOK("all", "--root", dir, "--format", "md", "--details"))
	golden(t, "lookup-plugin.txt", te.mustOK("lookup", "plugin", "--root", dir, "--from", "plugins/pay/x.go"))
	golden(t, "lookup-plugin.md", te.mustOK("lookup", "plugin", "--root", dir, "--format", "md"))
	golden(t, "lookup-ocp.json", strings.ReplaceAll(te.mustOK("lookup", "Open-Close Principle", "--root", dir, "--format", "json"), dir, "/ROOT"))
}

// Deterministic: the same input gives the same bytes, run after run.
func TestAllIsDeterministic(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	for _, args := range [][]string{{"all"}, {"all", "--format", "md", "--details"}} {
		a := te.mustOK(append(args, "--root", dir)...)
		for i := 0; i < 5; i++ {
			if b := te.mustOK(append(args, "--root", dir)...); a != b {
				t.Fatalf("ul %v changed between runs", args)
			}
		}
	}
}

// ul gen on the clean fixture writes exactly the UL.md files the fixture commits.
func TestGoldenGen(t *testing.T) {
	dir := fixture(t, "clean")
	var mds []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, _ error) error {
		if !d.IsDir() && d.Name() == mdName {
			mds = append(mds, p)
		}
		return nil
	})
	if len(mds) != 3 {
		t.Fatalf("clean fixture should commit 3 UL.md files, has %d", len(mds))
	}
	want := map[string]string{}
	for _, p := range mds {
		b, _ := os.ReadFile(p)
		want[p] = string(b)
		os.Remove(p)
	}
	newEnv(t, dir).mustOK("gen", "--root", dir)
	for p, w := range want {
		if got, _ := os.ReadFile(p); string(got) != w {
			t.Errorf("ul gen wrote a different %s than the one committed in testdata/clean:\n%s", p, got)
		}
	}
}

func TestJSONContract(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	raw := te.mustOK("all", "--root", dir)
	for _, must := range []string{`"schema": "ul/v1"`, `"replacedBy": null`, `"overrides": null`, `"session": null`,
		`"aliases": []`, `"avoid": []`, `"see": []`, `"mentions": []`, `"problems": []`} {
		if !strings.Contains(raw, must) {
			t.Errorf("JSON lacks %s", must)
		}
	}
	for _, mustNot := range []string{": null,\n      \"aliases\"", `"aliases": null`, `"see": null`, `"mentions": null`, `"avoid": null`, `"problems": null`} {
		if strings.Contains(raw, mustNot) {
			t.Errorf("JSON has %s; empty lists must be []", mustNot)
		}
	}
	r := te.report("all", "--root", dir)
	if r.Schema != "ul/v1" || r.Root != dir || r.Session != nil {
		t.Fatalf("header: %+v", r.Schema)
	}
	// Sorted by namespace, then word; paths relative to the root.
	prev := ""
	for _, w := range r.Words {
		key := w.Namespace + "\x00" + strings.ToLower(w.Word)
		if key < prev {
			t.Errorf("words out of order at %s", w.ID)
		}
		prev = key
		if filepath.IsAbs(w.File) || w.Line < 1 {
			t.Errorf("bad file/line on %s: %s:%d", w.ID, w.File, w.Line)
		}
	}
	ocp := findWord(t, r, "durian.cloud/engineering/standards/OOP#OCP")
	if strings.Join(ocp.Breadcrumb, ">") != "durian.cloud>engineering>standards>OOP" || ocp.Status != "active" || ocp.Layer != "repo" || ocp.Line != 4 {
		t.Errorf("OCP: %+v", ocp)
	}
	fac := findWord(t, r, "durian.cloud/engineering/standards/OOP#Facade")
	if fac.Status != "retired" || fac.ReplacedBy == nil || *fac.ReplacedBy != "durian.cloud/engineering/standards/OOP#Plugin" {
		t.Errorf("Facade: %+v", fac)
	}
}

func TestMarkdownShape(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	for _, args := range [][]string{{"all", "--format", "md"}, {"all", "--format", "md", "--details"}} {
		md := te.mustOK(append(args, "--root", dir)...)
		for _, line := range strings.Split(md, "\n") {
			if strings.HasPrefix(line, "|") {
				cells := strings.Count(strings.ReplaceAll(line, `\|`, ""), "|") - 1
				if cells > 4 {
					t.Errorf("table row with %d columns: %s", cells, line)
				}
			}
		}
		if strings.Contains(md, "](") {
			t.Errorf("ul all has no links, found one:\n%s", md)
		}
		if strings.Contains(md, "### Charge") != (len(args) == 4) {
			t.Errorf("word sections must appear only with --details (%v)", args)
		}
		if !strings.Contains(md, "durian.cloud > shop > billing > pay") {
			t.Errorf("breadcrumb line missing")
		}
	}
	gen, _ := os.ReadFile(filepath.Join(dir, "plugins/pay/UL.md"))
	if !strings.Contains(string(gen), "(../../docs/domain/billing/UL.md#charge)") {
		t.Errorf("a cross-namespace reference must link to the other UL.md:\n%s", gen)
	}
}

func TestAnchorsAreGitHubStyle(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"x/UL.yaml": "namespace: ex.org/x\npremise: P\nlanguage:\n" +
		"  C++:\n    desc: A language.\n    see: [Open Close, X]\n  Open Close:\n    desc: Two words.\n  C:\n    desc: Another.\n  X:\n    desc: The title twice.\n  \"X.\":\n    desc: Slug clash with X.\n"})
	te := newEnv(t, dir)
	te.mustOK("gen", "--root", dir)
	md, _ := os.ReadFile(filepath.Join(dir, "x/UL.md"))
	s := string(md)
	for _, want := range []string{"[Open Close](#open-close)", "[X](#x-1)", "## C++\n", "## Open Close\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("UL.md lacks %q:\n%s", want, s)
		}
	}
	// "X" and "X." both slug to x (and the title is x too): x, x-1, x-2 in heading order.
	if !strings.Contains(s, "## X\n") || !strings.Contains(s, "## X.\n") {
		t.Errorf("headings missing:\n%s", s)
	}
}
