package main

// Adversarial random definitions (hidden: `problemgen conformance random-defs`).
//
// Each candidate is a small ProblemSet whose names come from the identifiers the four target
// languages, their standard libraries and our own generators treat specially (keywords,
// core type names, generated members, case/underscore/prefix variants of each other), whose
// text fields carry awkward Unicode, and which covers every field kind. Every candidate goes
// through the real loader + lint (ParseDef, Resolve, Lint); only definitions with no
// diagnostic are kept, refusals are counted by code. A kept definition is written with one
// conformance vector per problem (random valid data for every field, private ones included),
// grouped into batches that run-fuzz.sh compiles in all four languages and round-trips.
//
// Determinism: candidate i of seed S draws from PCG(S, i) only, and nothing iterates a map.

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
)

// ---- name pools ----

var fzKeywordWords = strings.Fields(`
break case chan const continue default defer else fallthrough for func go goto if import
interface map package range return select struct switch type var any bool byte comparable
complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8
uint16 uint32 uint64 uintptr true false iota nil append cap clear close complex copy delete
imag len make max min new panic print println real recover
abstract as asserts async await bigint boolean catch class constructor debugger declare do enum
export extends finally from function get global implements in infer instanceof is keyof let
module namespace never null number object of out override private protected public readonly
require satisfies set static super symbol this throw try typeof undefined unique unknown using
void while with yield accessor arguments eval
base char checked decimal delegate double event explicit extern fixed float foreach implicit
internal lock long operator params ref sbyte sealed short sizeof stackalloc ulong unchecked
unsafe ushort virtual volatile add alias and ascending args by descending dynamic equals file
group init into join managed nameof nint not notnull nuint on or orderby partial record remove
required scoped unmanaged value when where field allows extension
assert covariant deferred external factory final hide late library mixin part rethrow show sync
typedef`)

var fzGeneratedWords = strings.Fields(`
Problem Unknown Parse Parsed errField errMissing errInvalid fieldError json strconv strings time
rt writeJSON instance cause received WithInstance WithCause isProblem ProblemMeta InstanceURI
ProblemCause ReceivedType RetryAfterSeconds Detail LogLine WriteData parse kind meta problemCause
receivedType retryAfterSeconds detail logLine writeData __Read __Write Meta Instance Cause
ReceivedDetail __Closed metadata _meta hasData _decode _received wire values index name built
Values decode write Wire Envelope Reason Writer Decode Text ProblemHttp ProblemValue Address
Fields NotProblem Logger TypeURI Marshal Unmarshal mustTime landscape emit bench line enc out
message main Program Print Emit Bench Main Landscape gen conftest item it raw data err ok result
obj key fields status title problem issues id entityType reason path code ValidationIssue
validationIssue validation_error entity_not_found conflict unauthenticated unauthorized
invalid_json EqualityContract PrintMembers Clone Equals GetHashCode ToString Deconstruct GetType
MemberwiseClone ReferenceEquals Finalize hashCode runtimeType toString noSuchMethod toJson
fromJson copyWith props readFrom writeTo encode decodeValue decodeList writeValue writeField
isEnum String Number Boolean Date`)

var fzStdlibWords = strings.Fields(`
JsonElement JsonSerializer JsonDocument JsonException JsonValueKind CultureInfo Dictionary
IReadOnlyList IList IEnumerable ICollection StringBuilder Encoding BitConverter Span ReadOnlySpan
Memory ArgumentException ArgumentOutOfRangeException ArgumentNullException
InvalidOperationException OverflowException NotSupportedException NotImplementedException
Collections Generic Globalization Diagnostics Linq Json Stopwatch File Console Func Lazy Char Byte
Single DateOnly TimeOnly Obsolete ObsoleteAttribute Serializable Flags Assembly Thread Interlocked
Math Convert Object Int32 Int64 Double Decimal DateTime DateTimeOffset TimeSpan Guid Uri Array
Exception Attribute Enum Type Nullable Error Map Set WeakMap Promise RegExp JSON Intl Reflect
Proxy globalThis Symbol Iterable Iterator Future Stream Completer Duration BigInt num double int
bool dynamic Never Null Record Comparable Pattern Sink StringBuffer UriData utf8 jsonDecode
jsonEncode identical Month Weekday Location Builder Reader Buffer Context Errors Unwrap fmt errors
math sort bytes io os unicode utf16 big http url regexp bufio Scanner NaN Infinity console
process window document self Option Some None Ok Err Result Self List Iterable Override Required
Readonly Partial Pick Omit Awaited Headers Request Response URL TextEncoder Uint8Array`)

var fzOrdinaryWords = strings.Fields(`
alpha bravo charlie delta echo foxtrot owner region user note order item count total amount
label tag size ratio score started ended expires attempts limit quota tenant project resource
zone shard bucket ledger invoice account balance currency token session device channel topic
queue batch window offset cursor page rank level stage phase step tier plan seat slot lane
inner outer child parent node leaf root branch entry record_id user_id`)

// fzDecorations are prefixes and suffixes our generators (and the reviewers) used to build
// derived identifiers: New<X>, decode<X>, meta<X>, With<X>, <X>Values, __<X>Wire, ...
var fzPrefixes = []string{"new", "decode", "meta", "is", "with", "write", "read", "parse", "encode", "to", "from", "get", "set", "has", "underscore"}
var fzSuffixes = []string{"problem", "values", "wire", "type", "s", "1", "2", "json", "value", "list", "data", "meta", "error", "exception", "x", "problem_problem"}

var (
	fzIDRe    = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	fzFieldRe = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	fzTypeRe  = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
)

