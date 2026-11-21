package gen

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolvedPath resolves existing ancestors and appends a missing tail. A dangling
// directory alias is resolved through its link target, not treated as a literal folder.
func resolvedPath(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	return resolveTail(filepath.Clean(abs), 0)
}
func resolveTail(name string, links int) (string, error) {
	if links > 255 {
		return "", fmt.Errorf("too many symlinks resolving %s", name)
	}
	tail := ""
	for {
		resolved, err := filepath.EvalSymlinks(name)
		if err == nil {
			return filepath.Join(resolved, tail), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		info, lerr := os.Lstat(name)
		if lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(name)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(name), target)
			}
			return resolveTail(filepath.Join(target, tail), links+1)
		}
		if lerr != nil && !os.IsNotExist(lerr) {
			return "", lerr
		}
		parent := filepath.Dir(name)
		if parent == name {
			return "", err
		}
		tail = filepath.Join(filepath.Base(name), tail)
		name = parent
	}
}

// generatedDestination enforces the same firm leaf rule for every generated file.
func generatedDestination(name string) (string, error) {
	info, err := os.Lstat(name)
	if err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s: generated-file destination must be a regular file or absent; symlinks are refused", name)
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return resolvedPath(name)
}
func validateDestinations(root string, outs []Output) error {
	seen := map[string]string{}
	for _, out := range outs {
		destination, err := generatedDestination(filepath.Join(root, filepath.FromSlash(out.Path)))
		if err != nil {
			return fmt.Errorf("%s: %w", out.Path, err)
		}
		if previous, exists := seen[destination]; exists {
			return fmt.Errorf("two outputs write %s and %s: the same resolved file", previous, out.Path)
		}
		seen[destination] = out.Path
	}
	return nil
}
