package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const oopNS = "durian.cloud/engineering/standards/OOP"

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

func TestSessionOverridesRepo(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	te.mustOK("add", "--root", dir, "--session", "s1", "--namespace", oopNS, "--word", "Plugin",
		"--desc", "Sharper: one part with its own types.", "--see", "OCP")
	r := te.report("all", "--root", dir, "--session", "s1")
	if r.Session == nil || *r.Session != "s1" {
		t.Fatalf("session = %v", r.Session)
	}
	p := findWord(t, r, oopNS+"#Plugin")
	if p.Layer != "session" || p.Overrides == nil || *p.Overrides != "repo" || p.Desc != "Sharper: one part with its own types." {
		t.Fatalf("session entry must replace the repo entry whole: %+v", p)
	}
	if p.Details != "" || !strings.HasSuffix(p.File, "s1.yaml") {
		t.Fatalf("replaced whole: details must not leak from the repo entry, file must be the session file: %+v", p)
	}
	n := 0
	for _, w := range r.Words {
		if w.ID == oopNS+"#Plugin" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the id appears %d times", n)
	}
	// The namespace stays a repo namespace, and other words keep their repo layer.
	for _, ns := range r.Namespaces {
		if ns.Namespace == oopNS && ns.Layer != "repo" {
			t.Errorf("namespace layer %s", ns.Layer)
		}
	}
	if ocp := findWord(t, r, oopNS+"#OCP"); ocp.Layer != "repo" || ocp.Overrides != nil {
		t.Errorf("OCP: %+v", ocp)
	}
	// Another session, or none, sees only the repo.
	if p := findWord(t, te.report("all", "--root", dir, "--session", "other"), oopNS+"#Plugin"); p.Layer != "repo" {
		t.Error("a different session must not see s1's words")
	}
	if te.report("all", "--root", dir).Session != nil {
		t.Error("no session id: session must be null")
	}
	if p := findWord(t, te.report("all", "--root", dir, "--session", "s1", "--no-session"), oopNS+"#Plugin"); p.Layer != "repo" {
		t.Error("--no-session must ignore the layer")
	}
	// Session words never reach check or gen.
	te.mustOK("check", "--root", dir, "--session", "s1")
}