// fzWords splits an identifier into lower-case words: "IReadOnlyList" -> i read only list,
// "__Read" -> read, "writeJSON" -> write json, "int64" -> int64, "a_1" -> a 1.
func fzWords(s string) []string {
	var out []string
	var cur []rune
	rs := []rune(s)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ':
			flush()
		case unicode.IsUpper(r):
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			prevUpper := i > 0 && unicode.IsUpper(rs[i-1])
			if prevLower || (prevUpper && nextLower) {
				flush()
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

func fzSnake(ws []string) string { return strings.Join(ws, "_") }
func fzCamel(ws []string) string {
	var b strings.Builder
	for i, w := range ws {
		if i == 0 {
			b.WriteString(w)
		} else if w != "" {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}
func fzPascal(ws []string) string {
	var b strings.Builder
	for _, w := range ws {
		if w != "" {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

// fzForm renders words for a role: 'p' problem id, 'e' enum value, 'f' field, 't' type.
// It returns "" when the role's pattern cannot hold the words.
func fzForm(role byte, ws []string) string {
	if len(ws) == 0 {
		return ""
	}
	var s string
	var re *regexp.Regexp
	max := 64
	switch role {
	case 'p', 'e':
		s, re = fzSnake(ws), fzIDRe
		if role == 'p' {
			max = 63
		}
	case 'f':
		s, re = fzCamel(ws), fzFieldRe
	default:
		s, re = fzPascal(ws), fzTypeRe
	}
	if len(s) > max || !re.MatchString(s) {
		return ""
	}
	return s
}

func fzRoleRe(role byte) *regexp.Regexp {
	switch role {
	case 'p', 'e':
		return fzIDRe
	case 'f':
		return fzFieldRe
	}
	return fzTypeRe
}

// fzAllRaw is every adversarial raw identifier, deduplicated, in a fixed order.
func fzAllRaw() []string {
	seen := map[string]bool{}
	var out []string
	add := func(xs ...string) {
		for _, x := range xs {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	add(fzKeywordWords...)
	add(fzGeneratedWords...)
	add(fzStdlibWords...)
	add(sortedNames(coreTypeNames)...)
	add(sortedNames(reservedTypeNames)...)
	add(sortedNames(reservedFieldNames)...)
	add(csRecordMembers...)
	add(dartObjectMembers...)
	return out
}

// fzScreen is the per-role list of adversarial names that lint accepts on their own (so a
// candidate built mostly from them has a fair chance to be kept), plus the raw list.
type fzScreen struct {
	raw       []string
	survivors map[byte][]string
	refused   map[byte]int
}

var (
	fzAloneMu    sync.Mutex
	fzAloneCache = map[string]bool{}
)

func fzAloneCached(role byte, n string) bool {
	k := string(role) + n
	fzAloneMu.Lock()
	ok, hit := fzAloneCache[k]
	fzAloneMu.Unlock()
	if hit {
		return ok
	}
	ok = fzAloneOK(role, n)
	fzAloneMu.Lock()
	fzAloneCache[k] = ok
	fzAloneMu.Unlock()
	return ok
}

var (
	fzScreenOnce sync.Once
	fzScreened   *fzScreen
)

func fzScreenNames() *fzScreen {
	fzScreenOnce.Do(func() {
		s := &fzScreen{raw: fzAllRaw(), survivors: map[byte][]string{}, refused: map[byte]int{}}
		for _, role := range []byte{'p', 'e', 'f', 't'} {
			seen := map[string]bool{}
			for _, raw := range s.raw {
				forms := []string{fzForm(role, fzWords(raw))}
				if fzRoleRe(role).MatchString(raw) {
					forms = append(forms, raw)
				}
				for _, n := range forms {
					if n == "" || seen[n] {
						continue
					}
					seen[n] = true
					if fzAloneOK(role, n) {
						s.survivors[role] = append(s.survivors[role], n)
					} else {
						s.refused[role]++
					}
				}
			}
		}
		fzScreened = s
	})
	return fzScreened
}

// fzAloneOK lints a minimal definition that uses name once in the given role.
func fzAloneOK(role byte, n string) bool {
	var src string
	const head = "apiVersion: problems.atomi.cloud/v1\nkind: ProblemSet\nmodule: m\nversion: v1\n"
	switch role {
	case 'p':
		src = head + "problems:\n  " + n + ": { title: T, status: 400, recoverable: true, detail: d }\n"
	case 'f':
		src = head + "problems:\n  pp:\n    title: T\n    status: 400\n    recoverable: true\n    detail: d\n    data:\n      " + n + ": { type: string }\n"
	case 'e':
		src = head + "types:\n  Ee: { enum: [" + n + "] }\nproblems:\n  pp:\n    title: T\n    status: 400\n    recoverable: true\n    detail: d\n    data:\n      xx: { type: Ee }\n"
	case 't':
		// both an enum and an object of that name must pass
		for _, body := range []string{"{ enum: [aa] }", "{ fields: { aa: { type: string } } }"} {
			src = head + "types:\n  " + n + ": " + body + "\nproblems:\n  pp:\n    title: T\n    status: 400\n    recoverable: true\n    detail: d\n    data:\n      xx: { type: " + n + " }\n"
			if _, ds := fzLint("alone.yaml", []byte(src)); len(ds) > 0 {
				return false
			}
		}
		return true
	}
	_, ds := fzLint("alone.yaml", []byte(src))
	return len(ds) == 0
}

// fzLint is LoadModule on bytes: the real parser, schema, resolver and lint.
func fzLint(name string, src []byte) (*Module, []Diag) {
	d, ds := ParseDef(name, src)
	if len(ds) > 0 {
		return nil, ds
	}
	if d.Kind != "ProblemSet" {
		return nil, []Diag{{"P004", Pos{File: name}, "expected kind ProblemSet"}}
	}
	m, rd := Resolve(d)
	ds = append(ds, rd...)
	ds = append(ds, Lint(m)...)
	return m, ds
}

// ---- text pools ----

var fzUnicodeText = []string{
	"café", "naïve", "Ångström", "日本語のテキスト", "中文字符", "한국어", "😀", "👩\u200d👩\u200d👧\u200d👦",
	"🏳\ufe0f\u200d🌈", "עברית", "مرحبا بالعالم", "e\u0301", "a\u0308\u0332", "x\u200dy", "zero\u200bwidth",
	"ﬁ", "straße", "İstanbul", "ǅ", "Σίσυφος", "𝄞", "🧑🏽\u200d💻",
	"\U0001F1EF\U0001F1F5", "ñ", "Ω≈ç√∫", "°C", "€100", "½", "‰", "…", "«quoted»", "„low“", "‘single’",
	"\u00a0nbsp", "\u3000ideo", "Ⅻ", "١٢٣", "ไทย", "ᚠᛇᚻ", "ⓐⓑ",
}

var fzASCIIText = []string{
	`"`, `'`, `\`, `\\`, `$`, `${x}`, "`", "$$", `\n`, `\u0000`, `\x00`, "%s", "%v", "%d", "%%",
	"*/", "/*", "//", "///", "<summary>", "</summary>", "&amp;", "&", "<", ">", "<!--", "-->", "#",
	"@", "?", "'''", `"""`, "<T>", `\"`, "$'", "#{x}", "<script>", ";", "--", "nil", "null",
	"undefined", "NaN", "#region", "#!", "::", "=>", "?.", "??", "||", "&&", "\\u{1F600}", "\\U0001F600",
	"@\"verbatim\"", "r'raw'", "${{", "it's", `say "hi"`, "C:\\path\\to", "a|b", "100%", "x=y",
	"line1 line2", "tab	sep", "  spaced  ", "-", "_", "~", "^", "!", "=", "+",
}

// fzAwkward are the code points readers disagree on (NEL, LS/PS, BOM, C1, non-characters).
var fzAwkward = []string{"\u200eltr", "\u202ertl-override\u202c", "\u0085", "\u2028", "\u2029", "\ufeff", "\u0080", "\u009f", "\u009b", "\ufffe", "\uffff", "\U0010FFFF", "\U000E0001", "\u061c", "\u2066"}

// ---- definition model (generation side) ----

type fzField struct {
	name               string
	kind               string // a K* constant
	format             string
	ref                string
	item               *fzField
	optional, private  bool
	desc, deprecated   string
	example            string // rendered YAML scalar, "" for none
	descSet, deprecSet bool
}

type fzType struct {
	name   string
	enum   []string
	fields []*fzField
	desc   string
	rank   int
}

type fzProblem struct {
	id, title         string
	status            int
	recoverable       bool
	desc, deprecated  string
	detail, log       string
	retryAfter        string
	data              []*fzField
	hasDesc, hasDepr  bool
	flowStyle         bool
	importedNeighbour bool
}

type fzImport struct {
	only   []string
	scalar bool
}

type fzDef struct {
	module, version string
	imports         []fzImport
	types           []*fzType
	problems        []*fzProblem
}

type fz struct {
	r      *rand.Rand
	sc     *fzScreen
	theme  []string
	def    *fzDef
	typeBy map[string]*fzType
}

func (g *fz) p(x float64) bool { return g.r.Float64() < x }
func (g *fz) pick(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[g.r.IntN(len(xs))]
}

// rawWord draws one adversarial or ordinary raw identifier.
func (g *fz) rawWord() string {
	if g.p(0.15) {
		return g.pick(fzOrdinaryWords)
	}
	return g.pick(g.sc.raw)
}

// name draws a name for a role, unique within taken.
func (g *fz) name(role byte, taken map[string]bool) string {
	for try := 0; try < 40; try++ {
		n := g.name1(role)
		// A name lint refuses on its own is let through rarely: it only feeds the refusal
		// counts. Names that pass alone are what the kept definitions are made of.
		if n != "" && !fzAloneCached(role, n) && !g.p(0.04) {
			continue
		}
		if n != "" && !taken[n] {
			taken[n] = true
			return n
		}
	}
	for i := 0; ; i++ {
		n := fzForm(role, []string{"zz", strconv.Itoa(i)})
		if role == 'f' {
			n = "zz" + strconv.Itoa(i)
		}
		if !taken[n] {
			taken[n] = true
			return n
		}
	}
}

func (g *fz) name1(role byte) string {
	x := g.r.Float64()
	switch {
	case x < 0.30 && len(g.theme) > 0:
		return g.variant(role, g.pick(g.theme))
	case x < 0.80:
		if g.p(0.25) {
			return g.variant(role, g.pick(g.sc.survivors[role]))
		}
		return g.pick(g.sc.survivors[role])
	case x < 0.88:
		return g.variant(role, g.pick(g.sc.raw))
	default:
		return g.variant(role, g.pick(fzOrdinaryWords))
	}
}

// variant applies the decorations generators use: a prefix, a suffix, a case or underscore
// change, digits split off, or a long repetition.
func (g *fz) variant(role byte, raw string) string {
	ws := fzWords(raw)
	if len(ws) == 0 {
		return ""
	}
	switch g.r.IntN(9) {
	case 0:
		pre := g.pick(fzPrefixes)
		if pre == "underscore" {
			pre = "x" // a leading underscore cannot pass any pattern; keep the shape
		}
		ws = append([]string{pre}, ws...)
	case 1:
		ws = append(append([]string{}, ws...), strings.Split(g.pick(fzSuffixes), "_")...)
	case 2: // collapse words: fooBar -> foobar, foo_bar -> foobar
		ws = []string{strings.Join(ws, "")}
	case 3: // split digits: int64 -> int_64, a1 -> a_1
		var out []string
		for _, w := range ws {
			i := strings.IndexFunc(w, unicode.IsDigit)
			if i > 0 {
				out = append(out, w[:i], w[i:])
			} else {
				out = append(out, w)
			}
		}
		ws = out
	case 4: // raw spelling when the role allows it (keeps writeJSON, hashCode, IReadOnlyList)
		if fzRoleRe(role).MatchString(raw) {
			return raw
		}
	case 5: // upper-case run inside a camel/pascal name (role f/t only): fooBAR
		if role == 'f' || role == 't' {
			n := fzForm(role, ws)
			if len(n) > 2 {
				return n[:len(n)-2] + strings.ToUpper(n[len(n)-2:])
			}
		}
	case 6: // long names near the length limits
		for len(strings.Join(ws, "_")) < 56 {
			ws = append(ws, ws[len(ws)-1])
		}
	}
	return fzForm(role, ws)
}

// ---- text ----

type fzCtx int

const (
	ctxTitle   fzCtx = iota // no braces, no C0
	ctxRecord               // record-bound: braces allowed but {{ / }} refused (L240)
	ctxFree                 // not record-bound (deprecation notes, private descriptions)
	ctxLiteral              // template literal text (braces are escaped by the caller)
	ctxDefault              // |default:text — no { } | or control characters
	ctxData                 // runtime string data: anything, including controls
)

func (g *fz) fragment(ctx fzCtx) string {
	x := g.r.Float64()
	switch {
	case x < 0.01 && ctx != ctxDefault:
		return g.pick(fzAwkward)
	case x < 0.013 && ctx != ctxDefault && ctx != ctxData:
		return string(rune(g.r.IntN(0x20))) // C0: the schema refuses it (P006)
	case x < 0.35:
		return g.pick(fzUnicodeText)
	case x < 0.60:
		f := g.pick(fzASCIIText)
		if ctx == ctxDefault {
			f = strings.Map(func(r rune) rune {
				if r == '{' || r == '}' || r == '|' || r < 0x20 || r == 0x7f {
					return 'x'
				}
				return r
			}, f)
		}
		return f
	case x < 0.70:
		switch ctx {
		case ctxRecord:
			if g.p(0.08) {
				return g.pick([]string{"{{", "}}", "{{ .Values }}"}) // L240
			}
			return g.pick([]string{"{", "}", "{x}", "}{", "{ }"})
		case ctxFree, ctxLiteral, ctxData:
			return g.pick([]string{"{", "}", "{{", "}}", "{x}", "{{.Values.x}}", "}{"})
		}
		return "x"
	case x < 0.75 && ctx == ctxData:
		return g.pick([]string{"\n", "\t", "\r\n", "\x00", "\x1b[31m", "\x7f", "\x08", "\x0c", "\x1f"})
	default:
		return g.pick(fzOrdinaryWords)
	}
}

func (g *fz) text(ctx fzCtx, minF, maxF int) string {
	n := minF + g.r.IntN(maxF-minF+1)
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, g.fragment(ctx))
	}
	sep := " "
	if g.p(0.3) {
		sep = ""
	}
	s := strings.Join(parts, sep)
	if ctx == ctxTitle {
		s = strings.Map(func(r rune) rune {
			if r == '{' || r == '}' {
				return -1
			}
			return r
		}, s)
		if utf8Len(s) > 200 {
			s = string([]rune(s)[:200])
		}
		if strings.TrimSpace(s) == "" {
			s = "Title " + g.pick(fzUnicodeText)
		}
	}
	if ctx == ctxDefault && s == "" {
		s = "-"
	}
	return s
}

func utf8Len(s string) int { return len([]rune(s)) }

// ---- generation ----

func fzCandidate(seed uint64, i int) *fzDef {
	g := &fz{r: rand.New(rand.NewPCG(seed, uint64(i))), sc: fzScreenNames(), typeBy: map[string]*fzType{}}
	d := &fzDef{}
	g.def = d
	d.module = g.pick([]string{"notes", "a", "123", "yes", "on", "null", "x-y", "007", "module", "problem", strings.Repeat("m", 63)})
	d.version = g.pick([]string{"v1", "v0", "v2", "v10", "v999999"})
	for t := 2 + g.r.IntN(3); t > 0; t-- {
		g.theme = append(g.theme, g.rawWord())
	}

	importedIDs := map[string]bool{}
	if g.p(0.3) {
		all := []string{"validation_error", "entity_not_found", "conflict", "unauthenticated", "unauthorized", "invalid_json"}
		imp := fzImport{scalar: g.p(0.5)}
		if g.p(0.5) {
			imp.scalar = false
			for _, id := range all {
				if g.p(0.4) {
					imp.only = append(imp.only, id)
				}
			}
			if len(imp.only) == 0 {
				imp.only = []string{g.pick(all)}
			}
		}
		d.imports = append(d.imports, imp)
		ids := imp.only
		if len(ids) == 0 {
			ids = all
		}
		for _, id := range ids {
			importedIDs[id] = true
		}
		if g.p(0.15) { // diamond: the same import twice
			d.imports = append(d.imports, fzImport{scalar: true})
			for _, id := range all {
				importedIDs[id] = true
			}
		}
	}

	// Named types: a DAG by rank (an object refers only to lower-ranked types).
	typeNames := map[string]bool{}
	if len(d.imports) > 0 {
		typeNames["ValidationIssue"] = g.p(0.9) // occasionally redefine it (L101)
	}
	nTypes := []int{0, 1, 1, 2, 2, 3, 3, 4, 5}[g.r.IntN(9)]
	for i := 0; i < nTypes; i++ {
		t := &fzType{name: g.name('t', typeNames), rank: i}
		if g.p(0.5) {
			t.desc = g.text(ctxRecord, 1, 4)
		}
		if g.p(0.45) {
			taken := map[string]bool{}
			for k := 1 + g.r.IntN(5); k > 0; k-- {
				t.enum = append(t.enum, g.name('e', taken))
			}
			if g.p(0.2) && len(t.enum) > 0 { // case/underscore twins: a1 / a_1
				w := fzWords(t.enum[0])
				if alt := fzForm('e', []string{strings.Join(w, "")}); alt != "" && !taken[alt] {
					taken[alt] = true
					t.enum = append(t.enum, alt)
				}
			}
		} else {
			taken := map[string]bool{}
			for k := 1 + g.r.IntN(5); k > 0; k-- {
				f := g.field(taken, d.types, false)
				t.fields = append(t.fields, f)
			}
		}
		d.types = append(d.types, t)
		g.typeBy[t.name] = t
	}

	// Problems.
	ids := map[string]bool{}
	for id := range importedIDs {
		ids[id] = g.p(0.95) // occasionally clash with an import (L102)
	}
	nProblems := 1 + g.r.IntN(4)
	if len(d.imports) > 0 && g.p(0.1) {
		nProblems = 0
	}
	for i := 0; i < nProblems; i++ {
		var id string
		if i > 0 && g.p(0.25) { // derived ids: foo / new_foo / foo_problem / decode_foo
			base := d.problems[g.r.IntN(len(d.problems))].id
			id = fzForm('p', append([]string{g.pick([]string{"new", "decode", "meta", "is", "with"})}, fzWords(base)...))
			if g.p(0.5) {
				id = fzForm('p', append(fzWords(base), g.pick([]string{"problem", "values", "wire", "data"})))
			}
			if id == "" || ids[id] {
				id = g.name('p', ids)
			} else {
				ids[id] = true
			}
		} else {
			id = g.name('p', ids)
		}
		d.problems = append(d.problems, g.problem(id, d.types))
	}
	if len(d.problems) == 0 && len(d.types) > 0 {
		d.problems = append(d.problems, g.problem(g.name('p', ids), d.types))
	}

	// Every named type must be used (L205): attach unused ones to a problem.
	used := map[string]bool{}
	var mark func(f *fzField)
	mark = func(f *fzField) {
		if f.kind == KRef {
			used[f.ref] = true
		}
		if f.item != nil {
			mark(f.item)
		}
	}
	for _, t := range d.types {
		for _, f := range t.fields {
			mark(f)
		}
	}
	for _, p := range d.problems {
		for _, f := range p.data {
			mark(f)
		}
	}
	for _, t := range d.types {
		if used[t.name] || len(d.problems) == 0 || g.p(0.02) {
			continue
		}
		p := d.problems[g.r.IntN(len(d.problems))]
		taken := map[string]bool{}
		for _, f := range p.data {
			taken[f.name] = true
		}
		f := g.refField(taken, t)
		if len(p.data) < 64 {
			p.data = append(p.data, f)
			used[t.name] = true
		}
	}
	for _, p := range d.problems {
		g.finishProblem(p)
	}
	return d
}

// refField is a field that refers to t, often named after it (Inner + inner).
func (g *fz) refField(taken map[string]bool, t *fzType) *fzField {
	f := &fzField{kind: KRef, ref: t.name}
	if g.p(0.4) {
		f = &fzField{kind: KArray, item: &fzField{kind: KRef, ref: t.name}}
	}
	n := fzForm('f', fzWords(t.name))
	if g.p(0.5) && n != "" && !taken[n] {
		taken[n] = true
		f.name = n
	} else {
		f.name = g.name('f', taken)
	}
	f.optional = g.p(0.3)
	if t.enum != nil || f.kind == KArray {
		f.private = g.p(0.25) // a type reached only through a private field (R2-7)
	}
	g.fieldMeta(f, !f.private)
	return f
}

var fzKinds = []struct {
	k string
	w int
}{{"string", 14}, {"fmt", 6}, {KInteger, 8}, {KInt64, 6}, {KNumber, 6}, {KBoolean, 6}, {KDateTime, 6},
	{"enum", 8}, {"object", 8}, {"arr-scalar", 6}, {"arr-enum", 4}, {"arr-object", 5}}

func (g *fz) kind(types []*fzType) (string, *fzType) {
	var enums, objs []*fzType
	for _, t := range types {
		if t.enum != nil {
			enums = append(enums, t)
		} else {
			objs = append(objs, t)
		}
	}
	for {
		total := 0
		for _, k := range fzKinds {
			total += k.w
		}
		x := g.r.IntN(total)
		var k string
		for _, kk := range fzKinds {
			if x < kk.w {
				k = kk.k
				break
			}
			x -= kk.w
		}
		switch k {
		case "enum", "arr-enum":
			if len(enums) == 0 {
				continue
			}
			return k, enums[g.r.IntN(len(enums))]
		case "object", "arr-object":
			if len(objs) == 0 {
				continue
			}
			return k, objs[g.r.IntN(len(objs))]
		}
		return k, nil
	}
}

func (g *fz) scalar(k string) *fzField {
	f := &fzField{}
	switch k {
	case "string":
		f.kind = KString
	case "fmt":
		f.kind = KString
		f.format = g.pick([]string{"uuid", "date", "uri"})
	default:
		f.kind = k
	}
	return f
}

// field draws one field. inProblem allows private.
func (g *fz) field(taken map[string]bool, types []*fzType, inProblem bool) *fzField {
	k, t := g.kind(types)
	var f *fzField
	switch k {
	case "enum", "object":
		return g.refFieldIn(taken, t, inProblem)
	case "arr-enum", "arr-object":
		f = &fzField{kind: KArray, item: &fzField{kind: KRef, ref: t.name}}
		if n := fzForm('f', fzWords(t.name)); g.p(0.4) && n != "" && !taken[n] {
			taken[n] = true
			f.name = n
		}
	case "arr-scalar":
		sk := []string{"string", "fmt", KInteger, KInt64, KNumber, KBoolean, KDateTime}[g.r.IntN(7)]
		f = &fzField{kind: KArray, item: g.scalar(sk)}
	default:
		f = g.scalar(k)
	}
	if f.name == "" {
		f.name = g.name('f', taken)
	}
	f.optional = g.p(0.3)
	if inProblem {
		f.private = g.p(0.2)
	}
	g.fieldMeta(f, !f.private)
	return f
}

func (g *fz) refFieldIn(taken map[string]bool, t *fzType, inProblem bool) *fzField {
	f := &fzField{kind: KRef, ref: t.name}
	if n := fzForm('f', fzWords(t.name)); g.p(0.45) && n != "" && !taken[n] {
		taken[n] = true
		f.name = n
	} else {
		f.name = g.name('f', taken)
	}
	f.optional = g.p(0.3)
	if inProblem && t.enum != nil {
		f.private = g.p(0.2)
	}
	if inProblem && t.enum == nil && g.p(0.01) {
		f.private = true // a private object can never be logged (L213/L217): refused
	}
	g.fieldMeta(f, !f.private)
	return f
}

// fieldMeta adds description, deprecation and an example. public = record-bound text.
func (g *fz) fieldMeta(f *fzField, public bool) {
	ctx := ctxRecord
	if !public {
		ctx = ctxFree
	}
	if g.p(0.4) {
		f.desc, f.descSet = g.text(ctx, 1, 4), true
	}
	if g.p(0.12) {
		f.deprecated, f.deprecSet = g.text(ctxFree, 1, 3), true
		if strings.TrimSpace(f.deprecated) == "" {
			f.deprecated = "use something else"
		}
	}
	if g.p(0.35) {
		f.example = g.example(f, ctx)
	}
}

func (g *fz) example(f *fzField, ctx fzCtx) string {
	switch f.kind {
	case KArray:
		if g.p(0.05) {
			return `"x"` // L207
		}
		return ""
	case KRef:
		t := g.typeBy[f.ref]
		if t == nil || t.enum == nil {
			if g.p(0.05) {
				return `"x"` // L207
			}
			return ""
		}
		if g.p(0.03) {
			return `"not_a_value"` // L206
		}
		return fzYAMLQuote(g.pick(t.enum))
	case KString:
		switch f.format {
		case "uuid":
			return fzYAMLQuote(g.uuid())
		case "date":
			return fzYAMLQuote(g.date())
		case "uri":
			return fzYAMLQuote(g.pick([]string{"https://例え.jp/パス?q=1", "urn:isbn:0451450523", "mailto:a@b.c", "https://example.com/a%20b#frag", "tag:x,2024:y"}))
		}
		return fzYAMLQuote(g.text(ctx, 1, 3))
	case KInteger:
		if g.p(0.03) {
			return "9007199254740992" // L206
		}
		return strconv.FormatInt(g.safeInt(), 10)
	case KInt64:
		return fzYAMLQuote(strconv.FormatInt(g.int64v(), 10))
	case KNumber:
		if g.p(0.02) {
			return ".inf" // L206
		}
		return g.pick([]string{"1.5", "-0.25", "3", "0", "1e+21", "1.0e-7", "123456.789", "-1"})
	case KBoolean:
		return g.pick([]string{"true", "false"})
	case KDateTime:
		return fzYAMLQuote(g.dateTime())
	}
	return ""
}

func (g *fz) problem(id string, types []*fzType) *fzProblem {
	p := &fzProblem{id: id, title: g.text(ctxTitle, 1, 4), status: 400 + g.r.IntN(200), recoverable: g.p(0.5)}
	if g.p(0.5) {
		p.desc, p.hasDesc = g.text(ctxRecord, 1, 5), true
	}
	if g.p(0.15) {
		p.deprecated, p.hasDepr = g.text(ctxFree, 1, 3), true
		if strings.TrimSpace(p.deprecated) == "" {
			p.deprecated = "use another problem"
		}
	}
	taken := map[string]bool{}
	n := []int{0, 1, 1, 2, 2, 3, 3, 4, 5, 6, 8}[g.r.IntN(11)]
	for i := 0; i < n; i++ {
		p.data = append(p.data, g.field(taken, types, true))
	}
	if g.p(0.03) && len(p.data) > 0 { // a field named like the problem itself (C# CS0542: L231)
		if fn := fzForm('f', fzWords(id)); fn != "" && !taken[fn] {
			taken[fn] = true
			f := g.scalar("string")
			f.name = fn
			p.data = append(p.data, f)
		}
	}
	p.flowStyle = g.p(0.2)
	return p
}

func (g *fz) renderable(f *fzField) bool {
	if f.kind == KRef {
		t := g.typeBy[f.ref]
		return t != nil && t.enum != nil
	}
	return true
}

func (g *fz) placeholder(f *fzField) string {
	s := "{" + f.name
	if f.kind == KArray {
		s += "|count"
	}
	if f.optional {
		s += "|default:" + g.text(ctxDefault, 1, 2)
	}
	return s + "}"
}

func fzEscapeLiteral(s string) string {
	return strings.NewReplacer("{", "{{", "}", "}}").Replace(s)
}

func (g *fz) template(fields []*fzField) string {
	var parts []string
	for _, f := range fields {
		if g.p(0.6) {
			parts = append(parts, fzEscapeLiteral(g.text(ctxLiteral, 0, 2)))
		}
		parts = append(parts, g.placeholder(f))
		if g.p(0.08) {
			parts = append(parts, g.placeholder(f)) // the same field twice
		}
	}
	if g.p(0.7) || len(parts) == 0 {
		parts = append(parts, fzEscapeLiteral(g.text(ctxLiteral, 1, 3)))
	}
	sep := g.pick([]string{" ", "", ": ", " · "})
	s := strings.Join(parts, sep)
	if strings.TrimSpace(s) == "" {
		s = "x"
	}
	return s
}

func (g *fz) finishProblem(p *fzProblem) {
	var public, private []*fzField
	for _, f := range p.data {
		if !g.renderable(f) {
			continue
		}
		if f.private {
			private = append(private, f)
		} else {
			public = append(public, f)
		}
	}
	var inDetail []*fzField
	for _, f := range public {
		if g.p(0.6) {
			inDetail = append(inDetail, f)
		}
	}
	g.r.Shuffle(len(inDetail), func(i, j int) { inDetail[i], inDetail[j] = inDetail[j], inDetail[i] })
	p.detail = g.template(inDetail)
	hasPrivate := false
	for _, f := range p.data {
		hasPrivate = hasPrivate || f.private
	}
	if hasPrivate || g.p(0.5) {
		inLog := append([]*fzField{}, private...)
		for _, f := range public {
			if g.p(0.4) {
				inLog = append(inLog, f)
			}
		}
		g.r.Shuffle(len(inLog), func(i, j int) { inLog[i], inLog[j] = inLog[j], inLog[i] })
		p.log = g.template(inLog)
	}
	if p.recoverable && g.p(0.4) {
		for _, f := range p.data {
			if f.kind == KInteger && !f.optional && !f.private {
				p.retryAfter = f.name
				break
			}
		}
	}
}

// ---- random values ----

func (g *fz) safeInt() int64 {
	switch g.r.IntN(6) {
	case 0:
		return g.pick64([]int64{0, 1, -1, 42, MaxSafeInteger, -MaxSafeInteger, MaxSafeInteger - 1, 1 << 31, -(1 << 31) - 1, 1 << 32})
	case 1:
		return g.r.Int64N(2*MaxSafeInteger+1) - MaxSafeInteger
	default:
		return g.r.Int64N(2001) - 1000
	}
}

func (g *fz) pick64(xs []int64) int64 { return xs[g.r.IntN(len(xs))] }

func (g *fz) int64v() int64 {
	switch g.r.IntN(4) {
	case 0:
		return g.pick64([]int64{0, -1, math.MaxInt64, math.MinInt64, MaxSafeInteger + 2, -MaxSafeInteger - 2, 1 << 62})
	case 1:
		return int64(g.r.Uint64())
	default:
		return g.r.Int64N(1_000_000) - 500_000
	}
}

func (g *fz) number() string {
	switch g.r.IntN(5) {
	case 0:
		return g.pick([]string{"0", "1.5", "0.1", "1e+21", "1e-07", "1.23e-20", "5e-324", "1.7976931348623157e+308",
			"-2.5", "100", "1e+20", "1e-06", "123456789012345680000", "0.30000000000000004", "-1e-07", "9007199254740993",
			"2.2250738585072014e-308", "4.35", "0.000001", "999999999999999900000"})
	case 1:
		return `{"$float":"-0"}`
	case 2:
		f := (g.r.Float64() - 0.5) * math.Pow(10, float64(g.r.IntN(40)-20))
		return strconv.FormatFloat(f, 'g', -1, 64)
	default:
		return strconv.FormatFloat(float64(g.r.IntN(20001)-10000)/100, 'g', -1, 64)
	}
}

func (g *fz) uuid() string {
	const hexd = "0123456789abcdef"
	var b strings.Builder
	for i, n := range []int{8, 4, 4, 4, 12} {
		if i > 0 {
			b.WriteByte('-')
		}
		for k := 0; k < n; k++ {
			b.WriteByte(hexd[g.r.IntN(16)])
		}
	}
	return b.String()
}

func (g *fz) date() string {
	y := 1 + g.r.IntN(9999)
	if g.p(0.2) {
		y = int(g.pick64([]int64{1, 9999, 2000, 2024, 1900}))
	}
	m := 1 + g.r.IntN(12)
	d := 1 + g.r.IntN(28)
	if g.p(0.1) {
		y, m, d = 2024, 2, 29
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}

func (g *fz) dateTime() string {
	y := 1 + g.r.IntN(9999)
	switch g.r.IntN(8) {
	case 0:
		y = 1
	case 1:
		y = 9999
	case 2:
		y = 1970
	}
	mo := 1 + g.r.IntN(12)
	d := 1 + g.r.IntN(28)
	h, mi, s, ms := g.r.IntN(24), g.r.IntN(60), g.r.IntN(60), g.r.IntN(1000)
	if g.p(0.1) {
		y, mo, d = 2024, 2, 29
	}
	if g.p(0.1) {
		mo, d, h, mi, s, ms = 12, 31, 23, 59, 59, 999
	}
	return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d.%03dZ", y, mo, d, h, mi, s, ms)
}

// dataString is runtime data: any UTF-16, including controls and lone surrogates.
func (g *fz) dataString() []uint16 {
	var u []uint16
	for n := g.r.IntN(4); n >= 0; n-- {
		x := g.r.Float64()
		switch {
		case x < 0.06:
			u = append(u, []uint16{0xd800, 0xdbff, 0xdc00, 0xdfff}[g.r.IntN(4)]) // lone surrogate
		case x < 0.10:
			u = append(u, 0xd83d, 0xde00) // a pair, written as escapes
		default:
			u = append(u, utf16.Encode([]rune(g.fragment(ctxData)))...)
		}
	}
	return u
}

// jsonUnits writes a JSON string from UTF-16 units (lone surrogates as \u escapes).
func jsonUnits(u []uint16) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\u%04x`, c)
		case c >= 0xd800 && c <= 0xdbff && i+1 < len(u) && u[i+1] >= 0xdc00 && u[i+1] <= 0xdfff:
			if (c+u[i+1])%2 == 0 {
				fmt.Fprintf(&b, `\u%04x\u%04x`, c, u[i+1])
			} else {
				b.WriteString(string(utf16.DecodeRune(rune(c), rune(u[i+1]))))
			}
			i++
		case c >= 0xd800 && c <= 0xdfff:
			fmt.Fprintf(&b, `\u%04x`, c)
		default:
			b.WriteRune(rune(c))
		}
	}
	b.WriteByte('"')
	return b.String()
}

func jsonStr(s string) string { return jsonUnits(utf16.Encode([]rune(s))) }

// value renders a random valid vector value of type t (JSON text).
func (g *fz) value(m *Module, t FieldType) string {
	switch t.Kind {
	case KString:
		switch t.Format {
		case "uuid":
			if g.p(0.8) {
				return jsonStr(g.uuid())
			}
		case "date":
			if g.p(0.8) {
				return jsonStr(g.date())
			}
		}
		return jsonUnits(g.dataString())
	case KInteger:
		return strconv.FormatInt(g.safeInt(), 10)
	case KInt64:
		return jsonStr(strconv.FormatInt(g.int64v(), 10))
	case KNumber:
		return g.number()
	case KBoolean:
		return strconv.FormatBool(g.p(0.5))
	case KDateTime:
		return jsonStr(g.dateTime())
	case KArray:
		var parts []string
		for n := g.r.IntN(4); n > 0; n-- {
			parts = append(parts, g.value(m, *t.Items))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case KRef:
		nt := m.TypeByName(t.Ref)
		if nt.IsEnum {
			return jsonStr(nt.Values[g.r.IntN(len(nt.Values))])
		}
		return g.object(m, nt.Fields)
	}
	return "null"
}

func (g *fz) object(m *Module, fields []*Field) string {
	var parts []string
	for _, f := range fields {
		if f.Optional && g.p(0.4) {
			continue
		}
		parts = append(parts, jsonStr(f.Name)+":"+g.value(m, f.Type))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// vectors renders one case per problem of the resolved module.
func fzVectors(seed uint64, i int, m *Module, landscape string) string {
	g := &fz{r: rand.New(rand.NewPCG(seed^0x9e3779b97f4a7c15, uint64(i)))}
	var cases []string
	for _, p := range m.Problems {
		c := `{"name":` + jsonStr(p.ID) + `,"problem":` + jsonStr(p.ID)
		if len(p.Data) > 0 {
			c += `,"data":` + g.object(m, p.Data)
		}
		if g.p(0.3) {
			c += `,"instance":` + jsonUnits(g.dataString())
		}
		cases = append(cases, c+"}")
	}
	return "{\"landscape\":" + jsonStr(landscape) + ",\"cases\":[\n" + strings.Join(cases, ",\n") + "\n]}\n"
}

// ---- YAML rendering ----

// fzYAMLQuote writes a YAML double-quoted scalar: printable text literal, everything else
// (C0, C1, NEL, LS/PS, BOM, non-characters, format characters) as an escape.
func fzYAMLQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == ' ':
			b.WriteByte(' ')
		case r == 0x85:
			b.WriteString(`\N`)
		case r == 0x2028:
			b.WriteString(`\L`)
		case r == 0x2029:
			b.WriteString(`\P`)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			fmt.Fprintf(&b, `\x%02x`, r)
		case r > 0xffff && !unicode.IsPrint(r):
			fmt.Fprintf(&b, `\U%08x`, r)
		case !unicode.IsPrint(r) && !unicode.Is(unicode.Mn, r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (f *fzField) yaml(ind string) string {
	var b strings.Builder
	w := func(k, v string) { fmt.Fprintf(&b, "%s  %s: %s\n", ind, k, v) }
	fmt.Fprintf(&b, "%s%s:\n", ind, f.name)
	item := ""
	switch f.kind {
	case KArray:
		w("type", "array")
		it := f.item
		t := it.kind
		if t == KRef {
			t = it.ref
		}
		item = "{ type: " + t
		if it.format != "" {
			item += ", format: " + it.format
		}
		item += " }"
		w("items", item)
	case KRef:
		w("type", f.ref)
	default:
		w("type", f.kind)
		if f.format != "" {
			w("format", f.format)
		}
	}
	if f.optional {
		w("optional", "true")
	}
	if f.private {
		w("private", "true")
	}
	if f.descSet {
		w("description", fzYAMLQuote(f.desc))
	}
	if f.example != "" {
		w("example", f.example)
	}
	if f.deprecSet {
		w("deprecated", fzYAMLQuote(f.deprecated))
	}
	return b.String()
}

func (d *fzDef) yaml() string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...); b.WriteByte('\n') }
	w("apiVersion: problems.atomi.cloud/v1")
	w("kind: ProblemSet")
	w("module: %s", fzYAMLQuote(d.module))
	w("version: %s", d.version)
	if len(d.imports) > 0 {
		w("imports:")
		for _, imp := range d.imports {
			if imp.scalar {
				w("  - builtin:generic/v1")
			} else if len(imp.only) == 0 {
				w("  - { from: builtin:generic/v1 }")
			} else {
				w("  - { from: builtin:generic/v1, only: [%s] }", strings.Join(imp.only, ", "))
			}
		}
	}
	if len(d.types) > 0 {
		w("types:")
		for _, t := range d.types {
			w("  %s:", t.name)
			if t.desc != "" {
				w("    description: %s", fzYAMLQuote(t.desc))
			}
			if t.enum != nil {
				var vs []string
				for _, v := range t.enum {
					vs = append(vs, fzYAMLQuote(v))
				}
				w("    enum: [%s]", strings.Join(vs, ", "))
				continue
			}
			w("    fields:")
			for _, f := range t.fields {
				b.WriteString(f.yaml("      "))
			}
		}
	}
	if len(d.problems) == 0 {
		w("problems: {}")
		return b.String()
	}
	w("problems:")
	for _, p := range d.problems {
		w("  %s:", p.id)
		w("    title: %s", fzYAMLQuote(p.title))
		w("    status: %d", p.status)
		w("    recoverable: %v", p.recoverable)
		if p.hasDesc {
			w("    description: %s", fzYAMLQuote(p.desc))
		}
		w("    detail: %s", fzYAMLQuote(p.detail))
		if p.log != "" {
			w("    log: %s", fzYAMLQuote(p.log))
		}
		if p.retryAfter != "" {
			w("    retryAfter: %s", p.retryAfter)
		}
		if p.hasDepr {
			w("    deprecated: %s", fzYAMLQuote(p.deprecated))
		}
		if len(p.data) > 0 {
			w("    data:")
			for _, f := range p.data {
				b.WriteString(f.yaml("      "))
			}
		}
	}
	return b.String()
}

// ---- driver ----

// FuzzStats is the summary random-defs writes (summary.json).
type FuzzStats struct {
	Seed              uint64         `json:"seed"`
	Candidates        int            `json:"candidates"`
	Kept              int            `json:"kept"`
	Refused           int            `json:"refused"`
	RefusedByCode     map[string]int `json:"refusedByCode"`     // candidates refused, per code they carried
	DiagnosticsByCode map[string]int `json:"diagnosticsByCode"` // every diagnostic, per code
	Batches           []string       `json:"batches"`
	ScreenSurvivors   map[string]int `json:"screenSurvivors"` // adversarial names lint accepts alone, per role
	ScreenRefused     map[string]int `json:"screenRefused"`
}

// FuzzKept is one kept definition.
type FuzzKept struct {
	Key       string
	Candidate int
	YAML      string
	Module    *Module
}

// FuzzRun draws candidates for seed until n are kept (or maxCandidates are drawn).
func FuzzRun(seed uint64, n, maxCandidates int, refused func(i int, src string, ds []Diag)) ([]FuzzKept, *FuzzStats) {
	st := &FuzzStats{Seed: seed, RefusedByCode: map[string]int{}, DiagnosticsByCode: map[string]int{}}
	var kept []FuzzKept
	for i := 0; len(kept) < n && i < maxCandidates; i++ {
		st.Candidates++
		src := fzCandidate(seed, i).yaml()
		m, ds := fzLint(fmt.Sprintf("candidate-%d.yaml", i), []byte(src))
		if len(ds) > 0 {
			st.Refused++
			codes := map[string]bool{}
			for _, d := range ds {
				st.DiagnosticsByCode[d.Code]++
				codes[d.Code] = true
			}
			for c := range codes {
				st.RefusedByCode[c]++
			}
			if refused != nil {
				refused(i, src, ds)
			}
			continue
		}
		kept = append(kept, FuzzKept{Candidate: i, YAML: src, Module: m})
	}
	st.Kept = len(kept)
	return kept, st
}

// fzBatchConfig renders problemgen.yaml for one batch directory.
func fzBatchConfig(r *rand.Rand, keys []string) string {
	pick := func(xs []string) string { return xs[r.IntN(len(xs))] }
	var b strings.Builder
	b.WriteString("apiVersion: problems.atomi.cloud/v1\nkind: GeneratorConfig\n")
	fmt.Fprintf(&b, "platform: %s\n", fzYAMLQuote(pick([]string{"nitroso", "123", "yes", "on", "a", "x--y", "0", strings.Repeat("p", 63)})))
	fmt.Fprintf(&b, "service: %s\n", fzYAMLQuote(pick([]string{"zinc", "007", "no", "off", "b", "null", "true", "s-1"})))
	fmt.Fprintf(&b, "portal: %s\n", pick([]string{"https://errors.atomi.cloud", "https://errors.example.com:8443", "https://x"}))
	b.WriteString("modules:\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "  - definition: defs/%s.problems.yaml\n    outputs:\n", k)
		fmt.Fprintf(&b, "      go: { file: go/gen/%s/problems.gen.go, package: %s, runtime: github.com/AtomiCloud/diene/problemrt }\n", k, k)
		fmt.Fprintf(&b, "      typescript: { file: ts/gen/%s.gen.ts, runtime: \"@atomicloud/problemrt\" }\n", k)
		fmt.Fprintf(&b, "      csharp: { file: cs/Gen/%s.g.cs, namespace: FuzzZq.%s, runtime: AtomiCloud.Problems.Runtime }\n", k, strings.ToUpper(k[:1])+k[1:])
		fmt.Fprintf(&b, "      dart: { file: dart/lib/gen/%s.g.dart, runtime: \"package:problemrt/problemrt.dart\" }\n", k)
		fmt.Fprintf(&b, "      record: { file: record/%s.yaml }\n", k)
		fmt.Fprintf(&b, "      catalog: { file: catalog/%s.json }\n", k)
	}
	return b.String()
}

// cmdRandomDefs: problemgen conformance random-defs -seed S -n N -out DIR [-batch B]
func cmdRandomDefs(args []string) int {
	fs := flag.NewFlagSet("random-defs", flag.ContinueOnError)
	seed := fs.Uint64("seed", 1, "seed")
	n := fs.Int("n", 300, "definitions to keep")
	out := fs.String("out", "", "output directory")
	batch := fs.Int("batch", 25, "definitions per batch")
	maxC := fs.Int("max-candidates", 200000, "give up after this many candidates")
	samples := fs.Int("refused-samples", 3, "refused candidates to keep per diagnostic code")
	defs := fs.String("defs", "", "comma-separated definition files to batch instead of random ones (repro replay)")
	if err := fs.Parse(args); err != nil || *out == "" || *batch < 1 {
		fmt.Fprintln(os.Stderr, "usage: problemgen conformance random-defs -seed S -n N -out DIR [-batch B]")
		return 2
	}
	sampled := map[string]int{}
	refDir := filepath.Join(*out, "refused")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var kept []FuzzKept
	var st *FuzzStats
	if *defs != "" {
		// Replay mode (fuzz/repro): the given definitions instead of random ones; each must
		// lint clean. Vectors are still random (seed, file index).
		st = &FuzzStats{Seed: *seed, RefusedByCode: map[string]int{}, DiagnosticsByCode: map[string]int{}}
		for i, f := range strings.Split(*defs, ",") {
			src, err := os.ReadFile(f)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			m, ds := fzLint(f, src)
			st.Candidates++
			if len(ds) > 0 {
				// A repro that lint now refuses is fixed at the lint stage: report, skip.
				st.Refused++
				codes := map[string]bool{}
				for _, d := range ds {
					st.DiagnosticsByCode[d.Code]++
					codes[d.Code] = true
				}
				for c := range codes {
					st.RefusedByCode[c]++
				}
				fmt.Printf("refused by lint (fixed at lint): %s: %s\n", f, ds[0])
				continue
			}
			kept = append(kept, FuzzKept{Candidate: i, YAML: string(src), Module: m})
		}
		st.Kept = len(kept)
		*n = len(kept)
	} else {
		kept, st = FuzzRun(*seed, *n, *maxC, func(i int, src string, ds []Diag) {
			c := ds[0].Code
			if sampled[c] >= *samples {
				return
			}
			sampled[c]++
			var hdr strings.Builder
			for _, d := range ds {
				fmt.Fprintf(&hdr, "# %s\n", strings.ReplaceAll(d.String(), "\n", " "))
			}
			_ = os.WriteFile(filepath.Join(refDir, fmt.Sprintf("%s-%d.yaml", c, i)), []byte(hdr.String()+src), 0o644)
		})
	}
	sc := fzScreenNames()
	st.ScreenSurvivors, st.ScreenRefused = map[string]int{}, map[string]int{}
	for _, role := range []byte{'p', 'e', 'f', 't'} {
		st.ScreenSurvivors[string(role)] = len(sc.survivors[role])
		st.ScreenRefused[string(role)] = sc.refused[role]
	}
	br := rand.New(rand.NewPCG(*seed, 0xba7c4))
	for start := 0; start < len(kept); start += *batch {
		end := min(start+*batch, len(kept))
		name := fmt.Sprintf("batch-%03d", start / *batch)
		dir := filepath.Join(*out, name)
		if err := os.MkdirAll(filepath.Join(dir, "defs"), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		landscape := []string{"lapras", "123", "on", "a", "x-1", strings.Repeat("l", 63)}[br.IntN(6)]
		var keys []string
		for k := start; k < end; k++ {
			key := fmt.Sprintf("f%04d", k)
			kept[k].Key = key
			keys = append(keys, key)
			hdr := fmt.Sprintf("# random-defs seed %d candidate %d\n", *seed, kept[k].Candidate)
			if err := os.WriteFile(filepath.Join(dir, "defs", key+".problems.yaml"), []byte(hdr+kept[k].YAML), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			vec := fzVectors(*seed, kept[k].Candidate, kept[k].Module, landscape)
			if err := os.WriteFile(filepath.Join(dir, "defs", key+".vectors.json"), []byte(vec), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "problemgen.yaml"), []byte(fzBatchConfig(br, keys)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		st.Batches = append(st.Batches, name)
	}
	js, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "summary.json"), append(js, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	codes := sortedNames(st.RefusedByCode)
	sort.SliceStable(codes, func(i, j int) bool { return st.RefusedByCode[codes[i]] > st.RefusedByCode[codes[j]] })
	var parts []string
	for _, c := range codes {
		parts = append(parts, fmt.Sprintf("%s=%d", c, st.RefusedByCode[c]))
	}
	fmt.Printf("seed %d: %d candidates, %d kept, %d refused (%s), %d batches\n", *seed, st.Candidates, st.Kept, st.Refused, strings.Join(parts, " "), len(st.Batches))
	if st.Kept < *n {
		fmt.Fprintf(os.Stderr, "only %d of %d definitions kept after %d candidates\n", st.Kept, *n, st.Candidates)
		return 1
	}
	return 0
}
