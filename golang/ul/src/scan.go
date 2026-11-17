package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const (
	yamlName = "UL.yaml"
	mdName   = "UL.md"
)

// scanResult lists the UL.yaml files under a root (sorted, relative, slash-separated) and
// the UL.md files committed beside them.
type scanResult struct {
	Root string
	Yaml []string
	MD   map[string]bool
}

var walkSkip = map[string]bool{".git": true, "node_modules": true, ".direnv": true, "vendor": true}

// resolveRoot applies spec §4.1: --root, else the git top level, else the current directory.
func resolveRoot(flagRoot, cwd string) (string, error) {
	if flagRoot != "" {
		abs := flagRoot
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(realPath(cwd), abs) // the kernel resolves .. on the real path
		}
		abs = filepath.Clean(abs)
		st, err := os.Stat(abs)
		if err != nil {
			return "", err
		}
		if !st.IsDir() {
			return "", &fs.PathError{Op: "root", Path: abs, Err: fs.ErrInvalid}
		}
		return realPath(abs), nil
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			return realPath(top), nil
		}
	}
	if err != nil && gitRefused(cwd, err) {
		return "", gitFailure(cwd, err)
	}
	return realPath(cwd), nil
}

// realPath resolves symlinks so the root and --from compare as the same real folders
// (git reports real paths; macOS /var is /private/var). A path that cannot be resolved is
// kept as it is.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// insideGitTree reports whether dir or one of its parents holds a .git entry. When git fails
// there, ul must not fall back to a walk: the walk would read ignored files, or (from a
// subfolder) see only part of the repo and pass a check it should fail.
func insideGitTree(dir string) bool {
	for d := filepath.Clean(dir); ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// gitRefused is true when git failed for a reason other than "not a git repository" while a
// .git entry sits in dir or a parent: a broken or refused repo, never a plain folder.
func gitRefused(dir string, err error) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) && bytes.Contains(ee.Stderr, []byte("not a git repository")) {
		return false
	}
	return insideGitTree(dir)
}

// gitFailure is the error for a git that refuses to run inside a git work tree (for example
// "dubious ownership" in a container): ul stops with exit 2 instead of guessing.
func gitFailure(dir string, err error) error {
	msg := err.Error()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(bytes.TrimSpace(ee.Stderr)) > 0 {
		msg = strings.TrimSpace(string(ee.Stderr))
	}
	return fmt.Errorf("git failed in %s, which is inside a git work tree: %s", dir, msg)
}

// scanRepo finds every UL.yaml. In git it asks git (tracked or untracked-but-not-ignored);
// elsewhere it walks the tree, skipping .git, node_modules, .direnv and vendor.
func scanRepo(root string) (*scanResult, error) {
	res := &scanResult{Root: root, MD: map[string]bool{}}
	cmd := exec.Command("git", "ls-files", "-co", "--exclude-standard", "-z", "--",
		":(glob)**/"+yamlName, ":(glob)**/"+mdName)
	cmd.Dir = root
	cmd.Env = append(gitEnv(), "GIT_OPTIONAL_LOCKS=0")
	if out, err := cmd.Output(); err == nil {
		for _, p := range bytes.Split(out, []byte{0}) {
			rel := string(p)
			if rel == "" {
				continue
			}
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
				continue // tracked but deleted from the work tree
			}
			res.note(rel)
		}
	} else if gitRefused(root, err) {
		return nil, gitFailure(root, err)
	} else if err := res.walk(root); err != nil {
		return nil, err
	}
	slices.Sort(res.Yaml)
	return res, nil
}

func (r *scanResult) note(rel string) {
	switch filepath.Base(rel) {
	case yamlName:
		r.Yaml = append(r.Yaml, rel)
	case mdName:
		r.MD[rel] = true
	}
}

func (r *scanResult) walk(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && walkSkip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == yamlName || d.Name() == mdName {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			r.note(filepath.ToSlash(rel))
		}
		return nil
	})
}

// gitEnv runs git in the C locale: gitRefused reads git's English "not a git repository", and a
// translated git (nix git ships gettext) would otherwise turn a plain folder into an error.
func gitEnv() []string {
	return append(os.Environ(), "LC_ALL=C", "LANGUAGE=")
}