func TestSessionNamespaceDefault(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	te.vars["UL_SESSION"] = "env-session"
	te.mustOK("add", "--root", dir, "--word", "Gizmo", "--desc", "A thing coined in chat.", "--alias", "Gadget", "--see", oopNS+"#OCP", "--avoid", "doohickey", "--details", "More.")
	r := te.report("all", "--root", dir)
	w := findWord(t, r, "session#Gizmo")
	if w.Layer != "session" || w.Overrides != nil || strings.Join(w.Breadcrumb, ">") != "session" || len(w.Aliases) != 1 || len(w.See) != 1 || w.Details != "More." {
		t.Fatalf("%+v", w)
	}
	var sess JNamespace
	for _, ns := range r.Namespaces {
		if ns.Namespace == "session" {
			sess = ns
		}
	}
	if sess.Layer != "session" || sess.Premise == "" {
		t.Fatalf("session namespace: %+v", sess)
	}
	// $FY_SESSION_ID is the last fallback, and loses to $UL_SESSION and to --session.
	delete(te.vars, "UL_SESSION")
	te.vars["FY_SESSION_ID"] = "fy-session"
	if te.report("all", "--root", dir).Session == nil || *te.report("all", "--root", dir).Session != "fy-session" {
		t.Error("FY_SESSION_ID not used")
	}
	te.vars["UL_SESSION"] = "env-session"
	if *te.report("all", "--root", dir).Session != "env-session" {
		t.Error("UL_SESSION must beat FY_SESSION_ID")
	}
	if *te.report("all", "--root", dir, "--session", "flag").Session != "flag" {
		t.Error("--session must beat the environment")
	}
	// The session file lives under UL_STATE_DIR/sessions/<id>.yaml, outside the repo.
	p := strings.TrimSpace(te.mustOK("session", "path", "--root", dir))
	if want := filepath.Join(te.vars["UL_STATE_DIR"], "sessions", "env-session.yaml"); p != want {
		t.Fatalf("session path %s, want %s", p, want)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	te.mustOK("check", "--root", dir)
	if code, out, _ := te.ul("gen", "--check", "--root", dir); code != 0 {
		t.Fatalf("ul add must never dirty the repo: gen --check %d %s", code, out)
	}
}

func TestStateDirFallbacks(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := stateDir(get(map[string]string{"XDG_STATE_HOME": "/x", "HOME": "/h"})); got != "/x/ul" {
		t.Error(got)
	}
	if got := stateDir(get(map[string]string{"HOME": "/h"})); got != "/h/.local/state/ul" {
		t.Error(got)
	}
	if got := stateDir(get(map[string]string{"UL_STATE_DIR": "/u", "XDG_STATE_HOME": "/x"})); got != "/u" {
		t.Error(got)
	}
}

func TestAddRefusals(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	add := func(args ...string) (int, string) {
		code, _, errs := te.ul(append([]string{"add", "--root", dir, "--session", "s"}, args...)...)
		return code, errs
	}
	if code, errs := add("--word", "Ok", "--desc", "Fine."); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	sessionFile := strings.TrimSpace(te.mustOK("session", "path", "--session", "s"))
	before, _ := os.ReadFile(sessionFile)
	cases := []struct {
		name string
		args []string
		code string
	}{
		{"unresolved see", []string{"--word", "A1", "--desc", "x", "--see", "Nope"}, "UL201"},
		{"ambiguous see", []string{"--word", "A2", "--desc", "x", "--see", "Plugin"}, "UL202"},
		{"desc too long", []string{"--word", "A3", "--desc", strings.Repeat("x ", 81)}, "UL106"},
		{"multi-line desc", []string{"--word", "A4", "--desc", "a\nb"}, "UL106"},
		{"bad word", []string{"--word", "-A5", "--desc", "x"}, "UL105"},
		{"case clash with a session word", []string{"--word", "ok", "--desc", "x"}, "UL107"},
		{"alias clash with a repo word", []string{"--namespace", oopNS, "--word", "Thing", "--desc", "x", "--alias", "srp"}, "UL107"},
		{"avoid a word of the namespace", []string{"--namespace", oopNS, "--word", "Thing", "--desc", "x", "--avoid", "SRP"}, "UL109"},
		{"bad namespace", []string{"--namespace", "nodomain/x", "--word", "A6", "--desc", "x"}, "UL102"},
		{"see a retired repo word", []string{"--namespace", oopNS, "--word", "Thing", "--desc", "x", "--see", "Facade"}, "UL203"},
	}
	for _, c := range cases {
		code, errs := add(c.args...)
		if code != 1 || !strings.Contains(errs, "refused: "+c.code) {
			t.Errorf("%s: exit %d, want 1 with %s: %s", c.name, code, c.code, errs)
		}
		if after, _ := os.ReadFile(sessionFile); string(after) != string(before) {
			t.Errorf("%s: a refused word must leave the session file unchanged", c.name)
		}
	}
	// A refused add of a word the repo already breaks does not blame the new word.
	te.mustOK("add", "--root", dir, "--session", "s", "--word", "Fresh", "--desc", "Still fine.")
}

func TestAddNeedsASession(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	if code, _, errs := te.ul("add", "--root", dir, "--word", "W", "--desc", "x"); code != 2 || !strings.Contains(errs, "no session id") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	te.vars["UL_SESSION"] = "s"
	if code, _, _ := te.ul("add", "--root", dir, "--no-session", "--word", "W", "--desc", "x"); code != 2 {
		t.Fatalf("--no-session add: exit %d", code)
	}
	for _, args := range [][]string{{"add", "--word", "W"}, {"add", "--desc", "x"}, {"add", "--word", "W", "--desc", "x", "--file", "f.yaml"}, {"add"}} {
		if code, _, _ := te.ul(append(args, "--root", dir)...); code != 2 {
			t.Errorf("ul %v: exit %d, want 2", args, code)
		}
	}
	// All and lookup still run, on the repo layer alone.
	delete(te.vars, "UL_SESSION")
	te.mustOK("all", "--root", dir)
	if code, _, _ := te.ul("session", "path", "--root", dir); code != 2 {
		t.Errorf("session path without an id: exit %d", code)
	}
	if code, _, errs := te.ul("all", "--root", dir, "--session", "../evil"); code != 2 || !strings.Contains(errs, "session id") {
		t.Errorf("a path-like session id must be refused: %d %s", code, errs)
	}
}

func TestAddFileAndStdinAndUpdate(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	frag := "language:\n  Alpha:\n    desc: First fragment word.\n    aliases: [A1]\n  Beta:\n    desc: Second fragment word.\n    see: [Alpha]\n"
	writeTree(t, dir, map[string]string{"frag.yaml": frag})
	out := te.mustOK("add", "--root", dir, "--session", "s", "--file", "frag.yaml")
	if !strings.Contains(out, "added session#Alpha") || !strings.Contains(out, "added session#Beta") {
		t.Fatalf("%s", out)
	}
	code, out, _ := te.run("language:\n  Alpha:\n    desc: Updated.\n", "add", "--root", dir, "--session", "s", "--file", "-")
	if code != 0 || !strings.Contains(out, "updated session#Alpha") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = te.run("Long details\nin two lines\n", "add", "--root", dir, "--session", "s", "--word", "Beta", "--desc", "Third.", "--details", "-")
	if code != 0 || !strings.Contains(out, "updated session#Beta") {
		t.Fatalf("%d %s", code, out)
	}
	r := te.report("all", "--root", dir, "--session", "s")
	if a := findWord(t, r, "session#Alpha"); a.Desc != "Updated." || len(a.Aliases) != 0 {
		t.Fatalf("an update replaces the entry whole: %+v", a)
	}
	if b := findWord(t, r, "session#Beta"); b.Details != "Long details\nin two lines\n" || len(b.See) != 0 {
		t.Fatalf("%+v", b)
	}
	// A bad fragment is refused with the rule's code.
	code, _, errs := te.run("language:\n  Gamma:\n    desc: ok\n    colour: red\n", "add", "--root", dir, "--session", "s", "--file", "-")
	if code != 1 || !strings.Contains(errs, "UL101") {
		t.Fatalf("%d %s", code, errs)
	}
	// session show prints UL.yaml text that ul itself accepts; clear removes it.
	shown := te.mustOK("session", "show", "--session", "s")
	docs, probs := parseDocs("show", []byte(shown), parseOpts{session: true})
	if len(probs) != 0 || len(docs) != 1 || docs[0].Namespace != "session" || len(docs[0].Entries) != 2 {
		t.Fatalf("session show is not valid UL.yaml text: %v %v\n%s", probs, docs, shown)
	}
	te.mustOK("session", "clear", "--session", "s")
	if got := te.mustOK("session", "show", "--session", "s"); got != "" {
		t.Fatalf("after clear: %q", got)
	}
	te.mustOK("session", "clear", "--session", "s") // clearing twice is fine
}

func TestSessionSharpensRepoNamespaceKeepsPremise(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	te.mustOK("add", "--root", dir, "--session", "s", "--namespace", oopNS, "--word", "Mixin", "--desc", "A word added to a repo namespace.")
	shown := te.mustOK("session", "show", "--session", "s")
	if !strings.Contains(shown, "premise: When writing code, best practice when using object-oriented programming") {
		t.Fatalf("the premise of a repo namespace is copied:\n%s", shown)
	}
	r := te.report("all", "--root", dir, "--session", "s")
	if w := findWord(t, r, oopNS+"#Mixin"); w.Layer != "session" || w.Overrides != nil {
		t.Fatalf("a new word in a repo namespace overrides nothing: %+v", w)
	}
}

func TestBrokenSessionFile(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	p := filepath.Join(te.vars["UL_STATE_DIR"], "sessions", "bad.yaml")
	writeTree(t, filepath.Dir(p), map[string]string{"bad.yaml": "namespace: [oops\n"})
	r := te.report("all", "--root", dir, "--session", "bad")
	if len(r.Problems) != 1 || r.Problems[0].Code != "UL101" || r.Problems[0].File != p {
		t.Fatalf("%+v", r.Problems)
	}
	if code, _, _ := te.ul("add", "--root", dir, "--session", "bad", "--word", "W", "--desc", "x"); code != 2 {
		t.Fatalf("add on a broken session file: exit %d", code)
	}
}

// Lookup order (spec §4.3): word before alias; session before repo; closeness to --from;
// then namespace A–Z.
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
	te.vars["UL_SESSION"] = "s"
	same := func(got, want []string) bool { return strings.Join(got, " ") == strings.Join(want, " ") }

	// 1. a word match before an alias match, whatever the namespace order.
	if got := te.lookupIDs("--root", dir, "cog"); !same(got, []string{"ex.org/b#Cog", "ex.org/a#Gear"}) {
		t.Errorf("word before alias: %v", got)
	}
	// 2. the session layer before the repo layer.
	te.mustOK("add", "--root", dir, "--word", "Cog", "--desc", "Cog from the chat.")
	if got := te.lookupIDs("--root", dir, "COG"); !same(got, []string{"session#Cog", "ex.org/b#Cog", "ex.org/a#Gear"}) {
		t.Errorf("session before repo: %v", got)
	}
	te.mustOK("session", "clear")
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
	code, out, errs := te.ul("lookup", "Plugn", "--root", dir)
	if code != 1 || out != "" || !strings.Contains(errs, `no match for "Plugn"`) || !strings.Contains(errs, "near: Plugin") {
		t.Fatalf("exit %d\nout: %s\nerr: %s", code, out, errs)
	}
	code, out, _ = te.ul("lookup", "Plugn", "--root", dir, "--format", "json")
	var res lookupResult
	if code != 1 || json.Unmarshal([]byte(out), &res) != nil || res.Schema != "ul/v1" || res.Query != "Plugn" || len(res.Matches) != 0 || len(res.Near) != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if code, _, errs := te.ul("lookup", "zzzzzzzz", "--root", dir); code != 1 || strings.Contains(errs, "near:") {
		t.Fatalf("far-off names have no near matches: %d %s", code, errs)
	}
	if code, _, _ := te.ul("lookup", "--root", dir); code != 2 {
		t.Fatalf("lookup with no name: exit %d", code)
	}
	// Interspersed flags: the name may come first.
	te.mustOK("lookup", "SRP", "--root", dir, "--format", "json")
	if got := te.mustOK("lookup", "single responsibility", "--root", dir); !strings.HasPrefix(got, "SRP  (durian.cloud > engineering > standards > OOP)  [repo]") {
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
	rec := findWord(t, r, "durian.cloud/shop/billing/pay#Receipt")
	want := []string{oopNS + "#Plugin", "durian.cloud/shop/billing#Charge", "durian.cloud/shop/billing#Plugin"}
	if strings.Join(rec.Mentions, " ") != strings.Join(want, " ") {
		t.Errorf("Receipt mentions %v, want %v", rec.Mentions, want)
	}
	srp := findWord(t, r, oopNS+"#SRP")
	if strings.Join(srp.Mentions, " ") != oopNS+"#OCP "+oopNS+"#Plugin" {
		t.Errorf("an alias hit (Open-Close Principle) must mention OCP: %v", srp.Mentions)
	}
}
