package main

import (
	"strings"
	"testing"
)

func segTexts(segs []Seg) string {
	var parts []string
	for _, s := range segs {
		parts = append(parts, strings.TrimSpace(strings.ReplaceAll(s.Text, gapMark, " ")))
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
	// An empty front matter is empty: a field of the body below it is not metadata.
	if fm, b, n := splitFrontMatter([]byte("---\n---\nnamespaces: [x]\n---\nbody\n")); len(fm) != 0 || n != 2 || string(b) != "namespaces: [x]\n---\nbody\n" {
		t.Fatalf("empty front matter: %q %q %d", fm, b, n)
	}
	if fm, b, n := splitFrontMatter([]byte("---\n---")); len(fm) != 0 || n != 1 || len(b) != 0 {
		t.Fatalf("empty front matter at the end: %q %q %d", fm, b, n)
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
		{"comment line", "sequenceDiagram\n  %% note; A->>B: griddle\n  A->>B: Hi\n", "Hi"},
		{"front matter", "---\ntitle: Kitchen map\nconfig:\n  theme: dark\n---\nflowchart LR\n  A[Pan] --> B[Oven]\n", "Kitchen map | Pan | Oven"},
		{"entity semicolon", "sequenceDiagram\n  A->>B: Ready#59; use the pan\n", "Ready; use the pan"},
		{"two statements", "flowchart LR\n  A[One]; B[Two]\n", "One | Two"},
		{"pie", "pie title Pets\n  \"Dogs\" : 386\n", "Pets | Dogs"},
		{"pie showData", "pie showData title Pets and cats\n  \"Dogs\" : 386\n", "Pets and cats | Dogs"},
		{"pie quoted title", "pie title \"Pets\"\n  \"Dogs\" : 386\n", "Pets | Dogs"},
		{"pie no title", "pie\n  \"Dogs\" : 386\n", "Dogs"},
		{"node metadata", "flowchart LR\n  A@{ img: \"https://example.com/griddle.png\", label: \"Cookware\" }\n  B@{ shape: rect, label: Pan, icon: \"fa:griddle\" } --> C[Oven]\n", "Cookware | Pan | Oven"},
		{"class", "classDiagram\n  Animal <|-- Duck : is a\n  Animal --> Food : eats\n", "is a | eats"},
		{"seq links", "sequenceDiagram\n  participant A\n  links A: {\"Dashboard\": \"https://x.test/griddle\"}\n  A->>B: Hi\n", "Hi"},
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

// A backslash escape is read before the reference it would begin: \&#105; is the text &#105;, and
// an unescaped &#105; is the letter i.
func TestEscapedAmpersandIsNotAnEntity(t *testing.T) {
	for in, want := range map[string]string{
		`gr\&#105;ddle`:   `gr&#105;ddle`,
		`gr&#105;ddle`:    `griddle`,
		`a &amp; b`:       `a & b`,
		`\*not em\*`:      `*not em*`,
		`\\&#105;`:        `\i`,
		`&nosuchentity;`:  `&nosuchentity;`,
		`AT&T and a &lt;`: `AT&T and a <`,
		`&notgriddle;`:    `&notgriddle;`,
		`&not; &notin;`:   `¬ ∉`,
	} {
		if got := unescapeMd(in); got != want {
			t.Errorf("unescapeMd(%q) = %q, want %q", in, got, want)
		}
	}
}
