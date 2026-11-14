package main

// Regression tests for the second independent review (REVIEW-2). Each names its finding.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const r2Head = "apiVersion: problems.atomi.cloud/v1\nkind: ProblemSet\nmodule: m\nversion: v1\n"

func loadSrc(t *testing.T, src string) (*Module, []Diag) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "x.yaml")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadModule(f)
}

// R2-1: a property named like a generated type (field `inner`, type array<Inner>) must not
// capture the decoder call: every expression-context type reference is fully qualified.
func TestCSharpQualifiesTypeReferences(t *testing.T) {
	m, ds := loadSrc(t, r2Head+"types:\n  Inner: { fields: { x: { type: string } } }\n  Outer: { fields: { inner: { type: array, items: { type: Inner } } } }\n"+
		"problems:\n  p: { title: P, status: 400, recoverable: true, detail: d, data: { inner: { type: array, items: { type: Inner } }, outer: { type: Outer } } }\n")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	cs := langs["csharp"].Generate(m, testCfg, Outputs{CSharp: &CSOut{Namespace: "N.Gen", Runtime: "R"}}, "x")
	if !strings.Contains(cs, "global::N.Gen.Inner.__Read(item)") {
		t.Errorf("array decoder not qualified:\n%s", cs)
	}
	for _, line := range strings.Split(cs, "\n") {
		if strings.Contains(line, " Inner.__Read(") || strings.Contains(line, ">Inner.__Read(") || strings.Contains(line, "(Inner.__Read(") {
			t.Errorf("unqualified decoder reference: %s", line)
		}
	}
}

// R2-7: a type reached only through private fields gets no wire helpers in Dart
// (an unused private helper is an analyzer warning).
func TestDartNoHelpersForPrivateOnlyTypes(t *testing.T) {
	m, ds := loadSrc(t, r2Head+"types:\n  Stage: { enum: [ok, not_ok] }\n  Spot: { fields: { x: { type: string } } }\n"+
		"problems:\n  p: { title: P, status: 400, recoverable: true, detail: d, log: '{stage|default:-} {spots|count}', data: { stage: { type: Stage, private: true, optional: true }, spots: { type: array, items: { type: Spot }, private: true } } }\n")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	dart := GenerateDart(m, testCfg, &DartOut{Runtime: "package:r/r.dart"}, "x")
	for _, h := range []string{"_decodeStage", "_decodeSpot", "_writeSpot"} {
		if strings.Contains(dart, h) {
			t.Errorf("Dart emits unused helper %s", h)
		}
	}
	if !strings.Contains(dart, "enum Stage {") || !strings.Contains(dart, "final class Spot {") {
		t.Errorf("the private-only types themselves must still be emitted")
	}
}

// R2-4: a private field's example never reaches a diagnostic, at any stage.
func TestPrivateExampleRedactedEverywhere(t *testing.T) {
	for name, field := range map[string]string{
		"out-of-range": "{ type: integer, private: true, example: 9223372036854775808 }",
		"tagged":       "{ type: integer, private: true, example: !!int PRIVATE-EXAMPLE-MARKER }",
		"infinite":     "{ type: number, private: true, example: .inf }",
		"description":  "{ type: string, private: true, description: \"PRIVATE-DESC\\u2028x\" }",
	} {
		_, ds := loadSrc(t, r2Head+"problems:\n  p: { title: P, status: 400, recoverable: true, detail: d, log: '{k}', data: { k: "+field+" } }\n")
		if len(ds) == 0 {
			t.Fatalf("%s: want a diagnostic", name)
		}
		for _, d := range ds {
			for _, secret := range []string{"9223372036854775808", "PRIVATE-EXAMPLE-MARKER", ".inf", "PRIVATE-DESC"} {
				if strings.Contains(d.Msg, secret) {
					t.Errorf("%s: diagnostic echoes private text: %s", name, d)
				}
			}
		}
	}
	// A public example is still shown (redaction is targeted).
	_, ds := loadSrc(t, r2Head+"problems:\n  p: { title: P, status: 400, recoverable: true, detail: '{k}', data: { k: { type: integer, example: 9223372036854775808 } } }\n")
	if len(ds) == 0 || !strings.Contains(ds[0].Msg, "9223372036854775808") {
		t.Errorf("public example should be shown: %v", ds)
	}
}

