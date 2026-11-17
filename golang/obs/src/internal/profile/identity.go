package profile

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func explicitSourceRoot(root string) (string, error) {
	root = sourceFile(root)
	if strings.HasPrefix(root, "/") || (len(root) > 2 && root[1] == ':') {
		return root, nil
	}
	abs, err := filepath.Abs(root)
	return sourceFile(abs), err
}

func inferredRootNote(p *Profile, own *ownMatcher, root string) string {
	files := map[string]bool{}
	for _, s := range p.Samples {
		for _, f := range s.Stack {
			if f.File != "" && own.match(f) {
				files[sourceFile(f.File)] = true
			}
		}
	}
	if len(files) != 1 {
		return ""
	}
	for file := range files {
		for _, part := range strings.Split(path.Dir(file), "/") {
			switch part {
			case "src", "internal", "cmd", "pkg", "lib", "app":
				return ""
			}
		}
		return fmt.Sprintf("source-root inference used single unanchored path %q (root %q); pass --source-root to choose the checkout root explicitly", file, root)
	}
	return ""
}

func sourceFile(file string) string {
	if strings.HasPrefix(file, "file://") {
		file = strings.TrimPrefix(file, "file://")
	}
	if unescaped, err := url.PathUnescape(file); err == nil {
		file = unescaped
	}
	file = strings.ReplaceAll(file, `\`, "/")
	if file == "" {
		return ""
	}
	return path.Clean(file)
}

// Formats do not record the checkout root. Infer the common directory of the
// selected own-code sources, then keep paths relative to it. This removes both
// checkout-specific absolute roots and file:// prefixes without using basenames
// as identities when multiple source directories are present.
func sourceRoot(p *Profile, own *ownMatcher) string {
	root, found := "", false
	for _, sample := range p.Samples {
		for _, f := range sample.Stack {
			if !own.match(f) || f.File == "" {
				continue
			}
			dir := path.Dir(sourceFile(f.File))
			if !found {
				root, found = dir, true
				continue
			}
			for dir != root && !strings.HasPrefix(dir, strings.TrimSuffix(root, "/")+"/") {
				next := path.Dir(root)
				if next == root {
					return ""
				}
				root = next
			}
		}
	}
	// Choose one root for the whole profile, never a different root per file:
	// client/src/index.js and server/src/index.js must remain distinct.
	absolute := strings.HasPrefix(root, "/")
	parts := strings.Split(strings.TrimPrefix(root, "/"), "/")
	anchors := map[string]bool{"src": true, "internal": true, "cmd": true, "pkg": true, "lib": true, "app": true}
	for i, part := range parts {
		if anchors[part] {
			prefix := strings.Join(parts[:i], "/")
			if absolute {
				return "/" + prefix
			}
			if prefix == "" {
				return "."
			}
			return prefix
		}
	}
	return root
}

func relativeSource(file, root string) string {
	file = sourceFile(file)
	if !strings.HasPrefix(file, "/") && !(len(file) > 2 && file[1] == ':') {
		return file
	}
	if root != "" && root != "." {
		file = strings.TrimPrefix(file, strings.TrimSuffix(root, "/")+"/")
	}
	return file
}

type functionKey struct{ name, file string }

func functionKeyOf(name, file string) functionKey { return functionKey{name, sourceFile(file)} }

func groupedFunctions(functions []FuncRow) map[functionKey][]int {
	groups := map[functionKey][]int{}
	for i, f := range functions {
		k := functionKeyOf(f.Name, f.File)
		groups[k] = append(groups[k], i)
	}
	for _, indices := range groups {
		sort.SliceStable(indices, func(i, j int) bool {
			a, b := functions[indices[i]], functions[indices[j]]
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Column < b.Column
		})
	}
	return groups
}

func ambiguousDuplicate(k functionKey, baseline, now int) string {
	return fmt.Sprintf("ambiguous duplicate %s in %s: %d in baseline, %d now; rename or baseline --update", k.name, k.file, baseline, now)
}

// matchFunctions never guesses from distance. Equal-size declaration groups
// match in source order; count changes make every baseline row in that group an
// error. Cold declarations are counted but do not become comparison rows.
func matchFunctions(base []Row, now []FuncRow) (map[int]int, map[int]string, map[int]bool) {
	baseGroups := map[functionKey][]int{}
	for i, b := range base {
		k := functionKeyOf(b.Name, b.File)
		baseGroups[k] = append(baseGroups[k], i)
	}
	nowGroups := groupedFunctions(now)
	matched, ambiguous, blocked := map[int]int{}, map[int]string{}, map[int]bool{}
	for k, indices := range baseGroups {
		sort.SliceStable(indices, func(i, j int) bool {
			a, b := base[indices[i]], base[indices[j]]
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Column < b.Column
		})
		expected := len(indices)
		for _, bi := range indices {
			if base[bi].Declarations > 0 {
				expected = base[bi].Declarations
				break
			}
		}
		current := nowGroups[k]
		if expected != len(current) {
			for _, bi := range indices {
				ambiguous[bi] = ambiguousDuplicate(k, expected, len(current))
			}
			for _, ni := range current {
				blocked[ni] = true
			}
			continue
		}
		for order, bi := range indices {
			if base[bi].Declarations > 0 {
				order = base[bi].SourceOrder
			}
			if order < 0 || order >= len(current) {
				ambiguous[bi] = "invalid declaration order; baseline --update"
				continue
			}
			matched[bi] = current[order]
		}
	}
	return matched, ambiguous, blocked
}
