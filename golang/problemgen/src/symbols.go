package main

import (
	"fmt"
	"strings"
)

// The generated-symbol model. For each language it lists every identifier the generator
// declares, per scope (package/module/namespace/library, and each struct/class/record/enum),
// exactly as gen_<lang>.go spells it. Lint refuses a definition when two entries of one
// scope are equal (L230) or an entry shadows a name the generated code or its standard
// library relies on (L231). It is a hand-kept mirror, not a proof of completeness:
// TestGoCollisionsAreCompileErrors type-checks the generated Go of the planted Go collision
// files, and the generative compile test (SPIKE fuzz/) compiles hundreds of random lint-clean
// definitions in all four languages to catch what this model misses. Expression-context
// references in C# are fully qualified (gen_cs.go q) so properties cannot shadow them.
//
// Keep this file in step with the generators: a new generated identifier goes here first.

type symEntry struct {
	origin string // what produced it: "generated", "problem foo", "field bar of Baz", ...
	pos    Pos
}

type symScope struct {
	lang, where string
	seen        map[string]symEntry
	r           *symReport
}

// symReport merges the same clash found in several languages into one diagnostic.
type symReport struct {
	hits  []*symHit
	index map[string]*symHit
}

type symHit struct {
	pos   Pos
	msg   string
	langs []string
}

func (r *symReport) scope(lang, where string) *symScope {
	return &symScope{lang: lang, where: where, seen: map[string]symEntry{}, r: r}
}

// fixed declares generator-owned names (never reported against each other).
func (s *symScope) fixed(names ...string) {
	for _, n := range names {
		s.seen[n] = symEntry{origin: "a generated member"}
	}
}

// put declares a name derived from the definition.
func (s *symScope) put(name, origin string, pos Pos) {
	if prev, ok := s.seen[name]; ok {
		msg := fmt.Sprintf("%s and %s both generate %q", origin, prev.origin, name)
		key := pos.String() + "|" + msg
		h := s.r.index[key]
		if h == nil {
			h = &symHit{pos: pos, msg: msg}
			s.r.index[key] = h
			s.r.hits = append(s.r.hits, h)
		}
		h.langs = append(h.langs, langName[s.lang]+" ("+s.where+")")
		return
	}
	s.seen[name] = symEntry{origin: origin, pos: pos}
}

var langName = map[string]string{"go": "Go", "ts": "TypeScript", "cs": "C#", "dart": "Dart"}

// coreTypeNames are standard-library type names the generated code or its users rely on
// unqualified (dart:core, TypeScript/JavaScript globals, System types C# users import).
// A problem or named type spelled like one would shadow it in the generated file: for
// example a problem `format_exception` would declare Dart's FormatException and every
// generated `on FormatException catch` would catch the problem class instead.
var coreTypeNames = setOf(
	// dart:core
	"AssertionError", "ArgumentError", "BigInt", "Comparable", "Comparator",
	"ConcurrentModificationError", "DateTime", "Deprecated", "Duration", "Enum", "Error",
	"Exception", "Expando", "Finalizer", "FormatException", "Function", "Future",
	"IndexError", "IntegerDivisionByZeroException", "Invocation", "Iterable", "Iterator",
	"List", "Map", "MapEntry", "Match", "NoSuchMethodError", "Null", "Never", "Object",
	"OutOfMemoryError", "Pattern", "RangeError", "Record", "RegExp", "RegExpMatch", "Runes",
	"Set", "Sink", "StackOverflowError", "StackTrace", "StateError", "Stopwatch", "Stream",
	"String", "StringBuffer", "StringSink", "Symbol", "Type", "TypeError", "UnimplementedError",
	"UnsupportedError", "Uri", "UriData", "WeakReference", "Override",
	// TypeScript / JavaScript globals
	"AggregateError", "Array", "ArrayBuffer", "ArrayLike", "Awaited", "Boolean", "Buffer",
	"Bun", "DataView", "Date", "Deno", "EvalError", "Exclude", "Extract", "Generator",
	"Headers", "InstanceType", "Intl", "JSON", "Math", "NonNullable", "Number", "Omit",
	"Parameters", "Partial", "Pick", "Promise", "PromiseLike", "Proxy", "Readonly",
	"ReferenceError", "Reflect", "Request", "Required", "Response", "ReturnType",
	"SyntaxError", "TextDecoder", "TextEncoder", "URIError", "URL", "URLSearchParams",
	"Uint8Array", "WeakMap", "WeakRef", "WeakSet",
	// C# System types (generic-only ones such as Span<T> are not shadowed: C# resolves by arity)
	"Action", "Activator", "Attribute", "Console", "Convert", "DateTimeOffset", "Decimal",
	"Delegate", "Double", "Environment", "EventArgs", "GC", "Guid", "HashCode",
	"IComparable", "IDisposable", "Index", "Int32", "Int64",
	"Nullable", "Random", "Range", "System", "Task", "TimeSpan", "Tuple", "ValueTask",
	"ValueTuple", "Version",
)

