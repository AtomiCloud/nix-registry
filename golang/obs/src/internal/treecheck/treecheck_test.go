package treecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const marked = "lib/logstorage/obs_fieldnames.go"

// The real tree: equal to tag v1.54.0 (commit 6433dae5) except the one marked file.
func TestThirdPartyTreeMatchesTag(t *testing.T) {
	root := "../../third_party/VictoriaLogs"
	problems, err := Verify(root, "../../third_party/VictoriaLogs.sha256", []string{marked})
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("third_party/VictoriaLogs differs from tag v1.54.0 (6433dae5):\n%s", strings.Join(problems, "\n"))
	}
	b, err := os.ReadFile(filepath.Join(root, marked))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "OBS CHANGE") {
		t.Fatalf("%s must carry the OBS CHANGE notice (Apache-2.0 section 4)", marked)
	}
}

// Planted drift: each kind of difference turns the check red.
func TestVerifyDetectsDrift(t *testing.T) {
	copyTree := func(t *testing.T) string {
		dst := t.TempDir()
		src := "../../third_party/VictoriaLogs"
		err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out := filepath.Join(dst, rel)
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, b, 0o644)
		})
		if err != nil {
			t.Fatal(err)
		}
		return dst
	}
	manifest := "../../third_party/VictoriaLogs.sha256"
	cases := map[string]struct {
		mutate func(root string) error
		want   string
	}{
		"edit a file": {func(r string) error {
			f, err := os.OpenFile(filepath.Join(r, "lib/logstorage/parser.go"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.WriteString("// tamper\n")
			return err
		}, "changed file lib/logstorage/parser.go"},
		"add a file": {func(r string) error {
			return os.WriteFile(filepath.Join(r, "lib/logstorage/extra.go"), []byte("package logstorage\n"), 0o644)
		}, "unexpected file lib/logstorage/extra.go"},
		"delete a file": {func(r string) error {
			return os.Remove(filepath.Join(r, "lib/logstorage/parser.go"))
		}, "missing file lib/logstorage/parser.go"},
		"delete the marked file": {func(r string) error {
			return os.Remove(filepath.Join(r, marked))
		}, "missing added file " + marked},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := copyTree(t)
			problems, err := Verify(root, manifest, []string{marked})
			if err != nil || len(problems) != 0 {
				t.Fatalf("baseline copy must verify clean: %v %v", err, problems)
			}
			if err := c.mutate(root); err != nil {
				t.Fatal(err)
			}
			problems, err = Verify(root, manifest, []string{marked})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(problems, "\n") != c.want {
				t.Fatalf("want exactly %q, got %v", c.want, problems)
			}
		})
	}
}
