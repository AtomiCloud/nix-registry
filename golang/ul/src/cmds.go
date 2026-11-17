package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// loaded is everything read from disk for one run.
type loaded struct {
	root  string
	scan  *scanResult
	repo  []*Doc
	probs []Problem
}

func load(g *globals, e *env) (*loaded, error) {
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
			// One unreadable or dangling UL.yaml is that file's problem, not the repo's:
			// every other UL still loads, and ul check reports it.
			l.probs = append(l.probs, Problem{Code: "UL101", File: rel, Line: 1, Message: "cannot read the file: " + readErrText(err)})
			continue
		}
		docs, probs := parseDocs(rel, data)
		l.repo = append(l.repo, docs...)
		l.probs = append(l.probs, probs...)
	}
	return l, nil
}

// readErrText drops the absolute path from a read error so the message stays repo-relative.
func readErrText(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

func (l *loaded) final() *Final {
	return buildFinal(l.root, l.repo, l.probs)
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
	l, err := load(g, e)
	if err != nil {
		return 2, err
	}
	f := l.final()
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
	l, err := load(g, e)
	if err != nil {
		return 2, err
	}
	f := l.final()
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
	fmt.Fprintf(&b, "%s  (%s)\n", m.E.Word, strings.Join(m.NS.Breadcrumb, " > "))
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

type problemsOut struct {
	Schema   string    `json:"schema"`
	Root     string    `json:"root"`
	Problems []Problem `json:"problems"`
}

func (f *Final) withMDProblems(l *loaded) []Problem {
	probs := slices.Clone(f.Problems)
	for _, ns := range f.NS {
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
	l, err := load(g, e)
	if err != nil {
		return 2, err
	}
	probs := l.final().withMDProblems(l)
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
	l, err := load(g, e)
	if err != nil {
		return 2, err
	}
	f := l.final()
	if !*check && len(f.Problems) > 0 {
		for _, p := range f.Problems {
			fmt.Fprintf(e.stderr, "%s:%d: %s %s\n", p.File, p.Line, p.Code, p.Message)
		}
		fmt.Fprintln(e.stderr, "ul gen: fix these problems first (ul check lists them)")
		return 1, nil
	}
	drift := 0
	for _, ns := range f.NS {
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
