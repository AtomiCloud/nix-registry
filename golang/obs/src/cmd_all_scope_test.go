package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllUsesOneMarkerlessExportRoot(t *testing.T) {
	root := repo(t, map[string]string{})
	t.Chdir(root)
	for _, app := range []string{"one", "two"} {
		gen2Write(t, root, app+"/go.mod", "module example.com/"+app+"\ngo 1.27.4\n")
		gen2Write(t, root, app+"/observability/contract.yaml", "service: shop\nmetrics: {shop.requests: {kind: counter}}\ngenerate: [{lang: go, out: gen}]\n")
	}
	if rc, output := obs("gen", "--all"); rc != 0 {
		t.Fatal(output)
	}
	for _, git := range []bool{true, false} {
		if !git {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		}
		if rc, output := obs("check", "--all"); rc != 1 || !strings.Contains(output, "metric-clash") {
			t.Errorf("git=%v cross-module clash: rc=%d %s", git, rc, output)
		}
		if rc, output := obs("gen", "--check", "--all"); rc != 0 || !strings.Contains(output, "one/observability/contract.yaml") || !strings.Contains(output, "two/observability/contract.yaml") {
			t.Errorf("git=%v stable whole-export headers/ids: rc=%d %s", git, rc, output)
		}
	}
}

func TestAllFiltersSharedCandidatesAndLoads(t *testing.T) {
	root := repo(t, map[string]string{
		".gitignore":                                       "observability/ignored/contract.yaml\nobservability/ignored/alerts/\nobservability/ignored/dashboards/\nobservability/shared/contract.yaml\nobservability/shared/dashboards/ignored.json\n",
		"observability/feature/contract.yaml":              "service: checkout\n",
		"observability/feature/seed.yaml":                  seedDoc,
		"observability/ignored/contract.yaml":              "service: [broken]\n",
		"observability/ignored/alerts/bad/warning.yaml":    "apiVersion: [\n",
		"observability/ignored/dashboards/bad.json":        `{"title":`,
		"observability/shared/contract.yaml":               "service: [broken]\n",
		"observability/shared/seed.yaml":                   seedDoc,
		"observability/shared/dashboards/shared.json":      `{"title":"Shared","schemaVersion":42,"panels":[]}`,
		"observability/shared/dashboards/ignored.json":     `{"title":`,
		"observability/shared/dashboards/nested/.git/HEAD": "ref: refs/heads/main\n",
		"observability/shared/dashboards/nested/bad.json":  `{"title":`,
	})
	t.Chdir(root)
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if rc, output := obs("gen", "--all"); rc != 0 || strings.Count(output, "--all:") != 1 {
		t.Errorf("generation discovery control: rc=%d %s", rc, output)
	}
	for _, cmd := range []string{"check", "test"} {
		if rc, output := obs(cmd, "--all"); rc != 0 || strings.Count(output, "--all:") != 2 || strings.Contains(output, "observability/ignored/") || !strings.Contains(output, "observability/shared/") {
			t.Errorf("%s must filter candidates and their loads: rc=%d %s", cmd, rc, output)
		}
	}
}

func TestDotnetDefaultItemPropertiesLastWriterWins(t *testing.T) {
	for _, property := range []string{"EnableDefaultItems", "EnableDefaultCompileItems"} {
		root := repo(t, map[string]string{
			"Directory.Build.props":           "<Project><PropertyGroup><" + property + ">false</" + property + "></PropertyGroup></Project>",
			"App/App.csproj":                  "<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><AssemblyName>App</AssemblyName><" + property + ">true</" + property + "></PropertyGroup></Project>",
			"App/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: Signals}]\n",
		})
		for _, cmd := range []string{"gen", "check"} {
			if rc, output := obs(cmd, "--dir", filepath.Join(root, "App/observability")); rc == 0 || !strings.Contains(output, "[generate-assembly]") {
				t.Errorf("%s %s: csproj true overrides props false: rc=%d %s", cmd, property, rc, output)
			}
		}
	}
}

func TestDotnetDefaultItemPropertiesAreIndependent(t *testing.T) {
	for _, property := range []string{"EnableDefaultItems", "EnableDefaultCompileItems"} {
		other := "EnableDefaultItems"
		if property == other {
			other = "EnableDefaultCompileItems"
		}
		root := repo(t, map[string]string{
			"Directory.Build.props":           "<Project><PropertyGroup><" + property + ">false</" + property + "></PropertyGroup></Project>",
			"App/App.csproj":                  "<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><AssemblyName>App</AssemblyName><" + other + ">true</" + other + "></PropertyGroup></Project>",
			"App/observability/contract.yaml": "service: shop\ngenerate: [{lang: dotnet, out: Signals}]\n",
		})
		if rc, output := obs("gen", "--dir", filepath.Join(root, "App/observability")); rc != 0 {
			t.Errorf("enabling %s must not undo disabled %s: rc=%d %s", other, property, rc, output)
		}
		gen2Write(t, root, "Directory.Build.targets", "<Project><PropertyGroup><"+property+">true</"+property+"></PropertyGroup></Project>")
		if rc, output := obs("gen", "--dir", filepath.Join(root, "App/observability")); rc == 0 || !strings.Contains(output, "[generate-assembly]") {
			t.Errorf("targets must be the last property writer: rc=%d %s", rc, output)
		}
	}
}
