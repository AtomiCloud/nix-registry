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

// matchFunctions matches name+normalized file. Lines only disambiguate repeated
// declarations: allocate the nearest available pair first, once per declaration.
func matchFunctions(base []Row, now []FuncRow) map[int]int {
	type pair struct {
		b, n           int
		distance       uint64
		columnDistance uint64
	}
	var pairs []pair
	for bi, b := range base {
		for ni, n := range now {
			if b.Name != n.Name || sourceFile(b.File) != sourceFile(n.File) {
				continue
			}
			d := b.Line - n.Line
			if d < 0 {
				d = -d
			}
			cd := b.Column - n.Column
			if cd < 0 {
				cd = -cd
			}
			pairs = append(pairs, pair{bi, ni, uint64(d), uint64(cd)})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		a, b := pairs[i], pairs[j]
		if a.distance != b.distance {
			return a.distance < b.distance
		}
		if a.columnDistance != b.columnDistance {
			return a.columnDistance < b.columnDistance
		}
		if base[a.b].Line != base[b.b].Line {
			return base[a.b].Line < base[b.b].Line
		}
		if now[a.n].Line != now[b.n].Line {
			return now[a.n].Line < now[b.n].Line
		}
		if a.b != b.b {
			return a.b < b.b
		}
		return a.n < b.n
	})
	matched, used := map[int]int{}, map[int]bool{}
	for _, p := range pairs {
		if _, ok := matched[p.b]; ok || used[p.n] {
			continue
		}
		matched[p.b], used[p.n] = p.n, true
	}
	return matched
}
