package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

// Pos is a source position for diagnostics.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string { return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col) }

// Primitive field kinds. Named types (enum/object) use KindRef.
const (
	KString   = "string"
	KInteger  = "integer"
	KInt64    = "int64"
	KNumber   = "number"
	KBoolean  = "boolean"
	KDateTime = "date-time"
	KArray    = "array"
	KRef      = "ref"
)

// FieldType is the type of a data field or array item.
type FieldType struct {
	Kind   string     // one of the K* constants
	Format string     // string formats: uuid, date, uri
	Ref    string     // named type, when Kind == KRef
	Items  *FieldType // when Kind == KArray
}

// Field is one member of a problem's `data` or of a named object type.
type Field struct {
	Name        string
	Type        FieldType
	Optional    bool
	Private     bool
	Description string
	Example     *yaml.Node
	Deprecated  string
	Pos         Pos
}

// NamedType is an entry of `types`.
type NamedType struct {
	Name        string
	IsEnum      bool
	Values      []string
	Fields      []*Field
	Description string
	Pos         Pos
	Origin      string // file the type was defined in
}

// Problem is one entry of `problems`.
type Problem struct {
	ID          string
	Title       string
	Status      int
	Recoverable bool
	DetailSrc   string
	LogSrc      string // empty => same as detail
	Detail      *Template
	Log         *Template
	Description string
	RetryAfter  string
	Deprecated  string
	Data        []*Field
	Pos         Pos
	Origin      string
}

