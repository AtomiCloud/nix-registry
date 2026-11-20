package dotnet

import (
	"bytes"
	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	"github.com/AtomiCloud/obs/internal/gen/gentest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultFeatureNamespace(t *testing.T) {
	for _, tc := range []struct{ file, want string }{
		{"observability/contract.yaml", "Shop.Signals"},
		{"Orders/observability/contract.yaml", "Shop.Orders.Signals"},
		{"observability/Orders/contract.yaml", "Shop.Orders.Signals"},
		{"App/Modules/Friends/observability/contract.yaml", "Shop.Friends.Signals"},
	} {
		c, err := contract.Parse(tc.file, []byte("service: shop\nlogs: {Failed: {event: shop.failed, level: error}}\n"))
		if err != nil {
			t.Fatal(err)
		}
		other := &contract.Contract{File: "Other/observability/contract.yaml", Service: "shop", Generate: []contract.Target{{Lang: "dotnet"}}}
		ns := gen.DotnetNamespaceForContracts(c, "", []*contract.Contract{c, other})
		fs, err := gen.Generate("dotnet", c, gen.Options{PackageID: "Shop", Namespace: ns})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(fs[0].Content), "namespace "+tc.want+";") {
			t.Errorf("%s: want namespace %s", tc.file, tc.want)
		}
	}
}

func TestDefaultFeatureGolden(t *testing.T) {
	root := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(gentest.Fixture(t)))), "default-features")
	var contracts []*contract.Contract
	for _, feature := range []string{"Orders", "Friends"} {
		c, err := contract.LoadValid(filepath.Join(root, feature, "observability/contract.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		c.File = feature + "/observability/contract.yaml"
		contracts = append(contracts, c)
	}
	for _, c := range contracts {
		feature := filepath.Base(filepath.Dir(filepath.Dir(c.File)))
		ns := gen.DotnetNamespaceForContracts(c, "", contracts)
		fs, err := gen.Generate("dotnet", c, gen.Options{PackageID: "Shop", Namespace: ns})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			p := filepath.Join(root, feature, "gen", f.Path)
			if os.Getenv("OBS_UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, f.Content, 0644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			have, err := os.ReadFile(p)
			if err != nil || !bytes.Equal(have, f.Content) {
				t.Errorf("%s differs from generated output", p)
			}
		}
	}
}

func TestAppRootDefaultNamespace(t *testing.T) {
	root := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(gentest.Fixture(t)))), "app-root")
	// Give this layout its own repository even in the Nix sandbox, where the
	// enclosing source checkout has no .git and would otherwise look like a
	// root observability/ contract without exercising the adjacent project rule.
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"App/App.csproj", "App/observability/contract.yaml"} {
		body, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(project, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(project, file), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := contract.LoadValid(filepath.Join(project, "App/observability/contract.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ns := gen.DotnetNamespace(c, "")
	if ns != "Shop.Signals" {
		t.Fatalf("App/App.csproj + App/observability is a root contract: got %s", ns)
	}
	c.File = "App/observability/contract.yaml"
	fs, err := gen.Generate("dotnet", c, gen.Options{PackageID: "Shop", Namespace: ns})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		p := filepath.Join(root, "App/gen", f.Path)
		if os.Getenv("OBS_UPDATE_GOLDEN") == "1" {
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, f.Content, 0644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		have, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(have, f.Content) {
			t.Errorf("%s differs from generated root contract", p)
		}
	}
}

func TestNestedProjectGolden(t *testing.T) {
	fixture := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(gentest.Fixture(t)))), "nested-project")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"src/Checkout.csproj", "services/Checkout/observability/contract.yaml"} {
		body, err := os.ReadFile(filepath.Join(fixture, file))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := contract.LoadValid(filepath.Join(root, "services/Checkout/observability/contract.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if namespace := gen.DotnetNamespace(c, ""); namespace != "Checkout.Signals" {
		t.Fatalf("sole nested project: %s", namespace)
	}
	files, err := gen.Generate("dotnet", c, gen.Options{PackageID: "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		destination := filepath.Join(fixture, "src/gen", file.Path)
		if os.Getenv("OBS_UPDATE_GOLDEN") == "1" {
			if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destination, file.Content, 0644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		body, err := os.ReadFile(destination)
		if err != nil || !bytes.Equal(body, file.Content) {
			t.Errorf("nested project golden differs: %v", err)
		}
	}
}
