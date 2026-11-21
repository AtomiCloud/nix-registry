package contract

import (
	"os"
	"path/filepath"
)

// OutputBase is the folder holding the nearest observability/ ancestor. Both
// feature/observability and observability/feature resolve from that same rule.
// A standalone observability repository resolves outputs from its own root.
func OutputBase(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	boundary := gitRoot(abs)
	if boundary == "" {
		boundary = ProjectRoot(abs)
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
	if rootIsObservability(boundary) {
		return boundary
	}
	return filepath.Dir(abs)
}

// ProjectRoot is the topmost ancestor containing a project marker, independent
// of Git and of evaluating a package identity. It is also the source-export
// discovery/header boundary. With no marker, stay within the selected project.
func ProjectRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	root := abs
	for folder := abs; ; folder = filepath.Dir(folder) {
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
		if filepath.Dir(folder) == folder {
			break
		}
	}
	return root
}

func projectMarker(dir string) bool {
	for _, name := range []string{"go.mod", "package.json", "pubspec.yaml"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.Mode().IsRegular() {
			return true
		}
	}
	for _, pattern := range []string{"*.sln", "*.csproj"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, file := range matches {
			if st, err := os.Stat(file); err == nil && st.Mode().IsRegular() {
				return true
			}
		}
	}
	return false
}
