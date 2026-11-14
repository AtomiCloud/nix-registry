package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// LintRules documents every semantic rule (structural rules come from the JSON Schema, code P006).
var LintRules = map[string]string{
	"L100": "an import cannot be loaded, is not a ProblemLibrary, or has imports of its own",
	"L101": "a named type is defined twice with different content (imports included)",
	"L102": "a problem id is defined twice; a local definition never overrides an import",
	"L103": "`only` names a problem the import does not have",
	"L201": "a field refers to a named type that does not exist",
	"L202": "named object types form a cycle (recursive types are excluded)",
	"L204": "fields of a named object type cannot be private (privacy is per problem field)",
	"L205": "a named type is never used",
	"L206": "an example does not match the field type",
	"L207": "examples are only allowed on scalar fields",
	"L208": "`format` is only allowed on string (or array of string) fields",
	"L210": "template grammar error",
	"L211": "a placeholder names a field the problem does not declare",
	"L212": "a private field is used in `detail` (detail is sent to the client)",
	"L213": "an object field cannot be a placeholder; an array needs |count",
	"L214": "|count is only allowed on array fields",
	"L215": "a placeholder on an optional field needs |default:text",
	"L216": "|default is only allowed on optional fields",
	"L217": "a private field is never used in `log`",
	"L220": "retryAfter must name a required, public, integer field",
	"L221": "retryAfter is only allowed on a recoverable problem",
	"L230": "two generated identifiers collide in one scope of one language (see symbols.go)",
	"L231": "a name is reserved (a keyword, a generated member, or a standard-library type in one of the four languages)",
	"L240": "record-bound text contains `{{` or `}}` (would break the Helm-rendered record)",
	"L241": "the Problem record for this module exceeds the size budget",
	"L242": "text holds a character a target reads as a line break, or an invisible control (C1, U+2028/2029, U+FEFF, bidi embeddings/overrides/isolates, noncharacters)",
}

// reservedFieldNames are refused as field names: keywords and predeclared identifiers of
// Go, TypeScript, C# and Dart (as parameter names), plus members every generated
// problem value has. Refusing them keeps generated code free of escaping rules.
var reservedFieldNames = setOf(
	// generated members
	"detail", "instance", "cause", "meta", "logLine", "writeData", "toJson", "toString",
	"hashCode", "runtimeType", "noSuchMethod", "equals", "getHashCode", "getType",
	"constructor", "prototype", "copyWith", "with", "kind", "retryAfterSeconds", "problemMeta",
	"instanceUri", "problemCause", "receivedType", "receivedDetail", "isProblem", "clone", "equalityContract", "printMembers", "hasData",
	// Go keywords + predeclared identifiers
	"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough",
	"for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range",
	"return", "select", "struct", "switch", "type", "var", "any", "bool", "byte", "comparable",
	"complex64", "complex128", "error", "float32", "float64", "int", "int8", "int16", "int32",
	"int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
	"true", "false", "iota", "nil", "append", "cap", "clear", "close", "complex", "copy",
	"delete", "imag", "len", "make", "max", "min", "new", "panic", "print", "println", "real",
	"recover", "time", "rt", "json",
	// Dart reserved words + await/yield
	"assert", "catch", "class", "do", "enum", "extends", "final", "finally", "in", "is",
	"null", "rethrow", "super", "this", "throw", "try", "void", "while", "await", "yield",
	// C# keywords (camelCase parameters)
	"abstract", "as", "base", "checked", "decimal", "delegate", "double", "event", "explicit",
	"extern", "fixed", "float", "foreach", "implicit", "internal", "lock", "long", "namespace",
	"object", "operator", "out", "override", "params", "private", "protected", "public",
	"readonly", "ref", "sbyte", "sealed", "short", "sizeof", "stackalloc", "static", "ushort",
	"ulong", "uint", "unchecked", "unsafe", "using", "virtual", "volatile", "value", "char",
	// TypeScript / JavaScript
	"arguments", "eval", "export", "function", "let", "typeof", "instanceof", "undefined",
	"implements", "package", "yield", "enum", "await", "symbol", "never", "unknown", "number",
	"boolean", "bigint", "declare", "keyof", "readonly", "infer", "satisfies", "accessor",
)

