package main

// Regression tests for the CodeRabbit review of the registry PR (one per accepted finding).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// r5run is r3run with a deadline: a regression that blocks on a FIFO must fail, not hang.
func r5run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=TestMainHelper", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "PROBLEMGEN_TEST_MAIN=1")
	out, _ := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("problemgen %v did not finish in 20s", args)
	}
	return cmd.ProcessState.ExitCode(), string(out)
}

func TestR5EnabledOutputNeedsFile(t *testing.T) {
	for _, outs := range []string{"{ go: {} }", "{ typescript: { runtime: r } }", "{ record: {} }", "{ catalog: {} }"} {
		cfg := r3Svc(t, t.TempDir(), "a", "a", outs)
		code, out := r5run(t, "generate", "-c", cfg)
		if code != 2 || !strings.Contains(out, "has no file") {
			t.Errorf("%s: want exit 2 and 'has no file', got %d: %s", outs, code, out)
		}
	}
}

func TestR5ManifestPathIsGuardedBeforeRead(t *testing.T) {
	for _, kind := range []string{"fifo", "dev-zero"} {
		t.Run(kind, func(t *testing.T) {
			cfg := r3Svc(t, t.TempDir(), "a", "a", "{ typescript: { file: out/gen.ts, runtime: r } }")
			if kind == "fifo" {
				if err := syscall.Mkfifo(cfg+".outputs", 0o644); err != nil {
					t.Skip(err)
				}
			} else if err := os.Symlink("/dev/zero", cfg+".outputs"); err != nil {
				t.Skip(err)
			}
			for _, sub := range []string{"generate", "verify"} {
				code, out := r5run(t, sub, "-c", cfg)
				if code != 2 || !strings.Contains(out, "refusing") {
					t.Errorf("%s: want a refusal (exit 2), got %d: %s", sub, code, out)
				}
			}
		})
	}
}

func TestR5NestedOutputsRefusedBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	cfg := r3Svc(t, dir, "a", "a", "{ go: { file: gen/problems.go, package: gen, runtime: r }, typescript: { file: gen/problems.go/problems.ts, runtime: r } }")
	code, out := r5run(t, "generate", "-c", cfg)
	if code != 2 || !strings.Contains(out, "nested") {
		t.Errorf("want a nested-output refusal (exit 2), got %d: %s", code, out)
	}
	if _, err := os.Lstat(filepath.Join(dir, "a", "gen")); err == nil {
		t.Errorf("the refused run wrote into gen/")
	}
}

func TestR5RecordFieldAndTypeDescriptionsEmitOne(t *testing.T) {
	m, ds := loadSrc(t, r2Head+"types:\n  T: { description: type words, fields: { x: { type: string } } }\n"+
		"problems:\n  p: { title: P, status: 400, recoverable: true, detail: d, data: { f: { type: T, description: field words }, g: { type: T }, h: { type: array, description: list words, items: { type: T } } } }\n")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	rec := RenderRecordYAML(m, testCfg, "lapras", "https://errors.atomi.cloud", "")
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rec), &doc); err != nil { // yaml.v3 rejects a duplicate mapping key
		t.Fatalf("record does not decode: %v\n%s", err, rec)
	}
	if !strings.Contains(rec, "field words") || !strings.Contains(rec, "type words") {
		t.Errorf("field description wins on f, type description stays on g:\n%s", rec)
	}
}

func TestR5JSONDuplicateMemberAndNumberGrammar(t *testing.T) {
	v, err := ParseJV([]byte(`{"x":"first","x":"last"}`))
	if err != nil {
		t.Fatal(err)
	}
	if x, _ := v.Get("x"); x.GoString() != "last" {
		t.Errorf("Get(x) = %q, want the last member", x.GoString())
	}
	for _, bad := range []string{"1.", "-.5", ".5", "+1", "01", "1e", "-", "0x10", "1_0"} {
		if _, err := ParseJV([]byte(bad)); err == nil {
			t.Errorf("%q is not a JSON number but parsed", bad)
		}
	}
	for _, good := range []string{"0", "-0", "1", "-1.5", "1e5", "2E-3", "0.25e+2", "1e999"} {
		if _, err := ParseJV([]byte(good)); err != nil {
			t.Errorf("%q is a JSON number but did not parse: %v", good, err)
		}
	}
}

func TestR5FuzzDeterministicManySeeds(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		a, _ := FuzzRun(seed, 3, 5000, nil)
		b, _ := FuzzRun(seed, 3, 5000, nil)
		for i := range a {
			if a[i].YAML != b[i].YAML {
				t.Fatalf("seed %d definition %d differs between runs", seed, i)
			}
		}
	}
}

