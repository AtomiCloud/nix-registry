package main

import (
	"bytes"
	"html"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	gtext "github.com/yuin/goldmark/text"
)

// The prose of a Markdown text, read with a CommonMark parser (design §4.4): code spans, fenced
// and indented code, raw HTML, link targets and images are left out, so a word is found only
// where a reader reads it. A mermaid block gives its node and edge labels.

var cmark = goldmark.New(goldmark.WithExtensions(extension.GFM))

// frontMatter splits a page into its front matter (without the --- lines) and its body, and
// says how many lines come before the body. A page with no front matter has none.
func splitFrontMatter(src []byte) (fm []byte, body []byte, bodyLine int) {
	s := normalizeNewlines(src)
	if !bytes.HasPrefix(s, []byte("---\n")) {
		return nil, s, 0
	}
	rest := s[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	tail := 5
	if end < 0 {
		if bytes.HasSuffix(rest, []byte("\n---")) {
			end, tail = len(rest)-4, 4
		} else {
			return nil, s, 0
		}
	}
	fm = rest[:end]
	body = rest[end+tail:]
	bodyLine = bytes.Count(s[:len(s)-len(body)], []byte("\n"))
	return fm, body, bodyLine
}

// mdSegs reads body with CommonMark. firstLine is the number of source lines before body.
func mdSegs(body []byte, firstLine int) []Seg {
	root := cmark.Parser().Parse(gtext.NewReader(body))
	starts := lineStarts(body)
	lineOf := func(off int) int {
		return firstLine + 1 + sort.Search(len(starts), func(i int) bool { return starts[i] > off })
	}
	var out []Seg
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		switch n.Kind() {
		case ast.KindFencedCodeBlock:
			fb := n.(*ast.FencedCodeBlock)
			if lang := strings.ToLower(string(fb.Language(body))); lang == "mermaid" {
				if l := fb.Lines(); l.Len() > 0 {
					var b strings.Builder
					for i := 0; i < l.Len(); i++ {
						seg := l.At(i)
						b.Write(seg.Value(body))
					}
					out = append(out, mermaidSegs(b.String(), lineOf(l.At(0).Start))...)
				}
			}
			return
		case ast.KindCodeBlock, ast.KindHTMLBlock, ast.KindThematicBreak:
			return
		case ast.KindParagraph, ast.KindHeading, ast.KindTextBlock, east.KindTableCell:
			var sb segBuilder
			inlines(n, body, lineOf, &sb)
			if s, ok := sb.seg(); ok {
				out = append(out, s)
			}
			return
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(root)
	return out
}

func lineStarts(b []byte) []int {
	st := []int{}
	for i, c := range b {
		if c == '\n' {
			st = append(st, i+1)
		}
	}
	return st
}

// inlines adds the text of the inline children of n.
func inlines(n ast.Node, src []byte, lineOf func(int) int, sb *segBuilder) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			sb.add(unescapeMd(string(v.Segment.Value(src))), lineOf(v.Segment.Start))
			if v.SoftLineBreak() || v.HardLineBreak() {
				sb.space()
			}
		case *ast.String:
			sb.add(string(v.Value), sb.lastLine())
		case *ast.CodeSpan, *ast.RawHTML, *ast.AutoLink, *ast.Image:
			sb.gap()
		case *ast.Link, *ast.Emphasis, *east.Strikethrough:
			inlines(c, src, lineOf, sb)
		default:
			inlines(c, src, lineOf, sb)
		}
	}
}

var mdEscape = regexp.MustCompile("\\\\([!-/:-@\\[-`{-~])")

// unescapeMd is the text a reader sees: backslash escapes and character entities resolved.
func unescapeMd(s string) string { return html.UnescapeString(mdEscape.ReplaceAllString(s, "$1")) }

// normalizeNewlines turns CRLF and a lone CR into LF, as dh does before it reads a page.
func normalizeNewlines(b []byte) []byte {
	return bytes.ReplaceAll(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
}
