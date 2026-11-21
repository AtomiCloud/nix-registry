package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// The word checks over the doc pages of a repo (design §4.1, ul table): every .md and .dh.md
// under docs/ whose front matter has `namespaces`. ul reads only `namespaces`, `concept` and
// `translation`; the rest of the front matter is doccheck's.

const i18nDir = docsDir + "/i18n/"

type page struct {
	rel         string
	dh          bool
	namespaces  []Named // with their front matter lines
	concept     string
	conceptLine int
	tr          *trInfo
	segs        []Seg
	isADR       bool
	i18n        bool
}

type trInfo struct {
	lang, source string
	line         int
}

// readFrontMatter reads the three fields ul needs. ok is false for a page whose front matter is
// missing, is not a mapping, or has no `namespaces` (those are doccheck's to refuse).
func readFrontMatter(fm []byte, i18n bool) (p page, ok bool) {
	if len(fm) == 0 {
		return p, false
	}
	var n yaml.Node
	if err := yaml.Unmarshal(fm, &n); err != nil || len(n.Content) == 0 {
		return p, false
	}
	m := resolveNode(n.Content[0])
	if m.Kind != yaml.MappingNode {
		return p, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := resolveNode(m.Content[i]), resolveNode(m.Content[i+1])
		switch k.Value {
		case "namespaces":
			ok = true
			if v.Kind == yaml.SequenceNode {
				for _, c := range v.Content {
					if c = resolveNode(c); c.Kind == yaml.ScalarNode && c.Value != "" {
						p.namespaces = append(p.namespaces, Named{c.Value, c.Line + 1})
					}
				}
			}
		case "concept":
			if v.Kind == yaml.ScalarNode {
				p.concept, p.conceptLine = v.Value, v.Line+1
			}
		case "translation":
			if v.Kind == yaml.MappingNode {
				t := &trInfo{line: k.Line + 1}
				for j := 0; j+1 < len(v.Content); j += 2 {
					kk, vv := resolveNode(v.Content[j]), resolveNode(v.Content[j+1])
					switch kk.Value {
					case "lang":
						t.lang = vv.Value
					case "source":
						t.source = vv.Value
					}
				}
				p.tr = t
				if t.lang != "" && t.source != "" && i18n {
					ok = true // a translation under docs/i18n/ is read for its source, with or without namespaces
				}
			}
		}
	}
	return p, ok
}

// loadPages reads the doc pages: front matter first, then the prose of each page (Markdown
// with CommonMark here, .dh.md through dh).
func (l *loaded) loadPages() (pages []*page, probs []Problem, err error) {
	var dhRels, probe []string
	byRel := map[string]*page{}
	want := map[string]string{} // the text dh must print back for each page
	for _, rel := range l.scan.Docs {
		i18n := strings.HasPrefix(rel, i18nDir)
		if i18n && l.opts.translations == modeFree {
			continue // translations are not checked; nothing reads them
		}
		data, rerr := os.ReadFile(filepath.Join(l.root, filepath.FromSlash(rel)))
		if rerr != nil {
			probs = append(probs, Problem{Code: "UL410", File: rel, Line: 1, Message: "cannot read the page: " + readErrText(rerr)})
			continue
		}
		fm, body, bodyLine := splitFrontMatter(data)
		want[rel] = strings.ReplaceAll(string(normalizeNewlines(data)), "\x00", "\ufffd")
		p, ok := readFrontMatter(fm, i18n)
		if !ok {
			if len(fm) > 0 && strings.HasSuffix(rel, ".dh.md") && !frontMatterParses(fm) {
				probe = append(probe, rel) // dh refuses it, with its line
			}
			continue
		}
		p.rel, p.i18n = rel, i18n
		p.dh = strings.HasSuffix(rel, ".dh.md")
		p.isADR = slices.Contains(strings.Split(path.Dir(rel), "/"), "adr")
		if p.dh {
			dhRels = append(dhRels, rel)
		} else {
			p.segs = mdSegs(body, bodyLine)
		}
		pg := p
		pages = append(pages, &pg)
		byRel[rel] = &pg
	}
	if len(dhRels)+len(probe) > 0 {
		docs, dprobs, derr := runDh(l.opts.dh, l.root, append(slices.Clone(dhRels), probe...))
		if derr != nil {
			return nil, nil, derr
		}
		probs = append(probs, dprobs...)
		for _, rel := range dhRels {
			if d := docs[rel]; d != nil {
				if w, ok := want[rel]; ok && d.Source != w {
					return nil, nil, fmt.Errorf("dh json printed another page for %s: its source is not the file", rel)
				}
				segs, sp := dhSegs(d, rel)
				byRel[rel].segs = segs
				probs = append(probs, sp...)
			}
		}
	}
	return pages, probs, nil
}

