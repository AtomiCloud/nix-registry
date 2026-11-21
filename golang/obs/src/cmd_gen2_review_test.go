package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllGenerationGoWorkspaceExport(t *testing.T) {
	root := repo(t, map[string]string{"go.work": "go 1.27.4\nuse (\n./apps/orders\n./apps/billing\n)\n"})
	for _, feature := range []string{"orders", "billing"} {
		gen2Write(t, root, "apps/"+feature+"/go.mod", "module example.com/"+feature+"\ngo 1.27.4\n")
		gen2Write(t, root, "apps/"+feature+"/observability/contract.yaml", "service: shop\nlogs: {Failed: {event: "+feature+".failed, level: error}}\ngenerate: [{lang: go, out: gen}]\n")
	}
	if rc, output := obs("gen", "--all", "--dir", root); rc != 0 {
		t.Fatal(output)
	}
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	if rc, output := obs("gen", "--check", "--all", "--dir", root); rc != 0 || !strings.Contains(output, "apps/orders/observability/contract.yaml") || !strings.Contains(output, "apps/billing/observability/contract.yaml") {
		t.Errorf("complete workspace export must retain both contract headers: rc=%d %s", rc, output)
	}
	for _, feature := range []string{"orders", "billing"} {
		b, err := os.ReadFile(filepath.Join(root, "apps", feature, "gen/signals.go"))
		if err != nil || !strings.Contains(string(b), "// contract: apps/"+feature+"/observability/contract.yaml") {
			t.Errorf("workspace contract header: %v %s", err, b)
		}
	}
}

