// Package treecheck proves a vendored source tree equals the upstream tag it was
// copied from: every file is listed with its sha256 in a manifest made from the
// pristine tag, and the only files allowed beyond the manifest are the ones obs
// added and marked.
package treecheck

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Verify compares the files under root with the manifest (`<sha256>  <relative path>`
// per line). added lists the relative paths obs may add (and may differ from nothing).
// It returns one problem per differing, missing or unexpected file.
func Verify(root, manifest string, added []string) ([]string, error) {
	want := map[string]string{}
	f, err := os.Open(manifest)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, path, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			return nil, fmt.Errorf("%s: bad line %q", manifest, sc.Text())
		}
		want[path] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	allow := map[string]bool{}
	for _, a := range added {
		allow[a] = true
	}
	seen := map[string]bool{}
	var problems []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		seen[rel] = true
		if allow[rel] {
			return nil
		}
		sum, ok := want[rel]
		if !ok {
			problems = append(problems, "unexpected file "+rel)
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != sum {
			problems = append(problems, "changed file "+rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for rel := range want {
		if !seen[rel] {
			problems = append(problems, "missing file "+rel)
		}
	}
	for a := range allow {
		if !seen[a] {
			problems = append(problems, "missing added file "+a)
		}
	}
	sort.Strings(problems)
	return problems, nil
}
