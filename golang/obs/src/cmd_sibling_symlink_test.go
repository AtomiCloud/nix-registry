package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSingleFolderSkipsSiblingContractSymlinks(t *testing.T) {
	for _, kind := range []string{"valid", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			root := repo(t, map[string]string{
				"observability/one/contract.yaml": "service: one\nmetrics: {one.requests: {kind: counter}}\n",
				"observability/two/contract.yaml": "service: two\nmetrics: {two.requests: {kind: counter}}\n",
			})
			t.Chdir(root)
			if rc, out := obs("check", "--dir", "observability/one"); rc != 0 {
				t.Fatalf("regular sibling baseline: rc=%d %s", rc, out)
			}
			alias := filepath.Join(root, "observability/alias/contract.yaml")
			if err := os.MkdirAll(filepath.Dir(alias), 0755); err != nil {
				t.Fatal(err)
			}
			target := "../two/contract.yaml"
			if kind == "dangling" {
				target = "../missing/contract.yaml"
			}
			if err := os.Symlink(target, alias); err != nil {
				t.Fatal(err)
			}
			if rc, out := obs("check", "--dir", "observability/one"); rc != 0 {
				t.Errorf("single-folder discovery followed %s sibling link: rc=%d %s", kind, rc, out)
			}
			if kind == "valid" {
				if rc, out := obs("check", "--dir", "observability/alias"); rc != 0 {
					t.Errorf("direct selection stopped loading the valid link: rc=%d %s", rc, out)
				}
			}
			// All-mode discovery deliberately follows in-scope links and reports
			// broken ones, independently of the single-folder compatibility rule.
			rc, out := obs("check", "--all")
			if kind == "valid" {
				if rc != 1 || !strings.Contains(out, "[metric-clash]") || !strings.Contains(out, "observability/alias/contract.yaml") {
					t.Errorf("all-mode stopped following the valid link: rc=%d %s", rc, out)
				}
			} else if rc != 2 || !strings.Contains(out, "observability/alias/contract.yaml") {
				t.Errorf("all-mode stopped reporting the dangling link: rc=%d %s", rc, out)
			}
		})
	}
}
