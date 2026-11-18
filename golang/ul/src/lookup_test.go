package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const kitchenNS = "example.org/cooking/kitchen"

func (te *testEnv) lookupIDs(args ...string) []string {
	te.t.Helper()
	var res lookupResult
	out := te.mustOK(append([]string{"lookup", "--format", "json"}, args...)...)
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		te.t.Fatal(err)
	}
	var ids []string
	for _, m := range res.Matches {
		ids = append(ids, m.ID)
	}
	return ids
}

// There is no session layer: no add or session command, no session flags, no layer fields.
func TestNoSessionLayer(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	te.vars["UL_SESSION"] = "s"
	te.vars["FY_SESSION_ID"] = "s"
	for _, args := range [][]string{{"add", "--word", "W", "--desc", "x"}, {"session", "path"}, {"all", "--session", "s"}, {"all", "--no-session"}} {
		if code, _, _ := te.ul(append(args, "--root", dir)...); code != 2 {
			t.Errorf("ul %v: exit %d, want 2 (unknown command or flag)", args, code)
		}
	}
	raw := te.mustOK("all", "--root", dir)
	for _, gone := range []string{`"session"`, `"layer"`, `"overrides"`} {
		if strings.Contains(raw, gone) {
			t.Errorf("ul/v1 output still has %s", gone)
		}
	}
	// A word is never `session#...`, and `session` is an ordinary (too short) namespace.
	if r := te.report("all", "--root", dir); len(r.Words) != 9 {
		t.Fatalf("env session ids must change nothing: %v", wordIDs(r))
	}
}

func TestLookupOrder(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a/UL.yaml":     "namespace: ex.org/a\npremise: A\nlanguage:\n  Gear:\n    desc: Gear in a.\n    aliases: [Cog]\n  Item:\n    desc: Item in a.\n",
		"b/UL.yaml":     "namespace: ex.org/b\npremise: B\nlanguage:\n  Cog:\n    desc: Cog in b.\n  Item:\n    desc: Item in b.\n",
		"a/sub/UL.yaml": "namespace: ex.org/a/sub\npremise: A sub\nlanguage:\n  Item:\n    desc: Item in a sub.\n",
		"p/q/UL.yaml":   "namespace: ex.org/p/q\npremise: PQ\nlanguage:\n  Leaf:\n    desc: Leaf in p/q.\n",
		"p/r/s/UL.yaml": "namespace: ex.org/p/r/s\npremise: PRS\nlanguage:\n  Leaf:\n    desc: Leaf in p/r/s.\n",
		"UL.yaml":       "namespace: ex.org/top\npremise: Top\nlanguage:\n  Leaf:\n    desc: Leaf at the root.\n",
	})
	te := newEnv(t, dir)
	same := func(got, want []string) bool { return strings.Join(got, " ") == strings.Join(want, " ") }

	// 1. a word match before an alias match, whatever the namespace order.
	if got := te.lookupIDs("--root", dir, "cog"); !same(got, []string{"ex.org/b#Cog", "ex.org/a#Gear"}) {
		t.Errorf("word before alias: %v", got)
	}
	// 4. A–Z when nothing else ranks.
	if got := te.lookupIDs("--root", dir, "item"); !same(got, []string{"ex.org/a#Item", "ex.org/a/sub#Item", "ex.org/b#Item"}) {
		t.Errorf("A-Z: %v", got)
	}
	// 3. closest to --from: the deepest folder that contains the path.
	closeness := []struct {
		from string
		want []string
	}{
		{"a/sub/x.go", []string{"ex.org/a/sub#Item", "ex.org/a#Item", "ex.org/b#Item"}},
		{"a/sub/deep/er/x.go", []string{"ex.org/a/sub#Item", "ex.org/a#Item", "ex.org/b#Item"}},
		{"a/other/x.go", []string{"ex.org/a#Item", "ex.org/a/sub#Item", "ex.org/b#Item"}},
		{"b", []string{"ex.org/b#Item", "ex.org/a#Item", "ex.org/a/sub#Item"}},
		{"b/not-yet/x.go", []string{"ex.org/b#Item", "ex.org/a#Item", "ex.org/a/sub#Item"}},
		{dir + "/b/x.go", []string{"ex.org/b#Item", "ex.org/a#Item", "ex.org/a/sub#Item"}},
	}
	for _, c := range closeness {
		if got := te.lookupIDs("--root", dir, "--from", c.from, "item"); !same(got, c.want) {
			t.Errorf("--from %s: %v, want %v", c.from, got, c.want)
		}
	}
	// then the longest shared folder prefix, when no namespace folder contains the path.
	// ex.org/top lives in the root, so it contains every path: it ranks first.
	if got := te.lookupIDs("--root", dir, "--from", "p/r/t/x.go", "leaf"); !same(got, []string{"ex.org/top#Leaf", "ex.org/p/r/s#Leaf", "ex.org/p/q#Leaf"}) {
		t.Errorf("shared prefix: %v", got)
	}
	if got := te.lookupIDs("--root", dir, "--from", "p/q/x.go", "leaf"); !same(got, []string{"ex.org/p/q#Leaf", "ex.org/top#Leaf", "ex.org/p/r/s#Leaf"}) {
		t.Errorf("contained wins over shared prefix: %v", got)
	}
	// a path outside the root ranks nothing.
	if got := te.lookupIDs("--root", dir, "--from", "../../elsewhere/x.go", "item"); !same(got, []string{"ex.org/a#Item", "ex.org/a/sub#Item", "ex.org/b#Item"}) {
		t.Errorf("outside the root: %v", got)
	}
	// a full id, a bare alias and a case-folded name all find their word.
	if got := te.lookupIDs("--root", dir, "ex.org/a#cog"); !same(got, []string{"ex.org/a#Gear"}) {
		t.Errorf("full id by alias: %v", got)
	}
}

