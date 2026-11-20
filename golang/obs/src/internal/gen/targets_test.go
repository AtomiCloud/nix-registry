package gen_test

import (
	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutputTargetClashesAcrossLanguages(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"go", "bun", "dotnet", "dart"} {
		t.Run(lang, func(t *testing.T) {
			a := &contract.Contract{File: filepath.Join(root, "Orders/observability/contract.yaml"), Service: "shop", Generate: []contract.Target{{Lang: "go", Out: "gen"}}}
			b := &contract.Contract{File: filepath.Join(root, "Friends/observability/contract.yaml"), Service: "shop", Generate: []contract.Target{{Lang: lang, Out: "../Orders/gen"}}}
			got := gen.TargetClashes([]*contract.Contract{a, b})
			if len(got) != 1 || !strings.Contains(got[0], "generate-out-clash") {
				t.Fatalf("%s out collision: %v", lang, got)
			}
			b.Generate[0].Out = "own-gen"
			if got := gen.TargetClashes([]*contract.Contract{a, b}); len(got) != 0 {
				t.Fatalf("distinct out folders: %v", got)
			}
		})
	}
	if err := os.MkdirAll(filepath.Join(root, "Orders/gen"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Friends"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "Orders/gen"), filepath.Join(root, "Friends/alias")); err != nil {
		t.Fatal(err)
	}
	a := &contract.Contract{File: filepath.Join(root, "Orders/observability/contract.yaml"), Generate: []contract.Target{{Lang: "go", Out: "gen"}}}
	b := &contract.Contract{File: filepath.Join(root, "Friends/observability/contract.yaml"), Generate: []contract.Target{{Lang: "bun", Out: "alias"}}}
	if got := gen.TargetClashes([]*contract.Contract{a, b}); len(got) != 1 {
		t.Fatalf("symlink output alias must clash: %v", got)
	}
}

func TestDotnetNamespaceSpelling(t *testing.T) {
	for _, valid := range []string{"Shop.Signals", "Shop.@Signals", "@class.Signals", "Shop.Σήματα"} {
		canonical, err := gen.CanonicalDotnetNamespace(valid)
		if err != nil {
			t.Errorf("valid %q: %v", valid, err)
		}
		if valid == "Shop.@Signals" && canonical != "Shop.Signals" {
			t.Errorf("escaped canonical namespace = %q", canonical)
		}
	}
	for _, invalid := range []string{"Shop . Signals", "Shop..Signals", "Shop.@", "Shop.@@Signals", "Shop.class", "Shop.9Signals", "Shop.Signals; class Injected {}"} {
		if _, err := gen.CanonicalDotnetNamespace(invalid); err == nil {
			t.Errorf("invalid namespace accepted: %q", invalid)
		}
	}
	c := &contract.Contract{File: "observability/contract.yaml", Service: "shop"}
	if _, err := gen.Generate("dotnet", c, gen.Options{Namespace: "Shop . Signals"}); err == nil || !strings.Contains(err.Error(), "namespace:") || !strings.Contains(err.Error(), "observability/contract.yaml") {
		t.Fatalf("direct generation must name key and contract: %v", err)
	}
	a := &contract.Contract{File: "Orders/observability/contract.yaml", Generate: []contract.Target{{Lang: "dotnet", Out: "orders", Module: "Shop.Signals"}}}
	b := &contract.Contract{File: "Friends/observability/contract.yaml", Generate: []contract.Target{{Lang: "dotnet", Out: "friends", Module: "Shop.@Signals"}}}
	if got := gen.TargetClashes([]*contract.Contract{a, b}); len(got) != 1 || !strings.Contains(got[0], "generate-namespace-clash") {
		t.Fatalf("canonical namespace collision: %v", got)
	}
}

func TestDotnetNamespaceLeadingFormatCharacter(t *testing.T) {
	for _, namespace := range []string{"Shop.\u200bSignals", "Shop.@\u200bSignals"} {
		if _, err := gen.CanonicalDotnetNamespace(namespace); err == nil {
			t.Errorf("leading U+200B accepted: %q", namespace)
		}
		c := &contract.Contract{File: "observability/contract.yaml", Service: "shop"}
		if _, err := gen.Generate("dotnet", c, gen.Options{Namespace: namespace}); err == nil {
			t.Errorf("generator accepted leading U+200B: %q", namespace)
		}
	}
}