// reservedTypeNames are refused as problem identifiers (PascalCase) and named types.
var reservedTypeNames = setOf(
	"Problem", "Unknown", "Meta", "Module", "Parse", "Parsed", "Catalog", "Context", "Options",
	"Writer", "Envelope", "Object", "String", "Error", "Exception", "Type", "Function", "Date",
	"DateTime", "Map", "List", "Set", "Record", "Result", "Enum", "Int64", "BigInt", "Number",
	"Boolean", "Symbol", "Never", "Any", "Dynamic", "Void", "Null", "Uri", "Guid", "Time",
	"Duration", "Json", "Self",
)

func setOf(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// Pascal converts snake_case or lowerCamel to PascalCase.
func Pascal(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r == '_' || r == '-' {
			up = true
			continue
		}
		if up {
			b.WriteRune(unicode.ToUpper(r))
			up = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Camel converts snake_case to lowerCamel.
func Camel(s string) string {
	p := Pascal(s)
	if p == "" {
		return p
	}
	return strings.ToLower(p[:1]) + p[1:]
}

// Lint runs every semantic rule over a resolved module.
func Lint(m *Module) []Diag {
	var ds []Diag
	add := func(code string, p Pos, format string, a ...any) {
		ds = append(ds, Diag{code, p, fmt.Sprintf(format, a...)})
	}

	// Named types.
	used := map[string]bool{}
	var checkFieldType func(f *Field, t FieldType, inObject bool)
	checkFieldType = func(f *Field, t FieldType, inObject bool) {
		switch t.Kind {
		case KRef:
			used[t.Ref] = true
			if m.TypeByName(t.Ref) == nil {
				add("L201", f.Pos, "field %q: unknown type %q", f.Name, t.Ref)
			}
		case KArray:
			if t.Items != nil {
				checkFieldType(f, *t.Items, inObject)
			}
		}
		if t.Format != "" && t.Kind != KString {
			add("L208", f.Pos, "field %q: format %q needs type string", f.Name, t.Format)
		}
	}
	checkField := func(f *Field, inObject bool) {
		checkFieldType(f, f.Type, inObject)
		if inObject && f.Private {
			add("L204", f.Pos, "field %q: fields of a named object cannot be private", f.Name)
		}
		if reservedFieldNames[f.Name] {
			add("L231", f.Pos, "field name %q is reserved (keyword or generated member in Go/TS/C#/Dart)", f.Name)
		}
		if f.Example != nil {
			lintExample(f, m, add)
		}
	}
	for _, t := range m.Types {
		if reservedTypeNames[t.Name] {
			add("L231", t.Pos, "type name %q is reserved", t.Name)
		}
		for _, v := range t.Values {
			c := Camel(v)
			if reservedFieldNames[c] {
				add("L231", t.Pos, "enum %s: value %q becomes the reserved identifier %q", t.Name, v, c)
			}
			if Pascal(v) == t.Name {
				add("L231", t.Pos, "enum %s: value %q becomes a member named like its type (C# CS0542)", t.Name, v)
			}
		}
		for _, f := range t.Fields {
			checkField(f, true)
			if Pascal(f.Name) == t.Name {
				add("L231", f.Pos, "type %s: field %q becomes a member named like its type (C# CS0542)", t.Name, f.Name)
			}
		}
	}
	// Cycles among object types.
	state := map[string]int{}
	var visit func(name string, at Pos) bool
	visit = func(name string, at Pos) bool {
		t := m.TypeByName(name)
		if t == nil || t.IsEnum {
			return false
		}
		switch state[name] {
		case 1:
			add("L202", at, "type %s is recursive (directly or through other types)", name)
			return true
		case 2:
			return false
		}
		state[name] = 1
		for _, f := range t.Fields {
			ft := f.Type
			if ft.Kind == KArray && ft.Items != nil {
				ft = *ft.Items
			}
			if ft.Kind == KRef && visit(ft.Ref, f.Pos) {
				state[name] = 2
				return true
			}
		}
		state[name] = 2
		return false
	}
	for _, t := range m.Types {
		visit(t.Name, t.Pos)
	}

	// Problems.
	checkSymbols(m, add)
	checkTextChars(m, add)
	for _, p := range m.Problems {
		pn := Pascal(p.ID)
		if reservedTypeNames[pn] {
			add("L231", p.Pos, "problem %q becomes the reserved identifier %s", p.ID, pn)
		}
		for _, f := range p.Data {
			checkField(f, false)
			if Pascal(f.Name) == pn {
				add("L231", f.Pos, "problem %q: field %q becomes a member named like its type (C# CS0542)", p.ID, f.Name)
			}
		}
		for _, rt := range recordText(m, p) {
			if strings.Contains(rt.text, "{{") || strings.Contains(rt.text, "}}") {
				add("L240", rt.pos, "problem %q: %s must not contain {{ or }} (it is copied into the Helm-rendered record)", p.ID, rt.what)
			}
		}

		// Templates.
		dt, err := ParseTemplate(p.DetailSrc)
		if err != nil {
			add("L210", p.Pos, "problem %q detail: %v", p.ID, err)
		}
		p.Detail = dt
		logSrc := p.LogSrc
		if logSrc == "" {
			logSrc = p.DetailSrc
		}
		lt, err := ParseTemplate(logSrc)
		if err != nil {
			add("L210", p.Pos, "problem %q log: %v", p.ID, err)
		}
		p.Log = lt
		usedInLog := map[string]bool{}
		for _, wt := range []struct {
			which string
			t     *Template
		}{{"detail", dt}, {"log", lt}} {
			which, t := wt.which, wt.t
			if t == nil {
				continue
			}
			for _, part := range t.Parts {
				if !part.IsPlaceholder() {
					continue
				}
				f := p.Field(part.Field)
				if f == nil {
					add("L211", p.Pos, "problem %q %s: {%s} is not a declared data field", p.ID, which, part.Field)
					continue
				}
				if which == "log" {
					usedInLog[f.Name] = true
				}
				if which == "detail" && f.Private {
					add("L212", p.Pos, "problem %q detail: {%s} is private and must never reach the client", p.ID, part.Field)
				}
				isObj := f.Type.Kind == KRef && m.TypeByName(f.Type.Ref) != nil && !m.TypeByName(f.Type.Ref).IsEnum
				switch {
				case f.Type.Kind == KArray && !part.Count:
					add("L213", p.Pos, "problem %q %s: {%s} is an array; write {%s|count}", p.ID, which, part.Field, part.Field)
				case isObj:
					add("L213", p.Pos, "problem %q %s: {%s} is an object and cannot be rendered", p.ID, which, part.Field)
				case part.Count && f.Type.Kind != KArray:
					add("L214", p.Pos, "problem %q %s: |count on non-array field %q", p.ID, which, part.Field)
				}
				if f.Optional && !part.HasDefault {
					add("L215", p.Pos, "problem %q %s: {%s} is optional; add |default:text", p.ID, which, part.Field)
				}
				if !f.Optional && part.HasDefault {
					add("L216", p.Pos, "problem %q %s: {%s} is required; remove |default", p.ID, which, part.Field)
				}
			}
		}
		for _, f := range p.Data {
			if f.Private && !usedInLog[f.Name] && lt != nil {
				add("L217", f.Pos, "problem %q: private field %q is never used in log (dead data)", p.ID, f.Name)
			}
		}

		// Retry hint.
		if p.RetryAfter != "" {
			f := p.Field(p.RetryAfter)
			if f == nil || f.Type.Kind != KInteger || f.Optional || f.Private {
				add("L220", p.Pos, "problem %q: retryAfter %q must name a required public integer field", p.ID, p.RetryAfter)
			}
			if !p.Recoverable {
				add("L221", p.Pos, "problem %q: retryAfter on a non-recoverable problem", p.ID)
			}
		}
	}
	for _, t := range m.Types {
		if !used[t.Name] {
			add("L205", t.Pos, "type %s is never used", t.Name)
		}
	}

	// Record size budget (etcd's object limit is 1.5 MiB; keep a margin for metadata/status),
	// and a backstop for L240: the rendered record body must not open a template action,
	// whatever model path put the text there.
	if len(ds) == 0 && m.Def.Kind == "ProblemSet" {
		cfg := &Config{Platform: "p", Service: "s", Portal: "https://errors.example.com"}
		rec := RenderRecordYAML(m, cfg, "x", "")
		// Only {{ opens a Helm action; nested JSON objects legitimately end in }}.
		if strings.Contains(rec, "{{") {
			add("L240", Pos{File: m.Def.File, Line: 1, Col: 1}, "the rendered Problem record contains {{ outside the landscape expression")
		}
		if len(rec) > recordBudget {
			add("L241", Pos{File: m.Def.File, Line: 1, Col: 1}, "the Problem record is %d bytes; the budget is %d (etcd limit 1.5 MiB). Split the module", len(rec), recordBudget)
		}
	}
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.Pos.File != b.Pos.File {
			return a.Pos.File < b.Pos.File
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line < b.Pos.Line
		}
		if a.Pos.Col != b.Pos.Col {
			return a.Pos.Col < b.Pos.Col
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Msg < b.Msg
	})
	return ds
}

type recordString struct {
	pos  Pos
	what string
	text string
}

// recordText lists every definition string the Problem record copies for one problem:
// title, description, and through the data schema each public field's description and
// example plus, transitively, every named type it reaches (type description, nested field
// descriptions and examples, enum values). Private fields never reach the record.
func recordText(m *Module, p *Problem) []recordString {
	out := []recordString{{p.Pos, "the title", p.Title}, {p.Pos, "the description", p.Description}}
	seen := map[string]bool{}
	var field func(f *Field, owner string)
	var typ func(t FieldType)
	typ = func(t FieldType) {
		switch t.Kind {
		case KArray:
			if t.Items != nil {
				typ(*t.Items)
			}
		case KRef:
			nt := m.TypeByName(t.Ref)
			if nt == nil || seen[nt.Name] {
				return
			}
			seen[nt.Name] = true
			out = append(out, recordString{nt.Pos, "the description of type " + nt.Name, nt.Description})
			for _, v := range nt.Values {
				out = append(out, recordString{nt.Pos, "a value of enum " + nt.Name, v})
			}
			for _, f := range nt.Fields {
				field(f, "type "+nt.Name)
			}
		}
	}
	field = func(f *Field, owner string) {
		out = append(out, recordString{f.Pos, fmt.Sprintf("the description of field %q of %s", f.Name, owner), f.Description})
		if f.Example != nil {
			out = append(out, recordString{f.Pos, fmt.Sprintf("the example of field %q of %s", f.Name, owner), f.Example.Value})
		}
		typ(f.Type)
	}
	for _, f := range p.PublicData() {
		field(f, "problem "+p.ID)
	}
	return out
}

const recordBudget = 900 * 1024

func lintExample(f *Field, m *Module, add func(string, Pos, string, ...any)) {
	ex := f.Example
	t := f.Type
	shown := strconv.Quote(ex.Value)
	if f.Private {
		shown = "(private, not shown)" // diagnostics are logged by CI; private metadata stays out
	}
	switch t.Kind {
	case KArray:
		add("L207", f.Pos, "field %q: examples are only allowed on scalar fields", f.Name)
		return
	case KRef:
		nt := m.TypeByName(t.Ref)
		if nt == nil {
			return
		}
		if !nt.IsEnum {
			add("L207", f.Pos, "field %q: examples are only allowed on scalar fields", f.Name)
			return
		}
		for _, v := range nt.Values {
			if v == ex.Value && ex.Tag == "!!str" {
				return
			}
		}
		add("L206", f.Pos, "field %q: example %s is not a value of %s", f.Name, shown, nt.Name)
		return
	}
	ok := false
	switch t.Kind {
	case KString, KDateTime, KInt64:
		ok = ex.Tag == "!!str"
	case KInteger:
		ok = ex.Tag == "!!int"
	case KNumber:
		ok = ex.Tag == "!!int" || ex.Tag == "!!float"
	case KBoolean:
		ok = ex.Tag == "!!bool"
	}
	if ok {
		switch {
		case t.Kind == KDateTime:
			_, err := parseCanonicalDateTime(ex.Value)
			ok = err == nil
		case t.Kind == KInt64:
			_, err := parseInt64String(ex.Value)
			ok = err == nil
		case t.Kind == KString && t.Format != "":
			ok = checkFormat(t.Format, ex.Value) == nil
		case t.Kind == KInteger:
			// The generated schema says minimum/maximum ±(2^53-1); the example must obey it.
			i, err := strconv.ParseInt(ex.Value, 0, 64)
			ok = err == nil && i >= -MaxSafeInteger && i <= MaxSafeInteger
		case t.Kind == KNumber:
			fl, err := strconv.ParseFloat(strings.ReplaceAll(ex.Value, "_", ""), 64)
			if ex.Tag == "!!int" {
				var i int64
				i, err = strconv.ParseInt(ex.Value, 0, 64)
				fl = float64(i)
			}
			_, ferr := formatNumber(fl)
			ok = err == nil && ferr == nil // finite only: .inf / .nan have no JSON form
		}
	}
	if !ok {
		add("L206", f.Pos, "field %q: example %s does not match type %s", f.Name, shown, typeSig(t))
	}
}

// unsafeTextRune reports runes refused in definition text. C# and JavaScript end a // comment
// at U+0085/U+2028/U+2029 and C# refuses them inside string literals; YAML 1.1 readers treat
// NEL and U+2028/2029 as line breaks; C1 controls, U+FEFF and noncharacters are invisible or
// invalid in YAML; bidi embedding/override/isolate controls can make generated source read
// differently than it compiles ("trojan source"). Tabs and newlines are refused earlier by the
// schema's text pattern (P006), so a description is one line (no folded/literal blocks).
func unsafeTextRune(r rune) bool {
	switch {
	case r < 0x20 || (r >= 0x7f && r <= 0x9f):
		return true
	case r == 0x2028 || r == 0x2029 || r == 0xfeff || r == 0xfffe || r == 0xffff:
		return true
	case (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069):
		// embeddings, overrides and isolates; the plain marks LRM, RLM and ALM
		// (U+200E, U+200F, U+061C) are allowed: real RTL text needs them
		return true
	case r >= 0xfdd0 && r <= 0xfdef, r&0xfffe == 0xfffe:
		return true
	}
	return false
}

// checkTextChars applies L242 to every free text of the definition. The message names the
// code point, never the text (private descriptions stay out of logs).
func checkTextChars(m *Module, add func(string, Pos, string, ...any)) {
	check := func(p Pos, what, text string) {
		for _, r := range text {
			if unsafeTextRune(r) {
				add("L242", p, "%s contains U+%04X (refused: line break or invisible control in some target)", what, r)
				return
			}
		}
	}
	field := func(f *Field, owner string) {
		check(f.Pos, fmt.Sprintf("the description of field %q of %s", f.Name, owner), f.Description)
		check(f.Pos, fmt.Sprintf("the deprecation note of field %q of %s", f.Name, owner), f.Deprecated)
		if f.Example != nil && f.Example.Kind == yaml.ScalarNode {
			check(f.Pos, fmt.Sprintf("the example of field %q of %s", f.Name, owner), f.Example.Value)
		}
	}
	for _, t := range m.Types {
		check(t.Pos, "the description of type "+t.Name, t.Description)
		for _, v := range t.Values {
			check(t.Pos, "a value of enum "+t.Name, v)
		}
		for _, f := range t.Fields {
			field(f, "type "+t.Name)
		}
	}
	for _, p := range m.Problems {
		o := "problem " + p.ID
		check(p.Pos, "the title of "+o, p.Title)
		check(p.Pos, "the description of "+o, p.Description)
		check(p.Pos, "the deprecation note of "+o, p.Deprecated)
		check(p.Pos, "the detail template of "+o, p.DetailSrc)
		check(p.Pos, "the log template of "+o, p.LogSrc)
		for _, f := range p.Data {
			field(f, o)
		}
	}
}
