package main

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Seg is one run of prose: text a reader reads, with the source line of its parts. Code, link
// targets and part data never enter a Seg. A word cannot span two Segs.
type Seg struct {
	Text   string
	breaks []segBreak // sorted; the line of each stretch of Text
	// refine: when the source gives only the lines a run sits in (a .dh.md block), the hit's
	// own line is looked for in these source lines.
	src      []string
	from, to int // 1-based, inclusive
	occ      int // how many earlier runs of the page hold the same text (in any case)
	// exact: Text is written in the source as it is (a title, a label), so it anchors whole.
	exact bool
	// occCase: for an exact run, how many earlier runs hold the same text in the same case, so
	// two labels that differ only in case each find their own line.
	occCase int
}

type segBreak struct{ off, line int }

// lineAt is the source line of the hit at byte offset off, whose text is matched.
func (s *Seg) lineAt(off int, matched string) int {
	if len(s.breaks) > 0 {
		i := sort.Search(len(s.breaks), func(i int) bool { return s.breaks[i].off > off }) - 1
		if i < 0 {
			i = 0
		}
		return s.breaks[i].line
	}
	if s.from < 1 {
		return 1
	}
	m := strings.ToLower(matched)
	from := s.from
	// Anchor on where the run itself starts, so two runs with the same word get their own lines.
	a := anchorOf(s.Text)
	if s.exact {
		a = strings.ToLower(strings.TrimSpace(s.Text))
		// The line that writes the text in this very case comes first; the case-blind search below
		// is for text the source spells differently.
		if w, seen := strings.TrimSpace(s.Text), 0; w != "" {
			for l := s.from; l <= s.to && l <= len(s.src); l++ {
				if strings.Contains(s.src[l-1], w) {
					if seen == s.occCase {
						return s.hitLine(l, m, off)
					}
					seen++
				}
			}
		}
	}
	if a != "" {
		seen := 0
		for l := s.from; l <= s.to && l <= len(s.src); l++ {
			if strings.Contains(strings.ToLower(s.src[l-1]), a) {
				if seen == s.occ {
					from = l
					break
				}
				seen++
			}
		}
	}
	return s.hitLine(from, m, off)
}

// hitLine is the line, from line from on, that holds the hit at byte offset off of the run: the
// k-th time the word is written in the source, where k is how many times the run holds it before
// off, so a word written on two lines of one paragraph is found on each.
func (s *Seg) hitLine(from int, m string, off int) int {
	if off > len(s.Text) {
		off = len(s.Text)
	}
	k := countWord(strings.ToLower(s.Text[:off]), m)
	seen := 0
	for l := from; l <= s.to && l <= len(s.src); l++ {
		n := countWord(visible(s.src[l-1]), m)
		if k < seen+n {
			return l
		}
		seen += n
	}
	for l := from; l <= s.to && l <= len(s.src); l++ {
		if containsWord(visible(s.src[l-1]), m) {
			return l
		}
	}
	for l := from; l <= s.to && l <= len(s.src); l++ {
		if strings.Contains(strings.ToLower(s.src[l-1]), m) {
			return l
		}
	}
	for from < s.to && from <= len(s.src) && strings.TrimSpace(s.src[from-1]) == "" {
		from++ // the run starts on the next line that holds text
	}
	return from
}

// segBuilder joins text pieces of one block into one Seg.
type segBuilder struct {
	b      strings.Builder
	breaks []segBreak
}

func (sb *segBuilder) add(text string, line int) {
	if text == "" {
		return
	}
	if n := len(sb.breaks); n == 0 || sb.breaks[n-1].line != line {
		sb.breaks = append(sb.breaks, segBreak{sb.b.Len(), line})
	}
	sb.b.WriteString(text)
}

// gap puts a break no word crosses where code, a link target or other data was left out.
func (sb *segBuilder) gap() {
	if s := sb.b.String(); s != "" && !strings.HasSuffix(s, gapMark) {
		sb.b.WriteString(gapMark)
	}
}

// gapMark stands where code or data was left out. It is not a space, so a name written as
// "Frying `x` pan" is not read as the name "Frying pan".
const gapMark = "\uFFFC"

func (sb *segBuilder) seg() (Seg, bool) {
	t := strings.TrimSpace(sb.b.String())
	if t == "" {
		return Seg{}, false
	}
	return Seg{Text: sb.b.String(), breaks: sb.breaks}, true
}

// plainSeg is a Seg of text that sits wholly on one line.
func plainSeg(text string, line int) Seg {
	return Seg{Text: text, breaks: []segBreak{{0, line}}}
}

func (sb *segBuilder) lastLine() int {
	if n := len(sb.breaks); n > 0 {
		return sb.breaks[n-1].line
	}
	return 1
}

var codeSpanRe = regexp.MustCompile("`[^`\n]*`")

// linkTargetRe finds what a reader never reads of a link: the (target) of [text](target) and an
// autolink <https://...>.
var linkTargetRe = regexp.MustCompile(`\]\([^)]*\)|<[a-zA-Z][a-zA-Z0-9+.-]*:[^>\s]*>`)

// visible is a source line as lower case with the code spans and link targets left out, the text the
// scanner matches.
func visible(line string) string {
	return linkTargetRe.ReplaceAllString(codeSpanRe.ReplaceAllString(strings.ToLower(line), " "), "] ")
}

// countWord is how many times line holds m as a whole word, not counting overlaps.
func countWord(line, m string) int {
	n := 0
	for i := 0; i < len(line); {
		j := strings.Index(line[i:], m)
		if j < 0 {
			break
		}
		a, b := i+j, i+j+len(m)
		if (a == 0 || !isWordRune(lastRune(line[:a]))) && (b >= len(line) || !isWordRune(firstRune(line[b:]))) {
			n++
			i = b
		} else {
			i = a + 1
		}
	}
	return n
}

// containsWord reports whether line holds m as a whole word (letters, digits and _ end a word).
func containsWord(line, m string) bool {
	for i := 0; ; {
		j := strings.Index(line[i:], m)
		if j < 0 {
			return false
		}
		a, b := i+j, i+j+len(m)
		before := a == 0 || !isWordRune(lastRune(line[:a]))
		after := b >= len(line) || !isWordRune(firstRune(line[b:]))
		if before && after {
			return true
		}
		i = a + 1
	}
}

func firstRune(s string) rune { r, _ := utf8.DecodeRuneInString(s); return r }
func lastRune(s string) rune  { r, _ := utf8.DecodeLastRuneInString(s); return r }

// anchorOf is the start of a run's text, as found in the source line it begins on: its first
// words, up to 24 bytes, cut at a word break. A run that starts with something the source
// writes differently (markup) has no anchor.
func anchorOf(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[:i]
	}
	if len(t) > 24 {
		t = t[:24]
		if i := strings.LastIndexByte(t, ' '); i > 8 {
			t = t[:i]
		}
	}
	return t
}

// space is a line break inside a paragraph: it reads as the one space it is.
func (sb *segBuilder) space() {
	if s := sb.b.String(); s != "" && !strings.HasSuffix(s, " ") && !strings.HasSuffix(s, gapMark) {
		sb.b.WriteByte(' ')
	}
}
