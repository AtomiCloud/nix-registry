package main

// Generative test (SPIKE/fuzz, in-process part): for 300 seeds, the first lint-clean random
// definition generates Go, TypeScript, C#, Dart, the record and the catalog without panic,
// its vectors render through the oracle, and the Go output parses and type-checks with no
// error at all. The runtime package is type-checked from ../runtime/go when that directory
// exists; otherwise (nix sandbox) an empty stand-in is imported and only errors that cannot
// come from the missing runtime are counted. The compilers of the other three languages run
// in fuzz/run-fuzz.sh.

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const fuzzRT = "example.com/rt"

type fuzzImporter struct {
	std  types.Importer
	once sync.Once
	rt   *types.Package
	real bool
}

func (f *fuzzImporter) Import(path string) (*types.Package, error) {
	if path != fuzzRT {
		return f.std.Import(path)
	}
	f.once.Do(func() {
		files, _ := filepath.Glob("../runtime/go/*.go")
		fset := token.NewFileSet()
		var asts []*ast.File
		for _, p := range files {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			a, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				asts = nil
				break
			}
			asts = append(asts, a)
		}
		if len(asts) > 0 {
			conf := types.Config{Importer: f.std}
			if pkg, err := conf.Check(fuzzRT, fset, asts, nil); err == nil {
				f.rt, f.real = pkg, true
				return
			}
		}
		f.rt = types.NewPackage(fuzzRT, "rt")
		f.rt.MarkComplete()
	})
	return f.rt, nil
}

func fuzzTypeCheckGo(imp *fuzzImporter, src string) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gen.go", src, parser.SkipObjectResolution)
	if err != nil {
		return "", fmt.Errorf("generated Go does not parse: %v", err)
	}
	var msgs []string
	conf := types.Config{
		Importer: imp,
		Error: func(err error) {
			if !imp.real && strings.Contains(err.Error(), "rt.") {
				return // the stand-in runtime is empty
			}
			msgs = append(msgs, err.Error())
		},
	}
	conf.Check("p", fset, []*ast.File{file}, nil)
	return strings.Join(msgs, "\n"), nil
}

func TestFuzzDefinitionsGenerate(t *testing.T) {
	seeds := 300
	if testing.Short() {
		seeds = 30
	}
	imp := &fuzzImporter{std: importer.Default()}
	cfg := &Config{Platform: "123", Service: "yes", Portal: "https://errors.example.com"}
	refused := 0
	for seed := uint64(1); seed <= uint64(seeds); seed++ {
		kept, st := FuzzRun(seed, 1, 5000, nil)
		if len(kept) != 1 {
			t.Fatalf("seed %d: no lint-clean definition in %d candidates", seed, st.Candidates)
		}
		refused += st.Refused
		k := kept[0]
		fail := func(format string, a ...any) {
			t.Errorf("seed %d candidate %d: %s\n--- definition ---\n%s", seed, k.Candidate, fmt.Sprintf(format, a...), k.YAML)
		}
		var goSrc string
		func() {
			defer func() {
				if r := recover(); r != nil {
					fail("generator panicked: %v", r)
				}
			}()
			goSrc = GenerateGo(k.Module, cfg, &GoOut{Package: "p", Runtime: fuzzRT}, "x.yaml")
			o := Outputs{
				TypeScript: &TSOut{Runtime: "@atomicloud/problemrt"},
				CSharp:     &CSOut{Namespace: "Fuzz.Gen", Runtime: "AtomiCloud.Problems.Runtime"},
				Dart:       &DartOut{Runtime: "package:problemrt/problemrt.dart"},
			}
			for _, l := range []string{"typescript", "csharp", "dart"} {
				if langs[l].Generate(k.Module, cfg, o, "x.yaml") == "" {
					fail("%s output is empty", l)
				}
			}
			_ = RenderRecordYAML(k.Module, cfg, "lapras", "x.yaml")
			_ = RenderRecordYAML(k.Module, cfg, "", "x.yaml")
			_ = RenderCatalogJSON(k.Module, cfg)
			// Every vector renders (the data is valid by construction).
			vf := filepath.Join(t.TempDir(), "v.json")
			if err := os.WriteFile(vf, []byte(fzVectors(seed, k.Candidate, k.Module, "lapras")), 0o644); err != nil {
				t.Fatal(err)
			}
			v, err := LoadVectors(vf)
			if err != nil {
				fail("vectors do not load: %v", err)
				return
			}
			exp, err := Oracle(k.Module, cfg, v)
			if err != nil {
				fail("oracle: %v", err)
				return
			}
			for _, e := range exp {
				if !e.OK {
					fail("vector %s refused by the oracle: %s", e.Name, e.Error)
				}
			}
		}()
		if goSrc == "" {
			continue
		}
		errs, err := fuzzTypeCheckGo(imp, goSrc)
		if err != nil {
			fail("%v", err)
		} else if errs != "" {
			fail("generated Go does not type-check:\n%s", errs)
		}
	}
	t.Logf("%d seeds, %d refused candidates on the way; runtime type-checked from source: %v", seeds, refused, imp.real)
}

// The generator is deterministic per seed (the batches and repros depend on it).
func TestFuzzDeterministic(t *testing.T) {
	a, _ := FuzzRun(42, 3, 5000, nil)
	b, _ := FuzzRun(42, 3, 5000, nil)
	for i := range a {
		if a[i].YAML != b[i].YAML || fzVectors(42, a[i].Candidate, a[i].Module, "x") != fzVectors(42, b[i].Candidate, b[i].Module, "x") {
			t.Fatalf("seed 42 definition %d differs between runs", i)
		}
	}
}

// The type checker this test relies on does fail on a known-bad output (a planted L230 file
// whose Go output redeclares a name), so a pass above is not a silent no-op.
func TestFuzzTypeCheckIsLive(t *testing.T) {
	m, _ := LoadModule("testdata/bad/L230-go-new-prefix.yaml")
	if m == nil {
		t.Skip("planted file missing")
	}
	imp := &fuzzImporter{std: importer.Default()}
	errs, err := fuzzTypeCheckGo(imp, GenerateGo(m, testCfg, &GoOut{Package: "p", Runtime: fuzzRT}, "x"))
	if err != nil || errs == "" {
		t.Fatalf("expected type errors for a colliding definition, got none (err %v)", err)
	}
}
