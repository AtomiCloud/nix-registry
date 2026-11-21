package contract

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
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

// Folders derives candidates from paths that survived the very same walk filters
// as contract discovery. It never probes ignored child paths with os.Stat.
func (s *Scope) Folders() ([]string, error) {
	seen := map[string]bool{}
	rootObs := rootIsObservability(s.Root, s)
	err := filepath.WalkDir(s.Root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("observability folders: cannot read %s: %w", s.Rel(file), err)
		}
		if entry.IsDir() {
			if s.Skip.w.dir(file, entry.Name()) {
				return filepath.SkipDir
			}
			if entry.Name() == "alerts" || entry.Name() == "dashboards" {
				folder := filepath.Dir(file)
				if belowObservability(s.Root, folder, rootObs, true) {
					seen[folder] = true
				}
			}
		} else if entry.Name() == "contract.yaml" && entry.Type().IsRegular() && !s.Skip.w.ignored[file] {
			folder := filepath.Dir(file)
			if belowObservability(s.Root, folder, rootObs, true) {
				seen[folder] = true
			}
		}
		return nil
	})
	var folders []string
	for folder := range seen {
		folders = append(folders, folder)
	}
	sort.Strings(folders)
	return folders, err
}
