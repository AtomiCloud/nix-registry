// Command problemgen lints problem definitions, checks breaking changes, and generates
// typed problem code (Go, TypeScript, C#, Dart) plus the Problem record from them.
package main

import (
	"bytes"
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
		abs, _ := filepath.Abs(newPath)
		dir := filepath.Dir(abs)
		top, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, "diff: not in a git repository")
			return 2
		}
		rel, _ := filepath.Rel(strings.TrimSpace(string(top)), abs)
		src, err := exec.Command("git", "-C", dir, "show", *gitRef+":"+filepath.ToSlash(rel)).Output()
		if err != nil {
			fmt.Printf("%s: no previous version at %s (new file): ok\n", newPath, *gitRef)
			return 0
		}
		tmp := filepath.Join(dir, ".problemgen-old-"+filepath.Base(newPath))
		if err := os.WriteFile(tmp, src, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		defer os.Remove(tmp)
		old, ds = LoadModule(tmp)
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

type plannedFile struct {
	path    string
	content string
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
		src := filepath.ToSlash(mc.Definition)
		names := make([]string, 0, len(langs))
		for n := range langs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			l := langs[n]
			if f := l.File(mc.Outputs); f != "" {
				out = append(out, plannedFile{cfg.Path(f), l.Generate(m, cfg, mc.Outputs, src)})
			}
		}
		if mc.Outputs.Record != nil {
			out = append(out, plannedFile{cfg.Path(mc.Outputs.Record.File), RenderRecordYAML(m, cfg, "", src)})
		}
		if mc.Outputs.Catalog != nil {
			out = append(out, plannedFile{cfg.Path(mc.Outputs.Catalog.File), RenderCatalogJSON(m, cfg)})
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
	seen := map[string]bool{}
	drift := 0
	for _, f := range files {
		if seen[f.path] {
			fmt.Fprintf(os.Stderr, "two outputs write %s\n", f.path)
			return 2
		}
		seen[f.path] = true
		if verify {
			cur, err := os.ReadFile(f.path)
			if err != nil || !bytes.Equal(cur, []byte(f.content)) {
				fmt.Fprintf(os.Stderr, "out of date: %s (run problemgen generate)\n", f.path)
				drift++
			}
			continue
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
