package obsyaml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseUnreadableConfigIsNotAbsent(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		for _, target := range []string{"missing", "obs.yaml"} {
			root := t.TempDir()
			parent := filepath.Join(root, "observability")
			child := filepath.Join(parent, "orders")
			if err := os.MkdirAll(child, 0755); err != nil {
				t.Fatal(err)
			}
			folder := child
			if inherited {
				folder = parent
			}
			file := filepath.Join(folder, "obs.yaml")
			if err := os.Symlink(target, file); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadInRoot(child, root); err == nil || !strings.Contains(err.Error(), file) {
				t.Errorf("eligible config treated as absent: inherited=%v target=%s err=%v", inherited, target, err)
			}
		}
	}
}
