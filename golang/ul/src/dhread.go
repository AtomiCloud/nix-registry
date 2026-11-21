package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"maps"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// .dh.md pages are read through the dh tool, never by a Go copy of the format (design §4.4).
// `dh json <page...>` prints one line of JSON per page: the typed Document of the
// durian-hypermedia package (ast 1). ul reads from it only the prose: Markdown blocks, the
// titles and text of containers and parts, and the author's :::llm texts. Part names,
// attributes, ids and data are never read.
//
// An animation block carries `texts` (dh 0.2.0): the strings dh's own scene compiler reads as
// labels, captions, say and note texts. A page with an animation and no `texts` is refused (UL410).

type dhProblem struct {
	Line    int    `json:"line"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type dhDoc struct {
	AST    int `json:"ast"`
	Banner *struct {
		Text []mdInline `json:"text"`
	} `json:"banner"`
	Source   string      `json:"source"`
	Blocks   *[]dhBlock  `json:"blocks"`
	Problems []dhProblem `json:"problems"`
}

type dhBlock struct {
	Type    string            `json:"type"`
	Line    int               `json:"line"`
	Md      []mdBlock         `json:"md"`
	Items   []dhItem          `json:"items"`
	Body    json.RawMessage   `json:"body"`
	Steps   json.RawMessage   `json:"steps"`
	Trees   []dhTree          `json:"trees"`
	Chart   json.RawMessage   `json:"chart"`
	Tier    json.RawMessage   `json:"tier"`
	Align   json.RawMessage   `json:"align"`
	Anim    json.RawMessage   `json:"anim"`
	Title   *string           `json:"title"`
	Attrs   map[string]string `json:"attrs"`
	Texts   *[]string         `json:"texts"`
	LLM     *string           `json:"llm"`
	LLMKind string            `json:"llmKind"`
	Message string            `json:"message"`
}

type dhItem struct {
	Title string    `json:"title"`
	Body  []dhBlock `json:"body"`
}

type dhTree struct {
	Title *string `json:"title"`
	Rows  []struct {
		Note string `json:"note"`
	} `json:"rows"`
}

type mdBlock struct {
	T     string          `json:"t"`
	C     json.RawMessage `json:"c"`
	Items []struct {
		C []mdBlock `json:"c"`
	} `json:"items"`
	Head [][]mdInline   `json:"head"`
	Rows [][][]mdInline `json:"rows"`
}

type mdInline struct {
	T string     `json:"t"`
	V string     `json:"v"`
	C []mdInline `json:"c"`
}

// runDh reads pages (paths relative to root) through the dh command. It prints one JSON value
// per page, in order. dh stops at the first page that does not parse and names it as
// path:line: message; that page becomes a UL410 problem and the rest are read again.
// dhBatch is the most pages one dh run is given, so the argument list stays far under the limit of
// any system.
const dhBatch = 300

func runDh(dh, root string, pages []string) (map[string]*dhDoc, []Problem, error) {
	docs := map[string]*dhDoc{}
	var probs []Problem
	for len(pages) > 0 {
		n := min(dhBatch, len(pages))
		d, p, err := runDhBatch(dh, root, pages[:n])
		if err != nil {
			return nil, nil, err
		}
		maps.Copy(docs, d)
		probs = append(probs, p...)
		pages = pages[n:]
	}
	return docs, probs, nil
}

func runDhBatch(dh, root string, pages []string) (map[string]*dhDoc, []Problem, error) {
	docs := map[string]*dhDoc{}
	var probs []Problem
	rest := slices.Clone(pages)
	for len(rest) > 0 {
		cmd := exec.Command(dh, append([]string{"json"}, rest...)...)
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			if _, lookErr := exec.LookPath(dh); lookErr != nil {
				return nil, nil, fmt.Errorf("%q is not on PATH (docs pages are read through the dh tool; set --dh or UL_DH)", dh)
			}
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			bad, line, text := whichPage(msg, root, rest)
			if bad == "" {
				return nil, nil, fmt.Errorf("dh json failed: %s", msg)
			}
			probs = append(probs, Problem{Code: "UL410", File: bad, Line: max(line, 1), Message: "the page does not parse: " + text + " (dh reads every page; its own checks are doccheck's)"})
			rest = slices.DeleteFunc(rest, func(p string) bool { return p == bad })
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(out))
		for _, p := range rest {
			d := &dhDoc{}
			if err := dec.Decode(d); err != nil {
				return nil, nil, fmt.Errorf("dh json printed fewer pages than it was given, or something that is not a page (%s): %v", p, err)
			}
			if d.AST != 1 || d.Source == "" || d.Blocks == nil {
				return nil, nil, fmt.Errorf("dh json printed something that is not a page for %s (no \"ast\": 1)", p)
			}
			docs[p] = d
		}
		if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
			return nil, nil, fmt.Errorf("dh json printed more than the pages it was given")
		}
		break
	}
	return docs, probs, nil
}

var dhErrRe = regexp.MustCompile(`^(.+?):(\d+): (.*)$`)

// whichPage finds the page a dh error line names.
func whichPage(msg, root string, pages []string) (page string, line int, text string) {
	m := dhErrRe.FindStringSubmatch(strings.SplitN(msg, "\n", 2)[0])
	if m == nil {
		return "", 0, ""
	}
	n, _ := strconv.Atoi(m[2])
	// The exact path of a page of the batch first: a suffix can name another page too (docs/a.dh.md
	// is the end of docs/nested/docs/a.dh.md).
	for _, p := range pages {
		if m[1] == p || m[1] == filepath.Join(root, filepath.FromSlash(p)) {
			return p, n, m[3]
		}
	}
	// A suffix is believed only when it names one page.
	hit, hits := "", 0
	for _, p := range pages {
		if strings.HasSuffix(m[1], "/"+p) {
			hit, hits = p, hits+1
		}
	}
	if hits == 1 {
		return hit, n, m[3]
	}
	return "", 0, ""
}

// dhSegs lists the prose of a page, and the problems that make it unreadable.
func dhSegs(d *dhDoc, rel string) ([]Seg, []Problem) {
	var probs []Problem
	for _, p := range d.Problems {
		if p.Level == "error" {
			probs = append(probs, Problem{Code: "UL410", File: rel, Line: max(p.Line, 1), Message: "the page does not parse: " + p.Message + " (dh reads every .dh.md page; its checks are doccheck's)"})
		}
	}
	lines := strings.Split(d.Source, "\n")
	r := &dhReader{lines: lines, rel: rel}
	if d.Banner != nil {
		var sb segBuilder
		inlineText(d.Banner.Text, &sb, 1)
		r.add(&sb, 1, frontMatterEnd(lines))
	}
	if d.Blocks != nil {
		r.blocks(*d.Blocks, len(lines))
	}
	return r.segs, append(probs, r.probs...)
}

type dhReader struct {
	lines []string
	rel   string
	segs  []Seg
	probs []Problem
	seen  map[string]int
}

func (r *dhReader) add(sb *segBuilder, from, to int) {
	if s, ok := sb.seg(); ok {
		s.breaks = nil
		s.src, s.from, s.to = r.lines, from, to
		r.push(s)
	}
}

// push adds a run, counting the earlier runs of the page that hold the same text, so a second
// identical paragraph is found on its own line.
func (r *dhReader) push(s Seg) {
	if r.seen == nil {
		r.seen = map[string]int{}
	}
	k := fmt.Sprintf("%d|%s", s.from, strings.ToLower(s.Text))
	s.occ = r.seen[k]
	r.seen[k]++
	if s.exact { // the same text in the same case, so labels that differ in case keep their own lines
		k = fmt.Sprintf("%d|=%s", s.from, s.Text)
		s.occCase = r.seen[k]
		r.seen[k]++
	}
	r.segs = append(r.segs, s)
}

// text adds a short text a part holds (a title, a note, a label) that sits on line.
func (r *dhReader) text(s string, line, to int) {
	if strings.TrimSpace(s) != "" {
		r.push(Seg{Text: s, src: r.lines, from: line, to: max(to, line), exact: true})
	}
}

// blocks reads sibling blocks; end is the last line they can reach.
func (r *dhReader) blocks(bs []dhBlock, end int) {
	for i, b := range bs {
		to := end
		if i+1 < len(bs) {
			to = max(bs[i+1].Line-1, b.Line)
		}
		r.block(b, to)
	}
}

func (r *dhReader) block(b dhBlock, to int) {
	switch b.Type {
	case "markdown":
		for _, m := range b.Md {
			r.md(m, b.Line, to)
		}
		return
	case "toc", "error", "host":
		return
	}
	// An author's :::llm text is Markdown, read like any prose.
	if b.LLMKind == "text" && b.LLM != nil {
		body := []byte(*b.LLM)
		from := max(b.Line, 1) // a block with no line is read from the top of the page
		for l := from; l <= to && l <= len(r.lines); l++ {
			if strings.HasPrefix(strings.TrimSpace(r.lines[l-1]), ":::llm") {
				from = l
				break
			}
		}
		for _, s := range mdSegs(body, 0) {
			s.breaks = nil
			s.src, s.from, s.to = r.lines, from, to
			r.push(s)
		}
	}
	switch b.Type {
	case "tabs", "accordion":
		for _, it := range b.Items {
			r.text(it.Title, b.Line, to)
			r.blocks(it.Body, to)
		}
	case "human":
		var body []dhBlock
		if json.Unmarshal(b.Body, &body) == nil {
			r.blocks(body, to)
		}
	case "terminal", "editor":
		var steps []struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(b.Steps, &steps) == nil {
			for _, s := range steps {
				r.text(s.Title, b.Line, to)
			}
		}
		if b.Title != nil {
			r.text(*b.Title, b.Line, to)
		}
	case "filetree":
		for _, t := range b.Trees {
			if t.Title != nil {
				r.text(*t.Title, b.Line, to)
			}
			for _, row := range t.Rows {
				r.text(row.Note, b.Line, to)
			}
		}
	case "codewalk":
		var steps []struct {
			Note []mdInline `json:"note"`
		}
		if json.Unmarshal(b.Steps, &steps) == nil {
			for _, s := range steps {
				var sb segBuilder
				inlineText(s.Note, &sb, b.Line)
				r.add(&sb, b.Line, to)
			}
		}
	case "mermaid":
		var steps []struct {
			Title string `json:"title"`
			Src   string `json:"src"`
		}
		if json.Unmarshal(b.Steps, &steps) == nil {
			for _, s := range steps {
				r.text(s.Title, b.Line, to)
				for _, m := range mermaidSegs(s.Src, 1) {
					m.breaks, m.src, m.from, m.to = nil, r.lines, b.Line, to
					r.push(m)
				}
			}
		}
	case "chart":
		var v any
		if json.Unmarshal(b.Chart, &v) == nil {
			walkTextKeys(v, "", func(s string) { r.text(s, b.Line, to) })
		}
	case "tierlist", "alignment":
		raw := b.Tier
		if b.Type == "alignment" {
			raw = b.Align
		}
		var v any
		if json.Unmarshal(raw, &v) == nil {
			walkTextKeys(v, "", func(s string) { r.text(s, b.Line, to) })
		}
	case "animation":
		if b.Texts == nil {
			r.probs = append(r.probs, Problem{Code: "UL410", File: r.rel, Line: max(b.Line, 1), Message: "dh json gave no texts for this animation; use dh 0.2.0 or newer (ul reads the labels and captions from texts)"})
			break
		}
		for _, t := range *b.Texts {
			r.text(t, b.Line, to)
		}
	}
}

// textKeys name the strings of a data part (a chart, a tier list, an alignment grid) that a reader
// reads: the keys dh's own text reader lists (TEXT_KEYS in durian-hypermedia), and the axis names
// of an alignment grid. walkTextKeys calls f with each such string.
var textKeys = map[string]bool{"label": true, "caption": true, "text": true, "title": true, "note": true, "name": true, "button": true, "xs": true, "ys": true}

func walkTextKeys(v any, key string, f func(string)) {
	switch x := v.(type) {
	case string:
		if textKeys[key] {
			f(x)
		}
	case []any:
		for _, y := range x {
			walkTextKeys(y, key, f)
		}
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) { // one order on every run
			if k == "let" {
				continue // named expressions: data, whatever their keys are called
			}
			walkTextKeys(x[k], k, f)
		}
	}
}

func (r *dhReader) md(m mdBlock, from, to int) {
	var sb segBuilder
	switch m.T {
	case "p", "text", "h":
		var c []mdInline
		if json.Unmarshal(m.C, &c) == nil {
			inlineText(c, &sb, from)
			r.add(&sb, from, to)
		}
	case "quote", "callout":
		var c []mdBlock
		if json.Unmarshal(m.C, &c) == nil {
			for _, x := range c {
				r.md(x, from, to)
			}
		}
	case "list":
		for _, it := range m.Items {
			for _, x := range it.C {
				r.md(x, from, to)
			}
		}
	case "table":
		for _, cell := range m.Head {
			var sb segBuilder
			inlineText(cell, &sb, from)
			r.add(&sb, from, to)
		}
		for _, row := range m.Rows {
			for _, cell := range row {
				var sb segBuilder
				inlineText(cell, &sb, from)
				r.add(&sb, from, to)
			}
		}
	}
}

// inlineText adds the text of inlines: text and the text inside emphasis and links. Code, images,
// keys and mouse actions are left out, with a break where they were.
func inlineText(in []mdInline, sb *segBuilder, line int) {
	for _, x := range in {
		switch x.T {
		case "text":
			sb.add(strings.ReplaceAll(html.UnescapeString(x.V), "\n", " "), line)
		case "strong", "em", "del", "link", "coderef":
			inlineText(x.C, sb, line)
		case "br":
			sb.space()
		default: // code, img, kbd, mouse, ref
			sb.gap()
		}
	}
}

// pageName is the path dh sees for a page (slash-separated, relative to the root).
func pageName(rel string) string { return filepath.ToSlash(rel) }

// frontMatterEnd is the line of the closing --- of the front matter (1 when there is none).
func frontMatterEnd(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 1
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return i + 1
		}
	}
	return len(lines)
}
