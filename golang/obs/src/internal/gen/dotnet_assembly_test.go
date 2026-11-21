package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
)

func TestConditionalRemoveCannotHideAssemblyMismatch(t *testing.T) {
	for _, scope := range []string{`<ItemGroup Condition="false"><Compile Remove="gen/*.g.cs" /></ItemGroup>`, `<ItemGroup><Compile Remove="gen/*.g.cs" Condition="false" /></ItemGroup>`} {
		root := t.TempDir()
		body := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><AssemblyName>App</AssemblyName></PropertyGroup>` + scope + `</Project>`
		if err := os.WriteFile(filepath.Join(root, "App.csproj"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		if err := checkDotnetAssembly(root, contract.Target{Lang: "dotnet", Out: "gen"}); err == nil || !strings.Contains(err.Error(), "Signals") {
			t.Errorf("false Remove cannot exclude this file from App: %v", err)
		}
	}
}

func TestMultipleProjectIdentityErrorKeepsTargetHint(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"One.csproj", "Two.csproj"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("<Project/>"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := PackageIdentity(root, contract.Target{Lang: "dotnet", Out: "gen"})
	if err == nil || !strings.Contains(err.Error(), `out: "gen"`) || !strings.Contains(err.Error(), "multiple .csproj") || !strings.Contains(err.Error(), "or set package_id:") {
		t.Fatalf("ambiguous identity needs the target and override hint: %v", err)
	}
}

func TestGoWorkspaceOverrideRetainsExportIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.27.4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, feature := range []string{"orders", "billing"} {
		dir := filepath.Join(root, "apps", feature, "observability")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "contract.yaml"), []byte("service: shop\n"), 0644); err != nil {
			t.Fatal(err)
		}
		id, err := RegistrationIdentity(dir, contract.Target{Lang: "go", Out: "gen", PackageID: "shared"})
		want := "shared:apps/" + feature + "/observability/contract.yaml"
		if err != nil || id != want {
			t.Errorf("workspace override: got %q (%v), want %q", id, err, want)
		}
	}
}

func TestHomeMarkersAndManifestKeepFeatureIdentitiesDistinct(t *testing.T) {
	outer := t.TempDir()
	home := filepath.Join(outer, "root")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	// Model Docker's HOME/WORKDIR without changing the agent's environment.
	t.Setenv("HOME", home)
	t.Chdir(home)
	for file, body := range map[string]string{
		filepath.Join(outer, "go.mod"):      "module example.com/outside-home\ngo 1.27.4\n",
		filepath.Join(home, "go.work"):      "go 1.27.4\n",
		filepath.Join(home, "package.json"): `{"name":"shop"}`,
	} {
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, feature := range []string{"one", "two"} {
		dir := filepath.Join(home, "features", feature, "observability")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "contract.yaml")
		if err := os.WriteFile(file, []byte("service: shop\n"), 0644); err != nil {
			t.Fatal(err)
		}
		for _, target := range []contract.Target{{Lang: "bun", Out: "gen"}, {Lang: "go", Out: "gen", PackageID: "shared"}} {
			id, err := RegistrationIdentity(dir, target)
			pkg := "shop"
			if target.Lang == "go" {
				pkg = "shared"
			}
			want := pkg + ":features/" + feature + "/observability/contract.yaml"
			if err != nil || id != want {
				t.Errorf("home identity got %q (%v), want %q", id, err, want)
			}
		}
		if got := contract.RelPath(file); got != "features/"+feature+"/observability/contract.yaml" {
			t.Errorf("home header lost feature: %s", got)
		}
	}
}
