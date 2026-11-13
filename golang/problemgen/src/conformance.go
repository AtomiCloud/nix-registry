package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Vectors is the shared conformance input (conformance/vectors.json).
type Vectors struct {
	Landscape string
	Cases     []VCase
	Foreign   []VForeign
}

// VCase builds one problem with typed inputs.
type VCase struct {
	Name     string
	Problem  string
	Data     *JV
	Instance string
	Golden   string // optional hand-written expected wire (checks the oracle itself)
	GoldLog  string
}

// VForeign is a received body every language must classify (parse-only).
type VForeign struct {
	Name   string
	Wire   string
	Expect string // a problem id, "unknown", or "not-problem"
}

// LoadVectors parses vectors.json with the order/surrogate-preserving reader.
func LoadVectors(path string) (*Vectors, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	root, err := ParseJV(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	v := &Vectors{}
	if l, ok := root.Get("landscape"); ok {
		v.Landscape = l.GoString()
	}
	if !ValidLandscape(v.Landscape) {
		return nil, fmt.Errorf("%s: landscape %q is not a DNS label (a-z, 0-9, -; at most 63 characters)", path, v.Landscape)
	}
	cs, _ := root.Get("cases")
	if cs != nil {
		for _, c := range cs.Arr {
			vc := VCase{}
			if x, ok := c.Get("name"); ok {
				vc.Name = x.GoString()
			}
			if x, ok := c.Get("problem"); ok {
				vc.Problem = x.GoString()
			}
			if x, ok := c.Get("data"); ok {
				vc.Data = x
			}
			if x, ok := c.Get("instance"); ok {
				vc.Instance = x.GoString()
			}
			if x, ok := c.Get("golden"); ok {
				vc.Golden = x.GoString()
			}
			if x, ok := c.Get("goldenLog"); ok {
				vc.GoldLog = x.GoString()
			}
			v.Cases = append(v.Cases, vc)
		}
	}
	fs, _ := root.Get("foreign")
	if fs != nil {
		for _, c := range fs.Arr {
			vf := VForeign{}
			if x, ok := c.Get("name"); ok {
				vf.Name = x.GoString()
			}
			if x, ok := c.Get("wire"); ok {
				vf.Wire = x.GoString()
			}
			if x, ok := c.Get("expect"); ok {
				vf.Expect = x.GoString()
			}
			v.Foreign = append(v.Foreign, vf)
		}
	}
	return v, nil
}

// Expected is the oracle's answer for one case.
type Expected struct {
	Name    string `json:"name"`
	Problem string `json:"problem"`
	OK      bool   `json:"ok"`
	Wire    string `json:"wire,omitempty"`
	Log     string `json:"log,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Oracle renders every case.
func Oracle(m *Module, cfg *Config, v *Vectors) ([]Expected, error) {
	var out []Expected
	for _, c := range v.Cases {
		var p *Problem
		for _, q := range m.Problems {
			if q.ID == c.Problem {
				p = q
			}
		}
		if p == nil {
			return nil, fmt.Errorf("case %s: no problem %q", c.Name, c.Problem)
		}
		r, err := RenderVector(m, cfg, v.Landscape, p, c.Data, c.Instance)
		e := Expected{Name: c.Name, Problem: c.Problem}
		if err != nil {
			e.Error = err.Error()
		} else {
			e.OK, e.Wire, e.Log = true, r.Wire, r.Log
			if c.Golden != "" && c.Golden != r.Wire {
				return nil, fmt.Errorf("case %s: oracle wire differs from the hand-written golden:\n oracle %s\n golden %s", c.Name, r.Wire, c.Golden)
			}
			if c.GoldLog != "" && c.GoldLog != r.Log {
				return nil, fmt.Errorf("case %s: oracle log differs from the hand-written golden:\n oracle %q\n golden %q", c.Name, r.Log, c.GoldLog)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// HarnessLine is one line a language harness prints.
type HarnessLine struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Wire    string `json:"wire"`
	Log     string `json:"log"`
	Error   string `json:"error"`
	Variant string `json:"variant"` // parse mode: problem id or "unknown" or "not-problem"
	Rewire  string `json:"rewire"`  // parse mode: re-serialised value (known variants)
}

func readLines(path string) (map[string]HarnessLine, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	out := map[string]HarnessLine{}
	var order []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var h HarnessLine
		if err := json.Unmarshal([]byte(line), &h); err != nil {
			return nil, nil, fmt.Errorf("%s: bad line %q: %w", path, line, err)
		}
		out[h.Name] = h
		order = append(order, h.Name)
	}
	return out, order, sc.Err()
}

// cmdConformance: problemgen conformance <oracle|check|emit> ...
func cmdConformance(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "conformance: oracle | emit | check-emit | check-parse | wire-file")
		return 2
	}
	switch args[0] {
	case "random-defs": // hidden: adversarial definitions for SPIKE/fuzz (fuzzdefs.go)
		return cmdRandomDefs(args[1:])
	case "batch-emit", "batch-wires", "batch-check-emit", "batch-check-parse":
		return cmdBatch(args[0], args[1:])
	}
	fs := flag.NewFlagSet("conformance", flag.ContinueOnError)
	cfgPath := fs.String("c", "", "generator config")
	def := fs.String("def", "", "definition file")
	vec := fs.String("vectors", "", "vectors.json")
	lang := fs.String("lang", "", "harness language")
	results := fs.String("results", "", "harness output (jsonl)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	m, ds := LoadModule(*def)
	if len(ds) > 0 {
		printDiags(ds)
		return 1
	}
	v, err := LoadVectors(*vec)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	switch args[0] {
	case "oracle":
		exp, err := Oracle(m, cfg, v)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		for _, e := range exp {
			_ = enc.Encode(e)
		}
		return 0
	case "emit":
		h, ok := harnesses[*lang]
		if !ok {
			fmt.Fprintf(os.Stderr, "no harness emitter for %q\n", *lang)
			return 2
		}
		var mc *ModuleConfig
		for i := range cfg.Modules {
			if cfg.Path(cfg.Modules[i].Definition) == *def {
				mc = &cfg.Modules[i]
			}
		}
		if mc == nil {
			fmt.Fprintln(os.Stderr, "definition not in config")
			return 2
		}
		os.Stdout.WriteString(h(m, cfg, mc.Outputs, v))
		return 0
	case "wire-file":
		// All oracle wires (and foreign bodies) as jsonl {name, wire} for parse mode.
		exp, err := Oracle(m, cfg, v)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		for _, e := range exp {
			if e.OK {
				_ = enc.Encode(map[string]string{"name": e.Name, "wire": e.Wire})
			}
		}
		for _, f := range v.Foreign {
			_ = enc.Encode(map[string]string{"name": f.Name, "wire": f.Wire})
		}
		return 0
	case "check-emit":
		exp, err := Oracle(m, cfg, v)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		got, _, err := readLines(*results)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fail := 0
		for _, e := range exp {
			g, ok := got[e.Name]
			switch {
			case !ok:
				fmt.Printf("FAIL %s: missing\n", e.Name)
				fail++
			case e.OK != g.OK:
				fmt.Printf("FAIL %s: oracle ok=%v (%s) harness ok=%v (%s)\n", e.Name, e.OK, e.Error, g.OK, g.Error)
				fail++
			case e.OK && e.Wire != g.Wire:
				fmt.Printf("FAIL %s wire:\n  want %s\n  got  %s\n", e.Name, e.Wire, g.Wire)
				fail++
			case e.OK && e.Log != g.Log:
				fmt.Printf("FAIL %s log:\n  want %q\n  got  %q\n", e.Name, e.Log, g.Log)
				fail++
			}
		}
		fmt.Printf("emit %s: %d/%d match\n", *results, len(exp)-fail, len(exp))
		if fail > 0 {
			return 1
		}
		return 0
	case "check-parse":
		exp, err := Oracle(m, cfg, v)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		got, _, err := readLines(*results)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fail, total := 0, 0
		for _, e := range exp {
			if !e.OK {
				continue
			}
			total++
			g, ok := got[e.Name]
			switch {
			case !ok:
				fmt.Printf("FAIL %s: missing\n", e.Name)
				fail++
			case g.Variant != e.Problem:
				fmt.Printf("FAIL %s: variant %q, want %q (%s)\n", e.Name, g.Variant, e.Problem, g.Error)
				fail++
			case g.Rewire != e.Wire:
				fmt.Printf("FAIL %s rewire:\n  want %s\n  got  %s\n", e.Name, e.Wire, g.Rewire)
				fail++
			}
		}
		for _, f := range v.Foreign {
			total++
			g, ok := got[f.Name]
			if !ok || g.Variant != f.Expect {
				fmt.Printf("FAIL %s: variant %q, want %q (%s)\n", f.Name, g.Variant, f.Expect, g.Error)
				fail++
			}
		}
		fmt.Printf("parse %s: %d/%d match\n", *results, total-fail, total)
		if fail > 0 {
			return 1
		}
		return 0
	}
	fmt.Fprintln(os.Stderr, "conformance: unknown subcommand")
	return 2
}

// harnesses maps a language to its conformance-harness emitter. The harness is a program
// that builds every case with the GENERATED typed constructors (literal values, so the
// compiler checks every argument) and prints one HarnessLine per case; in parse mode it
// parses received bodies with the generated Parse and re-serialises them.
var harnesses = map[string]func(m *Module, cfg *Config, o Outputs, v *Vectors) string{}

func sortedNames[T any](m map[string]T) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// ---- batch mode: many modules per compiled harness (SPIKE/fuzz) ----
//
// A batch is one problemgen.yaml listing several modules; module <key> is defs/<key>.problems.yaml
// with its vectors in defs/<key>.vectors.json. Case names are "<key>/<case>" everywhere, so one
// program per language emits every module's cases and parses every module's wires.

// BatchModule is one module of a batch.
type BatchModule struct {
	Key string
	M   *Module
	Out Outputs
	V   *Vectors
}

// LoadBatch loads every module of a batch config with its vectors.
func LoadBatch(cfgPath string) (*Config, []BatchModule, error) {
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	var out []BatchModule
	for _, mc := range cfg.Modules {
		def := cfg.Path(mc.Definition)
		m, ds := LoadModule(def)
		if len(ds) > 0 {
			var b strings.Builder
			for _, d := range ds {
				b.WriteString(d.String() + "\n")
			}
			return nil, nil, fmt.Errorf("%s does not lint:\n%s", def, b.String())
		}
		v, err := LoadVectors(strings.TrimSuffix(def, ".problems.yaml") + ".vectors.json")
		if err != nil {
			return nil, nil, err
		}
		out = append(out, BatchModule{Key: strings.TrimSuffix(filepath.Base(def), ".problems.yaml"), M: m, Out: mc.Outputs, V: v})
	}
	return cfg, out, nil
}

// batchHarnesses maps a language to its batch-harness emitter (harness_<lang>.go).
var batchHarnesses = map[string]func(cfg *Config, mods []BatchModule) string{}

func cmdBatch(sub string, args []string) int {
	fs := flag.NewFlagSet(sub, flag.ContinueOnError)
	cfgPath := fs.String("c", "", "batch generator config")
	lang := fs.String("lang", "", "harness language (batch-emit)")
	results := fs.String("results", "", "harness output (jsonl)")
	if err := fs.Parse(args); err != nil || *cfgPath == "" {
		return 2
	}
	cfg, mods, err := LoadBatch(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	oracle := func(bm BatchModule) ([]Expected, bool) {
		exp, err := Oracle(bm.M, cfg, bm.V)
		if err != nil {
			fmt.Printf("FAIL %s: oracle: %v\n", bm.Key, err)
			return nil, false
		}
		return exp, true
	}
	switch sub {
	case "batch-emit":
		h, ok := batchHarnesses[*lang]
		if !ok {
			fmt.Fprintf(os.Stderr, "no batch harness for %q\n", *lang)
			return 2
		}
		os.Stdout.WriteString(h(cfg, mods))
		return 0
	case "batch-wires":
		for _, bm := range mods {
			exp, ok := oracle(bm)
			if !ok {
				return 1
			}
			for _, e := range exp {
				if e.OK {
					_ = enc.Encode(map[string]string{"name": bm.Key + "/" + e.Name, "wire": e.Wire})
				}
			}
		}
		return 0
	}
	got, _, err := readLines(*results)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fail, total := 0, 0
	for _, bm := range mods {
		exp, ok := oracle(bm)
		if !ok {
			fail++
			continue
		}
		for _, e := range exp {
			name := bm.Key + "/" + e.Name
			g, ok := got[name]
			if sub == "batch-check-emit" {
				total++
				switch {
				case !ok:
					fmt.Printf("FAIL %s: missing\n", name)
					fail++
				case e.OK != g.OK:
					fmt.Printf("FAIL %s: oracle ok=%v (%s) harness ok=%v (%s)\n", name, e.OK, e.Error, g.OK, g.Error)
					fail++
				case e.OK && e.Wire != g.Wire:
					fmt.Printf("FAIL %s wire:\n  want %s\n  got  %s\n", name, e.Wire, g.Wire)
					fail++
				case e.OK && e.Log != g.Log:
					fmt.Printf("FAIL %s log:\n  want %q\n  got  %q\n", name, e.Log, g.Log)
					fail++
				}
				continue
			}
			if !e.OK {
				continue
			}
			total++
			switch {
			case !ok:
				fmt.Printf("FAIL %s: missing\n", name)
				fail++
			case g.Variant != e.Problem:
				fmt.Printf("FAIL %s: variant %q, want %q (%s)\n", name, g.Variant, e.Problem, g.Error)
				fail++
			case g.Rewire != e.Wire:
				fmt.Printf("FAIL %s rewire:\n  want %s\n  got  %s\n", name, e.Wire, g.Rewire)
				fail++
			}
		}
	}
	what := "emit"
	if sub == "batch-check-parse" {
		what = "parse"
	}
	fmt.Printf("%s %s: %d/%d match\n", what, *results, total-fail, total)
	if fail > 0 {
		return 1
	}
	return 0
}
