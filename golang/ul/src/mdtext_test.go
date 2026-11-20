package main

import (
	"strings"
	"testing"
)

func segTexts(segs []Seg) string {
	var parts []string
	for _, s := range segs {
		parts = append(parts, strings.TrimSpace(s.Text))
	}
	return strings.Join(parts, " | ")
}

func TestMdSegs(t *testing.T) {
	src := "---\ntitle: T\n---\n\n# Head `code` end\n\nOne **bold word** and `code span` plus [link text](https://x.test/target) and <b>html</b>.\nSecond line.\n\n```go\nfenced\n```\n\n    indented\n\n- item one\n  - nested item\n\n| A | B |\n|---|---|\n| cell `c` | other |\n\n> quote text\n\n![alt](img.png) after image\n"
	fm, body, first := splitFrontMatter([]byte(src))
	if string(fm) != "title: T" || first != 3 {
		t.Fatalf("front matter %q, body starts after %d lines", fm, first)
	}
	got := segTexts(mdSegs(body, first))
	want := "Head  end | One bold word and  plus link text and  html . Second line. | item one | nested item | A | B | cell  | other | quote text | after image"
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if norm(got) != norm(want) {
		t.Fatalf("got  %s\nwant %s", norm(got), norm(want))
	}
	// A page with no front matter keeps all its text from line 1.
	if _, b, n := splitFrontMatter([]byte("no front matter\n")); n != 0 || string(b) != "no front matter\n" {
		t.Fatalf("no front matter: %d %q", n, b)
	}
	// An unclosed front matter is no front matter: doccheck refuses it.
	if fm, _, _ := splitFrontMatter([]byte("---\ntitle: x\nbody\n")); fm != nil {
		t.Fatal("an unclosed front matter must not be read")
	}
}

func TestMermaidLabels(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"flow", "%%{init: {\"x\": 1}}%%\ngraph LR\n  person[\"user or author\"]\n  a[Plain label] -->|edge text| b((Round))\n  b --> c{Decide?}\n  classDef x fill:#fff\n  style a fill:#f00\n  subgraph s1 [Group title]\n    d\n  end\n  e -- long edge --> f\n", "user or author | Plain label | Round | edge text | Decide? | Group title | long edge"},
		{"seq", "sequenceDiagram\n  participant A as Alice the cook\n  A->>B: Hello there\n  Note over A,B: A note text\n", "Alice the cook | Hello there | A note text"},
		{"state", "stateDiagram-v2\n  [*] --> Still\n  Still --> Moving : go now\n  Moving : the moving state\n", "go now | the moving state"},
		{"pie", "pie title Pets\n  \"Dogs\" : 386\n", "Dogs"},
	}
	for _, c := range cases {
		var parts []string
		for _, s := range mermaidSegs(c.src, 1) {
			parts = append(parts, s.Text)
		}
		if got := strings.Join(parts, " | "); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
