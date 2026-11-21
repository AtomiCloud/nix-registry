package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useFakeDh makes the test binary the dh of this test (it replays JSON recorded from the real dh)
// and returns its path.
func useFakeDh(t *testing.T) string {
	t.Helper()
	t.Setenv(fakeDhEnv, "1")
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// checkJSON runs `ul check` on dir and returns the problems and flags as "file:line: CODE" lines.
func checkLines(t *testing.T, te *testEnv, dir string, extra ...string) (probs, flags []string, code int) {
	t.Helper()
	args := append([]string{"check", "--root", dir, "--format", "json", "--dh", useFakeDh(t)}, extra...)
	code, out, errs := te.ul(args...)
	var res problemsOut
	if err := jsonUnmarshal(out, &res); err != nil {
		t.Fatalf("check: exit %d, not JSON: %v\n%s%s", code, err, out, errs)
	}
	for _, p := range res.Problems {
		probs = append(probs, fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Code))
	}
	for _, p := range res.Flags {
		flags = append(flags, fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Code))
	}
	return probs, flags, code
}

// Every part holds the avoided word "griddle" where a reader reads it, and where only data
// lives (an id, a terminal command, a file name, chart values, a mermaid node id). The check
// finds the first kind and never the second.
func TestDhPartsTextOnly(t *testing.T) {
	dir := fixture(t, "dh/parts")
	te := newEnv(t, dir)
	probs, _, code := checkLines(t, te, dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	const page = "docs/domain/kitchen/implementation/parts.dh.md"
	var want []string
	// paragraph, tab title and body, accordion title and body, human text, :::llm text, terminal title
	// and step title, tree step title and note, chart title and series name, mermaid label, animation
	// label, caption and note, tier list title and item, alignment cell label.
	for _, l := range []int{9, 12, 14, 22, 24, 28, 32, 35, 36, 42, 44, 49, 53, 59, 63, 66, 69, 72, 74, 80} {
		want = append(want, fmt.Sprintf("%s:%d: UL403", page, l))
	}
	if strings.Join(probs, "\n") != strings.Join(want, "\n") {
		t.Errorf("refused at\n%s\nwant\n%s", strings.Join(probs, "\n"), strings.Join(want, "\n"))
	}
}

func readAll(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A dh that prints no texts for an animation (before 0.2.0) is refused for that page, never read
// in part.
func TestDhAnimationNeedsTexts(t *testing.T) {
	dir := fixture(t, "dh/parts")
	rec := filepath.Join(dir, "docs/domain/kitchen/implementation/parts.dh.md.dhjson")
	var doc map[string]any
	if err := jsonUnmarshal(readAll(t, rec), &doc); err != nil {
		t.Fatal(err)
	}
	for _, b := range doc["blocks"].([]any) {
		delete(b.(map[string]any), "texts")
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec, out, 0o644); err != nil {
		t.Fatal(err)
	}
	probs, _, _ := checkLines(t, newEnv(t, dir), dir)
	got := strings.Join(probs, " ")
	if !strings.Contains(got, "parts.dh.md:62: UL410") {
		t.Errorf("an animation without texts must be refused: %s", got)
	}
	for _, l := range []int{63, 66, 69} {
		if strings.Contains(got, fmt.Sprintf("parts.dh.md:%d: UL403", l)) {
			t.Errorf("the animation text was read without texts (line %d)", l)
		}
	}
}

// A page that does not parse is a UL410 problem of that page; the others are still read.
func TestDhParseError(t *testing.T) {
	dir := fixture(t, "dh/parts")
	writeTree(t, dir, map[string]string{
		"docs/broken.dh.md":       "---\ntitle: B\nconcept: b\ndescription: d\nnamespaces: [example.org/cooking/kitchen]\n---\n\n```terminal\n$ never closed\n",
		"docs/broken.dh.md.dherr": "docs/broken.dh.md:9: a fence is never closed: close it with ```\n",
	})
	probs, _, code := checkLines(t, newEnv(t, dir), dir)
	got := strings.Join(probs, " ")
	if code != 1 || !strings.Contains(got, "docs/broken.dh.md:9: UL410") || !strings.Contains(got, "parts.dh.md:9: UL403") {
		t.Fatalf("exit %d: %s", code, got)
	}
}

// Pages that are not .dh.md never need dh; a missing dh is exit 2, not a silent pass.
func TestDhOnlyWhenNeeded(t *testing.T) {
	dir := fixture(t, "bad/UL403-avoided-word")
	te := newEnv(t, dir)
	code, _, errs := te.ul("check", "--root", dir, "--dh", "/no/such/dh")
	if code != 1 {
		t.Fatalf("a .md-only repo must not call dh: exit %d: %s", code, errs)
	}
	dir = fixture(t, "dh/parts")
	code, _, errs = newEnv(t, dir).ul("check", "--root", dir, "--dh", "/no/such/dh")
	if code != 2 || !strings.Contains(errs, "dh") {
		t.Fatalf("missing dh: exit %d: %s", code, errs)
	}
	// UL_DH is read from the environment ul runs in.
	te = newEnv(t, dir)
	te.vars["UL_DH"] = useFakeDh(t)
	if code, _, errs := te.ul("check", "--root", dir); code != 1 {
		t.Fatalf("UL_DH: exit %d: %s", code, errs)
	}
}

// Two identical paragraphs give two hits on their own lines, and the banner is prose too.
func TestDhRepeatedProseAndBanner(t *testing.T) {
	dir := fixture(t, "dh/repeat")
	probs, _, _ := checkLines(t, newEnv(t, dir), dir)
	const page = "docs/domain/kitchen/implementation/repeat.dh.md"
	want := []string{page + ":9: UL403", page + ":12: UL403", page + ":16: UL403"}
	if strings.Join(probs, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v\nwant %v", probs, want)
	}
}

// What dh prints is checked: an object that is not a page, or too few or too many pages, is
// exit 2, never a page silently cleared.
func TestDhOutputValidated(t *testing.T) {
	good := readAll(t, filepath.Join("testdata", "dh/repeat/docs/domain/kitchen/implementation/repeat.dh.md.dhjson"))
	for name, rec := range map[string]string{"empty object": "{}", "no pages": "", "two pages": good + good, "only ast": "{\"ast\":1}", "stray bracket": good + "}"} {
		dir := fixture(t, "dh/repeat")
		rp := filepath.Join(dir, "docs/domain/kitchen/implementation/repeat.dh.md.dhjson")
		if err := os.WriteFile(rp, []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
		code, _, errs := newEnv(t, dir).ul("check", "--root", dir, "--dh", useFakeDh(t))
		if code != 2 || !strings.Contains(errs, "dh json") {
			t.Errorf("%s: exit %d: %s", name, code, errs)
		}
		// The trailing-output cases begin with a valid page, so only the trailing check can refuse them.
		if (name == "two pages" || name == "stray bracket") && !strings.Contains(errs, "more than the pages") {
			t.Errorf("%s: not refused for the trailing output: %s", name, errs)
		}
	}
}

// A --dh path is relative to where ul was started, not to --root.
func TestDhRelativePath(t *testing.T) {
	dir := fixture(t, "dh/repeat")
	bin := useFakeDh(t)
	cwd := filepath.Dir(bin)
	te := newEnv(t, cwd)
	code, _, errs := te.ul("check", "--root", dir, "--dh", "./"+filepath.Base(bin))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

// Part data is not prose even under a key called label (a chart's named expressions), the banner
// is read wherever the front matter ends, and an entity in rich prose is the letter it stands for.
func TestDhDataEntityAndBanner(t *testing.T) {
	dir := fixture(t, "dh/data")
	probs, _, _ := checkLines(t, newEnv(t, dir), dir)
	const page = "docs/domain/kitchen/implementation/data.dh.md"
	want := []string{page + ":14: UL403", page + ":17: UL403"}
	if strings.Join(probs, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v\nwant %v", probs, want)
	}
}

// A page whose front matter is not YAML still goes to dh, which refuses it with its line.
func TestDhUnreadableFrontMatter(t *testing.T) {
	dir := fixture(t, "dh/repeat")
	writeTree(t, dir, map[string]string{
		"docs/b.dh.md":       "---\nnamespaces: [unclosed\n---\n\n```terminal\n$ x\n```\n",
		"docs/b.dh.md.dherr": "docs/b.dh.md:2: the front matter is not valid YAML: close the list with ]\n",
	})
	probs, _, code := checkLines(t, newEnv(t, dir), dir)
	if code != 1 || !strings.Contains(strings.Join(probs, " "), "docs/b.dh.md:2: UL410") {
		t.Fatalf("exit %d: %v", code, probs)
	}
}

// A large page comes through the pipe whole: the packaged dh once lost the end of any output over
// 8,192 bytes when stdout was a pipe.
func TestDhLargePageThroughPipe(t *testing.T) {
	dir := fixture(t, "dh/repeat")
	rp := filepath.Join(dir, "docs/domain/kitchen/implementation/repeat.dh.md.dhjson")
	var doc map[string]any
	if err := jsonUnmarshal(readAll(t, rp), &doc); err != nil {
		t.Fatal(err)
	}
	doc["llm"] = strings.Repeat("x", 3<<20) // the LLM view of a long page
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rp, out, 0o644); err != nil {
		t.Fatal(err)
	}
	probs, _, code := checkLines(t, newEnv(t, dir), dir)
	if code != 1 || len(probs) != 3 {
		t.Fatalf("exit %d: %v", code, probs)
	}
}

// A name wrapped over a line break in rich prose is the name, as it is in .md.
func TestDhWrappedName(t *testing.T) {
	dir := fixture(t, "dh/wrap")
	probs, _, _ := checkLines(t, newEnv(t, dir), dir)
	if strings.Join(probs, "|") != "docs/domain/kitchen/implementation/wrap.dh.md:8: UL402" {
		t.Fatalf("got %v", probs)
	}
}

// ul only believes a page dh printed for the file it asked about, and a page with blocks.
func TestDhOutputIsThePage(t *testing.T) {
	dir := fixture(t, "dh/repeat")
	rp := filepath.Join(dir, "docs/domain/kitchen/implementation/repeat.dh.md.dhjson")
	for name, rec := range map[string]string{
		"another page": `{"ast":1,"source":"another page","blocks":[]}`,
		"no blocks":    `{"ast":1,"source":"` + strings.ReplaceAll(readSourceOf(t, rp), "\n", `\n`) + `"}`,
	} {
		if err := os.WriteFile(rp, []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, _, errs := newEnv(t, dir).ul("check", "--root", dir, "--dh", useFakeDh(t)); code != 2 {
			t.Errorf("%s: exit %d: %s", name, code, errs)
		}
	}
}

func readSourceOf(t *testing.T, recording string) string {
	t.Helper()
	var doc map[string]any
	if err := jsonUnmarshal(readAll(t, recording), &doc); err != nil {
		t.Fatal(err)
	}
	return doc["source"].(string)
}

// dh names the page that failed by its exact path before any path that merely ends the same way.
func TestDhErrorNamesExactPage(t *testing.T) {
	pages := []string{"docs/a.dh.md", "docs/nested/docs/a.dh.md"}
	page, line, text := whichPage("docs/nested/docs/a.dh.md:7: a fence is never closed", "/repo", pages)
	if page != "docs/nested/docs/a.dh.md" || line != 7 || text != "a fence is never closed" {
		t.Errorf("got %q %d %q", page, line, text)
	}
	// Two pages end the same way and none is the exact path: no page is blamed.
	if page, _, _ := whichPage("/other/docs/a.dh.md:7: x", "/repo", []string{"docs/a.dh.md", "a.dh.md"}); page != "" {
		t.Errorf("an ambiguous suffix named %q", page)
	}
}

// A block with no line (a dh that prints none) is read, not a panic.
func TestDhBlockWithoutLine(t *testing.T) {
	dir := fixture(t, "dh/parts")
	rec := filepath.Join(dir, "docs/domain/kitchen/implementation/parts.dh.md.dhjson")
	var doc map[string]any
	if err := jsonUnmarshal(readAll(t, rec), &doc); err != nil {
		t.Fatal(err)
	}
	for _, b := range doc["blocks"].([]any) {
		delete(b.(map[string]any), "line")
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec, out, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := checkLines(t, newEnv(t, dir), dir); code != 1 {
		t.Fatalf("exit %d, want 1 (the avoided words are still found)", code)
	}
}

// A word that a paragraph holds on two lines is found on each, and two labels that differ only in
// case keep their own lines whatever order the chart's keys are read in.
func TestDhEachHitOnItsOwnLine(t *testing.T) {
	dir := fixture(t, "dh/lines")
	const page = "docs/domain/kitchen/implementation/lines.dh.md"
	// "Griddle" is written on line 16 and "griddle" on line 17, the other way round from the order
	// of the chart's keys (x, then y).
	want := strings.Join([]string{page + ":9: UL403 griddle", page + ":10: UL403 griddle", page + ":16: UL403 Griddle", page + ":17: UL403 griddle"}, "|")
	for i := 0; i < 20; i++ { // map order differs from run to run
		te := newEnv(t, dir)
		_, out, errs := te.ul("check", "--root", dir, "--format", "json", "--dh", useFakeDh(t))
		var res problemsOut
		if err := jsonUnmarshal(out, &res); err != nil {
			t.Fatalf("not JSON: %v\n%s%s", err, out, errs)
		}
		var got []string
		for _, p := range res.Problems {
			w := p.Message[strings.Index(p.Message, "\"")+1:]
			got = append(got, fmt.Sprintf("%s:%d: %s %s", p.File, p.Line, p.Code, w[:strings.Index(w, "\"")]))
		}
		if strings.Join(got, "|") != want {
			t.Fatalf("run %d: got %v\nwant %v", i, got, want)
		}
	}
}
