package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// docsCheck runs `ul check` and returns exit code, stdout and stderr.
func docsCheck(t *testing.T, dir string, extra ...string) (int, string, string) {
	t.Helper()
	return newEnv(t, dir).ul(append([]string{"check", "--root", dir}, extra...)...)
}

func rewrite(t *testing.T, dir, rel, from, to string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), from) {
		t.Fatalf("%s has no %q", rel, from)
	}
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), from, to)), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	clnUL  = "docs/domain/kitchen/UL.yaml"
	clnTr  = "docs/i18n/es/domain/kitchen/model/frying-pan.md"
	clnSrc = "docs/domain/kitchen/model/frying-pan.md"
)

// The clean docs fixture passes: code spans, fenced and indented code, link targets and the
// text of an ADR's retired and avoided words are not refused. Its two flags warn and pass.
func TestDocsCleanPasses(t *testing.T) {
	dir := fixture(t, "docs/clean")
	code, out, errs := docsCheck(t, dir)
	if code != 0 || out != "" {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
	golden(t, "docs-clean-flags.txt", errs)
	_, js, _ := docsCheck(t, dir, "--format", "json")
	golden(t, "docs-clean.json", strings.ReplaceAll(js, dir, "/ROOT"))
}

// A fixed term shows in the JSON of every word that has one.
func TestTranslationsInJSON(t *testing.T) {
	dir := fixture(t, "docs/clean")
	r := newEnv(t, dir).report("all", "--root", dir)
	w := findWord(t, r, "example.org/cooking/kitchen#Oven")
	if w.Translations["es"] != "horno" || w.Translations["zh-Hans"] != "烤箱" {
		t.Fatalf("translations %v", w.Translations)
	}
}

// Owner pick 4: the three modes, each tested. The default is the recommended one.
func TestTranslationModes(t *testing.T) {
	if defaultTranslations != modeFixed {
		t.Fatalf("the default must be the recommended mode (fixed), not %s", defaultTranslations)
	}
	t.Run("fixed", func(t *testing.T) {
		dir := fixture(t, "docs/clean")
		if code, out, errs := docsCheck(t, dir, "--translations", "fixed"); code != 0 {
			t.Fatalf("exit %d\n%s%s", code, out, errs)
		}
		rewrite(t, dir, clnTr, "El horno (Oven)", "La cocina (Oven)")
		code, out, _ := docsCheck(t, dir)
		if code != 1 || !strings.Contains(out, clnTr+":7: UL502") || !strings.Contains(out, `"horno"`) || strings.Count(out, "\n") != 1 {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
	t.Run("fixed-no-term", func(t *testing.T) {
		dir := fixture(t, "docs/clean")
		rewrite(t, dir, clnTr, "lang: es", "lang: pt")
		code, out, _ := docsCheck(t, dir)
		if code != 1 || strings.Count(out, "UL501") != 3 { // Frying pan, Oven, Conflict resolver have no pt term
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
	t.Run("english", func(t *testing.T) {
		dir := fixture(t, "docs/clean")
		// There is no translations field in this mode.
		code, out, _ := docsCheck(t, dir, "--translations", "english")
		if code != 1 || !strings.Contains(out, "UL101") || !strings.Contains(out, `unknown field "translations"`) {
			t.Fatalf("exit %d\n%s", code, out)
		}
		stripTranslations(t, dir)
		// The translation keeps every English word of its source (in parentheses): passes.
		if code, out, errs := docsCheck(t, dir, "--translations", "english"); code != 0 {
			t.Fatalf("exit %d\n%s%s", code, out, errs)
		}
		rewrite(t, dir, clnTr, " (Oven)", "")
		code, out, _ = docsCheck(t, dir, "--translations", "english")
		if code != 1 || !strings.Contains(out, "UL502") || !strings.Contains(out, "English word") {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
	t.Run("free", func(t *testing.T) {
		dir := fixture(t, "docs/clean")
		code, out, _ := docsCheck(t, dir, "--translations", "free")
		if code != 1 || !strings.Contains(out, "UL101") {
			t.Fatalf("the field is refused in free mode too: exit %d\n%s", code, out)
		}
		stripTranslations(t, dir)
		// The check is off: a translation that uses none of the words passes, and is not read.
		rewrite(t, dir, clnTr, "Una sartén (Frying pan) está sobre el fuego. El horno (Oven) seca la comida. Un árbitro (Conflict resolver) decide.", "Nada de las palabras.")
		if code, out, errs := docsCheck(t, dir, "--translations", "free"); code != 0 {
			t.Fatalf("exit %d\n%s%s", code, out, errs)
		}
		// ... while the same repo is refused in the other two modes.
		if code, _, _ := docsCheck(t, dir, "--translations", "english"); code != 1 {
			t.Fatalf("english must refuse: exit %d", code)
		}
	})
	t.Run("bad mode", func(t *testing.T) {
		dir := fixture(t, "docs/clean")
		if code, _, errs := docsCheck(t, dir, "--translations", "latin"); code != 2 || !strings.Contains(errs, "fixed, english, free") {
			t.Fatalf("exit %d: %s", code, errs)
		}
	})
}

func stripTranslations(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, clnUL)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	skip := false
	for _, l := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(l, "    translations:"):
			skip = true
		case skip && strings.HasPrefix(l, "      "):
		default:
			skip = false
			keep = append(keep, l)
		}
	}
	if err := os.WriteFile(p, []byte(strings.Join(keep, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Owner pick 2: the what folder is model by default, and a flag names another.
func TestWhatFolder(t *testing.T) {
	dir := fixture(t, "bad/UL408-concept-not-word")
	if code, out, _ := docsCheck(t, dir); code != 1 || !strings.Contains(out, "UL408") {
		t.Fatalf("model/ is a what folder by default: exit %d\n%s", code, out)
	}
	if code, out, _ := docsCheck(t, dir, "--what-folder", "core"); code != 0 {
		t.Fatalf("with core, model/ is no what folder: exit %d\n%s", code, out)
	}
	if err := os.Rename(filepath.Join(dir, "docs/domain/kitchen/model"), filepath.Join(dir, "docs/domain/kitchen/core")); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := docsCheck(t, dir, "--what-folder", "core"); code != 1 || !strings.Contains(out, "core/stove.md") || !strings.Contains(out, "UL408") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// A word two listed namespaces define resolves to both (a flag); a word of an unlisted namespace
// is refused and the message names every namespace that defines it.
func TestDocsResolution(t *testing.T) {
	dir := fixture(t, "docs/clean")
	rewrite(t, dir, "docs/domain/kitchen/index.md", "  - example.org/cooking/kitchen\n  - example.org/cooking/pantry\n", "  - example.org/cooking/pantry\n")
	code, out, _ := docsCheck(t, dir)
	if code != 0 || out != "" {
		t.Fatalf("a word of the one listed namespace resolves to it: exit %d\n%s", code, out)
	}
	rewrite(t, dir, "docs/domain/kitchen/index.md", "  - example.org/cooking/pantry\n", "[]\n")
	rewrite(t, dir, "docs/domain/kitchen/index.md", "namespaces:\n[]\n", "namespaces: []\n")
	code, out, _ = docsCheck(t, dir)
	if code != 1 || !strings.Contains(out, `UL402 "Oven" is a UL word of example.org/cooking/kitchen and example.org/cooking/pantry`) {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// A bare avoided word is refused; the same word inside a longer UL word is not.
func TestAvoidedInsideLongerWord(t *testing.T) {
	dir := fixture(t, "docs/clean")
	writeTree(t, dir, map[string]string{"docs/a.md": "---\ntitle: A\nnamespaces: [example.org/cooking/kitchen]\n---\n\nA Conflict resolver, a conflict resolver and a bare resolver; a Frying pan.\n"})
	code, out, _ := docsCheck(t, dir)
	if code != 1 || strings.Count(out, "UL403") != 1 || !strings.Contains(out, `docs/a.md:6: UL403 avoided word "resolver"; write "Conflict resolver"`) {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// A page with no front matter, or with front matter but no namespaces, is not ul's: doccheck
// refuses it. A repo with docs but no such page never needs dh.
func TestDocsNeedNamespaces(t *testing.T) {
	dir := fixture(t, "docs/clean")
	writeTree(t, dir, map[string]string{
		"docs/plain.md":  "A griddle and a Skillet with no front matter.\n",
		"docs/nons.md":   "---\ntitle: X\n---\n\nA griddle.\n",
		"docs/bad.md":    "---\ntitle: [unclosed\n---\n\nA griddle.\n",
		"docs/x.dh.md":   "no front matter, never sent to dh: a griddle.\n",
		"docs/UL.md":     "# generated\n",
		"docs/notes.txt": "griddle",
	})
	if code, out, errs := docsCheck(t, dir, "--dh", "/no/such/dh"); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
}

// Words two pages name as their concept: one page per word, through aliases too.
func TestConceptOnePerWord(t *testing.T) {
	dir := fixture(t, "bad/UL409-two-pages-one-word")
	code, out, _ := docsCheck(t, dir)
	if code != 1 || !strings.Contains(out, "frying-pan.md:3: UL409") || !strings.Contains(out, "fry-pan.md") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// Every command finishes in under one second on a repo with 1,000 words in 100 files and 100
// doc pages that use them (the speed rule of the ul design, with docs).
func TestSpeedDocs(t *testing.T) {
	dir := t.TempDir()
	genRepo(t, dir, 100, 10)
	pages := map[string]string{}
	for f := 0; f < 100; f++ {
		var b strings.Builder
		fmt.Fprintf(&b, "---\ntitle: P%d\nconcept: c%d\ndescription: d\nnamespaces:\n  - speed.example/area%03d/part\n---\n\n", f, f, f)
		for w := 0; w < 10; w++ {
			fmt.Fprintf(&b, "The Word%03d x%02d meets Alias%03d x%02d here. %s\n\n", f, w, f, (w+1)%10, strings.Repeat("Plain filler text that matches nothing. ", 8))
		}
		fmt.Fprintf(&b, "```sh\nWord%03d x00 in code is never read\n```\n", f)
		pages[fmt.Sprintf("docs/p%03d.md", f)] = b.String()
	}
	writeTree(t, dir, pages)
	te := newEnv(t, dir)
	var worst time.Duration
	for i := 0; i < 3; i++ {
		start := time.Now()
		code, out, errs := te.ul("check", "--root", dir)
		d := time.Since(start)
		if code != 0 || out != "" {
			t.Fatalf("exit %d\n%s%s", code, out, errs[:min(len(errs), 600)])
		}
		worst = max(worst, d)
	}
	t.Logf("check on 1,000 words, 100 files and 100 pages: %v", worst)
	if worst >= time.Second {
		t.Fatalf("ul check took %v on 1,000 words and 100 pages; the limit is one second", worst)
	}
}

func cleanWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := fixture(t, "docs/clean")
	writeTree(t, dir, files)
	return dir
}

const kitchenNSLine = "namespaces: [example.org/cooking/kitchen]"

// UL405 flags a listed namespace the page never uses, except an engineering standard's.
func TestUnusedNamespaceFlag(t *testing.T) {
	dir := cleanWith(t, map[string]string{
		"docs/a.md":               "---\ntitle: A\nnamespaces: [example.org/cooking/kitchen, example.org/cooking/pantry]\n---\n\nThe Frying pan here, nothing of the pantry.\n",
		"docs/domain/std/UL.yaml": "namespace: example.org/engineering/standards/std\npremise: A standard\nlanguage:\n  Rule:\n    desc: A rule.\n",
		"docs/b.md":               "---\ntitle: B\nnamespaces: [example.org/engineering/standards/std]\n---\n\nNothing here.\n",
	})
	code, out, errs := docsCheck(t, dir)
	if code != 0 || out != "" {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
	if !strings.Contains(errs, "docs/a.md:3: UL405 flag: namespaces lists example.org/cooking/pantry") || strings.Contains(errs, "docs/b.md") {
		t.Fatalf("flags:\n%s", errs)
	}
}

// UL406 flags an alias that another namespace defines, retired words included.
func TestSameWordFlagAliasAndRetired(t *testing.T) {
	dir := cleanWith(t, map[string]string{"docs/domain/pantry/UL.yaml": "namespace: example.org/cooking/pantry\npremise: Pantry\nlanguage:\n  Shelf:\n    desc: A board.\n    aliases: [Fry pan]\n  Old:\n    desc: Retired one.\n    status: retired\n    aliases: [Skillet]\n"})
	_, _, errs := docsCheck(t, dir)
	if !strings.Contains(errs, `UL406 flag: "Fry pan" is also defined in`) || !strings.Contains(errs, `UL406 flag: "Skillet" is also defined in`) {
		t.Fatalf("flags:\n%s", errs)
	}
}

// The intro of an overview.yaml is prose against the context's own namespace.
func TestOverviewIntro(t *testing.T) {
	dir := cleanWith(t, map[string]string{"docs/domain/kitchen/overview.yaml": "intro: The Frying pan and the Oven.\n"})
	if code, out, errs := docsCheck(t, dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
	writeTree(t, dir, map[string]string{"docs/domain/kitchen/overview.yaml": "intro: |\n  First line.\n  A griddle here.\n"})
	code, out, _ := docsCheck(t, dir)
	if code != 1 || !strings.Contains(out, `docs/domain/kitchen/overview.yaml:3: UL403 avoided word "griddle"`) {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// A translation is read for its source whether or not its front matter has namespaces.
func TestTranslationWithoutNamespaces(t *testing.T) {
	dir := fixture(t, "docs/clean")
	rewrite(t, dir, clnTr, "namespaces:\n  - example.org/cooking/kitchen\n", "")
	rewrite(t, dir, clnTr, "El horno (Oven)", "La cocina (Oven)")
	code, out, _ := docsCheck(t, dir)
	if code != 1 || !strings.Contains(out, "UL502") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// A language tag is matched ignoring case.
func TestLanguageTagCase(t *testing.T) {
	dir := fixture(t, "docs/clean")
	rewrite(t, dir, clnTr, "lang: es", "lang: ES")
	if code, out, errs := docsCheck(t, dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
}

// What a reader reads is what is matched: a name split by code is not the name, an entity is the
// letter it stands for, a mermaid label is read and a mermaid click target is not.
func TestReaderEdgeCases(t *testing.T) {
	dir := cleanWith(t, map[string]string{
		"docs/split.md":  "---\ntitle: S\nnamespaces: []\n---\n\nFrying `x` pan is not the name.\n",
		"docs/entity.md": "---\ntitle: E\nnamespaces: " + kitchenNSLine[len("namespaces: "):] + "\n---\n\nA gr&#105;ddle.\n",
		"docs/lines.md":  "---\ntitle: L\nnamespaces: []\n---\n\nThe Frying\npan is one name over a line break.\n",
		"docs/mer.md":    "---\ntitle: M\nnamespaces: [example.org/cooking/kitchen]\n---\n\n```mermaid\nflowchart LR\n  a[\"A griddle\"] --> b[Oven]\n  click b \"https://x.test/griddle\"\n```\n",
	})
	code, out, _ := docsCheck(t, dir)
	if code != 1 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"docs/entity.md:6: UL403",
		`docs/lines.md:6: UL402 "Frying pan"`,
		"docs/mer.md:8: UL403",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	for _, not := range []string{"docs/split.md", "docs/mer.md:9"} {
		if strings.Contains(out, not) {
			t.Errorf("unexpected %q in\n%s", not, out)
		}
	}
}

// Which namespace of a page's list comes first does not decide UL409.
func TestConceptOrderIndependent(t *testing.T) {
	dir := cleanWith(t, map[string]string{
		"docs/domain/pantry/UL.yaml": "namespace: example.org/cooking/pantry\npremise: Pantry\nlanguage:\n  Fry pan:\n    desc: The pantry's own pan.\n",
		"docs/a.md":                  "---\ntitle: A\nconcept: Frying pan\nnamespaces: [example.org/cooking/kitchen]\n---\n\nThe Frying pan.\n",
		"docs/b.md":                  "---\ntitle: B\nconcept: Fry pan\nnamespaces: [example.org/cooking/pantry, example.org/cooking/kitchen]\n---\n\nThe Fry pan.\n",
	})
	// the pantry defines its own Fry pan; the kitchen's alias is also Fry pan
	code, out, _ := docsCheck(t, dir)
	if code != 1 || !strings.Contains(out, "docs/b.md:3: UL409") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestTranslationTagRules(t *testing.T) {
	for _, tag := range []string{"en-12", "ES", "english", "zh-Hans-CN-x"} {
		want := tag == "ES"
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"UL.yaml": "namespace: example.org/a/b\npremise: P\nlanguage:\n  Oven:\n    desc: A box.\n    translations:\n      " + tag + ": horno\n"})
		code, out, _ := docsCheck(t, dir)
		if (code == 0) != want {
			t.Errorf("tag %s: exit %d (accepted %v, want %v)\n%s", tag, code, code == 0, want, out)
		}
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"UL.yaml": "namespace: example.org/a/b\npremise: P\nlanguage:\n  Oven:\n    desc: A box.\n    translations:\n      es: horno\n      ES: forno\n"})
	if code, out, _ := docsCheck(t, dir); code != 1 || !strings.Contains(out, "UL110") || !strings.Contains(out, "repeats a language") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// The source of a translation is a page, never the overview file of a context.
func TestTranslationSourceIsAPage(t *testing.T) {
	dir := fixture(t, "docs/clean")
	writeTree(t, dir, map[string]string{"docs/domain/kitchen/overview.yaml": "intro: The Oven.\n"})
	rewrite(t, dir, clnTr, "source: ../../../../../domain/kitchen/model/frying-pan.md", "source: ../../../../../domain/kitchen/overview.yaml")
	if code, out, _ := docsCheck(t, dir); code != 1 || !strings.Contains(out, "UL503") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// More of what a reader sees: an escaped hyphen, a lone-CR file and a mermaid statement after a
// semicolon.
func TestReaderEdgeCases2(t *testing.T) {
	dir := cleanWith(t, map[string]string{
		"docs/esc.md": "---\ntitle: E\nnamespaces: [example.org/cooking/kitchen]\n---\n\nThe Fry\\ pan is not a name but \\*griddle\\* is.\n",
		"docs/cr.md":  "---\rtitle: C\rnamespaces: [example.org/cooking/kitchen]\r---\r\rA griddle.\r",
		"docs/mer.md": "---\ntitle: M\nnamespaces: [example.org/cooking/kitchen]\n---\n\n```mermaid\nclassDiagram\n  Animal --|> Food : griddle\n```\n\n```mermaid\nflowchart LR\n  A[Acceptable] --> B[Oven]; click B \"https://x.test/griddle\"\n```\n",
	})
	code, out, _ := docsCheck(t, dir)
	if code != 1 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"docs/esc.md:6: UL403", "docs/cr.md:6: UL403", "docs/mer.md:8: UL403"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "docs/mer.md:13") {
		t.Errorf("a click target was read:\n%s", out)
	}
}
