package main

import (
	"cmp"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// lookup returns every entry that matches a word, alias or full id, in lookup order
// (spec §4.3): word before alias; closest namespace to `from`; A–Z.
func (f *Final) lookup(query string, from []string, haveFrom bool) []*FEntry {
	q := fold(strings.TrimSpace(query))
	name := q
	var cands []*FEntry
	if i := strings.Index(query, "#"); i >= 0 {
		ns := query[:i]
		name = fold(strings.TrimSpace(query[i+1:]))
		for _, fe := range f.Entries {
			if fe.NS.Namespace == ns && matchesName(fe, name) > 0 {
				cands = append(cands, fe)
			}
		}
	} else {
		cands = slices.Clone(f.nameIdx[q])
	}
	rank := func(fe *FEntry) (word int, depth int, shared int) {
		if matchesName(fe, name) == 2 {
			word = 1
		}
		if haveFrom {
			dir := splitDir(path.Dir(fe.NS.File))
			if len(dir) <= len(from) && slices.Equal(dir, from[:len(dir)]) {
				depth = len(dir) + 1 // contained: deeper wins
			}
			for shared < len(dir) && shared < len(from) && dir[shared] == from[shared] {
				shared++
			}
		}
		return
	}
	slices.SortStableFunc(cands, func(a, b *FEntry) int {
		aw, ad, ash := rank(a)
		bw, bd, bsh := rank(b)
		return cmp.Or(
			cmp.Compare(bw, aw), cmp.Compare(bd, ad), cmp.Compare(bsh, ash),
			cmpFold(a.NS.Namespace, b.NS.Namespace), cmpFold(a.E.Word, b.E.Word))
	})
	return cands
}

// matchesName: 2 when the entry's word is name, 1 when one of its aliases is, else 0.
func matchesName(fe *FEntry, name string) int {
	if fold(fe.E.Word) == name {
		return 2
	}
	for _, a := range fe.E.Aliases {
		if fold(a.Name) == name {
			return 1
		}
	}
	return 0
}

// fromParts turns --from into the folder parts below the root. ok is false when the path is
// outside the root (it then ranks nothing).
func fromParts(root, cwd, p string) ([]string, bool) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(realPath(cwd), abs) // the kernel resolves .. on the real path
	}
	abs = filepath.Clean(abs)
	st, serr := os.Stat(abs)
	switch r, rerr := filepath.EvalSymlinks(abs); {
	case serr == nil && st.IsDir():
		// a folder keeps its own name
	case serr == nil && rerr == nil:
		abs = filepath.Dir(r) // an existing file: its real folder, even when the file is a symlink
	default:
		abs = filepath.Dir(abs) // a missing path: the nearest existing parent is resolved below
	}
	abs = realExisting(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, false
	}
	return splitDir(filepath.ToSlash(rel)), true
}

// realExisting resolves symlinks in the nearest existing parent of p and keeps the rest, so a
// --from path through a symlink ranks like its real path (the root is a real path).
func realExisting(p string) string {
	rest := ""
	for d := p; ; {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return p
		}
		rest = filepath.Join(filepath.Base(d), rest)
		d = parent
	}
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// near lists up to five entries whose word or alias is close to the query (a substring match
// or a small edit distance).
func (f *Final) near(query string) []*FEntry {
	q := fold(strings.TrimSpace(query))
	if i := strings.Index(q, "#"); i >= 0 {
		q = q[i+1:]
	}
	if q == "" {
		return nil
	}
	qr := []rune(q)
	limit := 1
	if utf8.RuneCountInString(q) >= 5 {
		limit = 2
	}
	type scored struct {
		fe   *FEntry
		dist int
	}
	var out []scored
	for _, fe := range f.Entries {
		best := -1
		for _, n := range append([]string{fe.E.Word}, names(fe.E.Aliases)...) {
			ln := fold(n)
			d := levenshtein(qr, []rune(ln))
			if d > limit && utf8.RuneCountInString(q) >= 2 && (strings.Contains(ln, q) || strings.Contains(q, ln)) {
				d = limit
			}
			if d <= limit && (best < 0 || d < best) {
				best = d
			}
		}
		if best >= 0 {
			out = append(out, scored{fe, best})
		}
	}
	slices.SortStableFunc(out, func(a, b scored) int {
		return cmp.Or(cmp.Compare(a.dist, b.dist), cmpFold(a.fe.E.Word, b.fe.E.Word), cmpFold(a.fe.NS.Namespace, b.fe.NS.Namespace))
	})
	var res []*FEntry
	for i := 0; i < len(out) && i < 5; i++ {
		res = append(res, out[i].fe)
	}
	return res
}
