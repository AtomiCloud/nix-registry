// Command problemgen lints problem definitions, checks breaking changes, and generates
// typed problem code (Go, TypeScript, C#, Dart) plus the Problem record from them.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const toolVersion = "0.1.0"

// Lang is one code target. Each gen_<lang>.go registers itself in init().
type Lang struct {
	Name string
	// File returns the configured output path for this module ("" when not configured).
	File func(o Outputs) string
	// Generate renders the file content.
	Generate func(m *Module, cfg *Config, o Outputs, source string) string
}

var langs = map[string]*Lang{}

func registerLang(l *Lang) { langs[l.Name] = l }

func init() {
	registerLang(&Lang{
		Name: "go",
		File: func(o Outputs) string {
			if o.Go == nil {
				return ""
			}
			return o.Go.File
		},
		Generate: func(m *Module, cfg *Config, o Outputs, src string) string { return GenerateGo(m, cfg, o.Go, src) },
	})
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "lint":
		code = cmdLint(os.Args[2:])
	case "diff":
		code = cmdDiff(os.Args[2:])
	case "generate":
		code = cmdGenerate(os.Args[2:], false)
	case "verify":
		code = cmdGenerate(os.Args[2:], true)
	case "schema":
		os.Stdout.Write(definitionSchemaJSON)
	case "rules":
		code = cmdRules()
	case "conformance":
		code = cmdConformance(os.Args[2:])
	case "--version", "version":
		fmt.Println("problemgen " + toolVersion)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "problemgen: unknown command %q\n", os.Args[1])
		usage()
		code = 2
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprint(os.Stderr, `problemgen `+toolVersion+` — definition-first RFC 9457 problems

  problemgen lint <problems.yaml>...          structural (JSON Schema) + semantic rules
  problemgen lint --library <library.yaml>... lint a ProblemLibrary on its own
  problemgen diff --old <file> <new.yaml>     breaking changes within one (module, version)
  problemgen diff --git <ref> <new.yaml>      same, old file read from git (git show <ref>:<path>)
  problemgen generate [-c problemgen.yaml]    write every configured output
  problemgen verify   [-c problemgen.yaml]    fail when any output differs from what generate writes
  problemgen schema                           print the JSON Schema of problems.yaml
  problemgen rules                            list every lint and diff rule
  problemgen conformance ...                  conformance-suite helpers (oracle, harness emitters)
`)
}

func printDiags(ds []Diag) {
	for _, d := range ds {
		fmt.Fprintln(os.Stderr, d.String())
	}
}

func cmdLint(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	lib := fs.Bool("library", false, "lint ProblemLibrary files")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "lint: no files")
		return 2
	}
	bad := 0
	for _, f := range fs.Args() {
		var ds []Diag
		if *lib {
			d, ld := LoadDef(f)
			ds = ld
			if d != nil {
				if d.Kind != "ProblemLibrary" {
					ds = append(ds, Diag{"P004", Pos{File: f}, "expected kind ProblemLibrary"})
				} else {
					// Lint a library as if imported by an otherwise empty set.
					m := &Module{Def: &Def{Kind: "ProblemLibrary", File: f, Module: "lib", Version: "v1"}, Types: d.Types, Problems: d.Problems}
					ds = append(ds, Lint(m)...)
				}
			}
		} else {
			_, ds = LoadModule(f)
		}
		if len(ds) > 0 {
			printDiags(ds)
			bad++
		} else {
			fmt.Printf("%s: ok\n", f)
		}
	}
	if bad > 0 {
		return 1
	}
	return 0
}