// R2-6: definition text with a YAML/C#/JS line break is refused (L242); the record writer
// escapes such characters anyway, and ordinary Unicode survives the record unchanged.
func TestRecordPreservesUnicode(t *testing.T) {
	m, ds := LoadModule("testdata/good/unicode-text.yaml")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	var doc struct {
		Spec struct{ Problems []map[string]any } `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(RenderRecordYAML(m, testCfg, "lapras", "https://errors.atomi.cloud", "")), &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Spec.Problems[0]["title"]; got != m.Problems[0].Title {
		t.Errorf("title changed through the record: %q -> %q", m.Problems[0].Title, got)
	}
	// Backstop: bypass lint and render a title with NEL / U+2028 directly.
	m.Problems[0].Title = "one\u0085two three"
	rec := RenderRecordYAML(m, testCfg, "lapras", "https://errors.atomi.cloud", "")
	if strings.ContainsRune(rec, 0x85) || strings.ContainsRune(rec, 0x2028) {
		t.Fatalf("record carries a raw line-break character")
	}
	if err := yaml.Unmarshal([]byte(rec), &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Spec.Problems[0]["title"]; got != m.Problems[0].Title {
		t.Errorf("escaped title does not read back: %q", got)
	}
}

// R2-2 / R2-3: each config owns exactly the files its manifest lists. Another config's
// outputs in the same directory are never reported; a dropped catalog output is found;
// generate removes what it no longer plans; a module without outputs is refused.
func TestManifestOwnership(t *testing.T) {
	dir := t.TempDir()
	def := r2Head + "problems:\n  p: { title: P, status: 400, recoverable: true, detail: d }\n"
	cfg := func(name, outputs string) string {
		return "apiVersion: problems.atomi.cloud/v1\nkind: GeneratorConfig\nplatform: nitroso\nservice: " + name +
			"\nportal: https://errors.atomi.cloud\nmodules:\n  - definition: def.yaml\n    outputs: " + outputs + "\n"
	}
	for _, n := range []string{"a", "b"} {
		os.MkdirAll(filepath.Join(dir, n), 0o755)
		os.WriteFile(filepath.Join(dir, n, "def.yaml"), []byte(def), 0o644)
		os.WriteFile(filepath.Join(dir, n, "cfg.yaml"), []byte(cfg(n,
			"{ typescript: { file: ../shared/"+n+".ts, runtime: r }, catalog: { file: ../shared/"+n+".json } }")), 0o644)
	}
	run := func(args ...string) (int, string) {
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=TestMainHelper", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "PROBLEMGEN_TEST_MAIN=1")
		out, _ := cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), string(out)
	}
	a, b := filepath.Join(dir, "a", "cfg.yaml"), filepath.Join(dir, "b", "cfg.yaml")
	for _, c := range []string{a, b} {
		if code, out := run("generate", "-c", c); code != 0 {
			t.Fatalf("generate %s: %d %s", c, code, out)
		}
	}
	for _, c := range []string{a, b} {
		if code, out := run("verify", "-c", c); code != 0 {
			t.Errorf("verify %s claims another config's files: %d %s", c, code, out)
		}
	}
	// Drop a's catalog output: verify reports the stale JSON. A catalog has no ownership
	// header, so generate never deletes it (review 3): it reports it, keeps it listed, and
	// verify keeps failing until a person removes it. b is untouched throughout.
	os.WriteFile(a, []byte(cfg("a", "{ typescript: { file: ../shared/a.ts, runtime: r } }")), 0o644)
	if code, out := run("verify", "-c", a); code != 1 || !strings.Contains(out, "stale generated file") || !strings.Contains(out, "a.json") {
		t.Errorf("verify missed the stale catalog: %d %s", code, out)
	}
	if code, out := run("generate", "-c", a); code != 1 || !strings.Contains(out, "not removing") || !strings.Contains(out, "catalog") {
		t.Errorf("generate must refuse to delete a catalog: %d %s", code, out)
	}
	if code, out := run("verify", "-c", a); code != 1 || !strings.Contains(out, "a.json") {
		t.Errorf("the refused stale catalog was forgotten (R3-4): %d %s", code, out)
	}
	os.Remove(filepath.Join(dir, "shared", "a.json"))
	if code, out := run("generate", "-c", a); code != 0 {
		t.Errorf("generate after removing the catalog by hand: %d %s", code, out)
	}
	// Drop a's TypeScript output too: an unchanged, contained, owned code file is removed.
	os.WriteFile(a, []byte(cfg("a", "{ record: { file: ../shared/a.record.yaml } }")), 0o644)
	if code, out := run("generate", "-c", a); code != 0 || !strings.Contains(out, "removed stale") {
		t.Errorf("generate did not remove its own stale TS file: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "shared", "a.ts")); !os.IsNotExist(err) {
		t.Errorf("a.ts still present")
	}
	for _, f := range []string{"b.ts", "b.json"} {
		if _, err := os.Stat(filepath.Join(dir, "shared", f)); err != nil {
			t.Errorf("b's %s was touched: %v", f, err)
		}
	}
	for _, c := range []string{a, b} {
		if code, out := run("verify", "-c", c); code != 0 {
			t.Errorf("verify %s after regenerate: %d %s", c, code, out)
		}
	}
	// Outputless module.
	os.WriteFile(a, []byte(cfg("a", "{}")), 0o644)
	if code, out := run("verify", "-c", a); code != 2 || !strings.Contains(out, "has no outputs") {
		t.Errorf("outputless module accepted: %d %s", code, out)
	}
}
