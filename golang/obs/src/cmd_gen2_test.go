package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gen2Write(t *testing.T, root, file, body string) {
	t.Helper()
	p := filepath.Join(root, file)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationOutputBaseByLayout(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		root := repo(t, map[string]string{"package.json": `{"name":"shop"}`})
		dir, out := "src/modules/orders/observability", "signals"
		if grouped {
			dir, out = "observability/orders", "src/modules/orders/signals"
		}
		gen2Write(t, root, dir+"/contract.yaml", "service: shop\nlogs: {Failed: {event: orders.failed, level: error}}\ngenerate: [{lang: bun, out: "+out+"}]\n")
		if rc, output := obs("gen", "--dir", filepath.Join(root, dir)); rc != 0 {
			t.Fatal(output)
		}
		if _, err := os.Stat(filepath.Join(root, "src/modules/orders/signals/signals.ts")); err != nil {
			t.Errorf("grouped=%v output must be relative to the folder holding observability: %v", grouped, err)
		}
		if grouped {
			if _, err := os.Stat(filepath.Join(root, "observability/src/modules/orders/signals/signals.ts")); !os.IsNotExist(err) {
				t.Error("Bun output landed below observability instead of the project folder")
			}
		}
	}
}

func TestGroupedSharedOutputRefused(t *testing.T) {
	root := repo(t, map[string]string{"go.mod": "module example.com/shop\ngo 1.24\n"})
	for _, feature := range []string{"orders", "billing"} {
		gen2Write(t, root, "observability/"+feature+"/contract.yaml", "service: shop\nlogs: {Failed: {event: "+feature+".failed, level: error}}\ngenerate: [{lang: go, out: gen}]\n")
	}
	for _, cmd := range []string{"gen", "check"} {
		if rc, output := obs(cmd, "--dir", filepath.Join(root, "observability/orders")); rc == 0 || !strings.Contains(output, "generate-out-clash") || !strings.Contains(output, "observability/orders/contract.yaml") || !strings.Contains(output, "observability/billing/contract.yaml") {
			t.Errorf("%s must refuse the overwrite naming both contracts: rc=%d %s", cmd, rc, output)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "gen/signals.go")); !os.IsNotExist(err) {
		t.Error("refused generation wrote a shared file")
	}
}

func TestGenerationCheckSourceExport(t *testing.T) {
	for _, lang := range []string{"bun", "go"} {
		t.Run(lang, func(t *testing.T) { generationCheckSourceExport(t, lang) })
	}
}

func generationCheckSourceExport(t *testing.T, lang string) {
	for _, grouped := range []bool{false, true} {
		root := repo(t, map[string]string{"package.json": `{"name":"shop"}`, "go.mod": "module example.com/shop\ngo 1.24\n"})
		dirs := []string{}
		before := map[string]string{}
		for _, feature := range []string{"orders", "billing"} {
			dir, out := feature+"/observability", "gen"
			if grouped {
				dir, out = "observability/"+feature, "gen/"+feature
			}
			dirs = append(dirs, dir)
			gen2Write(t, root, dir+"/contract.yaml", "service: shop\nlogs: {Failed: {event: "+feature+".failed, level: error}}\ngenerate: [{lang: "+lang+", out: "+out+"}]\n")
		}
		for _, dir := range dirs {
			if rc, output := obs("gen", "--dir", filepath.Join(root, dir)); rc != 0 {
				t.Fatal(output)
			}
		}
		if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if filepath.Base(p) == "signals.ts" || filepath.Base(p) == "signals.go" {
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				before[p] = string(b)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
			t.Fatal(err)
		}
		for _, dir := range dirs {
			if rc, output := obs("gen", "--check", "--dir", filepath.Join(root, dir)); rc != 0 {
				t.Errorf("clean export grouped=%v: rc=%d %s", grouped, rc, output)
			}
			if rc, output := obs("gen", "--dir", filepath.Join(root, dir)); rc != 0 {
				t.Fatal(output)
			}
		}
		for file, want := range before {
			b, err := os.ReadFile(file)
			if err != nil || string(b) != want {
				t.Errorf("export regeneration changed header/identity in %s: %v", file, err)
			}
		}
	}
}

func TestGenerationDefaultGoImport(t *testing.T) {
	root := repo(t, map[string]string{"go.mod": "module example.com/shop\ngo 1.24\n", "observability/contract.yaml": "service: shop\ngenerate: [{lang: go, out: gen}]\n"})
	if rc, output := obs("gen", "--dir", filepath.Join(root, "observability")); rc != 0 {
		t.Fatal(output)
	}
	b, err := os.ReadFile(filepath.Join(root, "gen/signals.go"))
	if err != nil || !strings.Contains(string(b), `github.com/AtomiCloud/diene.go-interfaces/obs`) || strings.Contains(string(b), "github.com/ProtoDurian/diene.go-interfaces/obs") {
		t.Errorf("wrong default module import: %v %s", err, b)
	}
}

