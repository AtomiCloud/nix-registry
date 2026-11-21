package gitcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/AtomiCloud/obs/internal/testfix"
)

func TestSameRepoHookIndexWithTrailingSpaceGitDir(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), "metadata ")
	if out, err := testfix.Git(root, "init", "-q", "--separate-git-dir", gitDir).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "staged.txt"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(t.TempDir(), "hook-index")
	add := testfix.Git(root, "add", "staged.txt")
	add.Env = append(add.Env, "GIT_INDEX_FILE="+index)
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("stage: %v %s", err, out)
	}
	t.Setenv("GIT_DIR", gitDir)
	t.Setenv("GIT_WORK_TREE", root)
	t.Setenv("GIT_INDEX_FILE", index)
	cmd := exec.Command("git", "-C", root, "ls-files", "--cached")
	cmd.Env = Env(root)
	if out, err := cmd.Output(); err != nil || string(out) != "staged.txt\n" {
		t.Fatalf("same-repo staged index lost for Git dir %q: out=%q err=%v", gitDir, out, err)
	}
}