func TestLookupNotFound(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	code, out, errs := te.ul("lookup", "Ovn", "--root", dir)
	if code != 1 || out != "" || !strings.Contains(errs, `no match for "Ovn"`) || !strings.Contains(errs, "near: Oven") {
		t.Fatalf("exit %d\nout: %s\nerr: %s", code, out, errs)
	}
	code, out, _ = te.ul("lookup", "Ovn", "--root", dir, "--format", "json")
	var res lookupResult
	if code != 1 || json.Unmarshal([]byte(out), &res) != nil || res.Schema != "ul/v1" || res.Query != "Ovn" || len(res.Matches) != 0 || len(res.Near) != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if code, _, errs := te.ul("lookup", "zzzzzzzz", "--root", dir); code != 1 || strings.Contains(errs, "near:") {
		t.Fatalf("far-off names have no near matches: %d %s", code, errs)
	}
	if code, _, _ := te.ul("lookup", "--root", dir); code != 2 {
		t.Fatalf("lookup with no name: exit %d", code)
	}
	// Interspersed flags: the name may come first.
	te.mustOK("lookup", "Wok", "--root", dir, "--format", "json")
	if got := te.mustOK("lookup", "round pan", "--root", dir); !strings.HasPrefix(got, "Wok  (example.org > cooking > kitchen)\n") {
		t.Fatalf("alias lookup: %s", got)
	}
}

// Mentions (spec §4.4): whole words, any case, at least two characters, never the entry
// itself; an ambiguous hit lists every candidate.
func TestMentions(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a/UL.yaml": `namespace: ex.org/a
premise: A
language:
  Hub:
    desc: Talks to the spoke, the SPOKE-like Rim and a Spokes list; also x and Hub itself.
    details: |
      The plugin design pattern; see Plugin Design and plugin.
      Wheel_rim is not the Rim. (Spoke), spoke's, 'Spoke' too.
  Spoke:
    desc: One arm.
  Rim:
    desc: The outside.
  Plugin:
    desc: In a.
    aliases: [Plugin Design]
  X:
    desc: A single-letter word is never matched.
`,
		"b/UL.yaml": `namespace: ex.org/b
premise: B
language:
  Plugin:
    desc: In b.
  User:
    desc: Uses a plugin from anywhere and Spoke.
  Self:
    desc: Mentions its own alias Me and Self, and a Hub.
    aliases: [Me]
`,
	})
	te := newEnv(t, dir)
	r := te.report("all", "--root", dir)
	hub := findWord(t, r, "ex.org/a#Hub")
	want := []string{"ex.org/a#Plugin", "ex.org/a#Rim", "ex.org/a#Spoke"}
	if strings.Join(hub.Mentions, " ") != strings.Join(want, " ") {
		t.Errorf("Hub mentions %v, want %v (X is too short, Spokes is another word, Wheel_rim has no boundary, Hub is itself)", hub.Mentions, want)
	}
	user := findWord(t, r, "ex.org/b#User")
	want = []string{"ex.org/a#Spoke", "ex.org/b#Plugin"}
	if strings.Join(user.Mentions, " ") != strings.Join(want, " ") {
		t.Errorf("User mentions %v, want %v (a bare hit prefers its own namespace)", user.Mentions, want)
	}
	self := findWord(t, r, "ex.org/b#Self")
	if strings.Join(self.Mentions, " ") != "ex.org/a#Hub" {
		t.Errorf("Self mentions %v: its own word and alias are never listed", self.Mentions)
	}
	// An ambiguous hit lists every candidate; the longest name wins at one position.
	dir2 := t.TempDir()
	writeTree(t, dir2, map[string]string{
		"a/UL.yaml": "namespace: ex.org/a\npremise: A\nlanguage:\n  Widget:\n    desc: In a.\n  Open Close:\n    desc: Long name.\n  Open:\n    desc: Short name.\n",
		"b/UL.yaml": "namespace: ex.org/b\npremise: B\nlanguage:\n  Widget:\n    desc: In b.\n",
		"c/UL.yaml": "namespace: ex.org/c\npremise: C\nlanguage:\n  Gadget:\n    desc: Takes a widget, and the open close rule, and an Open door.\n",
	})
	g := findWord(t, te.report("all", "--root", dir2), "ex.org/c#Gadget")
	want = []string{"ex.org/a#Open", "ex.org/a#Open Close", "ex.org/a#Widget", "ex.org/b#Widget"}
	if strings.Join(g.Mentions, " | ") != strings.Join(want, " | ") {
		t.Errorf("Gadget mentions %v, want %v", g.Mentions, want)
	}
	// The ambiguous bare reference in `see` is a problem (UL202) while a mention is not.
	if len(te.report("all", "--root", dir2).Problems) != 0 {
		t.Error("mentions never raise problems")
	}
}

func TestMentionsInClean(t *testing.T) {
	dir := fixture(t, "clean")
	r := newEnv(t, dir).report("all", "--root", dir)
	score := findWord(t, r, "example.org/cooking/pastry/bread#Score")
	want := []string{kitchenNS + "#Oven", "example.org/cooking/pastry#Crust", "example.org/cooking/pastry#Oven"}
	if strings.Join(score.Mentions, " ") != strings.Join(want, " ") {
		t.Errorf("Score mentions %v, want %v", score.Mentions, want)
	}
	wok := findWord(t, r, kitchenNS+"#Wok")
	if strings.Join(wok.Mentions, " ") != kitchenNS+"#Frying pan "+kitchenNS+"#Oven" {
		t.Errorf("an alias hit (Flat Pan) must mention Frying pan: %v", wok.Mentions)
	}
}
