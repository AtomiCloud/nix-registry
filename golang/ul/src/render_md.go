package main

import (
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Markdown rendering (spec §9). One renderer serves `ul gen` (one namespace per UL.md, with
// links) and `ul all --format md` (every namespace in one document, no links).

type mdOpts struct {
	level   int  // heading level of the namespace title: 1 in UL.md, 2 in `ul all`
	links   bool // GitHub anchors and cross-file links (UL.md only)
	details bool // a section per word
}

func slugBase(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// sections lists the words in section order: active A–Z, then retired A–Z.
func (ns *FNS) sections() (active, retired []*FEntry) {
	for _, fe := range ns.Entries {
		if fe.Retired {
			retired = append(retired, fe)
		} else {
			active = append(active, fe)
		}
	}
	return
}

// anchorsOf gives each word its GitHub-style heading anchor in the namespace's UL.md. The
// title heading counts too, so a word named like the title gets the "-1" suffix.
func (ns *FNS) anchorsOf() map[*FEntry]string {
	if ns.anchors != nil {
		return ns.anchors
	}
	used := map[string]int{}
	next := func(text string) string {
		base := slugBase(text)
		n := used[base]
		used[base]++
		if n == 0 {
			return base
		}
		return base + "-" + strconv.Itoa(n)
	}
	next(lastSegment(ns.Namespace))
	ns.anchors = map[*FEntry]string{}
	active, retired := ns.sections()
	for _, fe := range append(slices.Clone(active), retired...) {
		ns.anchors[fe] = next(fe.E.Word)
	}
	return ns.anchors
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

// refText renders a resolved id seen from namespace `from`.
func (f *Final) refText(from *FNS, id string, o mdOpts) string {
	t := f.byID[id]
	if t == nil {
		return id
	}
	if !o.links {
		if t.NS == from {
			return t.E.Word
		}
		return id
	}
	if t.NS == from {
		return "[" + t.E.Word + "](#" + from.anchorsOf()[t] + ")"
	}
	label := lastSegment(t.NS.Namespace) + "#" + t.E.Word
	if t.NS.Layer != layerRepo {
		return label
	}
	rel, err := relPath(path.Dir(from.File), path.Dir(t.NS.File))
	if err != "" {
		return label
	}
	link := mdName
	if rel != "." {
		link = rel + "/" + mdName
	}
	return "[" + label + "](" + strings.ReplaceAll(link, " ", "%20") + "#" + t.NS.anchorsOf()[t] + ")"
}

// relPath is the slash-separated path from directory a to directory b (both relative to the
// same root). The second result is non-empty when they cannot be related.
func relPath(a, b string) (string, string) {
	as, bs := splitDir(a), splitDir(b)
	i := 0
	for i < len(as) && i < len(bs) && as[i] == bs[i] {
		i++
	}
	var parts []string
	for range as[i:] {
		parts = append(parts, "..")
	}
	parts = append(parts, bs[i:]...)
	if len(parts) == 0 {
		return ".", ""
	}
	return strings.Join(parts, "/"), ""
}

func splitDir(d string) []string {
	if d == "." || d == "" {
		return nil
	}
	return strings.Split(d, "/")
}

func heading(level int, text string) string { return strings.Repeat("#", level) + " " + text }

// renderNS renders one namespace.
func (f *Final) renderNS(ns *FNS, o mdOpts) string {
	var blocks []string
	title := lastSegment(ns.Namespace)
	if ns.Namespace == sessionNamespace {
		title = "session"
	}
	blocks = append(blocks, heading(o.level, title), strings.Join(ns.Breadcrumb, " > "), ns.Premise)
	active, retired := ns.sections()
	if len(active) > 0 {
		var b strings.Builder
		b.WriteString("| Word | Means | Also called | See |\n|---|---|---|---|")
		for _, fe := range active {
			var see []string
			for _, id := range fe.See {
				see = append(see, f.refText(ns, id, o))
			}
			b.WriteString("\n| " + cell(fe.E.Word) + " | " + cell(fe.E.Desc) + " | " +
				cell(strings.Join(names(fe.E.Aliases), ", ")) + " | " + cell(strings.Join(see, ", ")) + " |")
		}
		blocks = append(blocks, b.String())
	}
	if len(retired) > 0 {
		var b strings.Builder
		b.WriteString("Retired words:\n\n| Word | Replaced by |\n|---|---|")
		for _, fe := range retired {
			rep := ""
			if fe.ReplacedBy != "" {
				rep = f.refText(ns, fe.ReplacedBy, o)
			}
			b.WriteString("\n| " + cell(fe.E.Word) + " | " + cell(rep) + " |")
		}
		blocks = append(blocks, b.String())
	}
	if o.details {
		for _, fe := range append(slices.Clone(active), retired...) {
			blocks = append(blocks, f.renderWord(fe, o.level+1, o)...)
		}
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

// renderWord is a word's section: the heading, then (retired) its desc and successor, the
// details, and the avoid line.
func (f *Final) renderWord(fe *FEntry, level int, o mdOpts) []string {
	blocks := []string{heading(level, fe.E.Word)}
	if fe.Retired {
		blocks = append(blocks, fe.E.Desc)
		line := "Retired."
		if fe.ReplacedBy != "" {
			line = "Retired. Use " + f.refText(fe.NS, fe.ReplacedBy, o) + " instead."
		}
		blocks = append(blocks, line)
	}
	if d := strings.TrimRight(fe.E.Details, " \t\r\n"); d != "" {
		blocks = append(blocks, d)
	}
	if len(fe.E.Avoid) > 0 {
		blocks = append(blocks, "Avoid: "+strings.Join(names(fe.E.Avoid), ", "))
	}
	return blocks
}

// renderGen is the UL.md of one namespace.
func (f *Final) renderGen(ns *FNS) string {
	return f.renderNS(ns, mdOpts{level: 1, links: true, details: true})
}

// renderAll is `ul all --format md`.
func (f *Final) renderAll(details bool) string {
	var b strings.Builder
	b.WriteString("# Ubiquitous language\n")
	for _, ns := range f.NS {
		b.WriteString("\n" + f.renderNS(ns, mdOpts{level: 2, details: details}))
	}
	return b.String()
}
