// Package gitcmd keeps repository-targeted Git subprocesses independent of a
// caller's repository selection while preserving a same-repository hook index.
package gitcmd

import (
	"os"
	"os/exec"
	"strings"
)

// Env removes repository/worktree overrides. An index belongs to this command
// only when GIT_DIR was unset or identifies the requested repository's git dir.
func Env(root string) []string {
	parent := os.Environ()
	inherited, set := os.LookupEnv("GIT_DIR")
	keepIndex := !set
	if set {
		clean := append(filtered(parent, false), "GIT_OPTIONAL_LOCKS=0")
		requested, rerr := absoluteDir(clean, "-C", root)
		given, gerr := absoluteDir(clean, "--git-dir="+inherited)
		if rerr == nil && gerr == nil {
			a, aerr := os.Stat(given)
			b, berr := os.Stat(requested)
			keepIndex = aerr == nil && berr == nil && a.IsDir() && b.IsDir() && os.SameFile(a, b)
		}
	}
	return filtered(parent, keepIndex)
}

func absoluteDir(env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append(args, "rev-parse", "--absolute-git-dir")...)
	cmd.Env = env
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\r\n"), err
}

func filtered(parent []string, keepIndex bool) []string {
	env := make([]string, 0, len(parent))
	for _, entry := range parent {
		name, _, _ := strings.Cut(entry, "=")
		if name == "GIT_DIR" || name == "GIT_WORK_TREE" || name == "GIT_INDEX_FILE" && !keepIndex {
			continue
		}
		env = append(env, entry)
	}
	return env
}
