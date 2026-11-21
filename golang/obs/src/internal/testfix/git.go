package testfix

import (
	"os"
	"os/exec"
	"strings"
)

// Every test package using fixtures starts independently of an enclosing hook.
// Individual hook-environment tests can still set their own variables afterward.
func init() {
	for _, name := range []string{"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE"} {
		_ = os.Unsetenv(name)
	}
}

// Git runs fixture commands in their own repository, even when go test inherits
// a hook caller's explicit repository, worktree or index.
func Git(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	parent := os.Environ()
	cmd.Env = make([]string, 0, len(parent))
	for _, entry := range parent {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	return cmd
}
