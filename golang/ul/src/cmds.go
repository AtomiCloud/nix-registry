package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// loaded is everything read from disk for one run.
type loaded struct {
	root      string
	scan      *scanResult
	repo      []*Doc
	probs     []Problem
	sid       string
	spath     string
	sess      []*Doc
	sessProbs []Problem
	sessRaw   []byte
}

func load(g *globals, e *env, withSession bool) (*loaded, error) {
	root, err := resolveRoot(g.root, e.cwd)
	if err != nil {
		return nil, usageErr{"--root: " + err.Error()}
	}
	l := &loaded{root: root}
	if l.scan, err = scanRepo(root); err != nil {
		return nil, err
	}
	for _, rel := range l.scan.Yaml {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		docs, probs := parseDocs(rel, data, parseOpts{})
		l.repo = append(l.repo, docs...)
		l.probs = append(l.probs, probs...)
	}
	if withSession {
		if l.sid, err = sessionID(g, e.getenv); err != nil {
			return nil, usageErr{err.Error()}
		}
		if l.sid != "" {
			l.spath = sessionPath(e.getenv, l.sid)
			if l.sess, l.sessProbs, l.sessRaw, err = loadSession(l.spath); err != nil {
				return nil, err
			}
		}
	}
	return l, nil
}

func (l *loaded) final(useSession bool) *Final {
	probs := l.probs
	var sid *string
	var sd []*Doc
	if useSession && l.sid != "" {
		s := l.sid
		sid, sd = &s, l.sess
		probs = append(slices.Clone(probs), l.sessProbs...)
	}
	return buildFinal(l.root, sid, l.repo, sd, l.spath, probs)
}

func wantFormat(g *globals, def string, allowed ...string) (string, error) {
	f := g.format
	if f == "" {
		f = def
	}
	if !slices.Contains(allowed, f) {
		return "", usageErr{fmt.Sprintf("--format %q: use one of %s", f, strings.Join(allowed, ", "))}
	}
	return f, nil
}

func noArgs(pos []string) error {
	if len(pos) > 0 {
		return usageErr{"unexpected argument " + pos[0]}
	}
	return nil
}

func cmdAll(g *globals, args []string, e *env) (int, error) {
	fs := newFlagSet("all", e)
	addCommon(fs, g)
	details := fs.Bool("details", false, "add the details of every word to --format md")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if err := noArgs(pos); err != nil {
		return 2, err
	}
	format, err := wantFormat(g, "json", "json", "md")
	if err != nil {
		return 2, err
	}
	l, err := load(g, e, true)
	if err != nil {
		return 2, err
	}
	f := l.final(true)
	if format == "md" {
		_, err = io.WriteString(e.stdout, f.renderAll(*details))
		return 0, err
	}
	return 0, writeJSON(e.stdout, f.report())
}

type lookupResult struct {
	Schema  string   `json:"schema"`
	Query   string   `json:"query"`
	Matches []JWord  `json:"matches"`
	Near    []string `json:"near"`
}

func cmdLookup(g *globals, args []string, e *env) (int, error) {
	fs := newFlagSet("lookup", e)
	addCommon(fs, g)
	from := fs.String("from", "", "rank matches by closeness to this file or folder")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(pos) != 1 {
		return 2, usageErr{"usage: ul lookup <name> [--from PATH] [--format text|json|md]"}
	}
	format, err := wantFormat(g, "text", "text", "json", "md")
	if err != nil {
		return 2, err
	}
	l, err := load(g, e, true)
	if err != nil {
		return 2, err
	}
	f := l.final(true)
	var parts []string
	var haveFrom bool
	if *from != "" {
		parts, haveFrom = fromParts(l.root, e.cwd, *from)
	}
	matches := f.lookup(pos[0], parts, haveFrom)
	near := f.near(pos[0])
	if len(matches) > 0 {
		near = nil
	}
	switch format {
	case "json":
		res := lookupResult{Schema: "ul/v1", Query: pos[0], Matches: []JWord{}, Near: []string{}}
		for _, m := range matches {
			res.Matches = append(res.Matches, m.jword())
		}
		for _, n := range near {
			res.Near = append(res.Near, n.ID)
		}
		if err := writeJSON(e.stdout, res); err != nil {
			return 2, err
		}
	case "md":
		var parts []string
		for _, m := range matches {
			parts = append(parts, strings.TrimRight(f.renderLookupMD(m), "\n"))
		}
		if len(parts) > 0 {
			io.WriteString(e.stdout, strings.Join(parts, "\n\n")+"\n")
		}
	default:
		for i, m := range matches {
			if i > 0 {
				fmt.Fprintln(e.stdout)
			}
			io.WriteString(e.stdout, f.renderLookupText(m))
		}
	}
	if len(matches) == 0 {
		if format != "json" {
			fmt.Fprintf(e.stderr, "ul: no match for %q\n", pos[0])
			if len(near) > 0 {
				var ns []string
				for _, n := range near {
					if !slices.Contains(ns, n.E.Word) {
						ns = append(ns, n.E.Word)
					}
				}
				fmt.Fprintf(e.stderr, "near: %s\n", strings.Join(ns, ", "))
			}
		}
		return 1, nil
	}
	return 0, nil
}