// used is one UL entry a page uses, and the names it wrote for it.
type used struct {
	fe    *FEntry
	names map[string]bool
}

type pageUse struct {
	entries []*used
	byID    map[string]*used
}

func (u *pageUse) mark(fe *FEntry, name string) {
	e := u.byID[fe.ID]
	if e == nil {
		e = &used{fe: fe, names: map[string]bool{}}
		u.byID[fe.ID] = e
		u.entries = append(u.entries, e)
	}
	e.names[fold(name)] = true
}

func isStandardNS(ns string) bool {
	segs := strings.Split(ns, "/")
	return len(segs) >= 3 && segs[1] == "engineering" && segs[2] == "standards"
}

func nsList(ents []*FEntry) string {
	var ns []string
	for _, fe := range ents {
		if !slices.Contains(ns, fe.NS.Namespace) {
			ns = append(ns, fe.NS.Namespace)
		}
	}
	return strings.Join(ns, " and ")
}

// docProblems runs the doc word checks. It returns the problems and flags.
func (l *loaded) docProblems(f *Final) (probs, flags []Problem, err error) {
	if len(l.scan.Docs) == 0 && len(l.scan.Overviews) == 0 {
		return nil, nil, nil
	}
	pages, probs, err := l.loadPages()
	if err != nil {
		return nil, nil, err
	}
	pages = append(pages, l.overviewPages(f)...)
	if len(pages) == 0 {
		return probs, nil, nil
	}
	add := func(code, file string, line int, flag bool, format string, a ...any) {
		p := Problem{Code: code, File: file, Line: max(line, 1), Message: fmt.Sprintf(format, a...)}
		if flag {
			flags = append(flags, p)
		} else {
			probs = append(probs, p)
		}
	}

	names := f.nameTrie()
	avoid := &trie{}
	for _, fe := range f.Entries {
		for _, a := range fe.E.Avoid {
			avoid.insert(a.Name, fe)
		}
	}

	uses := map[string]*pageUse{}
	for _, p := range pages {
		if p.i18n {
			continue
		}
		listed := map[string]bool{}
		for _, ns := range p.namespaces {
			listed[ns.Name] = true
			if _, ok := f.nsNames[ns.Name]; !ok {
				add("UL401", p.rel, ns.Line, false, "namespaces lists %s, which no UL.yaml in the repo defines", ns.Name)
			}
		}
		use := &pageUse{byID: map[string]*used{}}
		uses[p.rel] = use
		nsHit := map[string]bool{}
		seenName := map[string]bool{}
		for si := range p.segs {
			s := &p.segs[si]
			spans := names.scanSpans(s.Text)
			offs := byteOffsets(s.Text)
			for _, sp := range spans {
				matched := s.Text[offs[sp.start]:offs[sp.end]]
				line := s.lineAt(offs[sp.start], matched)
				var in []*FEntry
				for _, fe := range sp.ents {
					if listed[fe.NS.Namespace] {
						in = append(in, fe)
					}
				}
				if len(in) == 0 {
					if k := fold(matched); !seenName[k] {
						seenName[k] = true
						add("UL402", p.rel, line, false, "%q is a UL word of %s; add it to namespaces", matched, nsList(sp.ents))
					}
					continue
				}
				if k := "2:" + fold(matched); len(nsSet(in)) > 1 && !seenName[k] {
					seenName[k] = true
					add("UL404", p.rel, line, true, "%q is defined in both %s, which the page lists", matched, nsList(in))
				}
				retired := true
				for _, fe := range in {
					use.mark(fe, sp.name)
					nsHit[fe.NS.Namespace] = true
					if !fe.Retired {
						retired = false
					}
				}
				if retired && !p.isADR {
					add("UL407", p.rel, line, false, "%q is a retired word; %s", matched, retiredHint(f, in[0]))
				}
			}
			if p.isADR {
				continue
			}
			for _, a := range avoid.scanSpans(s.Text) {
				inside := false
				for _, sp := range spans {
					// Equal spans: an exact name wins, and between two plurals the longer base name wins
					// (Axes is Axe plus s, not the avoided Ax plus es).
					covers := sp.end-sp.start > a.end-a.start || !sp.plural || (sp.end-sp.start == a.end-a.start && len(sp.name) >= len(a.name))
					if sp.start <= a.start && a.end <= sp.end && covers {
						inside = true
						break
					}
				}
				if inside {
					continue
				}
				matched := s.Text[offs[a.start]:offs[a.end]]
				var write []string
				for _, fe := range a.ents {
					if w := fmt.Sprintf("%q", fe.E.Word); !slices.Contains(write, w) {
						write = append(write, w)
					}
				}
				add("UL403", p.rel, s.lineAt(offs[a.start], matched), false, "avoided word %q; write %s", matched, strings.Join(write, " or "))
			}
		}
		if strings.HasSuffix(p.rel, "/overview.yaml") {
			continue
		}
		for _, ns := range p.namespaces {
			if _, ok := f.nsNames[ns.Name]; ok && !nsHit[ns.Name] && !isStandardNS(ns.Name) {
				add("UL405", p.rel, ns.Line, true, "namespaces lists %s, but the page uses none of its words", ns.Name)
			}
		}
	}

	probs = append(probs, l.conceptProblems(f, pages)...)
	tp, err := l.translationProblems(f, pages, uses)
	if err != nil {
		return nil, nil, err
	}
	return append(probs, tp...), flags, nil
}

