package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedFileSymlinkRefused(t *testing.T) {
	for _, dangling := range []bool{true, false} {
		t.Run(map[bool]string{true: "dangling", false: "existing"}[dangling], func(t *testing.T) {
			root := repo(t, map[string]string{"observability/contract.yaml": "service: s\ngenerate: [{lang: go, out: gen, package_id: example.com/s}]\n"})
			if err := os.Mkdir(filepath.Join(root, "gen"), 0755); err != nil {
				t.Fatal(err)
			}
			if !dangling {
				if err := os.WriteFile(filepath.Join(root, "untouched.go"), []byte("keep"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("../untouched.go", filepath.Join(root, "gen/signals.go")); err != nil {
				t.Fatal(err)
			}
			if rc, out := obs("gen", "--dir", filepath.Join(root, "observability")); rc == 0 || !strings.Contains(out, "gen/signals.go") {
				t.Fatalf("generated-file symlink must be refused: rc=%d %s", rc, out)
			}
			if _, err := os.Stat(filepath.Join(root, "observability/contract/events.json")); !os.IsNotExist(err) {
				t.Fatal("wrote before symlink refusal")
			}
			if !dangling {
				if b, _ := os.ReadFile(filepath.Join(root, "untouched.go")); string(b) != "keep" {
					t.Fatal("followed output symlink")
				}
			}
		})
	}
}
func TestFeatureOutputAliasesMissingLeaf(t *testing.T) {
	root := repo(t, map[string]string{
		"Orders/observability/contract.yaml":  "service: s\ngenerate: [{lang: go, out: alias, package_id: s}]\n",
		"Billing/observability/contract.yaml": "service: s\ngenerate: [{lang: go, out: alias, package_id: s}]\n",
	})
	if err := os.Mkdir(filepath.Join(root, "shared"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"Orders", "Billing"} {
		if err := os.Symlink("../shared/gen", filepath.Join(root, f, "alias")); err != nil {
			t.Fatal(err)
		}
	}
	rc, out := obs("gen", "--dir", filepath.Join(root, "Orders/observability"))
	if rc == 0 || !strings.Contains(out, "Orders/observability/contract.yaml") || !strings.Contains(out, "Billing/observability/contract.yaml") {
		t.Fatalf("aliased outputs must name both contracts: rc=%d %s", rc, out)
	}
	if _, err := os.Stat(filepath.Join(root, "Orders/observability/contract/events.json")); !os.IsNotExist(err) {
		t.Fatal("wrote before cross-contract alias refusal")
	}
}
func TestMultipleProjectRootsOfSameService(t *testing.T) {
	root := repo(t, map[string]string{
		"src/Orders/Orders.csproj": "<Project/>", "src/Billing/Billing.csproj": "<Project/>",
		"src/Orders/observability/contract.yaml":  "service: shop\nlogs: {Failed: {event: orders.failed, level: error}}\ngenerate: [{lang: dotnet, out: gen, package_id: Shop}]\n",
		"src/Billing/observability/contract.yaml": "service: shop\nlogs: {Failed: {event: billing.failed, level: error}}\ngenerate: [{lang: dotnet, out: gen, package_id: Shop}]\n",
	})
	for _, feature := range []string{"Orders", "Billing"} {
		dir := filepath.Join(root, "src", feature, "observability")
		for _, cmd := range []string{"check", "gen"} {
			if rc, out := obs(cmd, "--dir", dir); rc != 0 {
				t.Fatalf("multiple root defaults %s: rc=%d %s", cmd, rc, out)
			}
		}
		b, err := os.ReadFile(filepath.Join(root, "src", feature, "gen/Signals.g.cs"))
		if err != nil || !strings.Contains(string(b), "namespace Shop."+feature+".Signals;") {
			t.Fatalf("%s namespace: %v %s", feature, err, b)
		}
	}
}

func TestGenerationDirectoryAliasNestedModule(t *testing.T) {
	root := repo(t, map[string]string{
		"go.mod": "module example.com/root\n\ngo 1.24\n", "shared/go.mod": "module example.com/shared\n\ngo 1.24\n",
		"observability/contract.yaml": "service: s\ngenerate: [{lang: go, out: alias}]\n",
	})
	if err := os.Symlink("shared/gen", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "observability")
	if rc, out := obs("gen", "--dir", dir); rc != 0 {
		t.Fatalf("directory alias with missing tail: rc=%d %s", rc, out)
	}
	b, err := os.ReadFile(filepath.Join(root, "shared/gen/signals.go"))
	if err != nil || !strings.Contains(string(b), "example.com/shared:observability/contract.yaml") {
		t.Fatalf("resolved module output: %v %s", err, b)
	}
	if rc, out := obs("gen", "--check", "--dir", dir); rc != 0 {
		t.Fatalf("resolved alias --check: rc=%d %s", rc, out)
	}
}