// Field looks up a data field by name.
func (p *Problem) Field(name string) *Field {
	for _, f := range p.Data {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// PublicData returns the fields that go on the wire and into the schema.
func (p *Problem) PublicData() []*Field {
	var out []*Field
	for _, f := range p.Data {
		if !f.Private {
			out = append(out, f)
		}
	}
	return out
}

// Import is one entry of `imports`.
type Import struct {
	From string
	Only []string
	Pos  Pos
}

// Def is one parsed definition file (before import resolution).
type Def struct {
	APIVersion string
	Kind       string
	Module     string
	Version    string
	Name       string
	Imports    []Import
	Types      []*NamedType
	Problems   []*Problem
	File       string
	SHA256     string
	Root       *yaml.Node
}

// Module is a ProblemSet with its imports expanded: the unit every generator consumes.
type Module struct {
	Def      *Def
	Types    []*NamedType // local + imported, in order
	Problems []*Problem   // imported first (in import order), then local
}

// TypeByName finds a named type.
func (m *Module) TypeByName(name string) *NamedType {
	for _, t := range m.Types {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Diag is one lint/diff finding.
type Diag struct {
	Code string
	Pos  Pos
	Msg  string
}

func (d Diag) String() string { return fmt.Sprintf("%s: %s %s", d.Pos, d.Code, d.Msg) }

type loadErr struct{ diags []Diag }

func (e *loadErr) Error() string {
	var b strings.Builder
	for _, d := range e.diags {
		b.WriteString(d.String())
		b.WriteByte('\n')
	}
	return b.String()
}

func readSource(path string) ([]byte, string, error) {
	if strings.HasPrefix(path, "builtin:") {
		name := strings.TrimPrefix(path, "builtin:")
		b, err := builtinFS.ReadFile("builtin/" + strings.ReplaceAll(name, "/", ".") + ".yaml")
		if err != nil {
			return nil, path, fmt.Errorf("unknown builtin %q", path)
		}
		return b, path, nil
	}
	b, err := readFile(path)
	return b, path, err
}

// readFile is the source of definition and library files. diff --git swaps it for a reader
// that serves every file (top-level and imports) from one git ref.
var readFile = os.ReadFile

// LoadDef parses one file: schema validation first, then the typed model.
func LoadDef(path string) (*Def, []Diag) {
	src, name, err := readSource(path)
	if err != nil {
		return nil, []Diag{{Code: "P000", Pos: Pos{File: path}, Msg: err.Error()}}
	}
	return ParseDef(name, src)
}

// ParseDef parses definition bytes.
func ParseDef(name string, src []byte) (*Def, []Diag) {
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(src))
	if err := dec.Decode(&doc); err != nil {
		return nil, []Diag{{Code: "P001", Pos: Pos{File: name}, Msg: "not valid YAML: " + err.Error()}}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, []Diag{{Code: "P001", Pos: Pos{File: name, Line: extra.Line}, Msg: "a definition file holds exactly one YAML document"}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, []Diag{{Code: "P001", Pos: Pos{File: name}, Msg: "the file must be one YAML mapping"}}
	}
	root := doc.Content[0]
	if diags := rejectYAMLFeatures(name, root); len(diags) > 0 {
		return nil, diags
	}
	if diags := checkStatusLexical(name, root); len(diags) > 0 {
		return nil, diags
	}
	if diags := validateSchema(name, root); len(diags) > 0 {
		return nil, diags
	}
	sum := sha256.Sum256(src)
	d := &Def{File: name, SHA256: hex.EncodeToString(sum[:]), Root: root}
	for _, kv := range pairs(root) {
		k, v := kv[0].Value, kv[1]
		switch k {
		case "apiVersion":
			d.APIVersion = v.Value
		case "kind":
			d.Kind = v.Value
		case "module":
			d.Module = v.Value
		case "version":
			d.Version = v.Value
		case "name":
			d.Name = v.Value
		case "imports":
			for _, it := range v.Content {
				imp := Import{Pos: pos(name, it)}
				if it.Kind == yaml.ScalarNode {
					imp.From = it.Value
				} else {
					for _, ikv := range pairs(it) {
						switch ikv[0].Value {
						case "from":
							imp.From = ikv[1].Value
						case "only":
							for _, o := range ikv[1].Content {
								imp.Only = append(imp.Only, o.Value)
							}
						}
					}
				}
				d.Imports = append(d.Imports, imp)
			}
		case "types":
			for _, tkv := range pairs(v) {
				d.Types = append(d.Types, parseNamedType(name, tkv[0], tkv[1]))
			}
		case "problems":
			for _, pkv := range pairs(v) {
				d.Problems = append(d.Problems, parseProblem(name, pkv[0], pkv[1]))
			}
		}
	}
	return d, nil
}

// rejectYAMLFeatures refuses anchors, aliases, merge keys and custom tags: a definition
// must read the same to a human as to the tool.
func rejectYAMLFeatures(file string, n *yaml.Node) []Diag {
	var out []Diag
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Anchor != "" || n.Kind == yaml.AliasNode {
			out = append(out, Diag{"P002", pos(file, n), "YAML anchors and aliases are not allowed in definitions"})
		}
		if n.Tag != "" && !strings.HasPrefix(n.Tag, "!!") {
			out = append(out, Diag{"P002", pos(file, n), "custom YAML tags are not allowed: " + n.Tag})
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Value == "<<" && k.Tag == "!!merge" {
					out = append(out, Diag{"P002", pos(file, k), "YAML merge keys are not allowed"})
				}
				if seen[k.Value] {
					out = append(out, Diag{"P003", pos(file, k), fmt.Sprintf("duplicate key %q", k.Value)})
				}
				seen[k.Value] = true
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(n)
	return out
}

func pairs(n *yaml.Node) [][2]*yaml.Node {
	var out [][2]*yaml.Node
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, [2]*yaml.Node{n.Content[i], n.Content[i+1]})
	}
	return out
}

func pos(file string, n *yaml.Node) Pos { return Pos{File: file, Line: n.Line, Col: n.Column} }

func parseFieldType(n *yaml.Node, items *yaml.Node, format string) FieldType {
	t := FieldType{Format: format}
	switch v := n.Value; v {
	case KString, KInteger, KInt64, KNumber, KBoolean, KDateTime:
		t.Kind = v
	case KArray:
		t.Kind = KArray
		if items != nil {
			var it *yaml.Node
			var f string
			for _, kv := range pairs(items) {
				switch kv[0].Value {
				case "type":
					it = kv[1]
				case "format":
					f = kv[1].Value
				}
			}
			if it != nil {
				inner := parseFieldType(it, nil, f)
				t.Items = &inner
			}
		}
	default:
		t.Kind = KRef
		t.Ref = v
	}
	return t
}

func parseField(file string, k, v *yaml.Node) *Field {
	f := &Field{Name: k.Value, Pos: pos(file, k)}
	var typeNode, itemsNode *yaml.Node
	var format string
	for _, kv := range pairs(v) {
		switch kv[0].Value {
		case "type":
			typeNode = kv[1]
		case "items":
			itemsNode = kv[1]
		case "format":
			format = kv[1].Value
		case "optional":
			f.Optional = kv[1].Value == "true"
		case "private":
			f.Private = kv[1].Value == "true"
		case "description":
			f.Description = kv[1].Value
		case "example":
			f.Example = kv[1]
		case "deprecated":
			f.Deprecated = kv[1].Value
		}
	}
	if typeNode != nil {
		f.Type = parseFieldType(typeNode, itemsNode, format)
	}
	return f
}

func parseNamedType(file string, k, v *yaml.Node) *NamedType {
	t := &NamedType{Name: k.Value, Pos: pos(file, k), Origin: file}
	for _, kv := range pairs(v) {
		switch kv[0].Value {
		case "description":
			t.Description = kv[1].Value
		case "enum":
			t.IsEnum = true
			for _, e := range kv[1].Content {
				t.Values = append(t.Values, e.Value)
			}
		case "fields":
			for _, fkv := range pairs(kv[1]) {
				t.Fields = append(t.Fields, parseField(file, fkv[0], fkv[1]))
			}
		}
	}
	return t
}

func parseProblem(file string, k, v *yaml.Node) *Problem {
	p := &Problem{ID: k.Value, Pos: pos(file, k), Origin: file}
	for _, kv := range pairs(v) {
		val := kv[1]
		switch kv[0].Value {
		case "title":
			p.Title = val.Value
		case "status":
			p.Status, _ = strconv.Atoi(val.Value) // lexical form checked by checkStatusLexical
		case "recoverable":
			p.Recoverable = val.Value == "true"
		case "detail":
			p.DetailSrc = val.Value
		case "log":
			p.LogSrc = val.Value
		case "description":
			p.Description = val.Value
		case "retryAfter":
			p.RetryAfter = val.Value
		case "deprecated":
			p.Deprecated = val.Value
		case "data":
			for _, fkv := range pairs(val) {
				p.Data = append(p.Data, parseField(file, fkv[0], fkv[1]))
			}
		}
	}
	return p
}

// Resolve expands imports into a Module. Import errors are returned as diagnostics.
func Resolve(d *Def) (*Module, []Diag) {
	m := &Module{Def: d}
	var diags []Diag
	typeOrigin := map[string]*NamedType{}
	probOrigin := map[string]*Problem{}
	addType := func(t *NamedType, at Pos) {
		if prev, ok := typeOrigin[t.Name]; ok {
			if sameType(prev, t) {
				return // diamond import of an identical definition
			}
			diags = append(diags, Diag{"L101", at, fmt.Sprintf("type %q is defined twice with different content (%s and %s)", t.Name, prev.Origin, t.Origin)})
			return
		}
		typeOrigin[t.Name] = t
		m.Types = append(m.Types, t)
	}
	addProblem := func(p *Problem, at Pos) {
		if prev, ok := probOrigin[p.ID]; ok {
			if prev.Origin != d.File && p.Origin != d.File && sameProblem(prev, p) {
				return
			}
			diags = append(diags, Diag{"L102", at, fmt.Sprintf("problem id %q clashes: defined in %s and %s (a local definition never overrides an import)", p.ID, prev.Origin, p.Origin)})
			return
		}
		probOrigin[p.ID] = p
		m.Problems = append(m.Problems, p)
	}
	for _, imp := range d.Imports {
		path := imp.From
		if !strings.HasPrefix(path, "builtin:") {
			path = filepath.Join(filepath.Dir(d.File), path)
		}
		lib, ld := LoadDef(path)
		if len(ld) > 0 {
			diags = append(diags, Diag{"L100", imp.Pos, fmt.Sprintf("import %q failed: %s", imp.From, ld[0].String())})
			continue
		}
		if lib.Kind != "ProblemLibrary" {
			diags = append(diags, Diag{"L100", imp.Pos, fmt.Sprintf("import %q is a %s; only a ProblemLibrary can be imported", imp.From, lib.Kind)})
			continue
		}
		if len(lib.Imports) > 0 {
			diags = append(diags, Diag{"L100", imp.Pos, fmt.Sprintf("import %q itself has imports; libraries are flat (no transitive imports)", imp.From)})
			continue
		}
		want := map[string]bool{}
		for _, o := range imp.Only {
			want[o] = true
		}
		found := map[string]bool{}
		// With only:, import exactly the named types the selected problems reach
		// (transitively); without only:, every type of the library.
		needed := map[string]bool{}
		if len(want) > 0 {
			var reach func(t FieldType)
			reach = func(t FieldType) {
				switch t.Kind {
				case KArray:
					if t.Items != nil {
						reach(*t.Items)
					}
				case KRef:
					if needed[t.Ref] {
						return
					}
					needed[t.Ref] = true
					for _, lt := range lib.Types {
						if lt.Name == t.Ref {
							for _, f := range lt.Fields {
								reach(f.Type)
							}
						}
					}
				}
			}
			for _, p := range lib.Problems {
				if want[p.ID] {
					for _, f := range p.Data {
						reach(f.Type)
					}
				}
			}
		}
		for _, t := range lib.Types {
			if len(want) == 0 || needed[t.Name] {
				addType(t, imp.Pos)
			}
		}
		for _, p := range lib.Problems {
			if len(want) > 0 && !want[p.ID] {
				continue
			}
			found[p.ID] = true
			addProblem(p, imp.Pos)
		}
		for _, o := range imp.Only {
			if !found[o] {
				diags = append(diags, Diag{"L103", imp.Pos, fmt.Sprintf("import %q has no problem %q", imp.From, o)})
			}
		}
	}
	for _, t := range d.Types {
		addType(t, t.Pos)
	}
	for _, p := range d.Problems {
		addProblem(p, p.Pos)
	}
	return m, diags
}

func sameType(a, b *NamedType) bool  { return canonicalTypeJSON(a) == canonicalTypeJSON(b) }
func sameProblem(a, b *Problem) bool { return canonicalProblemJSON(a) == canonicalProblemJSON(b) }

func canonicalTypeJSON(t *NamedType) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%v|%q|%q|", t.Name, t.IsEnum, t.Values, t.Description)
	for _, f := range t.Fields {
		b.WriteString(fieldSig(f))
	}
	return b.String()
}

func canonicalProblemJSON(p *Problem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%q|%d|%v|%q|%q|%q|%q|%q|", p.ID, p.Title, p.Status, p.Recoverable, p.DetailSrc, p.LogSrc, p.Description, p.RetryAfter, p.Deprecated)
	for _, f := range p.Data {
		b.WriteString(fieldSig(f))
	}
	return b.String()
}

func fieldSig(f *Field) string {
	ex := ""
	if f.Example != nil {
		ex = f.Example.Value
	}
	return fmt.Sprintf("{%s:%s:%v:%v:%q:%q:%q}", f.Name, typeSig(f.Type), f.Optional, f.Private, f.Description, ex, f.Deprecated)
}

func typeSig(t FieldType) string {
	switch t.Kind {
	case KArray:
		if t.Items == nil {
			return "array<?>"
		}
		return "array<" + typeSig(*t.Items) + ">"
	case KRef:
		return t.Ref
	}
	if t.Format != "" {
		return t.Kind + "/" + t.Format
	}
	return t.Kind
}

// LoadModule loads, validates structure, resolves imports and lints one ProblemSet.
func LoadModule(path string) (*Module, []Diag) {
	d, diags := LoadDef(path)
	if len(diags) > 0 {
		return nil, diags
	}
	if d.Kind != "ProblemSet" {
		return nil, []Diag{{"P004", Pos{File: path}, "expected kind ProblemSet (a ProblemLibrary is linted through the sets that import it, or with `lint --library`)"}}
	}
	m, rd := Resolve(d)
	diags = append(diags, rd...)
	diags = append(diags, Lint(m)...)
	return m, diags
}

var plainStatus = regexp.MustCompile(`^[1-9][0-9]{2}$`)

// checkStatusLexical refuses any status that is not a plain decimal integer: YAML would
// otherwise accept 4.22e2, 0x1a6 or 0o646 as numbers whose meaning differs by reader.
func checkStatusLexical(file string, root *yaml.Node) []Diag {
	var out []Diag
	for _, kv := range pairs(root) {
		if kv[0].Value != "problems" {
			continue
		}
		for _, p := range pairs(kv[1]) {
			for _, f := range pairs(p[1]) {
				if f[0].Value == "status" && (f[1].Kind != yaml.ScalarNode || f[1].Tag != "!!int" || !plainStatus.MatchString(f[1].Value)) {
					out = append(out, Diag{"P007", pos(file, f[1]), fmt.Sprintf("problem %q: status must be a plain three-digit decimal integer, not %q", p[0].Value, f[1].Value)})
				}
			}
		}
	}
	return out
}