func TestGenerationDotnetAssemblyName(t *testing.T) {
	for _, name := range []string{"App", "Signals"} {
		root := repo(t, map[string]string{"observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: gen}]\n", "gen/Signals.csproj": "<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><AssemblyName>" + name + "</AssemblyName></PropertyGroup></Project>"})
		for _, cmd := range []string{"gen", "check"} {
			rc, output := obs(cmd, "--dir", filepath.Join(root, "observability"))
			if name == "Signals" {
				if rc != 0 {
					t.Errorf("valid assembly %s: rc=%d %s", cmd, rc, output)
				}
				continue
			}
			if rc == 0 || !strings.Contains(output, "Signals") || !strings.Contains(output, "App") || !strings.Contains(output, "gen/Signals.csproj") {
				t.Errorf("%s must explain internal friend assembly boundary: rc=%d %s", cmd, rc, output)
			}
		}
	}
}

func TestGenerationMultipleProjectsErrorWrap(t *testing.T) {
	root := repo(t, map[string]string{"observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: gen}]\n", "One.csproj": "<Project/>", "Two.csproj": "<Project/>"})
	if rc, output := obs("gen", "--dir", filepath.Join(root, "observability")); rc == 0 || !strings.Contains(output, "multiple .csproj") || !strings.Contains(output, `out: "gen"`) || !strings.Contains(output, "or set package_id:") {
		t.Errorf("ambiguous identity error lost target/override hint: rc=%d %s", rc, output)
	}
}

func TestAllCommandsVisitEveryContract(t *testing.T) {
	root := repo(t, map[string]string{"package.json": `{"name":"shop"}`})
	t.Chdir(root)
	for _, feature := range []string{"orders", "billing"} {
		gen2Write(t, root, "observability/"+feature+"/contract.yaml", "service: checkout\nlogs: {Failed: {event: "+feature+".failed, level: error}}\ngenerate: [{lang: bun, out: gen/"+feature+"}]\n")
		gen2Write(t, root, "observability/"+feature+"/seed.yaml", seedDoc)
	}
	for _, git := range []bool{true, false} {
		if !git {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		}
		for _, args := range [][]string{{"gen", "--all"}, {"gen", "--check", "--all"}, {"check", "--all"}, {"test", "--all"}} {
			args = append(args, "--dir", root)
			rc, output := obs(args...)
			if rc != 0 || !strings.Contains(output, "observability/orders/contract.yaml") || !strings.Contains(output, "observability/billing/contract.yaml") {
				t.Errorf("git=%v %v must visit both contracts: rc=%d %s", git, args, rc, output)
			}
		}
	}
	gen2Write(t, root, "observability/billing/contract.yaml", "service: [broken]\n")
	for _, cmd := range []string{"gen", "check"} {
		if rc, output := obs(cmd, "--all", "--dir", root); rc == 0 || !strings.Contains(output, "observability/orders/contract.yaml") || !strings.Contains(output, "observability/billing/contract.yaml") {
			t.Errorf("%s --all must fail one folder and still visit the other: rc=%d %s", cmd, rc, output)
		}
	}
	gen2Write(t, root, "observability/billing/contract.yaml", "service: checkout\nlogs: {Failed: {event: billing.failed, level: error}}\n")
	gen2Write(t, root, "observability/billing/seed.yaml", "length: [broken]\n")
	if rc, output := obs("test", "--all", "--dir", root); rc == 0 || !strings.Contains(output, "observability/orders/contract.yaml") || !strings.Contains(output, "observability/billing/contract.yaml") {
		t.Errorf("test --all must fail one seed and still visit the other folder: rc=%d %s", rc, output)
	}
}

func TestAllCommandsUseDiscoverySkips(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := repo(t, map[string]string{
		".gitignore":                      "build/\n",
		"observability/one/contract.yaml": "service: shop\n",
		"charts/app/Chart.yaml":           "apiVersion: v2\nname: app\nversion: 1.0.0\n",
	})
	releaseGit(t, root, "init", "-q")
	for _, folder := range []string{"vendor", "node_modules", "fixtures", "testdata", "build", "nested", "charts/app/files", "charts/app/templates"} {
		gen2Write(t, root, folder+"/observability/contract.yaml", "service: [broken]\n")
	}
	if err := os.Mkdir(filepath.Join(root, "nested/.git"), 0755); err != nil {
		t.Fatal(err)
	}
	if rc, output := obs("check", "--all", "--dir", root); rc != 0 || strings.Count(output, "obs check --all:") != 1 || !strings.Contains(output, "observability/one/contract.yaml") {
		t.Errorf("all must share git/fixture/dependency/chart/nested-repo skips: rc=%d %s", rc, output)
	}
}

