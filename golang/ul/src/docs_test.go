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
