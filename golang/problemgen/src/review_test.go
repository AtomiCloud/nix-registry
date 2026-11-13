package main

// Regression tests for the first independent review (REVIEW-1). Each names its finding.

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var testCfg = &Config{Platform: "nitroso", Service: "zinc", Portal: "https://errors.atomi.cloud"}

// Files in testdata/good must lint clean (finding 8: `only` takes the type closure).
func TestGoodFiles(t *testing.T) {
	files, _ := filepath.Glob("testdata/good/*.yaml")
	if len(files) < 3 {
		t.Fatalf("expected the good files, found %d", len(files))
	}
	for _, f := range files {
		if _, ds := LoadModule(f); len(ds) > 0 {
			t.Errorf("%s: want no diagnostics, got %v", f, ds)
		}
	}
	m, _ := LoadModule("testdata/good/only-closure-reaches-type.yaml")
	if m.TypeByName("ValidationIssue") == nil {
		t.Errorf("only: [validation_error] must import ValidationIssue")
	}
}

// Finding 4: every planted Go collision really is a compile error in the generated Go
// (so the lint refusal is not hypothetical), shown by type-checking the generated file.
func TestGoCollisionsAreCompileErrors(t *testing.T) {
	files, _ := filepath.Glob("testdata/bad/L230-go-*.yaml")
	if len(files) < 5 {
		t.Fatalf("expected the Go collision files, found %d", len(files))
	}
	for _, f := range files {
		m, ds := LoadModule(f)
		if m == nil || !hasCode(ds, "L230") {
			t.Fatalf("%s: want L230, got %v", f, ds)
		}
		errs := typeCheckGo(t, GenerateGo(m, testCfg, &GoOut{Package: "p", Runtime: "example.com/rt"}, "x"))
		if !strings.Contains(errs, "redeclared") && !strings.Contains(errs, "field and method with the same name") &&
			!strings.Contains(errs, "already declared") && !strings.Contains(errs, "duplicate") {
			t.Errorf("%s: generated Go has no declaration conflict; type errors: %s", f, errs)
		}
	}
	// And a clean definition type-checks without any declaration conflict.
	m, ds := LoadModule("testdata/good/conformance.yaml")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	errs := typeCheckGo(t, GenerateGo(m, testCfg, &GoOut{Package: "p", Runtime: "example.com/rt"}, "x"))
	if strings.Contains(errs, "redeclared") || strings.Contains(errs, "same name") {
		t.Errorf("conformance definition: unexpected conflicts: %s", errs)
	}
}

// typeCheckGo returns the declaration-level type errors of a generated file. The runtime
// import cannot be resolved here, so errors that mention rt are ignored.
func typeCheckGo(t *testing.T, src string) string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gen.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("generated Go does not parse: %v", err)
	}
	var msgs []string
	conf := types.Config{
		Importer: fakeImporter{importer.Default()},
		Error: func(err error) {
			if !strings.Contains(err.Error(), "rt.") && !strings.Contains(err.Error(), "\"example.com/rt\"") {
				msgs = append(msgs, err.Error())
			}
		},
	}
	conf.Check("p", fset, []*ast.File{file}, nil)
	return strings.Join(msgs, "; ")
}

type fakeImporter struct{ std types.Importer }

func (f fakeImporter) Import(path string) (*types.Package, error) {
	if path == "example.com/rt" {
		return types.NewPackage(path, "rt"), nil
	}
	return f.std.Import(path)
}

