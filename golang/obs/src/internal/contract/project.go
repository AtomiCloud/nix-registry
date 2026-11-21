package contract

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// OutputBase is the folder holding the nearest observability/ ancestor. Both
// feature/observability and observability/feature resolve from that same rule.
// A standalone observability repository resolves outputs from its own root.
func OutputBase(dir string, scopes ...*Scope) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	boundary := ""
	if len(scopes) > 0 && scopes[0] != nil {
		boundary = scopes[0].Root
	} else {
		boundary = gitRoot(abs)
		if boundary == "" {
			boundary = ProjectRoot(abs)
		}
	}
	for folder := abs; ; folder = filepath.Dir(folder) {
		if folder == boundary {
			break
		}
		if filepath.Base(folder) == "observability" {
			return filepath.Dir(folder)
		}
		if filepath.Dir(folder) == folder {
			break
		}
	}
	if rootIsObservability(boundary, scopes...) {
		return boundary
	}
	return filepath.Dir(abs)
}

// ProjectFiles finds source project files using the repository discovery skips.
// It does not require an observability folder around the project itself.
func ProjectFiles(dir, suffix string, scopes ...*Scope) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root := ""
	var skip *walkSkip
	if len(scopes) > 0 && scopes[0] != nil {
		root = scopes[0].Root
		skip = scopes[0].Skip.w
	} else {
		root = gitRoot(abs)
		if root == "" {
			root = ProjectRoot(abs)
		}
		skip = newWalkSkip(root, abs)
	}
	var files []string
	err = filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("project discovery: cannot read %s: %w", file, err)
		}
		if entry.IsDir() {
			if skip.dir(file, entry.Name()) {
				return filepath.SkipDir
			}
		} else if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), suffix) && !skip.ignored[file] {
			files = append(files, file)
		}
		return nil
	})
	return files, err
}

// ProjectRoot is the topmost ancestor containing a project marker, independent
// of Git and of evaluating a package identity. It is also the source-export
// discovery/header boundary. With no marker, stay within the selected project.
func ProjectRoot(dir string) string {
	home, _ := os.UserHomeDir()
	return projectRoot(dir, home)
}

func projectRoot(dir, home string) string {
	if home != "" {
		home = filepath.Clean(home)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	root := abs
	for folder := abs; ; folder = filepath.Dir(folder) {
		if folder == home {
			break
		}
		if filepath.Base(folder) == "observability" {
			root = filepath.Dir(folder)
			break
		}
		if filepath.Dir(folder) == folder {
			break
		}
	}
	for folder := abs; ; folder = filepath.Dir(folder) {
		if projectMarker(folder) {
			root = folder
		}
		if folder == home || filepath.Dir(folder) == folder {
			break
		}
	}
	return root
}

// HomeBoundary is the last ancestor a Git-less manifest walk may inspect.
// Home-level markers/manifests count; nothing above the home is read.
func HomeBoundary(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	home = filepath.Clean(home)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(home, abs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return home
	}
	return ""
}

func projectMarker(dir string) bool {
	for _, name := range []string{"flake.nix", "go.work", "go.mod", "package.json", "pubspec.yaml"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.Mode().IsRegular() {
			return true
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sln") || strings.HasSuffix(entry.Name(), ".slnx") || strings.HasSuffix(entry.Name(), ".csproj") {
			if st, err := entry.Info(); err == nil && st.Mode().IsRegular() {
				return true
			}
		}
	}
	return false
}
