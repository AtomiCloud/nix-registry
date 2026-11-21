package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFixtureGitKeepsHookCallerIndex(t *testing.T) {
	caller := repo(t, map[string]string{"keep.txt": "tracked\n", "new.txt": "caller content\n"})
	fixture := repo(t, map[string]string{"new.txt": "fixture content\n"})
	// Bootstrap with an explicit environment so even the red control cannot
	// reach a real hook caller's repository or index.
	safeGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", caller}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + caller, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bootstrap git: %v %s", err, out)
		}
	}
	safeGit("init", "-q")
	safeGit("add", "-f", "keep.txt")
	before, err := os.ReadFile(filepath.Join(caller, ".git/index"))
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(caller, "hook-index")
	if err := os.WriteFile(index, before, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(caller, ".git"))
	t.Setenv("GIT_INDEX_FILE", index)
	t.Setenv("GIT_WORK_TREE", caller)
	releaseGit(t, fixture, "init", "-q")
	releaseGit(t, fixture, "add", "-f", "new.txt")
	if after, err := os.ReadFile(index); err != nil || !bytes.Equal(before, after) {
		t.Errorf("fixture git modified the hook caller's index: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture, ".git/index")); err != nil {
		t.Errorf("fixture git did not use its own index: %v", err)
	}
}
