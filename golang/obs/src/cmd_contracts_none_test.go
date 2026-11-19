package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review of PR 131: obs test --contracts none reaches the alerts part (an invalid sibling contract must not
// fail an isolated run); the default and obs alerts still read the sibling.
func TestObsTestContractsNoneReachesTheAlertsPart(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "apps", "a", "observability")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "tests", "fixtures", "test", "good", "observability"))); err != nil {
		t.Fatal(err)
	}
	fixWrite(t, filepath.Join(root, "apps", "b", "observability"), "contract.yaml", "service: b\nmetrics:\n  b.bad:\n    kind: nonsense\n")

	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc == 0 || strings.Contains(out, "obs test: alerts: ok") {
		t.Fatalf("default: the invalid sibling must fail the alerts part: exit %d\n%s", rc, out)
	}
	rc, out = fixRun(runTest, "--dir", dir, "--base", fixBase, "--contracts", "none")
	if rc != 0 || !strings.Contains(out, "obs test: alerts: ok") {
		t.Fatalf("--contracts none: alerts part must pass: exit %d\n%s", rc, out)
	}
	rc, out = fixRun(runAlerts, "--dir", dir, "--base", fixBase)
	if rc == 0 || !strings.Contains(out, "apps/b/observability/contract.yaml") {
		t.Fatalf("obs alerts keeps reading siblings: exit %d\n%s", rc, out)
	}
}