func shortRef(from *FNS, f *Final, id string) string {
	if t := f.byID[id]; t != nil && t.NS == from {
		return t.E.Word
	}
	return id
}

func (f *Final) renderLookupText(m *FEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  (%s)  [%s]\n", m.E.Word, strings.Join(m.NS.Breadcrumb, " > "), m.Layer)
	fmt.Fprintf(&b, "  %s\n", m.E.Desc)
	list := func(label string, items []string) {
		if len(items) > 0 {
			fmt.Fprintf(&b, "  %s: %s\n", label, strings.Join(items, ", "))
		}
	}
	list("Also called", names(m.E.Aliases))
	var see, men []string
	for _, id := range m.See {
		see = append(see, shortRef(m.NS, f, id))
	}
	for _, id := range m.Mentions {
		men = append(men, shortRef(m.NS, f, id))
	}
	list("See", see)
	list("Avoid", names(m.E.Avoid))
	list("Mentions", men)
	if m.Retired {
		if m.ReplacedBy != "" {
			fmt.Fprintf(&b, "  Retired; replaced by %s\n", shortRef(m.NS, f, m.ReplacedBy))
		} else {
			b.WriteString("  Retired\n")
		}
	}
	if d := strings.TrimRight(m.E.Details, " \t\r\n"); d != "" {
		b.WriteString("\n")
		for _, line := range strings.Split(d, "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

func (f *Final) renderLookupMD(m *FEntry) string {
	o := mdOpts{level: 2}
	blocks := []string{heading(2, m.E.Word), strings.Join(m.NS.Breadcrumb, " > "), m.E.Desc}
	line := func(label string, items []string) {
		if len(items) > 0 {
			blocks = append(blocks, label+": "+strings.Join(items, ", "))
		}
	}
	line("Also called", names(m.E.Aliases))
	var see []string
	for _, id := range m.See {
		see = append(see, f.refText(m.NS, id, o))
	}
	line("See", see)
	if m.Retired {
		if m.ReplacedBy != "" {
			blocks = append(blocks, "Retired. Use "+f.refText(m.NS, m.ReplacedBy, o)+" instead.")
		} else {
			blocks = append(blocks, "Retired.")
		}
	}
	if d := strings.TrimRight(m.E.Details, " \t\r\n"); d != "" {
		blocks = append(blocks, d)
	}
	line("Avoid", names(m.E.Avoid))
	return strings.Join(blocks, "\n\n") + "\n"
}

func cmdSession(g *globals, args []string, e *env) (int, error) {
	fs := newFlagSet("session", e)
	addCommon(fs, g)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(pos) != 1 || !slices.Contains([]string{"path", "show", "clear"}, pos[0]) {
		return 2, usageErr{"usage: ul session path|show|clear"}
	}
	sid, err := sessionID(g, e.getenv)
	if err != nil {
		return 2, usageErr{err.Error()}
	}
	if sid == "" {
		return 2, usageErr{"no session id: pass --session, or set $UL_SESSION or $FY_SESSION_ID"}
	}
	p := sessionPath(e.getenv, sid)
	switch pos[0] {
	case "path":
		fmt.Fprintln(e.stdout, p)
	case "show":
		data, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			return 2, err
		}
		e.stdout.Write(data)
	case "clear":
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return 2, err
		}
	}
	return 0, nil
}

type problemsOut struct {
	Schema   string    `json:"schema"`
	Root     string    `json:"root"`
	Problems []Problem `json:"problems"`
}

func (f *Final) withMDProblems(l *loaded) []Problem {
	probs := slices.Clone(f.Problems)
	for _, ns := range f.NS {
		if ns.Layer != layerRepo {
			continue
		}
		rel := path.Join(path.Dir(ns.File), mdName)
		if !l.scan.MD[rel] {
			continue
		}
		got, err := os.ReadFile(filepath.Join(l.root, filepath.FromSlash(rel)))
		if err != nil || string(got) != f.renderGen(ns) {
			probs = append(probs, Problem{Code: "UL301", File: rel, Line: 1, Message: "UL.md differs from what ul gen writes; run ul gen"})
		}
	}
	slices.SortStableFunc(probs, func(a, b Problem) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.Code, b.Code)
	})
	return probs
}

