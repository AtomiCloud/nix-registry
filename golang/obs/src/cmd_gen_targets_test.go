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
		feature := "Orders"
		if cmd == "gen" {
			feature = "Friends"
		}
		rc, out := obs(cmd, "--dir", filepath.Join(root, feature+"/observability"))
		if rc == 0 || !strings.Contains(out, "generate-namespace") || !strings.Contains(out, "Friends/observability/contract.yaml") || !strings.Contains(out, "Shop . Signals") {
			t.Errorf("%s must name invalid namespace and contract: rc=%d %s", cmd, rc, out)
		}
	}
}

func TestGenerationResolvedOutputCollision(t *testing.T) {
	for _, sameFile := range []bool{true, false} {
		name := "distinct filenames"
		second := "{lang: dotnet, out: link}"
		if sameFile {
			name = "same physical file"
			second = "{lang: go, out: link, package: b}"
		}
		t.Run(name, func(t *testing.T) {
			root := repo(t, map[string]string{
				"go.mod":                      "module example.com/shared\n\ngo 1.24\n",
				"Shared.csproj":               "<Project/>",
				"observability/contract.yaml": "service: shared\ngenerate: [{lang: go, out: actual, package: a}, " + second + "]\n",
			})
			if err := os.Mkdir(filepath.Join(root, "actual"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("actual", filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "observability")
			rc, out := obs("gen", "--dir", dir)
			if sameFile {
				if rc == 0 || !strings.Contains(out, "actual/signals.go") || !strings.Contains(out, "link/signals.go") {
					t.Fatalf("same resolved file must be refused before writes: rc=%d %s", rc, out)
				}
				for _, file := range []string{"actual/signals.go", "observability/contract/events.json"} {
					if _, err := os.Stat(filepath.Join(root, file)); !os.IsNotExist(err) {
						t.Fatalf("refused gen wrote %s", file)
					}
				}
			} else {
				if rc != 0 {
					t.Fatalf("distinct filenames through shared-folder alias: rc=%d %s", rc, out)
				}
				if rc, out := obs("gen", "--check", "--dir", dir); rc != 0 {
					t.Fatalf("shared-folder alias --check: rc=%d %s", rc, out)
				}
			}
		})
	}
}

func TestGenerationIgnoresInvalidSiblings(t *testing.T) {
	for _, bad := range []string{"service: [broken]\n", "service: broken\nmetrics: {bad: {kind: nonsense}}\n", "service: broken\ngenerate: [{lang: dotnet, out: gen, package_id: Broken, namespace: Shop . Signals}]\n", "service: valid\ngenerate: [{lang: dotnet, out: ../../outside, package_id: Broken}]\n"} {
		root := repo(t, map[string]string{
			"go.mod":                             "module example.com/valid\n\ngo 1.24\n",
			"observability/contract.yaml":        "service: valid\nlogs: {Failed: {event: valid.failed, level: error}}\ngenerate: [{lang: go, out: gen}]\n",
			"Broken/observability/contract.yaml": bad,
		})
		dir := filepath.Join(root, "observability")
		for _, args := range [][]string{{"gen", "--dir", dir}, {"gen", "--check", "--dir", dir}, {"gen", "--check", "--lang", "go", "--dir", dir}} {
			if rc, out := obs(args...); rc != 0 {
				t.Errorf("valid contract gen must ignore invalid sibling: rc=%d %s", rc, out)
			}
		}
		if rc, out := obs("check", "--dir", dir); rc == 0 || !strings.Contains(out, "Broken/observability/contract.yaml") {
			t.Errorf("check must still name invalid sibling: rc=%d %s", rc, out)
		}
	}
}

func TestGitlessDotnetFeatureNamespaces(t *testing.T) {
	root := repo(t, map[string]string{
		"Shop.csproj":                         "<Project><PropertyGroup><PackageId>$(AssemblyName)</PackageId></PropertyGroup></Project>",
		"Orders/observability/contract.yaml":  "service: shop\ngenerate: [{lang: dotnet, out: gen, package_id: Shop}]\n",
		"Friends/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: gen, package_id: Shop}]\n",
	})
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	for _, withGit := range []bool{false, true} {
		if withGit {
			if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
				t.Fatal(err)
			}
		}
		for _, feature := range []string{"Orders", "Friends"} {
			dir := filepath.Join(root, feature, "observability")
			if rc, out := obs("gen", "--dir", dir); rc != 0 {
				t.Fatal(out)
			}
			body, err := os.ReadFile(filepath.Join(root, feature, "gen/Signals.g.cs"))
			if err != nil || !strings.Contains(string(body), "namespace Shop."+feature+".Signals;") {
				t.Errorf("withGit=%v %s: %v %s", withGit, feature, err, body)
			}
			if !strings.Contains(string(body), "Shop:"+feature+"/observability/contract.yaml") {
				t.Errorf("withGit=%v %s lacks owning-project-relative override id", withGit, feature)
			}
		}
	}
}

func TestGitlessGenerationTargetClashes(t *testing.T) {
	root := repo(t, map[string]string{
		"Shop.csproj":                         "<Project/>",
		"Orders/observability/contract.yaml":  "service: shop\ngenerate: [{lang: dotnet, out: gen, namespace: Shared.Signals}]\n",
		"Friends/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: gen, namespace: Shared.Signals}]\n",
	})
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"gen", "check"} {
		if rc, out := obs(cmd, "--dir", filepath.Join(root, "Orders/observability")); rc == 0 || !strings.Contains(out, "generate-namespace-clash") || !strings.Contains(out, "Orders/observability/contract.yaml") || !strings.Contains(out, "Friends/observability/contract.yaml") {
			t.Errorf("%s must retain true clashes outside Git: rc=%d %s", cmd, rc, out)
		}
	}
}

