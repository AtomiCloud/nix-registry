package main

import (
	"strings"
	"testing"
)

// A hit is located by the text a reader reads: the target of a link and an autolink are not text, so
// a word in a URL on an earlier line does not take the hit.
func TestLineAtSkipsLinkTargets(t *testing.T) {
	src := []string{"", "See [this](https://example.test/griddle) and <https://example.test/griddle>", "and never a griddle."}
	s := Seg{Text: "See this and \nand never a griddle.", src: src, from: 2, to: 3}
	off := strings.Index(s.Text, "griddle")
	if got := s.lineAt(off, "griddle"); got != 3 {
		t.Fatalf("the hit is on line 3, got %d", got)
	}
}