// A chain of N types that each hold two fields of the next is 2^N comparisons without a memo.
func TestR5DiffSharedTypesTerminate(t *testing.T) {
	const n = 30
	var b strings.Builder
	b.WriteString(r2Head + "types:\n")
	for i := 0; i < n; i++ {
		if i == n-1 {
			fmt.Fprintf(&b, "  T%d: { fields: { a: { type: string } } }\n", i)
			continue
		}
		fmt.Fprintf(&b, "  T%d: { fields: { a: { type: T%d }, b: { type: T%d } } }\n", i, i+1, i+1)
	}
	b.WriteString("problems:\n  p: { title: P, status: 400, recoverable: true, detail: d, data: { r: { type: T0 } } }\n")
	// Resolve without Lint: lint also walks the data schema path by path, and this test is about Diff.
	resolve := func(src string) *Module {
		f := filepath.Join(t.TempDir(), "x.yaml")
		os.WriteFile(f, []byte(src), 0o644)
		d, ds := LoadDef(f)
		if len(ds) > 0 {
			t.Fatal(ds)
		}
		m, ds := Resolve(d)
		if len(ds) > 0 {
			t.Fatal(ds)
		}
		return m
	}
	old, cur := resolve(b.String()), resolve(b.String())
	done := make(chan DiffResult, 1)
	go func() { done <- Diff(old, cur) }()
	select {
	case r := <-done:
		if len(r.Breaking) > 0 {
			t.Errorf("identical modules differ: %v", r.Breaking)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Diff on shared named types did not finish (exponential re-comparison)")
	}
	// A change at the bottom of the chain is still reported through the memo.
	cur = resolve(strings.Replace(b.String(), fmt.Sprintf("T%d: { fields: { a: { type: string } } }", n-1), fmt.Sprintf("T%d: { fields: { a: { type: integer } } }", n-1), 1))
	if r := Diff(old, cur); len(r.Breaking) == 0 {
		t.Errorf("a type change at the end of the chain was missed")
	}
}

func TestR5OracleRefusingAGoldenCaseIsAnError(t *testing.T) {
	m, ds := loadSrc(t, r2Head+"problems:\n  p: { title: P, status: 400, recoverable: true, detail: d, data: { n: { type: integer } } }\n")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	bad, _ := ParseJV([]byte(`{"n":"not a number"}`))
	for _, c := range []VCase{{Name: "c", Problem: "p", Data: bad, Golden: `{"x":1}`}, {Name: "c", Problem: "p", Data: bad, GoldLog: "log"}} {
		v := &Vectors{Portal: "https://errors.atomi.cloud", Landscape: "x", Cases: []VCase{c}}
		if _, err := Oracle(m, testCfg, v); err == nil {
			t.Errorf("the oracle refused a case with a golden and returned no error: %+v", c)
		}
	}
	v := &Vectors{Portal: "https://errors.atomi.cloud", Landscape: "x", Cases: []VCase{{Name: "c", Problem: "p", Data: bad}}}
	if exp, err := Oracle(m, testCfg, v); err != nil || exp[0].OK {
		t.Errorf("a refused case without a golden stays an ok:false row, got %+v %v", exp, err)
	}
}

func TestR5ConformanceEmitNormalisesDefinitionPath(t *testing.T) {
	dir := t.TempDir()
	cfg := r3Svc(t, dir, "a", "a", "{ go: { file: out/gen.go, package: gen, runtime: r } }")
	vec := filepath.Join(dir, "a", "vectors.json")
	os.WriteFile(vec, []byte(`{"portal":"https://errors.atomi.cloud","landscape":"x","cases":[{"name":"c","problem":"p","data":{}}]}`), 0o644)
	def := filepath.Join(dir, "a") + "/./def.yaml"
	if code, out := r5run(t, "conformance", "emit", "-c", cfg, "-def", def, "-vectors", vec, "-lang", "go"); code != 0 {
		t.Errorf("emit with a ./ spelling of a listed definition: %d %s", code, out)
	}
}

func TestR5BatchChecksForeignBodies(t *testing.T) {
	dir := t.TempDir()
	defs := filepath.Join(dir, "defs")
	os.MkdirAll(defs, 0o755)
	os.WriteFile(filepath.Join(defs, "k.problems.yaml"), []byte(r3Def), 0o644)
	os.WriteFile(filepath.Join(defs, "k.vectors.json"), []byte(`{"portal":"https://errors.atomi.cloud","landscape":"x","cases":[],"foreign":[{"name":"f1","wire":"{\"type\":\"about:blank\"}","expect":"unknown"}]}`), 0o644)
	cfg := filepath.Join(dir, "cfg.yaml")
	os.WriteFile(cfg, []byte("apiVersion: problems.atomi.cloud/v1\nkind: GeneratorConfig\nplatform: nitroso\nservice: a\nportal: https://errors.atomi.cloud\nmodules:\n  - definition: defs/k.problems.yaml\n    outputs: { go: { file: out/k.go, package: k, runtime: r } }\n"), 0o644)
	if code, out := r5run(t, "conformance", "batch-wires", "-c", cfg); code != 0 || !strings.Contains(out, `"k/f1"`) {
		t.Errorf("batch-wires must list the foreign body as k/f1: %d %s", code, out)
	}
	results := filepath.Join(dir, "results.jsonl")
	os.WriteFile(results, []byte(`{"name":"k/f1","variant":"unknown"}`+"\n"), 0o644)
	if code, out := r5run(t, "conformance", "batch-check-parse", "-c", cfg, "-results", results); code != 0 || !strings.Contains(out, "1/1") {
		t.Errorf("matching foreign variant: %d %s", code, out)
	}
	os.WriteFile(results, []byte(`{"name":"k/f1","variant":"not-problem"}`+"\n"), 0o644)
	if code, out := r5run(t, "conformance", "batch-check-parse", "-c", cfg, "-results", results); code != 1 || !strings.Contains(out, "FAIL k/f1") {
		t.Errorf("wrong foreign variant must fail: %d %s", code, out)
	}
	os.WriteFile(results, []byte(""), 0o644)
	if code, _ := r5run(t, "conformance", "batch-check-parse", "-c", cfg, "-results", results); code != 1 {
		t.Errorf("a missing foreign result must fail, got exit %d", code)
	}
}

func TestR5HarnessesCaptureBadVectorsAndQualifyNames(t *testing.T) {
	m, ds := loadSrc(t, r2Head+"problems:\n  program: { title: P, status: 400, recoverable: true, detail: d, data: { at: { type: date-time } } }\n")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	data, _ := ParseJV([]byte(`{"at":"2024-02-30T00:00:00Z"}`))
	v := &Vectors{Portal: "https://errors.atomi.cloud", Landscape: "x", Cases: []VCase{{Name: "bad-date", Problem: "program", Data: data}}}
	cfg := &Config{Platform: "nitroso", Service: "zinc"}

	cs := emitCSHarness(m, cfg, Outputs{CSharp: &CSOut{Namespace: "N.Gen", Runtime: "R"}}, v)
	if !strings.Contains(cs, "new global::N.Gen.Program(") || strings.Contains(cs, "new Program(") {
		t.Errorf("a problem named program must be written global::N.Gen.Program in the C# harness:\n%s", cs)
	}

	goH := emitGoHarness(m, cfg, Outputs{Go: &GoOut{Package: "gen", Runtime: "r"}}, v)
	if !strings.Contains(goH, "recover()") || !strings.Contains(goH, `emit("bad-date", func() gen.Problem {`) {
		t.Errorf("each Go case must be built inside a recover boundary:\n%s", goH)
	}

	ts := emitTSHarness(m, cfg, Outputs{TypeScript: &TSOut{File: "x.ts", Runtime: "r"}}, v)
	if strings.Contains(ts, "2024-02-30") || !strings.Contains(ts, "new Date(NaN)") {
		t.Errorf("an unparseable date-time must become an Invalid Date in the TS harness:\n%s", ts)
	}
}

func TestR5LibraryWithImportsFailsSchema(t *testing.T) {
	f := filepath.Join(t.TempDir(), "lib.yaml")
	os.WriteFile(f, []byte("apiVersion: problems.atomi.cloud/v1\nkind: ProblemLibrary\nname: l\nimports: [{ from: 'builtin:generic/v1' }]\nproblems: {}\n"), 0o644)
	if code, out := r5run(t, "lint", "--library", f); code == 0 {
		t.Errorf("a library with imports must not lint clean: %s", out)
	}
}

func TestR5PrivateFieldValuesAreValidated(t *testing.T) {
	m, ds := loadSrc(t, r2Head+"problems:\n  p: { title: P, status: 400, recoverable: true, detail: d, log: '{xs|count}', data: { xs: { type: array, items: { type: integer }, private: true } } }\n")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	p := m.Problems[0]
	bad, _ := ParseJV([]byte(`{"xs":["bad"]}`))
	if _, err := RenderVector(m, testCfg, "https://errors.atomi.cloud", "x", p, bad, ""); err == nil {
		t.Errorf("a private array<integer> holding a string must be refused")
	}
	good, _ := ParseJV([]byte(`{"xs":[1,2]}`))
	r, err := RenderVector(m, testCfg, "https://errors.atomi.cloud", "x", p, good, "")
	if err != nil || r.Log != "2" {
		t.Errorf("a valid private value renders: %q %v", r.Log, err)
	}
}
