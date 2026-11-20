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
		fs, err := gen.Generate("dotnet", c, gen.Options{PackageID: "Shop"})
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
	for _, feature := range []string{"Orders", "Friends"} {
		c, err := contract.LoadValid(filepath.Join(root, feature, "observability/contract.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		c.File = feature + "/observability/contract.yaml"
		fs, err := gen.Generate("dotnet", c, gen.Options{PackageID: "Shop"})
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
