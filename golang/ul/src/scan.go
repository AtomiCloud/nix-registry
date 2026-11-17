package main

import (
	"bytes"
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
			abs = filepath.Join(cwd, abs)
		}
		abs = filepath.Clean(abs)
		st, err := os.Stat(abs)
		if err != nil {
			return "", err
		}
		if !st.IsDir() {
			return "", &fs.PathError{Op: "root", Path: abs, Err: fs.ErrInvalid}
		}
		return abs, nil
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	if out, err := cmd.Output(); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			return top, nil
		}
	}
	return cwd, nil
}

// scanRepo finds every UL.yaml. In git it asks git (tracked or untracked-but-not-ignored);
// elsewhere it walks the tree, skipping .git, node_modules, .direnv and vendor.
func scanRepo(root string) (*scanResult, error) {
	res := &scanResult{Root: root, MD: map[string]bool{}}
	cmd := exec.Command("git", "ls-files", "-co", "--exclude-standard", "-z", "--",
		":(glob)**/"+yamlName, ":(glob)**/"+mdName)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
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