func cmdCheck(g *globals, args []string, e *env) (int, error) {
	fs := newFlagSet("check", e)
	addCommon(fs, g)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if err := noArgs(pos); err != nil {
		return 2, err
	}
	format, err := wantFormat(g, "text", "text", "json")
	if err != nil {
		return 2, err
	}
	l, err := load(g, e, false)
	if err != nil {
		return 2, err
	}
	probs := l.final(false).withMDProblems(l)
	if format == "json" {
		out := problemsOut{Schema: "ul/v1", Root: l.root, Problems: probs}
		if out.Problems == nil {
			out.Problems = []Problem{}
		}
		if err := writeJSON(e.stdout, out); err != nil {
			return 2, err
		}
	} else {
		for _, p := range probs {
			fmt.Fprintf(e.stdout, "%s:%d: %s %s\n", p.File, p.Line, p.Code, p.Message)
		}
	}
	if len(probs) > 0 {
		return 1, nil
	}
	return 0, nil
}

func cmdGen(g *globals, args []string, e *env) (int, error) {
	fs := newFlagSet("gen", e)
	addCommon(fs, g)
	check := fs.Bool("check", false, "exit 1 when a UL.md is missing or differs, and write nothing")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if err := noArgs(pos); err != nil {
		return 2, err
	}
	l, err := load(g, e, false)
	if err != nil {
		return 2, err
	}
	f := l.final(false)
	if !*check && len(f.Problems) > 0 {
		for _, p := range f.Problems {
			fmt.Fprintf(e.stderr, "%s:%d: %s %s\n", p.File, p.Line, p.Code, p.Message)
		}
		fmt.Fprintln(e.stderr, "ul gen: fix these problems first (ul check lists them)")
		return 1, nil
	}
	drift := 0
	for _, ns := range f.NS {
		if ns.Layer != layerRepo {
			continue
		}
		rel := path.Join(path.Dir(ns.File), mdName)
		want := f.renderGen(ns)
		got, rerr := os.ReadFile(filepath.Join(l.root, filepath.FromSlash(rel)))
		same := rerr == nil && bytes.Equal(got, []byte(want))
		switch {
		case same:
		case *check:
			drift++
			msg := "UL.md differs from what ul gen writes; run ul gen"
			if rerr != nil {
				msg = "UL.md is missing; run ul gen"
			}
			fmt.Fprintf(e.stdout, "%s:1: UL301 %s\n", rel, msg)
		default:
			if err := os.WriteFile(filepath.Join(l.root, filepath.FromSlash(rel)), []byte(want), 0o644); err != nil {
				return 2, err
			}
			fmt.Fprintf(e.stdout, "wrote %s\n", rel)
		}
	}
	if drift > 0 {
		return 1, nil
	}
	return 0, nil
}

