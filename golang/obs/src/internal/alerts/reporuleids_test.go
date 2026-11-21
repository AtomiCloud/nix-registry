package alerts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/testfix"
)

func writeRepoFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const ruleDoc = `apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata: { name: g }
spec:
  interval: 1m
  rules:
    - uid: shared-errors
      title: t
`

// The selected folder's own ignored copy (a generated build/ below it) is not a definition of its uid in the
// repository-wide comparison; the same file when git does not ignore it is.
func TestSelectedFoldersIgnoredCopyIsNotARepoClash(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	cmd := testfix.Git(root, "init", "-q")
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	a := filepath.Join(root, "observability", "nfctag")
	b := filepath.Join(root, "observability", "friend")
	writeRepoFile(t, filepath.Join(a, "contract.yaml"), "service: a\n")
	writeRepoFile(t, filepath.Join(a, "alerts", "e", "warning.yaml"), ruleDoc)
	writeRepoFile(t, filepath.Join(b, "contract.yaml"), "service: b\n")
	writeRepoFile(t, filepath.Join(b, "alerts", "own", "warning.yaml"), strings.ReplaceAll(ruleDoc, "shared-errors", "friend-own"))
	writeRepoFile(t, filepath.Join(b, "alerts", "build", "e", "warning.yaml"), ruleDoc)
	p := &Project{Dir: b, Alerts: []Alert{
		{ID: "friend-own", File: "alerts/own/warning.yaml"},
		{ID: "shared-errors", File: "alerts/build/e/warning.yaml"},
	}}
	if err := RepoRuleIDs(p); err == nil || !strings.Contains(err.Error(), "rule-id-clash") {
		t.Fatalf("the control (copy not ignored) must clash: %v", err)
	}
	writeRepoFile(t, filepath.Join(root, ".gitignore"), "build/\n")
	if err := RepoRuleIDs(p); err != nil {
		t.Fatalf("an ignored copy inside the selected folder must not clash: %v", err)
	}
}