func TestAdditionalSourceExportMarkers(t *testing.T) {
	for _, marker := range []string{"Shop.slnx", "flake.nix"} {
		t.Run(marker, func(t *testing.T) {
			root := repo(t, map[string]string{marker: ""})
			lang := "go"
			for _, feature := range []string{"orders", "billing"} {
				if marker == "Shop.slnx" {
					lang = "dotnet"
					gen2Write(t, root, "App/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><AssemblyName>Signals</AssemblyName></PropertyGroup></Project>`)
					gen2Write(t, root, "App/Modules/"+feature+"/observability/contract.yaml", "service: shop\nlogs: {Failed: {event: "+feature+".failed, level: error}}\ngenerate: [{lang: dotnet, out: Signals}]\n")
				} else {
					gen2Write(t, root, "apps/"+feature+"/go.mod", "module example.com/"+feature+"\ngo 1.27.4\n")
					gen2Write(t, root, "apps/"+feature+"/observability/contract.yaml", "service: shop\nlogs: {Failed: {event: "+feature+".failed, level: error}}\ngenerate: [{lang: go, out: gen}]\n")
				}
			}
			if rc, output := obs("gen", "--all", "--dir", root); rc != 0 {
				t.Fatal(output)
			}
			before := map[string]string{}
			if err := filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && (entry.Name() == "signals.go" || entry.Name() == "Signals.g.cs") {
					b, err := os.ReadFile(file)
					if err != nil {
						return err
					}
					before[file] = string(b)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
			if rc, output := obs("gen", "--check", "--all", "--dir", root); rc != 0 || strings.Count(output, "--all:") != 2 {
				t.Errorf("%s export: rc=%d %s", lang, rc, output)
			}
			if rc, output := obs("gen", "--all", "--dir", root); rc != 0 {
				t.Fatal(output)
			}
			for file, want := range before {
				b, err := os.ReadFile(file)
				if err != nil || string(b) != want {
					t.Errorf("%s export changed contract id/header in %s: %v", marker, file, err)
				}
			}
		})
	}
}

func TestDotnetDirectoryBuildPropsRemove(t *testing.T) {
	root := repo(t, map[string]string{
		"Directory.Build.props":                          `<Project><ItemGroup><Compile Remove="Modules/*/Signals/*.g.cs" /></ItemGroup></Project>`,
		"App/App.csproj":                                 `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><AssemblyName>App</AssemblyName></PropertyGroup></Project>`,
		"Signals/Signals.csproj":                         `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><Compile Include="../App/Modules/*/Signals/*.g.cs" /></ItemGroup></Project>`,
		"App/Modules/Orders/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: Signals}]\n",
	})
	for _, cmd := range []string{"gen", "check"} {
		if rc, output := obs(cmd, "--dir", filepath.Join(root, "App/Modules/Orders/observability")); rc != 0 {
			t.Errorf("%s must honor imported literal Compile Remove: rc=%d %s", cmd, rc, output)
		}
	}
}

func TestAllCheckSourceExportRetainsCrossContractChecks(t *testing.T) {
	root := repo(t, map[string]string{"package.json": `{"name":"shop"}`})
	for _, feature := range []string{"one", "two"} {
		gen2Write(t, root, "observability/"+feature+"/contract.yaml", "service: shop\nmetrics: {shop.requests: {kind: counter}}\n")
	}
	for _, git := range []bool{true, false} {
		if !git {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		}
		if rc, output := obs("check", "--all", "--dir", root); rc != 1 || !strings.Contains(output, "metric-clash") || !strings.Contains(output, "observability/one/contract.yaml") || !strings.Contains(output, "observability/two/contract.yaml") {
			t.Errorf("git=%v all must enforce cross-contract metric uniqueness: rc=%d %s", git, rc, output)
		}
	}
	if rc, output := obs("check", "--dir", filepath.Join(root, "observability/one")); rc != 0 {
		t.Errorf("single-folder source-export behavior must stay unchanged: rc=%d %s", rc, output)
	}
}

func TestGenerationFindsExternalDotnetConsumer(t *testing.T) {
	root := repo(t, map[string]string{
		"Shop.sln":       "",
		"App/App.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><AssemblyName>App</AssemblyName></PropertyGroup><ItemGroup><Compile Remove="Modules/*/Signals/*.g.cs" /></ItemGroup></Project>`,
		"App/Modules/Orders/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: Signals}]\n",
	})
	for _, git := range []bool{true, false} {
		if !git {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"SharedSignals", "Signals"} {
			gen2Write(t, root, "Shared/Shared.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><AssemblyName>`+name+`</AssemblyName></PropertyGroup><ItemGroup><Compile Include="../App/Modules/*/Signals/*.g.cs" /></ItemGroup></Project>`)
			for _, cmd := range []string{"gen", "check"} {
				rc, output := obs(cmd, "--dir", filepath.Join(root, "App/Modules/Orders/observability"))
				if name == "Signals" {
					if rc != 0 {
						t.Errorf("valid external consumer %s: rc=%d %s", cmd, rc, output)
					}
				} else if rc == 0 || !strings.Contains(output, "Shared/Shared.csproj") || !strings.Contains(output, "SharedSignals") || !strings.Contains(output, "Signals") {
					t.Errorf("%s must diagnose actual external consumer: rc=%d %s", cmd, rc, output)
				}
				if name == "SharedSignals" {
					for _, line := range strings.Split(output, "\n") {
						if strings.Contains(line, "[generate-assembly]") && strings.Contains(line, root) {
							t.Errorf("project diagnostic must be repo-relative: %s", line)
						}
					}
					if cmd == "gen" && !strings.Contains(output, "generation target failures") {
						t.Errorf("assembly finding is not a clash: %s", output)
					}
				}
			}
		}
	}
}

func TestAllChecksIncludeContractlessObservabilityFolders(t *testing.T) {
	for _, kind := range []string{"dashboards", "alerts"} {
		t.Run(kind, func(t *testing.T) {
			root := repo(t, map[string]string{
				"package.json":                        `{"name":"shop"}`,
				"observability/feature/contract.yaml": "service: checkout\n",
				"observability/feature/seed.yaml":     seedDoc,
				"observability/shared/seed.yaml":      seedDoc,
			})
			folder := filepath.Join(root, "observability/shared", kind)
			if err := os.MkdirAll(folder, 0755); err != nil {
				t.Fatal(err)
			}
			withoutContracts := repo(t, map[string]string{"observability/shared/seed.yaml": seedDoc})
			if err := os.MkdirAll(filepath.Join(withoutContracts, "observability/shared", kind), 0755); err != nil {
				t.Fatal(err)
			}
			for _, cmd := range []string{"check", "test"} {
				if rc, output := obs(cmd, "--all", "--dir", withoutContracts); rc != 2 || !strings.Contains(output, "no contracts") {
					t.Errorf("shared data alone must not make a successful zero-contract run: rc=%d %s", rc, output)
				}
			}
			for _, git := range []bool{true, false} {
				if !git {
					if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
						t.Fatal(err)
					}
				}
				for _, cmd := range []string{"check", "test"} {
					if rc, output := obs(cmd, "--all", "--dir", root); rc != 0 || !strings.Contains(output, "observability/shared/") {
						t.Errorf("git=%v %s must visit the empty shared folder: rc=%d %s", git, cmd, rc, output)
					}
				}
				file, body := "broken.json", `{"title":`
				if kind == "alerts" {
					file, body = "broken/warning.yaml", "apiVersion: [\n"
				}
				gen2Write(t, folder, file, body)
				for _, cmd := range []string{"check", "test"} {
					if rc, output := obs(cmd, "--all", "--dir", root); rc == 0 || !strings.Contains(output, "broken") || !strings.Contains(output, "observability/feature/contract.yaml") {
						t.Errorf("git=%v %s must fail broken shared data and keep checking feature: rc=%d %s", git, cmd, rc, output)
					}
				}
				if err := os.Remove(filepath.Join(folder, file)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
