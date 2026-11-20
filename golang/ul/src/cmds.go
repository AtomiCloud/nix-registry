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
	opts  options
}

func load(g *globals, e *env) (*loaded, error) {
	opts, err := g.options(e.getenv)
	if err != nil {
		return nil, err
	}
	root, err := resolveRoot(g.root, e.cwd)
	if err != nil {
		if g.root != "" {
			return nil, usageErr{"--root: " + err.Error()}
		}
		return nil, err
	}
	l := &loaded{root: root, opts: opts}
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
		docs, probs := parseDocs(rel, data, opts)
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
			if _, err := io.WriteString(e.stdout, strings.Join(parts, "\n\n")+"\n"); err != nil {
				return 2, err
			}
		}
	default:
		for i, m := range matches {
			if i > 0 {
				if _, err := fmt.Fprintln(e.stdout); err != nil {
					return 2, err
				}
			}
			if _, err := io.WriteString(e.stdout, f.renderLookupText(m)); err != nil {
				return 2, err
			}
		}
	}
	if len(matches) == 0 {
		if format != "json" {
			if _, err := fmt.Fprintf(e.stderr, "ul: no match for %q\n", pos[0]); err != nil {
				return 2, err
			}
			if len(near) > 0 {
				var ns []string
				for _, n := range near {
					if !slices.Contains(ns, n.E.Word) {
						ns = append(ns, n.E.Word)
					}
				}
				if _, err := fmt.Fprintf(e.stderr, "near: %s\n", strings.Join(ns, ", ")); err != nil {
					return 2, err
				}
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
	Flags    []Problem `json:"flags,omitempty"` // rules that warn and pass
}

// mdRefusal says why ul must not read or write the UL.md at p, or "" when it may: a regular
// file or a missing one is fine; a symlink (its target can be anywhere) or anything else is not.
func mdRefusal(p string) string {
	st, err := os.Lstat(p)
	switch {
	case err == nil && st.Mode().IsRegular():
		return ""
	case err == nil && st.Mode()&fs.ModeSymlink != 0:
		return "UL.md is a symlink; replace it with a regular file"
	case err == nil:
		return "UL.md is not a regular file"
	case errors.Is(err, fs.ErrNotExist):
		return ""
	}
	return "UL.md cannot be inspected: " + readErrText(err)
}

func (f *Final) withMDProblems(l *loaded) []Problem {
	probs := slices.Clone(f.Problems)
	for _, ns := range f.NS {
		rel := path.Join(path.Dir(ns.File), mdName)
		if !l.scan.MD[rel] {
			continue
		}
		full := filepath.Join(l.root, filepath.FromSlash(rel))
		if why := mdRefusal(full); why != "" {
			probs = append(probs, Problem{Code: "UL301", File: rel, Line: 1, Message: why})
			continue
		}
		got, err := os.ReadFile(full)
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
	f := l.final()
	probs := f.withMDProblems(l)
	dp, dflags, err := l.docProblems(f)
	if err != nil {
		return 2, err
	}
	probs = sortProblems(append(probs, dp...))
	flags := sortProblems(append(slices.Clone(f.Flags), dflags...))
	if format == "json" {
		out := problemsOut{Schema: "ul/v1", Root: l.root, Problems: probs, Flags: flags}
		if out.Problems == nil {
			out.Problems = []Problem{}
		}
		if err := writeJSON(e.stdout, out); err != nil {
			return 2, err
		}
	} else {
		for _, p := range probs {
			if _, err := fmt.Fprintf(e.stdout, "%s:%d: %s %s\n", p.File, p.Line, p.Code, p.Message); err != nil {
				return 2, err
			}
		}
		// A flag warns and passes. It goes to stderr, so pre-commit (verbose) shows it and a
		// script reading stdout sees only what refuses.
		for _, p := range flags {
			if _, err := fmt.Fprintf(e.stderr, "%s:%d: %s flag: %s\n", p.File, p.Line, p.Code, p.Message); err != nil {
				return 2, err
			}
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
			if _, err := fmt.Fprintf(e.stderr, "%s:%d: %s %s\n", p.File, p.Line, p.Code, p.Message); err != nil {
				return 2, err
			}
		}
		if _, err := fmt.Fprintln(e.stderr, "ul gen: fix these problems first (ul check lists them)"); err != nil {
			return 2, err
		}
		return 1, nil
	}
	drift := 0
	for _, ns := range f.NS {
		rel := path.Join(path.Dir(ns.File), mdName)
		full := filepath.Join(l.root, filepath.FromSlash(rel))
		if why := mdRefusal(full); why != "" {
			// Never read or write through a symlinked UL.md: its target may be anywhere.
			drift++
			if *check {
				if _, err := fmt.Fprintf(e.stdout, "%s:1: UL301 %s\n", rel, why); err != nil {
					return 2, err
				}
			} else if _, err := fmt.Fprintf(e.stderr, "ul gen: refusing %s: %s\n", rel, why); err != nil {
				return 2, err
			}
			continue
		}
		want := f.renderGen(ns)
		got, rerr := os.ReadFile(full)
		same := rerr == nil && bytes.Equal(got, []byte(want))
		switch {
		case same:
		case *check:
			drift++
			msg := "UL.md differs from what ul gen writes; run ul gen"
			if rerr != nil {
				msg = "UL.md is missing; run ul gen"
			}
			if _, err := fmt.Fprintf(e.stdout, "%s:1: UL301 %s\n", rel, msg); err != nil {
				return 2, err
			}
		default:
			if err := os.WriteFile(full, []byte(want), 0o644); err != nil {
				return 2, err
			}
			if _, err := fmt.Fprintf(e.stdout, "wrote %s\n", rel); err != nil {
				return 2, err
			}
		}
	}
	if drift > 0 {
		return 1, nil
	}
	return 0, nil
}

func sortProblems(probs []Problem) []Problem {
	slices.SortStableFunc(probs, func(a, b Problem) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		if c := strings.Compare(a.Code, b.Code); c != 0 {
			return c
		}
		return strings.Compare(a.Message, b.Message)
	})
	return slices.Compact(probs)
}
