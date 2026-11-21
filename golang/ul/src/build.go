package main

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// FNS is one namespace of the final UL.
type FNS struct {
	Namespace  string
	Breadcrumb []string
	Premise    string
	File       string    // relative to the root
	Entries    []*FEntry // sorted A–Z
	anchors    map[*FEntry]string
}

// FEntry is one word of the final UL.
type FEntry struct {
	E          *Entry
	NS         *FNS
	ID         string
	File       string
	Retired    bool
	See        []string
	Mentions   []string
	ReplacedBy string
}

// Final is the UL of a repo: every UL.yaml found by the scan.
type Final struct {
	Root     string
	NS       []*FNS
	Entries  []*FEntry // by namespace, then word
	Problems []Problem
	Flags    []Problem // rules that warn and pass (UL406); only ul check shows them

	byID    map[string]*FEntry
	nameIdx map[string][]*FEntry          // lower-case word or alias -> distinct entries
	nsNames map[string]map[string]*FEntry // namespace -> lower-case word or alias -> entry
}

func fold(s string) string { return strings.ToLower(s) }

// buildFinal assembles the repo documents, then runs the rules that need the whole picture (UL103, UL107, UL109, UL201–UL204) and computes mentions.
func buildFinal(root string, repo []*Doc, base []Problem) *Final {
	f := &Final{Root: root, Problems: slices.Clone(base),
		byID: map[string]*FEntry{}, nameIdx: map[string][]*FEntry{}, nsNames: map[string]map[string]*FEntry{}}
	nsMap := map[string]*FNS{}
	problem := func(code, file string, line int, format string, a ...any) {
		f.Problems = append(f.Problems, Problem{Code: code, File: file, Line: max(line, 1), Message: fmt.Sprintf(format, a...)})
	}

	for _, d := range repo {
		if prev, ok := nsMap[d.Namespace]; ok {
			problem("UL103", d.File, d.NSLine, "namespace %s is already defined in %s", d.Namespace, prev.File)
			continue
		}
		ns := &FNS{Namespace: d.Namespace, Breadcrumb: breadcrumb(d.Namespace), Premise: d.Premise, File: d.File}
		nsMap[d.Namespace] = ns
		for _, e := range d.Entries {
			ns.Entries = append(ns.Entries, &FEntry{E: e, NS: ns, File: d.File})
		}
	}
	for _, ns := range nsMap {
		f.NS = append(f.NS, ns)
	}
	slices.SortFunc(f.NS, func(a, b *FNS) int { return cmpFold(a.Namespace, b.Namespace) })
	for _, ns := range f.NS {
		slices.SortFunc(ns.Entries, func(a, b *FEntry) int { return cmpFold(a.E.Word, b.E.Word) })
		for _, fe := range ns.Entries {
			fe.ID = makeID(ns.Namespace, fe.E.Word)
			fe.Retired = fe.E.Status == statusRetired
			f.byID[fe.ID] = fe
			f.Entries = append(f.Entries, fe)
		}
	}

	f.buildIndexes()
	f.checkNames(problem)
	f.flagSameWord()
	f.resolveRefs(problem)
	f.computeMentions()
	slices.SortStableFunc(f.Problems, func(a, b Problem) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		if c := strings.Compare(a.Code, b.Code); c != 0 {
			return c
		}
		return strings.Compare(a.Message, b.Message)
	})
	f.Problems = slices.Compact(f.Problems)
	return f
}

func (f *Final) buildIndexes() {
	for _, ns := range f.NS {
		m := map[string]*FEntry{}
		for _, fe := range ns.Entries { // words first, so a word beats an alias of the same name
			if _, ok := m[fold(fe.E.Word)]; !ok {
				m[fold(fe.E.Word)] = fe
			}
		}
		for _, fe := range ns.Entries {
			for _, a := range fe.E.Aliases {
				if _, ok := m[fold(a.Name)]; !ok {
					m[fold(a.Name)] = fe
				}
			}
		}
		f.nsNames[ns.Namespace] = m
		for _, fe := range ns.Entries {
			f.addName(fe.E.Word, fe)
			for _, a := range fe.E.Aliases {
				f.addName(a.Name, fe)
			}
		}
	}
}

func (f *Final) addName(name string, fe *FEntry) {
	k := fold(name)
	if l := f.nameIdx[k]; len(l) > 0 && l[len(l)-1] == fe {
		return
	}
	f.nameIdx[k] = append(f.nameIdx[k], fe)
}

