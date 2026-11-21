package contract

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope is resolved once by --all. Its root and filters follow every child;
// ordinary single-folder commands keep using their existing discovery rules.
type Scope struct {
	Root string
	Skip *RepoSkip
}

func NewScope(root string) (*Scope, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &Scope{Root: abs, Skip: &RepoSkip{newWalkSkip(abs, abs)}}, nil
}

func (s *Scope) Rel(file string) string {
	if s == nil {
		return RelPath(file)
	}
	abs, err := filepath.Abs(file)
	if err == nil {
		if rel, err := filepath.Rel(s.Root, abs); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(file)
}

func (s *Scope) Skips(folder, file string) bool {
	if s == nil {
		return false
	}
	abs, _ := filepath.Abs(file)
	base, _ := filepath.Abs(folder)
	return s.Skip.Skips(base, abs)
}

func LoadInScope(file string, scope *Scope) (*Contract, error) {
	c, err := Load(file)
	if err == nil {
		c.Scope = scope
		for i := range c.Generate {
			c.Generate[i].Scope = scope
		}
	}
	return c, err
}

func DiscoverInScope(dir string, scope *Scope) ([]string, error) {
	if scope == nil {
		return Discover(dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return discoverAt(abs, scope.Root, false, scope)
}

func (s *Scope) Contracts() ([]string, error) { return discoverAt(s.Root, s.Root, true, s) }

// PayloadSkip shares the repository filters with single-folder payload readers
// without changing their configuration or contract discovery boundary.
func PayloadSkip(dir string, scope *Scope) func(string) bool {
	if scope != nil {
		return func(file string) bool { return scope.Skips(dir, file) }
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	root := gitRoot(abs)
	if root == "" {
		return nil
	}
	skip := &RepoSkip{newWalkSkip(root, abs)}
	return func(file string) bool { p, _ := filepath.Abs(file); return skip.Skips(abs, p) }
}

func payloadDir(root, file string, rootObs bool) bool {
	name := filepath.Base(file)
	return file != root && (name == "alerts" || name == "dashboards") && belowObservability(root, filepath.Dir(file), rootObs, true)
}

// A symlink is a contract when its regular target remains within this scope.
func contractEntry(root, file string, entry fs.DirEntry) (bool, error) {
	if entry.Type().IsRegular() {
		return true, nil
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false, nil
	}
	target, err := filepath.EvalSymlinks(file)
	if err != nil {
		return false, fmt.Errorf("contract discovery: cannot read %s: %w", file, err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, fmt.Errorf("contract discovery: %s resolves outside %s", file, root)
	}
	info, err := os.Stat(file)
	if err != nil {
		return false, fmt.Errorf("contract discovery: cannot read %s: %w", file, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("contract discovery: %s is not a regular file", file)
	}
	return true, nil
}

// Folders derives candidates from paths that survived the very same walk filters
// as contract discovery. It never probes ignored child paths with os.Stat.
func (s *Scope) Folders() ([]string, error) {
	seen := map[string]bool{}
	contracts, payloads := map[string]bool{}, map[string]bool{}
	rootObs := rootIsObservability(s.Root, s)
	paths, err := s.Contracts()
	if err != nil {
		return nil, err
	}
	for _, file := range paths {
		folder := filepath.Dir(file)
		seen[folder] = true
		contracts[folder] = true
	}
	err = filepath.WalkDir(s.Root, func(file string, entry fs.DirEntry, err error) error {
		if s.Skip.w.ignored[file] {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("observability folders: cannot read %s: %w", s.Rel(file), err)
		}
		if entry.IsDir() {
			if s.Skip.w.dir(file, entry.Name()) {
				return filepath.SkipDir
			}
			if payloadDir(s.Root, file, rootObs) {
				folder := filepath.Dir(file)
				payloads[folder] = true
				return filepath.SkipDir
			}
		} else if payloadDir(s.Root, file, rootObs) && entry.Type()&os.ModeSymlink != 0 {
			if _, err := os.Stat(file); err != nil {
				return fmt.Errorf("observability folders: cannot read %s: %w", s.Rel(file), err)
			}
			payloads[filepath.Dir(file)] = true
		}
		return nil
	})
	for folder := range payloads {
		if belowObservability(s.Root, folder, rootObs, contracts[folder]) {
			seen[folder] = true
		}
	}
	var folders []string
	for folder := range seen {
		folders = append(folders, folder)
	}
	sort.Strings(folders)
	return folders, err
}
