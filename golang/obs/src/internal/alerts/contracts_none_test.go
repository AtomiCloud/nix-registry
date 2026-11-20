package alerts

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review of PR 131: `obs test --contracts none` reads this folder's contract only. The alerts part must not
// read or validate a sibling observability/contract.yaml either, or an invalid sibling fails an isolated run.
func TestNoOtherContractsReachesTheAlertsPart(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "apps", "a", "observability")
	if err := os.CopyFS(dir, os.DirFS("../../../tests/fixtures/test/good/observability")); err != nil {
		t.Fatal(err)
	}
	sib := filepath.Join(root, "apps", "b", "observability")
	if err := os.MkdirAll(sib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sib, "contract.yaml"), []byte("service: b\nmetrics:\n  b.bad:\n    kind: nonsense\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(o Options) (int, string) {
		var out, errb bytes.Buffer
		o.Dir, o.Workers, o.AllowEmptyExpect = dir, 1, true
		rc := Check(o, &out, &errb)
		return rc, out.String() + errb.String()
	}
	rc, msg := run(Options{})
	if rc == 0 || !strings.Contains(msg, "apps/b/observability/contract.yaml") {
		t.Fatalf("default (obs alerts) must read the sibling and name its file: rc %d\n%s", rc, msg)
	}
	rc, msg = run(Options{NoOtherContracts: true})
	if rc != 0 || strings.Contains(msg, "apps/b/observability") {
		t.Fatalf("NoOtherContracts must skip the sibling contract: rc %d\n%s", rc, msg)
	}
}
