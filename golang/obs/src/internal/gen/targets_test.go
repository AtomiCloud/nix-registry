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