// cmdAdd adds words to the session layer, after checking the result against the rules.
func cmdAdd(g *globals, args []string, e *env) (int, error) {
	fs := newFlagSet("add", e)
	addCommon(fs, g)
	word := fs.String("word", "", "the word")
	desc := fs.String("desc", "", "one line: what it means")
	ns := fs.String("namespace", "", "namespace (default: session)")
	details := fs.String("details", "", "Markdown details, or - to read stdin")
	file := fs.String("file", "", "a UL.yaml-shaped fragment (or - for stdin)")
	var aliases, see, avoid multi
	fs.Var(&aliases, "alias", "another name for the word (repeat)")
	fs.Var(&see, "see", "a related word or id (repeat)")
	fs.Var(&avoid, "avoid", "a word not to use for this thing (repeat)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if err := noArgs(pos); err != nil {
		return 2, err
	}
	sid, err := sessionID(g, e.getenv)
	if err != nil {
		return 2, usageErr{err.Error()}
	}
	if sid == "" {
		return 2, usageErr{"no session id: pass --session, or set $UL_SESSION or $FY_SESSION_ID (--no-session turns the layer off)"}
	}
	if (*file == "") == (*word == "") {
		return 2, usageErr{"give either --word (with --desc) or --file"}
	}
	defNS := *ns
	if defNS == "" {
		defNS = sessionNamespace
	}
	l, err := load(g, e, true)
	if err != nil {
		return 2, err
	}
	if len(l.sessProbs) > 0 {
		for _, p := range l.sessProbs {
			fmt.Fprintf(e.stderr, "%s:%d: %s %s\n", p.File, p.Line, p.Code, p.Message)
		}
		return 2, fmt.Errorf("the session file %s is not valid; fix it or run ul session clear", l.spath)
	}
	repoFinal := buildFinal(l.root, nil, l.repo, nil, "", l.probs)
	premiseOf := func(ns string) string {
		for _, n := range repoFinal.NS {
			if n.Namespace == ns {
				return n.Premise
			}
		}
		return ""
	}

	var add []*Doc
	if *file != "" {
		var data []byte
		if *file == "-" {
			data, err = io.ReadAll(e.stdin)
		} else {
			p := *file
			if !filepath.IsAbs(p) {
				p = filepath.Join(e.cwd, p)
			}
			data, err = os.ReadFile(p)
		}
		if err != nil {
			return 2, err
		}
		var probs []Problem
		add, probs = parseDocs(*file, data, parseOpts{fragment: true, defNS: defNS})
		if len(probs) > 0 {
			return refuse(e, probs), nil
		}
	} else {
		if *desc == "" {
			return 2, usageErr{"--desc is required with --word"}
		}
		text := *details
		if text == "-" {
			b, err := io.ReadAll(e.stdin)
			if err != nil {
				return 2, err
			}
			text = string(b)
		}
		ent := &Entry{Word: *word, Desc: *desc, Details: text, Status: statusActive}
		for _, a := range aliases {
			ent.Aliases = append(ent.Aliases, Named{a, 0})
		}
		for _, a := range see {
			ent.See = append(ent.See, Named{a, 0})
		}
		for _, a := range avoid {
			ent.Avoid = append(ent.Avoid, Named{a, 0})
		}
		add = []*Doc{{Namespace: defNS, Entries: []*Entry{ent}}}
	}

	// Merge into the session layer as it is now.
	docs := slices.Clone(l.sess)
	for i, d := range docs {
		c := *d
		c.Entries = slices.Clone(d.Entries)
		docs[i] = &c
	}
	var ids []string
	for _, nd := range add {
		prem := premiseOf(nd.Namespace)
		if prem == "" {
			prem = nd.Premise
		}
		if prem == "" {
			prem = "Words coined in this session."
		}
		di := slices.IndexFunc(docs, func(d *Doc) bool { return d.Namespace == nd.Namespace })
		if di < 0 {
			docs = append(docs, &Doc{Namespace: nd.Namespace, Premise: prem})
			di = len(docs) - 1
		}
		for _, ent := range nd.Entries {
			if ei := slices.IndexFunc(docs[di].Entries, func(x *Entry) bool { return x.Word == ent.Word }); ei >= 0 {
				docs[di].Entries[ei] = ent
				ids = append(ids, "updated "+makeID(nd.Namespace, ent.Word))
			} else {
				docs[di].Entries = append(docs[di].Entries, ent)
				ids = append(ids, "added "+makeID(nd.Namespace, ent.Word))
			}
		}
	}
	data, err := marshalDocs(docs)
	if err != nil {
		return 2, err
	}
	// Check the merged layer with every rule; refuse what the session did not already break.
	sd, sp := parseDocs(l.spath, data, parseOpts{session: true})
	before := l.final(true).Problems
	after := buildFinal(l.root, &sid, l.repo, sd, l.spath, append(slices.Clone(l.probs), sp...)).Problems
	known := map[string]bool{}
	for _, p := range before {
		known[p.Code+"|"+p.File+"|"+p.Message] = true
	}
	var fresh []Problem
	for _, p := range after {
		if !known[p.Code+"|"+p.File+"|"+p.Message] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) > 0 {
		return refuse(e, fresh), nil
	}
	if err := writeAtomic(l.spath, data); err != nil {
		return 2, err
	}
	for _, s := range ids {
		fmt.Fprintln(e.stdout, s)
	}
	return 0, nil
}

func refuse(e *env, probs []Problem) int {
	for _, p := range probs {
		fmt.Fprintf(e.stderr, "refused: %s %s\n", p.Code, p.Message)
	}
	return 1
}