func nsSet(ents []*FEntry) map[string]bool {
	m := map[string]bool{}
	for _, fe := range ents {
		m[fe.NS.Namespace] = true
	}
	return m
}

func retiredHint(f *Final, fe *FEntry) string {
	if fe.ReplacedBy != "" {
		if t := f.byID[fe.ReplacedBy]; t != nil {
			return fmt.Sprintf("write %q", t.E.Word)
		}
	}
	return "it has no replacement; reword"
}

// byteOffsets maps a rune index of s to its byte offset (the last entry is len(s)).
func byteOffsets(s string) []int {
	offs := make([]int, 0, len(s)+1)
	for i := range s {
		offs = append(offs, i)
	}
	return append(offs, len(s))
}

// conceptProblems: UL408 (a model/ concept is a word of its context) and UL409 (a word is the
// concept of one page).
func (l *loaded) conceptProblems(f *Final, pages []*page) []Problem {
	var probs []Problem
	type holder struct {
		p    *page
		fold string
	}
	byEntry := map[string][]holder{}
	nsFile := map[string]*FNS{}
	for _, ns := range f.NS {
		nsFile[ns.File] = ns
	}
	for _, p := range pages {
		if p.i18n || p.concept == "" || p.isADR {
			continue
		}
		dir := strings.Split(strings.TrimPrefix(path.Dir(p.rel), docsDir+"/"), "/")
		if len(dir) >= 3 && dir[0] == "domain" && dir[2] == l.opts.whatFolder {
			ulFile := path.Join(docsDir, "domain", dir[1], yamlName)
			ns := nsFile[ulFile]
			switch {
			case ns == nil:
				probs = append(probs, Problem{Code: "UL408", File: p.rel, Line: max(p.conceptLine, 1), Message: fmt.Sprintf("concept %q must be a word of its context, but %s has no namespace", p.concept, ulFile)})
			case f.nsNames[ns.Namespace][fold(p.concept)] == nil:
				probs = append(probs, Problem{Code: "UL408", File: p.rel, Line: max(p.conceptLine, 1), Message: fmt.Sprintf("concept %q is not a word or alias of %s", p.concept, ns.Namespace)})
			default:
				// the word UL408 resolved counts for UL409 whether or not the page lists the namespace
				fe := f.nsNames[ns.Namespace][fold(p.concept)]
				byEntry[fe.ID] = append(byEntry[fe.ID], holder{p, fold(p.concept)})
			}
		}
		// Every listed namespace that has the name counts, so the order of the list changes nothing.
		for _, ns := range p.namespaces {
			if fe := f.nsNames[ns.Name][fold(p.concept)]; fe != nil {
				if hs := byEntry[fe.ID]; len(hs) > 0 && hs[len(hs)-1].p == p {
					continue // a namespace listed twice counts the page once
				}
				byEntry[fe.ID] = append(byEntry[fe.ID], holder{p, fold(p.concept)})
			}
		}
	}
	ids := make([]string, 0, len(byEntry))
	for id := range byEntry {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		hs := byEntry[id]
		first := hs[0]
		for _, h := range hs[1:] {
			if h.p != first.p { // two pages, whatever spelling they write the concept in
				probs = append(probs, Problem{Code: "UL409", File: h.p.rel, Line: max(h.p.conceptLine, 1),
					Message: fmt.Sprintf("concept %q names the same UL word as concept %q of %s (%s); one page explains one word", h.p.concept, first.p.concept, first.p.rel, f.byID[id].E.Word)})
			}
		}
	}
	return probs
}

