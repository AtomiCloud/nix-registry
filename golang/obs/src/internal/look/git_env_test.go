package look

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/testfix"
)

func TestGitRunUsesSelectedRepoUnderForeignHook(t *testing.T) {
	root, foreign := t.TempDir(), t.TempDir()
	for _, dir := range []string{root, foreign} {
		if out, err := testfix.Git(dir, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("init: %v %s", err, out)
		}
	}
	t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
	t.Setenv("GIT_WORK_TREE", foreign)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(foreign, "hook-index"))
	got, err := gitRun(root, "rev-parse", "--show-toplevel")
	want, werr := filepath.EvalSymlinks(root)
	if err != nil || werr != nil || strings.TrimSpace(got) != want {
		t.Fatalf("selected repo replaced by hook repo: got=%q want=%q err=%v", got, want, err)
	}
}
