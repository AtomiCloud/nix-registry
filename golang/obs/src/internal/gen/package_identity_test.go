package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	_ "github.com/AtomiCloud/obs/internal/gen/all"
)

func TestPackageIdentity(t *testing.T) {
	for _, tc := range []struct{ lang, manifest, body, id string }{
		{"go", "go.mod", "module example.com/library\n\ngo 1.24\n", "example.com/library"},
		{"bun", "package.json", `{"name":"@example/library"}`, "@example/library"},
		{"dart", "pubspec.yaml", "name: example_library\n", "example_library"},
		{"dotnet", "Library.csproj", `<Project><PropertyGroup><PackageId>Example.Library</PackageId><AssemblyName>Ignored</AssemblyName></PropertyGroup></Project>`, "Example.Library"},
		{"dotnet", "Library.csproj", `<Project><PropertyGroup><AssemblyName>Example.Assembly</AssemblyName></PropertyGroup></Project>`, "Example.Assembly"},
		{"dotnet", "Library.csproj", `<Project/>`, "Library"},
	} {
		t.Run(tc.lang+"/"+tc.id, func(t *testing.T) {
			root := t.TempDir()
			obs := filepath.Join(root, "observability")
			if err := os.MkdirAll(obs, 0755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(obs, "contract.yaml")
			body := "service: s\nlogs: {Failed: {event: s.failed, level: error}}\ngenerate: [{lang: " + tc.lang + ", out: pkg/generated}]\n"
			if err := os.WriteFile(file, []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
			c, err := contract.LoadValid(file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := gen.Plan(c, obs, nil); err == nil || !strings.Contains(err.Error(), "pkg/generated") {
				t.Fatalf("missing manifest must name target: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(root, "pkg"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "pkg", tc.manifest), []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			// A nearer manifest must win over a different identity above it.
			if err := os.WriteFile(filepath.Join(root, tc.manifest), []byte(strings.ReplaceAll(tc.body, tc.id, "outer")), 0644); err != nil {
				t.Fatal(err)
			}
			outs, err := gen.Plan(c, obs, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range outs {
				if o.Lang != "" && (!strings.Contains(string(o.Content), tc.id+":observability/contract.yaml") || !strings.Contains(string(o.Content), "sha256:"+c.Hash)) {
					t.Fatalf("%s lacks identity/hash", o.Path)
				}
			}
			// Two independently shipped libraries have the same repo path, different package ids.
			if err := os.WriteFile(filepath.Join(root, "pkg", tc.manifest), []byte(strings.ReplaceAll(tc.body, tc.id, "other")), 0644); err != nil {
				t.Fatal(err)
			}
			if tc.id == "Library" {
				if err := os.Rename(filepath.Join(root, "pkg", tc.manifest), filepath.Join(root, "pkg", "other.csproj")); err != nil {
					t.Fatal(err)
				}
			}
			other, err := gen.Plan(c, obs, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range other {
				if o.Lang != "" && !strings.Contains(string(o.Content), "other:observability/contract.yaml") {
					t.Fatalf("%s lacks other library identity", o.Path)
				}
			}
		})
	}
}

func TestPackageIdentityOverride(t *testing.T) {
	root := t.TempDir()
	obs := filepath.Join(root, "observability")
	c, err := contract.Parse("observability/contract.yaml", []byte("service: s\ngenerate: [{lang: go, out: generated, package_id: example.com/override}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	outs, err := gen.Plan(c, obs, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outs {
		if o.Lang != "" && !strings.Contains(string(o.Content), "example.com/override:observability/contract.yaml") {
			t.Fatalf("override absent: %s", o.Content)
		}
	}
}

func TestPackageIdentityStopsAtProjectBoundary(t *testing.T) {
	for _, git := range []bool{true, false} {
		for _, tc := range []struct{ lang, manifest, body string }{
			{"go", "go.mod", "module example.com/ambient\n"},
			{"bun", "package.json", `{"name":"ambient"}`},
			{"dart", "pubspec.yaml", "name: ambient\n"},
			{"dotnet", "Ambient.csproj", "<Project/>"},
		} {
			t.Run(tc.lang+map[bool]string{true: "/git", false: "/no-git"}[git], func(t *testing.T) {
				parent := t.TempDir()
				root := filepath.Join(parent, "project")
				if err := os.MkdirAll(root, 0755); err != nil {
					t.Fatal(err)
				}
				if git {
					if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(parent, tc.manifest), []byte(tc.body), 0644); err != nil {
					t.Fatal(err)
				}
				target := contract.Target{Lang: tc.lang, Out: "gen"}
				if id, err := gen.PackageIdentity(root, target); err == nil {
					t.Fatalf("manifest above project boundary was used: %q", id)
				}
				target.PackageID = "explicit"
				if id, err := gen.PackageIdentity(root, target); err != nil || id != "explicit" {
					t.Fatalf("explicit override: %q %v", id, err)
				}
			})
		}
	}
}