func cmdDiff(args []string) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	oldPath := fs.String("old", "", "previous version of the definition file")
	gitRef := fs.String("git", "", "git ref holding the previous version (same path)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "diff: want exactly one new file and --old or --git")
		return 2
	}
	newPath := fs.Arg(0)
	cur, ds := LoadModule(newPath)
	if len(ds) > 0 {
		printDiags(ds)
		return 1
	}
	var old *Module
	switch {
	case *oldPath != "":
		old, ds = LoadModule(*oldPath)
	case *gitRef != "":
		snap, err := openGitSnapshot(newPath, *gitRef)
		if err != nil {
			fmt.Fprintln(os.Stderr, "diff: "+err.Error())
			return 2
		}
		exists, err := snap.exists(snap.top)
		if err != nil {
			fmt.Fprintln(os.Stderr, "diff: "+err.Error())
			return 2
		}
		if !exists {
			fmt.Printf("%s: no previous version at %s (%s; new file): ok\n", newPath, *gitRef, snap.commit[:12])
			return 0
		}
		// The old definition AND every library it imports are read from the same commit.
		readFile = snap.read
		old, ds = LoadModule(snap.top)
		readFile = os.ReadFile
	default:
		fmt.Fprintln(os.Stderr, "diff: --old or --git is required")
		return 2
	}
	if len(ds) > 0 {
		fmt.Fprintln(os.Stderr, "diff: the previous version does not lint:")
		printDiags(ds)
		return 1
	}
	r := Diff(old, cur)
	for _, a := range r.Allowed {
		fmt.Println("allowed: " + a)
	}
	if len(r.Breaking) > 0 {
		printDiags(r.Breaking)
		fmt.Fprintf(os.Stderr, "%d breaking change(s): bump `version` (new file) instead\n", len(r.Breaking))
		return 1
	}
	fmt.Printf("%s: no breaking changes\n", newPath)
	return 0
}

// gitSnapshot serves files from one commit. Every failure other than "this path does not
// exist in that commit" is an error: diff never mistakes a broken ref for a new file.
type gitSnapshot struct {
	root   string // repository top level (symlinks resolved)
	commit string // the ref, resolved once to a commit id
	top    string // absolute path of the definition file
}

func openGitSnapshot(newPath, ref string) (*gitSnapshot, error) {
	abs, err := filepath.Abs(newPath)
	if err != nil {
		return nil, err
	}
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
		return nil, err
	}
	out, err := gitOut(filepath.Dir(abs), "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not in a git repository: %v", err)
	}
	root, err := filepath.EvalSymlinks(strings.TrimSpace(out))
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(ref, "-") {
		return nil, fmt.Errorf("invalid git ref %q", ref)
	}
	c, err := gitOut(root, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("git ref %q does not name a commit here (shallow clone or unfetched base? fetch it first): %v", ref, err)
	}
	return &gitSnapshot{root: root, commit: strings.TrimSpace(c), top: abs}, nil
}

func (g *gitSnapshot) rel(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	r, err := filepath.Rel(g.root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the repository %s", path, g.root)
	}
	return filepath.ToSlash(r), nil
}

// exists reports whether path is a file in the commit; a git failure is an error.
func (g *gitSnapshot) exists(path string) (bool, error) {
	r, err := g.rel(path)
	if err != nil {
		return false, err
	}
	out, err := gitOut(g.root, "ls-tree", "--name-only", "-z", g.commit, "--", r)
	if err != nil {
		return false, fmt.Errorf("git ls-tree %s %s: %v", g.commit[:12], r, err)
	}
	return strings.TrimSuffix(out, "\x00") == r, nil
}

func (g *gitSnapshot) read(path string) ([]byte, error) {
	ok, err := g.exists(path)
	if err != nil {
		return nil, err
	}
	r, _ := g.rel(path)
	if !ok {
		return nil, fmt.Errorf("%s does not exist at %s", r, g.commit[:12])
	}
	out, err := gitOut(g.root, "cat-file", "blob", g.commit+":"+r)
	if err != nil {
		return nil, fmt.Errorf("git cat-file %s:%s: %v", g.commit[:12], r, err)
	}
	return []byte(out), nil
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%v: %s", err, msg)
		}
		return "", err
	}
	return string(out), nil
}

type plannedFile struct {
	path    string
	content string
	def     string // absolute definition path the file is generated from ("" for the manifest)
}

func planOutputs(cfg *Config) ([]plannedFile, []Diag) {
	var out []plannedFile
	var all []Diag
	for _, mc := range cfg.Modules {
		defPath := cfg.Path(mc.Definition)
		m, ds := LoadModule(defPath)
		if len(ds) > 0 {
			all = append(all, ds...)
			continue
		}
		def := absClean(defPath)
		// The header's source is relative to the generated file, so it names exactly one
		// definition wherever the config lives (ownership checks rely on it).
		src := func(out string) string {
			r, err := filepath.Rel(filepath.Dir(absClean(out)), def)
			if err != nil {
				return filepath.ToSlash(mc.Definition)
			}
			return filepath.ToSlash(r)
		}
		names := make([]string, 0, len(langs))
		for n := range langs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			l := langs[n]
			if f := l.File(mc.Outputs); f != "" {
				p := cfg.Path(f)
				out = append(out, plannedFile{p, l.Generate(m, cfg, mc.Outputs, src(p)), def})
			}
		}
		if mc.Outputs.Record != nil {
			p := cfg.Path(mc.Outputs.Record.File)
			out = append(out, plannedFile{p, RenderRecordYAML(m, cfg, "", src(p)), def})
		}
		if mc.Outputs.Catalog != nil {
			out = append(out, plannedFile{cfg.Path(mc.Outputs.Catalog.File), RenderCatalogJSON(m, cfg), def})
		}
	}
	return out, all
}