func TestInvalidSiblingTargetDoesNotAffectNamespace(t *testing.T) {
	root := repo(t, map[string]string{
		"Shop.csproj":                        "<Project/>",
		"Orders/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: gen, package_id: Shop}]\n",
		"Broken/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: ../../outside, package_id: Shop}]\n",
	})
	for _, git := range []bool{true, false} {
		if !git {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		}
		dir := filepath.Join(root, "Orders/observability")
		if rc, out := obs("gen", "--dir", dir); rc != 0 {
			t.Fatal(out)
		}
		body, err := os.ReadFile(filepath.Join(root, "Orders/gen/Signals.g.cs"))
		if err != nil || !strings.Contains(string(body), "namespace Shop.Signals;") {
			t.Errorf("git=%v invalid sibling changed sole-contract namespace: %v %s", git, err, body)
		}
		if rc, out := obs("gen", "--check", "--dir", dir); rc != 0 {
			t.Fatal(out)
		}
		if rc, out := obs("check", "--dir", dir); rc == 0 || !strings.Contains(out, "Broken/observability/contract.yaml") || !strings.Contains(out, "../../outside") {
			t.Errorf("git=%v check accepted invalid target: rc=%d %s", git, rc, out)
		}
	}
}

func TestGenerationRejectsDuplicateRegistrationIDsWithoutManifest(t *testing.T) {
	root := repo(t, map[string]string{
		"observability/contract.yaml":         "service: root\ngenerate: [{lang: dotnet, out: gen, package_id: Shared}]\n",
		"Feature/observability/contract.yaml": "service: feature\ngenerate: [{lang: dotnet, out: gen, package_id: Shared}]\n",
	})
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"gen", "check"} {
		if rc, out := obs(cmd, "--dir", filepath.Join(root, "observability")); rc == 0 || !strings.Contains(out, "generate-id-clash") || !strings.Contains(out, "Feature/observability/contract.yaml") || !strings.Contains(out, "Shared:observability/contract.yaml") {
			t.Errorf("%s must refuse both owners of one registration id: rc=%d %s", cmd, rc, out)
		}
	}
}
