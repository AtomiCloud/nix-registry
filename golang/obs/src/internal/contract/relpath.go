package contract

import (
	"os"
	"path/filepath"
)

// RelPath is how a message names a contract file: relative to the Git root (apps/box/observability/contract.yaml),
// never absolute, so the same message reads the same on every machine. Outside a Git repository it is
// relative to the topmost project marker, or the selected observability project's base.
// A file that is not on disk (a synthetic contract)
// keeps its supplied name, an absolute one that is gone keeps its last two segments.
func RelPath(name string) string {
	if name == "" {
		return "observability/contract.yaml"
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(name))
	}
	if _, err := os.Stat(abs); err != nil {
		if !filepath.IsAbs(name) {
			return filepath.ToSlash(filepath.Clean(name))
		}
		return filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(abs)), filepath.Base(abs)))
	}
	base := gitRoot(filepath.Dir(abs))
	if base == "" {
		base = ProjectRoot(filepath.Dir(abs))
	}
	if rel, err := filepath.Rel(base, abs); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(abs)), filepath.Base(abs)))
}

// Rel is RelPath of the contract's own file.
func (c *Contract) Rel() string { return c.Scope.Rel(c.File) }
