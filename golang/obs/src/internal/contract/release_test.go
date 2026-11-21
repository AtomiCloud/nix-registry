package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseLiteralProjectMarker(t *testing.T) {
	for _, ext := range []string{"sln", "slnx", "csproj"} {
		root := filepath.Join(t.TempDir(), "[shop]")
		folder := filepath.Join(root, "App", "observability")
		if err := os.MkdirAll(folder, 0755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, "Shop."+ext), "<Project />")
		if got := ProjectRoot(folder); got != root {
			t.Errorf("%s marker missed in literal path: got %s want %s", ext, got, root)
		}
	}
}

func TestReleaseScopeUnreadablePayloadEntry(t *testing.T) {
	for _, target := range []string{"missing", "alerts"} {
		root := t.TempDir()
		folder := filepath.Join(root, "observability")
		write(t, filepath.Join(folder, "contract.yaml"), "service: shop\n")
		file := filepath.Join(folder, "alerts")
		if err := os.Symlink(target, file); err != nil {
			t.Fatal(err)
		}
		scope, err := NewScope(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := scope.Folders(); err == nil || !strings.Contains(err.Error(), "observability/alerts") {
			t.Fatalf("eligible unreadable payload treated as absent: %v", err)
		}
	}
}
