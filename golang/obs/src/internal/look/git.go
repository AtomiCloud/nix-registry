package look

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// changed lists the dashboard files to review, relative to Dir with forward slashes:
// the explicit list, every dashboard (--all), or the ones that differ from the merge base
// with Base (committed, edited or new).
func changed(o Options) ([]string, error) {
	if len(o.Files) > 0 {
		return o.Files, nil
	}
	dashDir := "dashboards" // git runs inside Dir, so the pathspec is relative to it
	var out []string
	if o.All {
		list, err := o.Git(o.Dir, "ls-files", "--cached", "--others", "--exclude-standard", "--", dashDir)
		if err != nil {
			return nil, err
		}
		out = lines(list)
	} else {
		if o.Base == "" {
			return nil, fmt.Errorf("no base ref: pass --base <ref> (for example origin/main) or --all")
		}
		mb := o.Base
		if m, err := o.Git(o.Dir, "merge-base", o.Base, "HEAD"); err == nil && strings.TrimSpace(m) != "" {
			mb = strings.TrimSpace(m)
		}
		diff, err := o.Git(o.Dir, "diff", "--name-only", "--relative", "--diff-filter=ACMR", mb, "--", dashDir)
		if err != nil {
			return nil, err
		}
		untracked, err := o.Git(o.Dir, "ls-files", "--others", "--exclude-standard", "--", dashDir)
		if err != nil {
			return nil, err
		}
		out = append(lines(diff), lines(untracked)...)
	}
	seen := map[string]bool{}
	var res []string
	for _, f := range out {
		if !strings.HasSuffix(f, ".json") {
			continue
		}
		rel, err := filepath.Rel(".", f)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if !seen[rel] {
			seen[rel] = true
			res = append(res, rel)
		}
	}
	sort.Strings(res)
	return res, nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
