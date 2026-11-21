package all_test

import (
	"bytes"
	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	"github.com/AtomiCloud/obs/internal/gen/gentest"
	"os"
	"path/filepath"
	"testing"
)

// A second library deliberately reuses type and event identifiers in its own
// namespace. Its repo-relative source path equals checkout's; package ids differ.
func TestBillingGolden(t *testing.T) {
	root := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(gentest.Fixture(t)))), "billing")
	c, err := contract.LoadValid(filepath.Join(root, "observability/contract.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c.File = "observability/contract.yaml"
	for _, target := range c.Generate {
		id, err := gen.PackageIdentity(root, target)
		if err != nil {
			t.Fatal(err)
		}
		fs, err := gen.Generate(target.Lang, c, gen.Options{PackageID: id, Package: target.Package, Namespace: target.Module})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			p := filepath.Join(root, target.Out, f.Path)
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
