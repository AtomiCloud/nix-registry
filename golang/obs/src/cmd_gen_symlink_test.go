package main

import (
	"os"
	"os/exec"
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
		"src/Orders/observability/contract.yaml":  "service: shop\nlogs: {Failed: {event: orders.failed, level: error}}\ngenerate: [{lang: dotnet, out: gen}]\n",
		"src/Billing/observability/contract.yaml": "service: shop\nlogs: {Failed: {event: billing.failed, level: error}}\ngenerate: [{lang: dotnet, out: gen}]\n",
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
	if err != nil || !strings.Contains(string(b), "example.com/shared:../observability/contract.yaml") {
		t.Fatalf("resolved module output: %v %s", err, b)
	}
	if rc, out := obs("gen", "--check", "--dir", dir); rc != 0 {
		t.Fatalf("resolved alias --check: rc=%d %s", rc, out)
	}
}

func TestRegistrationIdentityIndependentOfGit(t *testing.T) {
	root := t.TempDir()
	write := func(file, body string) {
		t.Helper()
		p := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/workspace\n\ngo 1.24\n")
	ids := map[string]string{}
	for _, feature := range []string{"Orders", "Billing"} {
		write(feature+"/observability/contract.yaml", "service: shop\nlogs: {Failed: {event: "+strings.ToLower(feature)+".failed, level: error}}\ngenerate: [{lang: go, out: gen}]\n")
		dir := filepath.Join(root, feature, "observability")
		if rc, out := obs("gen", "--dir", dir); rc != 0 {
			t.Fatal(out)
		}
		body, err := os.ReadFile(filepath.Join(root, feature, "gen/signals.go"))
		if err != nil {
			t.Fatal(err)
		}
		want := "example.com/workspace:" + feature + "/observability/contract.yaml"
		if !strings.Contains(string(body), want) {
			t.Errorf("git-less %s lacks manifest-relative id %s", feature, want)
		}
		ids[feature] = want
	}
	// Compile/run the git-less export against the local port model: duplicate
	// registration ids would poison both feature events during package init.
	stub, err := os.ReadFile("../tests/typecheck/go/stub/obs/obs.go")
	if err != nil {
		t.Fatal(err)
	}
	write("interfaces-stub/obs/obs.go", string(stub))
	write("interfaces-stub/go.mod", "module github.com/ProtoDurian/diene.go-interfaces\n\ngo 1.24\n")
	write("go.mod", "module example.com/workspace\n\ngo 1.24\nrequire github.com/ProtoDurian/diene.go-interfaces v0.0.0\nreplace github.com/ProtoDurian/diene.go-interfaces => ./interfaces-stub\n")
	write("main.go", `package main
import("context";"fmt";orders "example.com/workspace/Orders/gen";billing "example.com/workspace/Billing/gen";obs "github.com/ProtoDurian/diene.go-interfaces/obs")
func main(){if err:=obs.Error(context.Background(),orders.Failed);err!=nil{panic(err)};if err:=obs.Error(context.Background(),billing.Failed);err!=nil{panic(err)};if obs.Dropped("register")!=0||obs.Dropped("poisoned")!=0{panic("poisoned")};fmt.Println("PASS git-less two-feature registration")}
`)
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(goTool, "run", ".")
	command.Dir = root
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOFLAGS=-mod=mod", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil || !strings.Contains(string(output), "PASS git-less two-feature registration") {
		t.Fatalf("git-less app: %v %s", err, output)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	for feature, want := range ids {
		if rc, out := obs("gen", "--dir", filepath.Join(root, feature, "observability")); rc != 0 {
			t.Fatal(out)
		}
		body, _ := os.ReadFile(filepath.Join(root, feature, "gen/signals.go"))
		if !strings.Contains(string(body), want) {
			t.Errorf("Git changed %s registration id", feature)
		}
	}
}

func TestSingleDotnetContractNestedProject(t *testing.T) {
	root := repo(t, map[string]string{
		"src/Checkout.csproj":                           "<Project/>",
		"services/Checkout/observability/contract.yaml": "service: checkout\ngenerate: [{lang: dotnet, out: ../../src/gen, package_id: Checkout}]\n",
	})
	if rc, out := obs("gen", "--dir", filepath.Join(root, "services/Checkout/observability")); rc != 0 {
		t.Fatal(out)
	}
	body, err := os.ReadFile(filepath.Join(root, "src/gen/Signals.g.cs"))
	if err != nil || !strings.Contains(string(body), "namespace Checkout.Signals;") {
		t.Fatalf("sole service contract must preserve namespace: %v %s", err, body)
	}
}
