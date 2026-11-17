// Package gentest is the golden-file test helper every language generator uses.
//
// The golden files are the fixture consumer repo's committed generated files:
// tests/fixtures/gen/good/gen/<lang dir>/ (the `generate:` targets of the fixture contract). A
// generator's test calls Golden(t, "<lang>"); OBS_UPDATE_GOLDEN=1 rewrites them. The test is
// skipped when the tests/ folder is not there (a build from src/ alone); the nix check runs the
// same comparison through `obs gen --check` in tests/run.sh.
package gentest

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	"github.com/AtomiCloud/obs/internal/testfix"
)

// Fixture finds the fixture contract by walking up from the working directory.
func Fixture(t testing.TB) string { t.Helper(); return testfix.GoodContract(t) }

// Golden generates lang from the fixture contract and compares every file with the committed one.
func Golden(t *testing.T, lang string) {
	t.Helper()
	cpath := Fixture(t)
	c, err := contract.LoadValid(cpath)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(cpath)) // folder holding observability/
	// This fixture is a standalone consumer repository, embedded inside obs's own
	// Git repository for storage. Its golden identity is relative to that consumer.
	c.File = "observability/contract.yaml"
	var target *contract.Target
	for i := range c.Generate {
		if c.Generate[i].Lang == lang {
			target = &c.Generate[i]
		}
	}
	if target == nil {
		t.Fatalf("fixture contract has no generate target for %s", lang)
	}
	files, err := gen.Generate(lang, c, gen.Options{Package: target.Package, Namespace: target.Module, Interfaces: target.Interfaces})
	if err != nil {
		t.Fatal(err)
	}
	// Determinism: a second run gives the same bytes.
	again, _ := gen.Generate(lang, c, gen.Options{Package: target.Package, Namespace: target.Module, Interfaces: target.Interfaces})
	for i := range files {
		if !bytes.Equal(files[i].Content, again[i].Content) {
			t.Fatalf("%s: two runs differ", files[i].Path)
		}
	}
	update := os.Getenv("OBS_UPDATE_GOLDEN") == "1"
	dir := filepath.Join(root, filepath.FromSlash(target.Out))
	want := map[string]bool{}
	for _, f := range files {
		want[f.Path] = true
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if update {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, f.Content, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		have, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("golden file missing: %s (OBS_UPDATE_GOLDEN=1 writes it)", p)
			continue
		}
		if !bytes.Equal(have, f.Content) {
			t.Errorf("%s differs from the generated file (OBS_UPDATE_GOLDEN=1 rewrites it)", p)
		}
	}
	// A golden file the generator no longer writes is stale.
	var have []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			r, _ := filepath.Rel(dir, p)
			have = append(have, filepath.ToSlash(r))
		}
		return nil
	})
	sort.Strings(have)
	for _, h := range have {
		if !want[h] && !update {
			t.Errorf("golden file %s/%s is not written by the %s generator any more", target.Out, h, lang)
		}
	}
}