// checkNames: UL107 (names that match ignoring case) and UL109 (avoid names that are names).
func (f *Final) checkNames(problem func(code, file string, line int, format string, a ...any)) {
	for _, ns := range f.NS {
		ordered := slices.Clone(ns.Entries)
		slices.SortFunc(ordered, func(a, b *FEntry) int {
			if c := strings.Compare(a.File, b.File); c != 0 {
				return c
			}
			return a.E.WordLine - b.E.WordLine
		})
		type seenName struct {
			name, kind, owner string
		}
		seen := map[string]seenName{}
		see := func(fe *FEntry, name, kind string, line int) {
			k := fold(name)
			if prev, ok := seen[k]; ok {
				problem("UL107", fe.File, line, "%s %q matches %s %q of %q (names match ignoring case)", kind, name, prev.kind, prev.name, prev.owner)
				return
			}
			seen[k] = seenName{name, kind, fe.E.Word}
		}
		for _, fe := range ordered {
			see(fe, fe.E.Word, "word", fe.E.WordLine)
			for _, a := range fe.E.Aliases {
				see(fe, a.Name, "alias", a.Line)
			}
		}
		for _, fe := range ordered {
			for _, av := range fe.E.Avoid {
				if hit, ok := f.nsNames[ns.Namespace][fold(av.Name)]; ok {
					problem("UL109", fe.File, av.Line, "%s: avoid %q is a word or alias of this namespace (%s)", fe.E.Word, av.Name, hit.E.Word)
				}
			}
		}
	}
}

type refResult struct {
	target *FEntry
	code   string // "", "UL201" or "UL202"
	msg    string
}

// resolveRef resolves a bare word or a full id from entry `from` (spec §2.3).
func (f *Final) resolveRef(from *FEntry, ref string) refResult {
	if i := strings.Index(ref, "#"); i >= 0 {
		ns, w := ref[:i], ref[i+1:]
		if names, ok := f.nsNames[ns]; ok {
			if t, ok := names[fold(w)]; ok {
				return refResult{target: t}
			}
		}
		return refResult{code: "UL201", msg: fmt.Sprintf("%s does not resolve", ref)}
	}
	if t, ok := f.nsNames[from.NS.Namespace][fold(ref)]; ok {
		return refResult{target: t}
	}
	switch c := f.nameIdx[fold(ref)]; len(c) {
	case 0:
		return refResult{code: "UL201", msg: fmt.Sprintf("%s does not resolve", ref)}
	case 1:
		return refResult{target: c[0]}
	default:
		ids := make([]string, len(c))
		for i, fe := range c {
			ids[i] = fe.ID
		}
		return refResult{code: "UL202", msg: fmt.Sprintf("%s is in more than one namespace (%s); write the full id", ref, strings.Join(ids, ", "))}
	}
}

func (f *Final) resolveRefs(problem func(code, file string, line int, format string, a ...any)) {
	for _, fe := range f.Entries {
		for _, s := range fe.E.See {
			r := f.resolveRef(fe, s.Name)
			if r.code != "" {
				problem(r.code, fe.File, s.Line, "see: %s", r.msg)
				continue
			}
			if !fe.Retired && r.target.Retired {
				problem("UL203", fe.File, s.Line, "see: %s is retired; see its replacement instead", s.Name)
			}
			if !slices.Contains(fe.See, r.target.ID) {
				fe.See = append(fe.See, r.target.ID)
			}
		}
		if rb := fe.E.ReplacedBy; rb.Name != "" {
			if !fe.Retired {
				problem("UL204", fe.File, rb.Line, "replacedBy is only for a retired word; %s is active", fe.E.Word)
			}
			r := f.resolveRef(fe, rb.Name)
			if r.code != "" {
				problem(r.code, fe.File, rb.Line, "replacedBy: %s", r.msg)
				continue
			}
			if r.target.Retired {
				problem("UL204", fe.File, rb.Line, "replacedBy: %s is retired too", rb.Name)
			}
			fe.ReplacedBy = r.target.ID
		}
	}
}

// trie matches every word and alias in one pass over a text (spec §4.4).
type trie struct {
	next map[rune]*trie
	ents []*FEntry
}

func foldRunes(s string) []rune {
	rs := []rune(s)
	for i, r := range rs {
		rs[i] = unicode.ToLower(r)
	}
	return rs
}

func (t *trie) insert(name string, fe *FEntry) {
	rs := foldRunes(name)
	if len(rs) < 2 {
		return
	}
	n := t
	for _, r := range rs {
		if n.next == nil {
			n.next = map[rune]*trie{}
		}
		c := n.next[r]
		if c == nil {
			c = &trie{}
			n.next[r] = c
		}
		n = c
	}
	if len(n.ents) == 0 || n.ents[len(n.ents)-1] != fe {
		n.ents = append(n.ents, fe)
	}
}

// scan calls hit for the entries of the longest whole-word name at each position.
func (t *trie) scan(s string, hit func(ents []*FEntry)) {
	rs := foldRunes(s)
	for i := 0; i < len(rs); {
		if i > 0 && isWordRune(rs[i-1]) {
			i++
			continue
		}
		n := t
		best := -1
		var bestEnts []*FEntry
		for j := i; j < len(rs); j++ {
			n = n.next[rs[j]]
			if n == nil {
				break
			}
			if len(n.ents) > 0 && (j+1 == len(rs) || !isWordRune(rs[j+1])) {
				best, bestEnts = j+1, n.ents
			}
		}
		if best > 0 {
			hit(bestEnts)
			i = best
		} else {
			i++
		}
	}
}