// csRecordMembers are members every C# record has (declared or synthesised).
var csRecordMembers = []string{
	"EqualityContract", "PrintMembers", "Clone", "Equals", "GetHashCode", "ToString",
	"Deconstruct", "GetType", "MemberwiseClone", "ReferenceEquals", "Finalize",
}

// dartObjectMembers are members every Dart object has.
var dartObjectMembers = []string{"hashCode", "runtimeType", "toString", "noSuchMethod"}

func checkSymbols(m *Module, add func(string, Pos, string, ...any)) {
	r := &symReport{index: map[string]*symHit{}}
	defer func() {
		for _, h := range r.hits {
			add("L230", h.pos, "%s in %s", h.msg, strings.Join(h.langs, ", "))
		}
	}()
	// A field already refused as reserved (L231) is not reported again here.
	skip := func(f *Field) bool { return reservedFieldNames[f.Name] }
	fo := func(f *Field, owner string) string { return fmt.Sprintf("field %q of %s", f.Name, owner) }

	for _, t := range m.Types {
		if coreTypeNames[t.Name] && !reservedTypeNames[t.Name] {
			add("L231", t.Pos, "type name %q shadows a standard-library type in Go/TS/C#/Dart output", t.Name)
		}
	}
	for _, p := range m.Problems {
		pn := Pascal(p.ID)
		if coreTypeNames[pn] && !reservedTypeNames[pn] {
			add("L231", p.Pos, "problem %q becomes %s, which shadows a standard-library type in Go/TS/C#/Dart output", p.ID, pn)
		}
	}

	// ---- Go ----
	pkg := r.scope("go", "the package")
	pkg.fixed("Problem", "Unknown", "Parse", "errField", "errMissing", "errInvalid", "fieldError",
		"json", "strconv", "strings", "time", "rt")
	for _, t := range m.Types {
		o := "type " + t.Name
		pkg.put(t.Name, o, t.Pos)
		if t.IsEnum {
			for _, v := range t.Values {
				pkg.put(t.Name+Pascal(v), fmt.Sprintf("value %q of enum %s", v, t.Name), t.Pos)
			}
			continue
		}
		pkg.put("New"+t.Name, o+" (constructor)", t.Pos)
		pkg.put("decode"+t.Name, o+" (decoder)", t.Pos)
		st := r.scope("go", "struct "+t.Name)
		st.fixed("writeJSON")
		goStruct(st, t.Fields, o, fo, skip)
	}
	for _, p := range m.Problems {
		o := "problem " + p.ID
		n := Pascal(p.ID)
		pkg.put(n, o, p.Pos)
		pkg.put("New"+n, o+" (constructor)", p.Pos)
		pkg.put("meta"+n, o+" (metadata)", p.Pos)
		pkg.put("decode"+n+"Problem", o+" (decoder)", p.Pos)
		st := r.scope("go", "struct "+n)
		st.fixed("instance", "cause", "received", "WithInstance", "WithCause", "isProblem",
			"ProblemMeta", "InstanceURI", "ProblemCause", "ReceivedType", "RetryAfterSeconds",
			"Detail", "LogLine", "WriteData")
		goStruct(st, p.Data, o, fo, skip)
	}

	// ---- TypeScript ----
	mod := r.scope("ts", "the module")
	mod.fixed("Problem", "Parsed", "Unknown", "parse", "rt")
	for _, t := range m.Types {
		o := "type " + t.Name
		mod.put(t.Name, o, t.Pos)
		if t.IsEnum {
			mod.put(Camel(t.Name)+"Values", o+" (value list)", t.Pos)
			mod.put("is"+t.Name, o+" (guard)", t.Pos)
		} else {
			mod.put("_write_"+t.Name, o+" (writer)", t.Pos)
			mod.put("_decode_"+t.Name, o+" (decoder)", t.Pos)
		}
	}
	for _, p := range m.Problems {
		o := "problem " + p.ID
		n := Pascal(p.ID)
		mod.put(n, o, p.Pos)
		cl := r.scope("ts", "class "+n)
		cl.fixed("constructor", "kind", "instance", "meta", "problemCause", "receivedType",
			"retryAfterSeconds", "detail", "logLine", "writeData", "#cause", "#received")
		for _, f := range p.Data {
			if skip(f) {
				continue
			}
			name := f.Name
			if f.Private {
				name = "#" + f.Name
			}
			cl.put(name, fo(f, o), f.Pos)
		}
	}

	// ---- C# ----
	ns := r.scope("cs", "the namespace")
	ns.fixed("Problem", "Unknown")
	for _, t := range m.Types {
		o := "type " + t.Name
		ns.put(t.Name, o, t.Pos)
		if t.IsEnum {
			ns.put("__"+t.Name+"Wire", o+" (wire helper)", t.Pos)
			en := r.scope("cs", "enum "+t.Name)
			for _, v := range t.Values {
				en.put(Pascal(v), fmt.Sprintf("value %q of enum %s", v, t.Name), t.Pos)
			}
			continue
		}
		csOwnName(t.Name, o, t.Pos, csObjectDeclared, add)
		rec := r.scope("cs", "record "+t.Name)
		rec.fixed("__Read", "__Write")
		rec.fixed(csRecordMembers...)
		for _, f := range t.Fields {
			if !skip(f) {
				rec.put(Pascal(f.Name), fo(f, o), f.Pos)
			}
		}
	}
	for _, p := range m.Problems {
		o := "problem " + p.ID
		n := Pascal(p.ID)
		ns.put(n, o, p.Pos)
		csOwnName(n, o, p.Pos, csProblemDeclared, add)
		rec := r.scope("cs", "record "+n)
		rec.fixed("Meta", "Instance", "Cause", "ReceivedType", "ReceivedDetail", "Detail",
			"LogLine", "WriteData", "RetryAfterSeconds", "Parse", "__Closed", "__Read", "__Write",
			"metadata")
		rec.fixed(csRecordMembers...)
		for _, f := range p.Data {
			if !skip(f) {
				rec.put(Pascal(f.Name), fo(f, o), f.Pos)
			}
		}
	}

	// ---- Dart ----
	lib := r.scope("dart", "the library")
	lib.fixed("Problem", "Unknown", "rt")
	for _, t := range m.Types {
		o := "type " + t.Name
		lib.put(t.Name, o, t.Pos)
		lib.put("_decode"+t.Name, o+" (decoder)", t.Pos)
		if t.IsEnum {
			en := r.scope("dart", "enum "+t.Name)
			en.fixed("wire", "values", "index", "name")
			en.fixed(dartObjectMembers...)
			for _, v := range t.Values {
				en.put(Camel(v), fmt.Sprintf("value %q of enum %s", v, t.Name), t.Pos)
			}
			continue
		}
		lib.put("_write"+t.Name, o+" (writer)", t.Pos)
		cl := r.scope("dart", "class "+t.Name)
		cl.fixed(dartObjectMembers...)
		for _, f := range t.Fields {
			if !skip(f) {
				cl.put(f.Name, fo(f, o), f.Pos)
			}
		}
	}
	// Dart class members shadow library-level names inside the class body, and generated
	// bodies call the library-level helpers: a private field `decodeInner` (member
	// _decodeInner) would capture the call to the helper _decodeInner.
	var dartHelpers []string
	for _, t := range m.Types {
		dartHelpers = append(dartHelpers, "_decode"+t.Name)
		if !t.IsEnum {
			dartHelpers = append(dartHelpers, "_write"+t.Name)
		}
	}
	for _, p := range m.Problems {
		o := "problem " + p.ID
		n := Pascal(p.ID)
		lib.put(n, o, p.Pos)
		cl := r.scope("dart", "class "+n)
		cl.fixed(dartHelpers...)
		cl.fixed("_meta", "meta", "retryAfterSeconds", "detail", "logLine", "hasData", "writeData",
			"_decode", "instance", "cause", "_received", "receivedType", "parse")
		cl.fixed(dartObjectMembers...)
		params := r.scope("dart", "the constructor parameters of "+n)
		params.fixed("instance", "cause", "received")
		for _, f := range p.Data {
			if skip(f) {
				continue
			}
			name := f.Name
			if f.Private {
				name = "_" + f.Name
			}
			cl.put(name, fo(f, o), f.Pos)
			params.put(f.Name, fo(f, o), f.Pos)
		}
	}
}