// overviewPages reads the `intro` of each docs/domain/<context>/overview.yaml as prose against the
// context's own namespace (design §4.1): a page of one run of text, listing that namespace.
func (l *loaded) overviewPages(f *Final) []*page {
	nsFile := map[string]*FNS{}
	for _, ns := range f.NS {
		nsFile[ns.File] = ns
	}
	var out []*page
	for _, rel := range l.scan.Overviews {
		data, err := os.ReadFile(filepath.Join(l.root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		var n yaml.Node
		if yaml.Unmarshal(data, &n) != nil || len(n.Content) == 0 {
			continue // doccheck refuses an overview that is not strict YAML
		}
		m := resolveNode(n.Content[0])
		if m.Kind != yaml.MappingNode {
			continue
		}
		p := &page{rel: rel}
		if ns := nsFile[path.Join(path.Dir(rel), yamlName)]; ns != nil {
			p.namespaces = []Named{{ns.Namespace, 1}}
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			if k, v := resolveNode(m.Content[i]), resolveNode(m.Content[i+1]); k.Value == "intro" && v.Kind == yaml.ScalarNode {
				p.segs = append(p.segs, introSegs(v, m.Content[i], data)...)
			}
		}
		out = append(out, p)
	}
	return out
}

// introSegs is the text of a scalar, one run per source line of a literal or folded block, so a
// hit names its own line. key is the node of the scalar's key and data the file.
func introSegs(v, key *yaml.Node, data []byte) []Seg {
	var sb segBuilder
	switch {
	case v.Style&yaml.LiteralStyle != 0:
		for i, l := range strings.Split(v.Value, "\n") {
			if strings.TrimSpace(l) != "" {
				sb.space()
				sb.add(l, v.Line+1+i)
			}
		}
	case v.Style&yaml.FoldedStyle != 0:
		// A folded value joins its lines, so the lines are read from the file: those after the header
		// that are blank or indented deeper than the key.
		src := strings.Split(string(normalizeNewlines(data)), "\n")
		for l := v.Line + 1; l <= len(src); l++ {
			line := src[l-1]
			if strings.TrimSpace(line) == "" {
				continue
			}
			if len(line)-len(strings.TrimLeft(line, " ")) < key.Column {
				break
			}
			sb.space()
			sb.add(strings.TrimSpace(line), l)
		}
	default:
		return []Seg{plainSeg(v.Value, v.Line)}
	}
	if s, ok := sb.seg(); ok {
		return []Seg{s}
	}
	return nil
}

func frontMatterParses(fm []byte) bool {
	var n yaml.Node
	return yaml.Unmarshal(fm, &n) == nil
}