func (f *Final) computeMentions() {
	root := &trie{}
	for _, fe := range f.Entries {
		root.insert(fe.E.Word, fe)
		for _, a := range fe.E.Aliases {
			root.insert(a.Name, fe)
		}
	}
	for _, fe := range f.Entries {
		found := map[string]bool{}
		hit := func(ents []*FEntry) {
			var same []*FEntry
			for _, c := range ents {
				if c.NS == fe.NS {
					same = append(same, c)
				}
			}
			if len(same) > 0 {
				if slices.Contains(same, fe) {
					return // the entry's own name
				}
				ents = same
			}
			for _, c := range ents {
				if c != fe {
					found[c.ID] = true
				}
			}
		}
		root.scan(fe.E.Desc, hit)
		if fe.E.Details != "" {
			root.scan(fe.E.Details, hit)
		}
		for id := range found {
			fe.Mentions = append(fe.Mentions, id)
		}
		slices.Sort(fe.Mentions)
	}
}

// span is one whole-word name found in a text: rune offsets [start, end). name is the name as
// defined (lower case, without a plural ending); plural says the text wrote it with one.
type span struct {
	start, end int
	name       string
	plural     bool
	ents       []*FEntry
}

func isVowel(r rune) bool { return strings.ContainsRune("aeiou", r) }

// scanSpans finds the longest whole-word name at each position, with where it sits. A name also
// matches with a plural ending as English writes it: -es after a name that ends in s, x, z, ch
// or sh, -ies for a name that ends in a consonant and y, -s after any other name (so "conflict
// resolvers" is the name "conflict resolver"). The longest match wins; at the same end the exact
// name beats a plural, and between two plurals the longer name wins.
func (t *trie) scanSpans(s string) []span {
	rs := foldRunes(s)
	var out []span
	atEnd := func(k int) bool { return k == len(rs) || !isWordRune(rs[k]) }
	hasAt := func(k int, suffix string) bool {
		x := 0
		for _, r := range suffix {
			if k+x >= len(rs) || rs[k+x] != r {
				return false
			}
			x++
		}
		return atEnd(k + x)
	}
	for i := 0; i < len(rs); {
		if i > 0 && isWordRune(rs[i-1]) {
			i++
			continue
		}
		n := t
		best := -1
		bestName, bestPlural := "", false
		var bestEnts []*FEntry
		take := func(end int, name string, plural bool, ents []*FEntry) {
			if end > best || (end == best && (!plural || bestPlural)) {
				best, bestName, bestPlural, bestEnts = end, name, plural, ents
			}
		}
		for j := i; j < len(rs); j++ {
			// n is the node after rs[i..j-1]: a name that ends in a consonant and y, written as "ies"
			if j > i && !isVowel(rs[j-1]) {
				if c := n.next['y']; c != nil && len(c.ents) > 0 && hasAt(j, "ies") {
					take(j+3, string(rs[i:j])+"y", true, c.ents)
				}
			}
			n = n.next[rs[j]]
			if n == nil {
				break
			}
			if len(n.ents) == 0 {
				continue
			}
			last := rs[j]
			two := ""
			if j > i {
				two = string(rs[j-1 : j+1])
			}
			switch {
			case atEnd(j + 1):
				take(j+1, string(rs[i:j+1]), false, n.ents)
			case last == 's' || last == 'x' || last == 'z' || two == "ch" || two == "sh":
				if hasAt(j+1, "es") {
					take(j+3, string(rs[i:j+1]), true, n.ents)
				}
			default:
				// a name that ends in a consonant and y has -ies, never -s (Cities, not Citys)
				consonantY := last == 'y' && j > i && !isVowel(rs[j-1])
				if !consonantY && hasAt(j+1, "s") {
					take(j+2, string(rs[i:j+1]), true, n.ents)
				}
			}
		}
		if best > 0 {
			out = append(out, span{i, best, bestName, bestPlural, bestEnts})
			i = best
		} else {
			i++
		}
	}
	return out
}

// nameTrie holds every word and alias of the final UL.
func (f *Final) nameTrie() *trie {
	root := &trie{}
	for _, fe := range f.Entries {
		root.insert(fe.E.Word, fe)
		for _, a := range fe.E.Aliases {
			root.insert(a.Name, fe)
		}
	}
	return root
}

// flagSameWord: UL406, a flag. The same word or alias (any case) defined in two namespaces,
// retired words included. Both are kept (the same-word rule); the later one is flagged.
func (f *Final) flagSameWord() {
	keys := make([]string, 0, len(f.nameIdx))
	for k := range f.nameIdx {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		ents := f.nameIdx[k]
		for _, fe := range ents[1:] {
			if fe.NS == ents[0].NS {
				continue
			}
			name, line := fe.E.Word, fe.E.WordLine
			if fold(fe.E.Word) != k {
				for _, a := range fe.E.Aliases {
					if fold(a.Name) == k {
						name, line = a.Name, a.Line
					}
				}
			}
			f.Flags = append(f.Flags, Problem{Code: "UL406", File: fe.File, Line: max(line, 1), Message: fmt.Sprintf("%q is also defined in %s; one repo should not define a word twice", name, ents[0].NS.Namespace)})
		}
	}
}
