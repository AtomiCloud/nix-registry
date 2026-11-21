package contract

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AtomiCloud/obs/internal/testfix"
)

func TestIgnoreDiscoveryUsesSelectedRepoAndHookIndex(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, mode := range []string{"same", "same-relative", "same-link", "same-file", "unset", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			root, foreign := t.TempDir(), t.TempDir()
			for _, dir := range []string{root, foreign} {
				cmd := testfix.Git(dir, "init", "-q")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("init: %v %s", err, out)
				}
				write(t, filepath.Join(dir, "build/copy.yaml"), "fixture\n")
			}
			write(t, filepath.Join(root, ".gitignore"), "build/\nother/\n")
			write(t, filepath.Join(root, "other/copy.yaml"), "ignored\n")
			write(t, filepath.Join(foreign, ".gitignore"), "foreign-only/\n")
			indexRepo := root
			if mode == "foreign" {
				indexRepo = foreign
			}
			index := filepath.Join(indexRepo, "hook-index")
			cmd := testfix.Git(indexRepo, "add", "-f", "build/copy.yaml")
			cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+index)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("alternate index: %v %s", err, out)
			}
			if mode == "same-relative" {
				t.Chdir(root)
				t.Setenv("GIT_DIR", ".git")
			} else if mode == "same-link" {
				alias := filepath.Join(t.TempDir(), "git-alias")
				if err := os.Symlink(filepath.Join(root, ".git"), alias); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GIT_DIR", alias)
			} else if mode == "same-file" {
				pointer := filepath.Join(t.TempDir(), "git-pointer")
				write(t, pointer, "gitdir: "+filepath.Join(root, ".git")+"\n")
				t.Setenv("GIT_DIR", pointer)
			} else if mode == "unset" {
				_ = os.Unsetenv("GIT_DIR")
			} else {
				t.Setenv("GIT_DIR", filepath.Join(indexRepo, ".git"))
			}
			t.Setenv("GIT_INDEX_FILE", index)
			t.Setenv("GIT_WORK_TREE", foreign)
			ignored, refused := ignoredPaths(root)
			if refused || !ignored[filepath.Join(root, "other")] {
				t.Errorf("selected repo's ignore rules lost: refused=%v ignored=%v", refused, ignored)
			}
			wantBuildIgnored := mode == "foreign"
			if ignored[filepath.Join(root, "build")] != wantBuildIgnored {
				t.Errorf("%s hook index routing: ignored=%v want build ignored=%v", mode, ignored, wantBuildIgnored)
			}
		})
	}
}