func TestAllCommandsForwardOptionsAndRefuseSharedFileExports(t *testing.T) {
	root := repo(t, map[string]string{})
	for _, feature := range []string{"one", "two"} {
		gen2Write(t, root, "observability/"+feature+"/contract.yaml", "service: shop\nmetrics: {shop.requests: {kind: counter}}\n")
	}
	if rc, output := obs("check", "--all", "--dir", root); rc == 0 || !strings.Contains(output, "metric-clash") {
		t.Errorf("default all checks must retain cross-contract rules: rc=%d %s", rc, output)
	}
	if rc, output := obs("check", "--all", "--contracts", "none", "--dir", root); rc != 0 || strings.Count(output, "obs check --all:") != 2 {
		t.Errorf("explicit per-folder scope was not forwarded: rc=%d %s", rc, output)
	}
	if rc, output := obs("check", "--all", "--pods", "0", "--dir", root); rc != 2 || strings.Count(output, "obs check --all:") != 2 || !strings.Contains(output, "--pods") {
		t.Errorf("invalid option must fail every selected folder: rc=%d %s", rc, output)
	}
	if rc, output := obs("test", "--all", "--out", filepath.Join(root, "shared.json"), "--dir", root); rc != 2 || !strings.Contains(output, "share one output file") {
		t.Errorf("all must not overwrite a shared artifact: rc=%d %s", rc, output)
	}
}

func TestAllGenerationInStandaloneObservabilityRepo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "observability")
	gen2Write(t, root, "package.json", `{"name":"standalone"}`)
	gen2Write(t, root, "contract.yaml", "service: shop\nlogs: {RootFailed: {event: root.failed, level: error}}\ngenerate: [{lang: bun, out: gen/root}]\n")
	gen2Write(t, root, "orders/contract.yaml", "service: shop\nlogs: {OrderFailed: {event: orders.failed, level: error}}\ngenerate: [{lang: bun, out: gen/orders}]\n")
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, git := range []bool{true, false} {
		if !git {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		}
		for _, check := range []bool{false, true} {
			args := []string{"gen", "--all", "--dir", root}
			if check {
				args = append(args, "--check")
			}
			if rc, output := obs(args...); rc != 0 || strings.Count(output, "--all:") != 2 {
				t.Errorf("standalone git=%v check=%v: rc=%d %s", git, check, rc, output)
			}
		}
		for _, file := range []string{"gen/root/signals.ts", "gen/orders/signals.ts"} {
			if _, err := os.Stat(filepath.Join(root, file)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDotnetSignalsGlobProjectIsAllowed(t *testing.T) {
	root := repo(t, map[string]string{
		"App/App.csproj":                                 `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><AssemblyName>App</AssemblyName></PropertyGroup><ItemGroup><Compile Remove="Modules/*/Signals/*.g.cs" /></ItemGroup></Project>`,
		"Signals/Signals.csproj":                         `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><Compile Include="../App/Modules/*/Signals/*.g.cs" /></ItemGroup></Project>`,
		"App/Modules/Orders/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: Signals}]\n",
	})
	for _, cmd := range []string{"gen", "check"} {
		if rc, output := obs(cmd, "--dir", filepath.Join(root, "App/Modules/Orders/observability")); rc != 0 {
			t.Errorf("%s rejected documented external Signals project: rc=%d %s", cmd, rc, output)
		}
	}
}

func TestAllCommandsRefuseEmptyAndUnreadableDiscovery(t *testing.T) {
	for _, cmd := range []string{"gen", "check", "test"} {
		root := repo(t, map[string]string{})
		if rc, output := obs(cmd, "--all", "--dir", root); rc != 2 || !strings.Contains(output, "no contracts") {
			t.Errorf("%s --all empty must be input error: rc=%d %s", cmd, rc, output)
		}
		gen2Write(t, root, "blocked/observability/contract.yaml", "service: shop\n")
		if err := os.Chmod(filepath.Join(root, "blocked"), 0000); err != nil {
			t.Fatal(err)
		}
		rc, output := obs(cmd, "--all", "--dir", root)
		_ = os.Chmod(filepath.Join(root, "blocked"), 0755)
		if rc != 2 || !strings.Contains(output, "blocked") {
			t.Errorf("%s --all unreadable must not silently skip: rc=%d %s", cmd, rc, output)
		}
	}
}
