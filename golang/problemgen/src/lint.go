package main

import (
	"fmt"
	"strings"
	"unicode"
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
	"L230": "two names map to the same generated identifier",
	"L231": "a name is reserved (a keyword or a generated member in one of the four languages)",
	"L240": "record-bound text contains `{{` or `}}` (would break the Helm-rendered record)",
	"L241": "the Problem record for this module exceeds the size budget",
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
	"ulong", "uint", "unchecked", "unsafe", "using", "virtual", "volatile", "value",
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
		seenVals := map[string]string{}
		for _, v := range t.Values {
			c := Camel(v)
			if prev, ok := seenVals[c]; ok {
				add("L230", t.Pos, "enum %s: values %q and %q both become %q", t.Name, prev, v, c)
			}
			seenVals[c] = v
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
	pascalSeen := map[string]string{}
	for _, t := range m.Types {
		pascalSeen[t.Name] = "type " + t.Name
	}
	for _, p := range m.Problems {
		pn := Pascal(p.ID)
		if prev, ok := pascalSeen[pn]; ok {
			add("L230", p.Pos, "problem %q becomes identifier %s, already used by %s", p.ID, pn, prev)
		}
		pascalSeen[pn] = "problem " + p.ID
		if reservedTypeNames[pn] {
			add("L231", p.Pos, "problem %q becomes the reserved identifier %s", p.ID, pn)
		}
		for _, f := range p.Data {
			checkField(f, false)
			if Pascal(f.Name) == pn {
				add("L231", f.Pos, "problem %q: field %q becomes a member named like its type (C# CS0542)", p.ID, f.Name)
			}
		}
		for _, txt := range []string{p.Title, p.Description} {
			if strings.Contains(txt, "{{") || strings.Contains(txt, "}}") {
				add("L240", p.Pos, "problem %q: title/description must not contain {{ or }}", p.ID)
			}
		}
		for _, f := range p.Data {
			if strings.Contains(f.Description, "{{") || strings.Contains(f.Description, "}}") ||
				(f.Example != nil && (strings.Contains(f.Example.Value, "{{") || strings.Contains(f.Example.Value, "}}"))) {
				add("L240", f.Pos, "field %q: description/example must not contain {{ or }}", f.Name)
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
		for which, t := range map[string]*Template{"detail": dt, "log": lt} {
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

	// Record size budget (etcd's object limit is 1.5 MiB; keep a margin for metadata/status).
	if len(ds) == 0 && m.Def.Kind == "ProblemSet" {
		cfg := &Config{Platform: "p", Service: "s", Portal: "https://errors.example.com"}
		rec := RenderRecordYAML(m, cfg, "x", "")
		if len(rec) > recordBudget {
			add("L241", Pos{File: m.Def.File, Line: 1, Col: 1}, "the Problem record is %d bytes; the budget is %d (etcd limit 1.5 MiB). Split the module", len(rec), recordBudget)
		}
	}
	return ds
}

const recordBudget = 900 * 1024

func lintExample(f *Field, m *Module, add func(string, Pos, string, ...any)) {
	ex := f.Example
	t := f.Type
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
		add("L206", f.Pos, "field %q: example %q is not a value of %s", f.Name, ex.Value, nt.Name)
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
		}
	}
	if !ok {
		add("L206", f.Pos, "field %q: example %q does not match type %s", f.Name, ex.Value, typeSig(t))
	}
}
