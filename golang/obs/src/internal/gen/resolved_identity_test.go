package gen_test

import (
	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageIdentityResolvedAlias(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing-tail", false: "existing"}[missing], func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "shared"), 0755); err != nil {
				t.Fatal(err)
			}
			if !missing {
				if err := os.Mkdir(filepath.Join(root, "shared/gen"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			for file, body := range map[string]string{"go.mod": "module example.com/root\n", "shared/go.mod": "module example.com/shared\n"} {
				if err := os.WriteFile(filepath.Join(root, file), []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("shared/gen", filepath.Join(root, "alias")); err != nil {
				t.Fatal(err)
			}
			id, err := gen.PackageIdentity(root, contract.Target{Lang: "go", Out: "alias"})
			if err != nil || id != "example.com/shared" {
				t.Fatalf("resolved nested module: id=%q err=%v", id, err)
			}
		})
	}
}
func TestPackageIdentityNoGitFeature(t *testing.T) {
	top := t.TempDir()
	feature := filepath.Join(top, "features/Orders")
	if err := os.MkdirAll(feature, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, "go.mod"), []byte("module example.com/workspace\n"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := gen.PackageIdentity(feature, contract.Target{Lang: "go", Out: "gen"})
	if err != nil || id != "example.com/workspace" {
		t.Fatalf("no-Git feature identity: %q %v", id, err)
	}
}
func TestNamespaceClashSuggestsOverride(t *testing.T) {
	a := &contract.Contract{File: "Orders/observability/contract.yaml", Generate: []contract.Target{{Lang: "dotnet", Out: "a", Module: "Shop.Signals"}}}
	b := &contract.Contract{File: "Billing/observability/contract.yaml", Generate: []contract.Target{{Lang: "dotnet", Out: "b", Module: "Shop.Signals"}}}
	issues := gen.TargetClashes([]*contract.Contract{a, b})
	if len(issues) != 1 || !strings.Contains(issues[0], "set namespace: on one target") {
		t.Fatalf("missing remedy: %v", issues)
	}
}

func TestGeneratedDestinationNamesMatchGenerators(t *testing.T) {
	for _, lang := range gen.Langs() {
		t.Run(lang, func(t *testing.T) {
			root := t.TempDir()
			c := &contract.Contract{File: filepath.Join(root, "observability/contract.yaml"), Service: "s", Generate: []contract.Target{{Lang: lang, Out: "gen"}}}
			fs, err := gen.Generate(lang, c, gen.Options{PackageID: "s"})
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range fs {
				p := filepath.Join(root, "gen", file.Path)
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("missing-target", p); err != nil {
					t.Fatal(err)
				}
				issues := gen.TargetClashes([]*contract.Contract{c})
				if len(issues) == 0 || !strings.Contains(strings.Join(issues, "\n"), file.Path) {
					t.Fatalf("%s output %s is missing from destination validation: %v", lang, file.Path, issues)
				}
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