// goStruct declares a Go struct's fields and methods: they share one namespace.
func goStruct(st *symScope, fields []*Field, owner string, fo func(*Field, string) string, skip func(*Field) bool) {
	if hasRequired(fields) {
		st.fixed("built")
	}
	for _, f := range fields {
		if skip(f) {
			continue
		}
		st.put(f.Name, fo(f, owner), f.Pos)
		if !f.Private {
			st.put(Pascal(f.Name), fo(f, owner)+" (accessor)", f.Pos)
		}
		if f.Optional {
			st.put("With"+Pascal(f.Name), fo(f, owner)+" (setter)", f.Pos)
		}
	}
}

// Members a generated C# record DECLARES (written by the generator or synthesised by the
// compiler for every record). A record cannot be named like one of its own declared members
// (CS0542); inherited members (Instance, Cause, GetType, ...) are fine.
var (
	csSynthesised     = []string{"EqualityContract", "PrintMembers", "Equals", "GetHashCode", "ToString"}
	csObjectDeclared  = append([]string{"__Read", "__Write"}, csSynthesised...)
	csProblemDeclared = append([]string{"Meta", "Detail", "LogLine", "WriteData", "RetryAfterSeconds", "__Closed", "__Read"}, csSynthesised...)
)

func csOwnName(name, origin string, p Pos, declared []string, add func(string, Pos, string, ...any)) {
	for _, d := range declared {
		if d == name {
			add("L231", p, "%s becomes C# record %s, which declares a member %s (CS0542)", origin, name, d)
		}
	}
}
