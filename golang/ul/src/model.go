package main

import (
	"cmp"
	"strings"
	"unicode"
)

// Problem is one rule violation (spec §8). File is relative to the root for repo files and
type Problem struct {
	Code    string `json:"code"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Named is a name (alias, avoid word or reference) with the line it was written on.
type Named struct {
	Name string
	Line int
}

// Entry is one word of a UL.yaml, as written.
// Translation is the fixed term of a word in one language (the translations field).
type Translation struct {
	Lang string
	Term string
	Line int
}

type Entry struct {
	Word         string
	WordLine     int
	Aliases      []Named
	Desc         string
	Details      string
	Avoid        []Named
	See          []Named
	Status       string        // "active" or "retired"
	ReplacedBy   Named         // Name == "" when absent
	Translations []Translation // only in --translations fixed
}

// Doc is one namespace as written in one file.
type Doc struct {
	File      string
	Namespace string
	NSLine    int
	Premise   string
	Entries   []*Entry
}

const (
	statusActive  = "active"
	statusRetired = "retired"
)

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// cmpFold orders A–Z ignoring case, then by the raw text so the order is total and stable.
func cmpFold(a, b string) int {
	if c := cmp.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return cmp.Compare(a, b)
}

func makeID(ns, word string) string { return ns + "#" + word }

func breadcrumb(ns string) []string { return strings.Split(ns, "/") }

func lastSegment(ns string) string {
	if i := strings.LastIndex(ns, "/"); i >= 0 {
		return ns[i+1:]
	}
	return ns
}

// validName reports why a word or alias name is malformed ("" when fine). Spec §2.2:
// 1–64 characters; first a letter or digit; then letters, digits, spaces and . _ + ' -
func validName(s string) string {
	n := 0
	for i, r := range s {
		n++
		switch {
		case i == 0:
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return "must start with a letter or digit"
			}
		case unicode.IsLetter(r), unicode.IsDigit(r), r == ' ', r == '.', r == '_', r == '+', r == '\'', r == '-':
		default:
			return "contains " + string(r) + ", which is not allowed"
		}
	}
	if n == 0 {
		return "is empty"
	}
	if n > 64 {
		return "is longer than 64 characters"
	}
	return ""
}

// validNamespace reports why a namespace is malformed ("" when fine). Spec §3.
func validNamespace(ns string) string {
	segs := strings.Split(ns, "/")
	if len(segs) < 2 {
		return "needs at least two segments joined by /"
	}
	for _, seg := range segs {
		if seg == "" {
			return "has an empty segment"
		}
		for i, r := range seg {
			switch {
			case i == 0:
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
					return "segment " + seg + " must start with a letter or digit"
				}
			case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '_', r == '-':
			default:
				return "segment " + seg + " contains " + string(r) + ", which is not allowed"
			}
		}
	}
	if !strings.Contains(segs[0], ".") {
		return "the first segment must be the org's domain (it contains a dot)"
	}
	return ""
}
