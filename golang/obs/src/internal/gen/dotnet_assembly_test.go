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
