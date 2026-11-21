package contract

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStandaloneOutputBaseStopsAtProjectBoundary(t *testing.T) {
	for _, git := range []bool{false, true} {
		root := filepath.Join(t.TempDir(), "observability")
		if err := os.MkdirAll(root, 0755); err != nil {
			t.Fatal(err)
		}
		if git {
			if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
				t.Fatal(err)
			}
		}
		for file, body := range map[string]string{"package.json": `{"name":"signals"}`, "contract.yaml": "service: shop\nlogs: {Failed: {event: root.failed, level: error}}\n"} {
			if err := os.WriteFile(filepath.Join(root, file), []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
		for _, dir := range []string{root, filepath.Join(root, "orders")} {
			if got := OutputBase(dir); got != root {
				t.Errorf("git=%v OutputBase(%s)=%s, want standalone root %s", git, dir, got, root)
			}
		}
	}
}