func cmdGenerate(args []string, verify bool) int {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	cfgPath := fs.String("c", "problemgen.yaml", "generator config")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	files, ds := planOutputs(cfg)
	if len(ds) > 0 {
		printDiags(ds)
		return 1
	}
	// The manifest (<config>.outputs) lists, with a sha256, every file this config generated.
	// It is how generate/verify know which earlier files are theirs; deleting one is further
	// guarded by safeToRemove. Stale entries that cannot be removed stay listed, so verify
	// keeps failing until a person deals with them.
	mpath := manifestPath(*cfgPath)
	previous, err := readManifest(mpath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	seen := map[string]bool{}
	defs := map[string]bool{}
	var writeDirs []string
	for _, f := range files {
		if seen[f.path] {
			fmt.Fprintf(os.Stderr, "two outputs write %s\n", f.path)
			return 2
		}
		seen[f.path] = true
		defs[f.def] = true
		writeDirs = append(writeDirs, filepath.Dir(f.path))
	}
	drift := 0
	var kept []manifestEntry
	for _, e := range previous {
		p := cfg.Path(filepath.FromSlash(e.rel))
		if !filepath.IsAbs(filepath.FromSlash(e.rel)) && seen[p] {
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			continue // already gone: forget it
		}
		if verify {
			fmt.Fprintf(os.Stderr, "stale generated file: %s (listed in %s, no longer an output; run problemgen generate)\n", p, mpath)
			kept = append(kept, e)
			drift++
			continue
		}
		if why := safeToRemove(cfg, e, writeDirs, defs); why != "" {
			fmt.Fprintf(os.Stderr, "not removing %s: %s; delete it by hand (it stays listed in %s until then)\n", p, why, mpath)
			kept = append(kept, e)
			drift++
			continue
		}
		if err := os.Remove(p); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Println("removed stale " + p)
	}
	files = append(files, plannedFile{mpath, renderManifest(cfg, files, kept), ""})
	for _, f := range files {
		if verify {
			cur, err := os.ReadFile(f.path)
			if err != nil || !bytes.Equal(cur, []byte(f.content)) {
				fmt.Fprintf(os.Stderr, "out of date: %s (run problemgen generate)\n", f.path)
				drift++
			}
			continue
		}
		if owner := foreignOwner(f); owner != "" {
			fmt.Fprintf(os.Stderr, "refusing to overwrite %s: it was generated from %s, not %s\n", f.path, owner, f.def)
			return 2
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if err := os.WriteFile(f.path, []byte(f.content), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Println("wrote " + f.path)
	}
	if drift > 0 {
		return 1
	}
	if verify {
		fmt.Printf("%d generated file(s) up to date\n", len(files))
	}
	return 0
}

func cmdRules() int {
	var keys []string
	for k := range LintRules {
		keys = append(keys, k)
	}
	for k := range DiffRules {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("P0xx  structural: YAML shape and the JSON Schema (problemgen schema)")
	for _, k := range keys {
		if v, ok := LintRules[k]; ok {
			fmt.Printf("%s  lint: %s\n", k, v)
		} else {
			fmt.Printf("%s  diff: %s\n", k, DiffRules[k])
		}
	}
	return 0
}

// manifestPath is the output manifest of a config: <config file>.outputs, next to it.
func manifestPath(cfgPath string) string { return cfgPath + ".outputs" }

type manifestEntry struct {
	sha string // sha256 of the content this config wrote
	rel string // path relative to the config, slash-separated
}

// renderManifest lists the planned outputs (and stale entries kept), config-relative, sorted,
// each with the sha256 of its content.
func renderManifest(cfg *Config, files []plannedFile, kept []manifestEntry) string {
	var es []manifestEntry
	for _, f := range files {
		r, err := filepath.Rel(absClean(cfg.Path(".")), absClean(f.path))
		if err != nil {
			r = f.path
		}
		sum := sha256.Sum256([]byte(f.content))
		es = append(es, manifestEntry{hex.EncodeToString(sum[:]), filepath.ToSlash(r)})
	}
	es = append(es, kept...)
	sort.Slice(es, func(i, j int) bool { return es[i].rel < es[j].rel })
	var b strings.Builder
	b.WriteString("# Code generated by problemgen " + toolVersion + ". DO NOT EDIT.\n")
	b.WriteString("# Files generated from this config: sha256 of the content written, path relative to the\n")
	b.WriteString("# config. A listed file that is no longer an output is reported by verify; generate removes\n")
	b.WriteString("# it only if it is unchanged, contained, not behind a symlink and generated from one of this\n")
	b.WriteString("# config's definitions (catalogs: never; delete them by hand).\n")
	for _, e := range es {
		b.WriteString(e.sha + "  " + e.rel + "\n")
	}
	return b.String()
}

// readManifest returns the entries of an existing manifest (none if absent).
func readManifest(path string) ([]manifestEntry, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []manifestEntry
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sha, rel, ok := strings.Cut(line, "  ")
		if !ok { // a hand-written line without a hash: listed, never removable
			sha, rel = "", line
		}
		out = append(out, manifestEntry{sha, strings.TrimSpace(rel)})
	}
	return out, nil
}

// safeToRemove returns "" when generate may delete a stale manifest entry, else the reason.
// All must hold: (1) the entry is a relative path that stays inside the config directory or
// a directory this config writes to now; (2) no component of it is a symlink (the real path
// equals the lexical one); (3) the content is exactly what this config wrote (sha256);
// (4) it carries the problemgen header whose source (relative to the file) is one of this
// config's definitions. Catalogs have no header, so ownership cannot be proven: never removed.
func safeToRemove(cfg *Config, e manifestEntry, writeDirs []string, defs map[string]bool) string {
	rel := filepath.FromSlash(e.rel)
	if rel == "" || filepath.IsAbs(rel) {
		return "the manifest entry is not a relative path"
	}
	p := absClean(cfg.Path(rel))
	inside := func(root string) bool {
		r, err := filepath.Rel(absClean(root), p)
		return err == nil && r != "." && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
	}
	contained := inside(cfg.Path("."))
	for _, d := range writeDirs {
		contained = contained || inside(d)
	}
	if !contained {
		return "it is outside the config directory and the directories this config writes to"
	}
	realCfg, err := filepath.EvalSymlinks(absClean(cfg.Path(".")))
	if err != nil {
		return err.Error()
	}
	r, _ := filepath.Rel(absClean(cfg.Path(".")), p)
	realP, err := filepath.EvalSymlinks(p)
	if err != nil || realP != filepath.Join(realCfg, r) {
		return "its path goes through a symlink"
	}
	if filepath.Ext(p) == ".json" {
		return "a catalog carries no ownership header"
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err.Error()
	}
	sum := sha256.Sum256(b)
	if e.sha == "" || hex.EncodeToString(sum[:]) != e.sha {
		return "its content is not what this config generated (edited by hand, or the manifest was)"
	}
	src := generatedSource(string(b[:min(len(b), 1024)]))
	if src == "" {
		return "it has no problemgen header"
	}
	if !defs[absClean(filepath.Join(filepath.Dir(p), filepath.FromSlash(src)))] {
		return "its header names a definition this config does not list"
	}
	return ""
}

// foreignOwner returns the definition an existing generated file names in its header when
// that definition exists and is not the one f is generated from: another config's output.
func foreignOwner(f plannedFile) string {
	if f.def == "" {
		return ""
	}
	b, err := os.ReadFile(f.path)
	if err != nil {
		return ""
	}
	src := generatedSource(string(b[:min(len(b), 1024)]))
	if src == "" {
		return ""
	}
	owner := absClean(filepath.Join(filepath.Dir(f.path), filepath.FromSlash(src)))
	if owner == f.def {
		return ""
	}
	if st, err := os.Stat(owner); err != nil || st.IsDir() {
		return "" // stale or pre-0.1 header (config-relative source): nothing to protect
	}
	return owner
}

// generatedSource returns the definition path named in a problemgen header, or "".
func generatedSource(head string) string {
	if !strings.Contains(head, "Code generated by problemgen") {
		return ""
	}
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "/#"))
		if rest, ok := strings.CutPrefix(line, "source: "); ok {
			if i := strings.Index(rest, " sha256:"); i > 0 {
				return rest[:i]
			}
		}
	}
	return ""
}

func absClean(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return a
}
