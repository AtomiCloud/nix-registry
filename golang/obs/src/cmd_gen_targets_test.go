package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationTargetClashes(t *testing.T) {
	for _, kind := range []string{"namespace", "out"} {
		t.Run(kind, func(t *testing.T) {
			root := repo(t, map[string]string{})
			write := func(feature, namespace, out string) {
				t.Helper()
				d := filepath.Join(root, feature, "observability")
				if err := os.MkdirAll(d, 0755); err != nil {
					t.Fatal(err)
				}
				body := "service: shop\nlogs: {Failed: {event: shop." + strings.ToLower(feature) + ".failed, level: error}}\ngenerate: [{lang: dotnet, out: " + out + ", package_id: Shop, namespace: " + namespace + "}]\n"
				if err := os.WriteFile(filepath.Join(d, "contract.yaml"), []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			write("Orders", "Shop.Orders.Signals", "gen")
			write("Friends", "Shop.Friends.Signals", "gen")
			dir := filepath.Join(root, "Orders/observability")
			for _, cmd := range []string{"check", "gen"} {
				if rc, out := obs(cmd, "--dir", dir); rc != 0 {
					t.Fatalf("baseline %s: rc=%d %s", cmd, rc, out)
				}
			}
			if kind == "namespace" {
				write("Friends", "Shop.Orders.Signals", "gen")
			} else {
				write("Friends", "Shop.Friends.Signals", "../Orders/gen")
			}
			token := "generate-" + kind + "-clash"
			for _, cmd := range []string{"check", "gen"} {
				rc, out := obs(cmd, "--dir", dir)
				if rc == 0 || !strings.Contains(out, token) || !strings.Contains(out, "Orders/observability/contract.yaml") || !strings.Contains(out, "Friends/observability/contract.yaml") {
					t.Errorf("%s must refuse both contracts' %s clash: rc=%d %s", cmd, kind, rc, out)
				}
			}
		})
	}
}
