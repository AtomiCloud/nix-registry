package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationTargetClashes(t *testing.T) {
	for _, kind := range []string{"namespace", "out"} {
		t.Run(kind, func(t *testing.T) {
			root := repo(t, map[string]string{})
			write := func(feature, namespace, out string) {
				t.Helper()
				d := filepath.Join(root, feature, "observability")
				if err := os.MkdirAll(d, 0755); err != nil {
					t.Fatal(err)
				}
				body := "service: shop\nlogs: {Failed: {event: shop." + strings.ToLower(feature) + ".failed, level: error}}\ngenerate: [{lang: dotnet, out: " + out + ", package_id: Shop, namespace: " + namespace + "}]\n"
				if err := os.WriteFile(filepath.Join(d, "contract.yaml"), []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			write("Orders", "Shop.Orders.Signals", "gen")
			write("Friends", "Shop.Friends.Signals", "gen")
			dir := filepath.Join(root, "Orders/observability")
			for _, cmd := range []string{"check", "gen"} {
				if rc, out := obs(cmd, "--dir", dir); rc != 0 {
					t.Fatalf("baseline %s: rc=%d %s", cmd, rc, out)
				}
			}
			if kind == "namespace" {
				write("Friends", "Shop.Orders.Signals", "gen")
			} else {
				write("Friends", "Shop.Friends.Signals", "../Orders/gen")
			}
			token := "generate-" + kind + "-clash"
			for _, cmd := range []string{"check", "gen"} {
				rc, out := obs(cmd, "--dir", dir)
				if rc == 0 || !strings.Contains(out, token) || !strings.Contains(out, "Orders/observability/contract.yaml") || !strings.Contains(out, "Friends/observability/contract.yaml") {
					t.Errorf("%s must refuse both contracts' %s clash: rc=%d %s", cmd, kind, rc, out)
				}
			}
		})
	}
}

func TestGenerationTargetsSharedOutSingleContract(t *testing.T) {
	root := repo(t, map[string]string{
		"go.mod":                      "module example.com/shared\n\ngo 1.24\n",
		"Shared.csproj":               "<Project/>",
		"observability/contract.yaml": "service: shared\nlogs: {Failed: {event: shared.failed, level: error}}\ngenerate: [{lang: go, out: gen}, {lang: dotnet, out: gen}]\n",
	})
	dir := filepath.Join(root, "observability")
	for _, cmd := range []string{"check", "gen"} {
		if rc, out := obs(cmd, "--dir", dir); rc != 0 {
			t.Fatalf("single-contract shared-out %s must retain 0.5 behavior: rc=%d %s", cmd, rc, out)
		}
	}
	for _, file := range []string{"gen/signals.go", "gen/Signals.g.cs"} {
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			t.Fatal(err)
		}
	}
	if rc, out := obs("gen", "--check", "--dir", dir); rc != 0 {
		t.Fatalf("shared-out --check: rc=%d %s", rc, out)
	}
	// Writing the same file twice remains an error within a single contract.
	cpath := filepath.Join(dir, "contract.yaml")
	body, err := os.ReadFile(cpath)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "{lang: dotnet, out: gen}", "{lang: go, out: gen}", 1))
	if err := os.WriteFile(cpath, body, 0644); err != nil {
		t.Fatal(err)
	}
	if rc, out := obs("gen", "--dir", dir); rc == 0 || !strings.Contains(out, "two outputs write gen/signals.go") {
		t.Fatalf("same-file collision: rc=%d %s", rc, out)
	}
}

func TestGenerationNamespaceValidation(t *testing.T) {
	root := repo(t, map[string]string{
		"Orders/observability/contract.yaml":  "service: shop\ngenerate: [{lang: dotnet, out: gen, package_id: Shop, namespace: Shop.Signals}]\n",
		"Friends/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: gen, package_id: Shop, namespace: Shop . Signals}]\n",
	})
	for _, cmd := range []string{"check", "gen"} {
		rc, out := obs(cmd, "--dir", filepath.Join(root, "Orders/observability"))
		if rc == 0 || !strings.Contains(out, "generate-namespace") || !strings.Contains(out, "Friends/observability/contract.yaml") || !strings.Contains(out, "Shop . Signals") {
			t.Errorf("%s must name invalid namespace and contract: rc=%d %s", cmd, rc, out)
		}
	}
}