func hasCode(ds []Diag, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

// Finding 2: the status is the validated integer, never a reparse of its spelling.
func TestStatusIsExact(t *testing.T) {
	m, ds := LoadModule("testdata/good/private-optional.yaml")
	if len(ds) > 0 || m.Problems[0].Status != 400 {
		t.Fatalf("status: %v %v", m.Problems[0].Status, ds)
	}
}

// Finding 3: every string-valued record field stays a string for a YAML reader, even
// values that look like numbers or YAML 1.1 booleans; the Helm form validates the landscape.
func TestRecordQuotesStrings(t *testing.T) {
	src := "apiVersion: problems.atomi.cloud/v1\nkind: ProblemSet\nmodule: \"456\"\nversion: v1\nproblems:\n  \"yes\": { title: \"on\", status: 400, recoverable: true, detail: d }\n"
	f := filepath.Join(t.TempDir(), "x.yaml")
	os.WriteFile(f, []byte(src), 0o644)
	m, ds := LoadModule(f)
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	cfg := &Config{Platform: "123", Service: "007", Portal: "https://errors.atomi.cloud"}
	rec := RenderRecordYAML(m, cfg, "no", "")
	var doc struct {
		Metadata map[string]any `yaml:"metadata"`
		Spec     struct {
			Platform, Service, Module, Landscape, Version any
			Problems                                      []map[string]any
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(rec), &doc); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{
		"metadata.name": doc.Metadata["name"], "metadata.namespace": doc.Metadata["namespace"],
		"spec.platform": doc.Spec.Platform, "spec.service": doc.Spec.Service, "spec.module": doc.Spec.Module,
		"spec.landscape": doc.Spec.Landscape, "spec.version": doc.Spec.Version,
		"problems[0].id": doc.Spec.Problems[0]["id"], "problems[0].title": doc.Spec.Problems[0]["title"],
		"problems[0].type": doc.Spec.Problems[0]["type"],
	} {
		if _, ok := v.(string); !ok {
			t.Errorf("%s = %#v (%T), want a string", name, v, v)
		}
	}
	for _, line := range strings.Split(rec, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "id:") && !strings.Contains(line, `"yes"`) {
			t.Errorf("id not quoted: %s", line)
		}
	}
	helm := RenderRecordYAML(m, cfg, "", "")
	if !strings.Contains(helm, `regexMatch "`+landscapeRe+`" $landscape`) || !strings.Contains(helm, "fail ") {
		t.Errorf("helm form does not validate the landscape:\n%s", helm)
	}
	for _, l := range []string{"", "Lapras", "a_b", "-a", strings.Repeat("a", 64), "a/b"} {
		if ValidLandscape(l) {
			t.Errorf("landscape %q accepted", l)
		}
	}
	for _, l := range []string{"lapras", "a", "a-1", strings.Repeat("a", 63)} {
		if !ValidLandscape(l) {
			t.Errorf("landscape %q refused", l)
		}
	}
}

// Finding 3 (config) and the empty-modules hole in verify.
func TestConfigBounds(t *testing.T) {
	dir := t.TempDir()
	write := func(platform, modules string) error {
		p := filepath.Join(dir, "problemgen.yaml")
		os.WriteFile(p, []byte("apiVersion: problems.atomi.cloud/v1\nkind: GeneratorConfig\nplatform: "+platform+
			"\nservice: zinc\nportal: https://errors.atomi.cloud\nmodules:"+modules+"\n"), 0o644)
		_, err := LoadConfig(p)
		return err
	}
	one := "\n  - definition: x.yaml\n    outputs: { catalog: { file: c.json } }"
	if err := write(strings.Repeat("a", 63), one); err != nil {
		t.Errorf("63-char platform refused: %v", err)
	}
	if err := write(strings.Repeat("a", 64), one); err == nil {
		t.Errorf("64-char platform accepted")
	}
	if err := write("nitroso", " []"); err == nil || !strings.Contains(err.Error(), "modules is empty") {
		t.Errorf("empty modules accepted: %v", err)
	}
}

// Finding 7: an optional private input can be supplied in C#; private metadata stays out
// of Dart source; lint never echoes a private example.
func TestPrivateInputs(t *testing.T) {
	m, ds := LoadModule("testdata/good/private-optional.yaml")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	cs := langs["csharp"].Generate(m, testCfg, Outputs{CSharp: &CSOut{Namespace: "N", Runtime: "R"}}, "x")
	if !strings.Contains(cs, "public P(long count, string? secret = null) {") || !strings.Contains(cs, "  Secret = secret;") {
		t.Errorf("C# constructor cannot take the optional private input:\n%s", cs)
	}
	dart := GenerateDart(m, testCfg, &DartOut{Runtime: "package:r/r.dart"}, "x")
	for _, s := range []string{"SECRET-DESCRIPTION", "SECRET-DEPRECATION"} {
		if strings.Contains(dart, s) {
			t.Errorf("Dart source carries private metadata %s", s)
		}
	}
	src := "apiVersion: problems.atomi.cloud/v1\nkind: ProblemSet\nmodule: m\nversion: v1\nproblems:\n  p: { title: P, status: 400, recoverable: true, detail: d, log: '{k}', data: { k: { type: integer, private: true, example: hunter2 } } }\n"
	f := filepath.Join(t.TempDir(), "x.yaml")
	os.WriteFile(f, []byte(src), 0o644)
	_, ds = LoadModule(f)
	if !hasCode(ds, "L206") {
		t.Fatalf("want L206, got %v", ds)
	}
	for _, d := range ds {
		if strings.Contains(d.Msg, "hunter2") {
			t.Errorf("diagnostic echoes a private example: %s", d)
		}
	}
}

// Diagnostics come out in one order every run (no map iteration order leaks).
func TestDiagnosticOrderIsStable(t *testing.T) {
	src := "apiVersion: problems.atomi.cloud/v1\nkind: ProblemSet\nmodule: m\nversion: v1\nproblems:\n  p: { title: P, status: 400, recoverable: true, detail: '{a} {b} {c}', log: '{x} {y} {z}' }\n  q: { title: Q, status: 400, recoverable: true, detail: '{d}', log: '{e}' }\n"
	f := filepath.Join(t.TempDir(), "x.yaml")
	os.WriteFile(f, []byte(src), 0o644)
	_, first := LoadModule(f)
	if len(first) < 8 {
		t.Fatalf("want many diagnostics, got %v", first)
	}
	for i := 0; i < 50; i++ {
		_, ds := LoadModule(f)
		for j := range ds {
			if ds[j] != first[j] {
				t.Fatalf("run %d: order changed at %d: %v vs %v", i, j, ds[j], first[j])
			}
		}
	}
}

// Findings 5 and 6: diff --git reads the old definition and its imports from the ref, and
// every git failure other than "path absent at that commit" fails closed.
func TestDiffGitSnapshot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required for this test (nativeCheckInputs)")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@e", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	lib := func(status string) string {
		return "apiVersion: problems.atomi.cloud/v1\nkind: ProblemLibrary\nname: shared\nproblems:\n  bad_input: { title: Bad Input, status: " + status + ", recoverable: true, detail: d }\n"
	}
	set := "apiVersion: problems.atomi.cloud/v1\nkind: ProblemSet\nmodule: notes\nversion: v1\nimports:\n  - { from: ./shared.yaml }\nproblems: {}\n"
	os.WriteFile(filepath.Join(dir, "shared.yaml"), []byte(lib("422")), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.yaml"), []byte(set), 0o644)
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	os.WriteFile(filepath.Join(dir, "shared.yaml"), []byte(lib("409")), 0o644) // notes.yaml unchanged

	run := func(args ...string) (int, string) {
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=TestMainHelper", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "PROBLEMGEN_TEST_MAIN=1")
		out, _ := cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), string(out)
	}
	notes := filepath.Join(dir, "notes.yaml")
	if code, out := run("diff", "--git", "HEAD", notes); code != 1 || !strings.Contains(out, "D102") {
		t.Errorf("imported status change at HEAD: want exit 1 with D102, got %d: %s", code, out)
	}
	if code, out := run("diff", "--git", "no-such-ref", notes); code != 2 || !strings.Contains(out, "does not name a commit") {
		t.Errorf("missing ref: want exit 2, got %d: %s", code, out)
	}
	if code, out := run("diff", "--git", "--upload-pack=x", notes); code != 2 {
		t.Errorf("option-like ref: want exit 2, got %d: %s", code, out)
	}
	os.WriteFile(filepath.Join(dir, "fresh.yaml"), []byte(strings.Replace(set, "module: notes", "module: fresh", 1)), 0o644)
	if code, out := run("diff", "--git", "HEAD", filepath.Join(dir, "fresh.yaml")); code != 0 || !strings.Contains(out, "new file") {
		t.Errorf("file absent at ref: want exit 0 (new file), got %d: %s", code, out)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".problemgen") {
			t.Errorf("diff left a temporary file: %s", e.Name())
		}
	}
}

// TestMainHelper runs main() in a child process for the CLI tests above.
func TestMainHelper(t *testing.T) {
	if os.Getenv("PROBLEMGEN_TEST_MAIN") != "1" {
		t.Skip("helper process only")
	}
	for i, a := range os.Args {
		if a == "--" {
			os.Args = append([]string{"problemgen"}, os.Args[i+1:]...)
			break
		}
	}
	main()
	os.Exit(0)
}
