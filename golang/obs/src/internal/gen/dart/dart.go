// Package dart is the Dart generator of `obs gen` (concept section 3.4).
//
// It writes ONE file, signals.g.dart, of types and constants only (same shape as the Go reference):
//
//   - the shared base types, prefixed DurianSignal (owner ruling D88 B12): sealed labels, level
//     markers, an event carrier that fixes its level in its type, name carriers per metric kind
//     and for spans, a bound spec whose of(n) returns a result, and a minimal Result. Every base
//     type has a private constructor and is sealed or final, so only this library can make one;
//   - one label value type per contract type: an enum is a Dart enum, a bounded number and a time
//     bucket are an extension type with a const-checked constructor plus a `<type>Spec` constant
//     whose of(n) returns a result, a calendar date is a final class, a free value is a String;
//   - one labels class per metric and one attributes class per span, each with a generated
//     `entries` getter (no reflection: Dart AOT has none);
//   - one name carrier class and one constant per signal, and data tables of allowed keys.
//
// The call surface is a method on the generated name (checkoutCharges.add(...)), written by the
// interfaces library as an extension on the carrier type; the carrier of each signal is its own
// class, so the labels type is fixed by the receiver. There are no per-metric functions here.
// The hole the compiler cannot close is a non-const Shard(n) (its assert runs in debug builds
// only); the port refuses an out-of-range value and a lint rule closes the hole in CI.
//
// The output is formatted the way `dart format` leaves it (tall style, 80 columns) without
// running it: obs is offline.
package dart

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

func init() { gen.Register(generator{}) }

type generator struct{}

func (generator) Lang() string { return "dart" }

// Files returns signals.g.dart.
func (generator) Files(c *contract.Contract, o gen.Options) ([]gen.File, error) {
	g := &emitter{c: c, pkg: o.Package, declared: map[string]string{}}
	if err := g.plan(); err != nil {
		return nil, err
	}
	return []gen.File{{Path: "signals.g.dart", Content: []byte(g.render())}}, nil
}

type emitter struct {
	c        *contract.Contract
	pkg      string
	declared map[string]string // top-level identifier -> what declared it

	typeRef  map[string]string   // contract type -> the Dart type of a label value
	enumVals map[string][]string // enum type -> member identifiers
	props    map[string][]string // "metrics.x" / "spans.x" -> field identifiers, in label order
	consts   map[string]string   // metric -> the top-level constant that carries it
	hasDate  bool                // the contract has a calendar date type, so the date helper is used
}

// Names the generated file declares itself.
var baseNames = []string{
	"DurianSignalWire", "DurianSignalLabels", "DurianSignalLevel", "DurianSignalResult",
	"DurianSignalOk", "DurianSignalErr", "DurianSignalBoundSpec", "DurianSignalEvent",
	"DurianSignalCounter", "DurianSignalHistogram", "DurianSignalGauge", "DurianSignalSpan",
	"DurianSignalMetricInfo", "DurianSignalSpanInfo", "DurianSignalLevelTrace",
	"DurianSignalLevelDebug", "DurianSignalLevelInfo", "DurianSignalLevelWarn",
	"DurianSignalLevelError", "DurianSignalLevelFatal", "metricTable", "spanTable", "eventNames",
	"_durianSignalDate",
}

// topReserved are the words a lowercase top-level constant may not take: Dart's reserved words and
// built-in identifiers, and the lowercase names dart:core (or the generated file) uses itself, which
// a constant would shadow (const int breaks every int annotation, const override breaks @override).
var topReserved = map[string]bool{
	"abstract": true, "as": true, "base": true, "covariant": true, "deferred": true, "dynamic": true,
	"export": true, "extension": true, "external": true, "factory": true, "get": true, "implements": true,
	"import": true, "interface": true, "late": true, "library": true, "mixin": true, "of": true,
	"on": true, "operator": true, "part": true, "set": true, "sealed": true, "show": true, "hide": true,
	"static": true, "typedef": true, "when": true, "async": true, "sync": true, "type": true,
	"int": true, "num": true, "bool": true, "double": true, "override": true, "deprecated": true,
	"pragma": true, "print": true, "identical": true, "identityHashCode": true,
}

// topName makes a lowercase top-level identifier safe: a reserved or shadowing word gets a trailing _.
func topName(id string) string {
	if reserved[id] || topReserved[id] {
		return id + "_"
	}
	return id
}

// dart:core (and the generated file's own) names a generated type may not take: it would shadow them.
var shadowing = map[string]bool{
	"String": true, "Object": true, "Enum": true, "List": true, "Map": true, "Set": true,
	"Iterable": true, "Duration": true, "DateTime": true, "Type": true, "Function": true,
	"Null": true, "Never": true, "Record": true, "Future": true, "Stream": true,
	"Exception": true, "Error": true, "Symbol": true, "Comparable": true, "Pattern": true,
	"Match": true, "Uri": true, "BigInt": true, "RegExp": true, "Sink": true, "Stopwatch": true,
	"StackTrace": true, "Invocation": true, "Deprecated": true, "Iterator": true, "Runes": true,
}

// Dart reserved words, plus names that clash with what a generated class or enum already has.
var reserved = map[string]bool{
	"assert": true, "break": true, "case": true, "catch": true, "class": true, "const": true,
	"continue": true, "default": true, "do": true, "else": true, "enum": true, "extends": true,
	"false": true, "final": true, "finally": true, "for": true, "if": true, "in": true, "is": true,
	"new": true, "null": true, "rethrow": true, "return": true, "super": true, "switch": true,
	"this": true, "throw": true, "true": true, "try": true, "var": true, "void": true, "while": true,
	"with": true, "await": true, "yield": true, "required": true,
	"hashCode": true, "runtimeType": true, "toString": true, "noSuchMethod": true,
	"values": true, "index": true, "name": true, "wire": true, "entries": true,
}

// clean turns every character that cannot be in an identifier into a word break (it's -> it_s).
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' || r == ' ' || r == '/' {
			return r
		}
		return '_'
	}, s)
}

// pascal makes a type identifier; a name that starts with a digit gets a V.
func pascal(s string) string {
	p := gen.Pascal(clean(s))
	if p == "" {
		return "Value"
	}
	if p[0] >= '0' && p[0] <= '9' {
		return "V" + p
	}
	return p
}

// camel makes a member identifier; a name that starts with a digit gets a v.
func camel(s string) string {
	p := gen.Camel(clean(s))
	if p == "" {
		return "value"
	}
	if p[0] >= '0' && p[0] <= '9' {
		return "v" + p
	}
	return p
}

// declare records a top-level identifier; two contract names that make one identifier fail.
func (g *emitter) declare(id, what string) error {
	if shadowing[id] {
		return fmt.Errorf("%s makes the Dart name %q, which would shadow a dart:core type; rename it", what, id)
	}
	if prev, dup := g.declared[id]; dup {
		return fmt.Errorf("%s and %s both make the Dart name %q; rename one", prev, what, id)
	}
	g.declared[id] = what
	return nil
}

// members makes member identifiers (enum values, label fields) and fails on a clash.
func members(path string, names []string) ([]string, error) {
	seen := map[string]string{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		m := camel(n)
		if reserved[m] {
			m += "_"
		}
		if prev, dup := seen[m]; dup {
			return nil, fmt.Errorf("%s: %q and %q both make the Dart member %q; rename one", path, prev, n, m)
		}
		seen[m] = n
		out = append(out, m)
	}
	return out, nil
}

func (g *emitter) plan() error {
	c := g.c
	g.typeRef = map[string]string{}
	g.enumVals = map[string][]string{}
	g.props = map[string][]string{}
	g.consts = map[string]string{}
	for _, b := range baseNames {
		g.declared[b] = "a generated base type"
	}
	for _, n := range c.TypeOrder {
		t := c.Types[n]
		from := "types." + n
		switch t.Kind {
		case contract.KindEnum:
			if err := g.declare(pascal(n), from); err != nil {
				return err
			}
			g.typeRef[n] = pascal(n)
			vals, err := members(from, t.Values)
			if err != nil {
				return err
			}
			g.enumVals[n] = vals
		case contract.KindInt, contract.KindTime:
			if err := g.declare(pascal(n), from); err != nil {
				return err
			}
			if err := g.declare(camel(n)+"Spec", from); err != nil {
				return err
			}
			g.typeRef[n] = pascal(n)
		case contract.KindDate:
			g.hasDate = true
			if err := g.declare(pascal(n), from); err != nil {
				return err
			}
			g.typeRef[n] = pascal(n)
		case contract.KindFree:
			g.typeRef[n] = "String"
		}
	}
	for _, m := range c.MetricOrder {
		p := "metrics." + m
		g.consts[m] = topName(camel(m))
		for _, id := range []string{pascal(m) + "Labels", pascal(m) + "Metric", g.consts[m]} {
			if err := g.declare(id, p); err != nil {
				return err
			}
		}
		ps, err := members(p+".labels", c.Metrics[m].LabelOrder)
		if err != nil {
			return err
		}
		g.props[p] = ps
	}
	for _, s := range c.SpanOrder {
		p := "spans." + s
		for _, id := range []string{pascal(s) + "Attributes", pascal(s) + "Span", camel(s) + "Span"} {
			if err := g.declare(id, p); err != nil {
				return err
			}
		}
		ps, err := members(p+".attributes", c.Spans[s].AttrOrder)
		if err != nil {
			return err
		}
		g.props[p] = ps
	}
	for _, k := range c.LogOrder {
		p := "logs." + k
		for _, id := range []string{pascal(k) + "Event", camel(k) + "Event"} {
			if err := g.declare(id, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- writer

type w struct{ b strings.Builder }

func (x *w) line(f string, a ...any) {
	if f != "" {
		fmt.Fprintf(&x.b, f, a...)
	}
	x.b.WriteByte('\n')
}

// blank writes one blank line, never two in a row.
func (x *w) blank() {
	if !strings.HasSuffix(x.b.String(), "\n\n") {
		x.b.WriteByte('\n')
	}
}

// dq quotes a string as a Dart single-quoted literal.
func dq(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteString(`\'`)
		case '\\':
			b.WriteString(`\\`)
		case '$':
			b.WriteString(`\$`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('\'')
	return b.String()
}

const width = 80

// superCtor writes `  const Name._() : super._(args);`, wrapped the way dart format wraps it:
// before the colon, then one argument per line.
func (x *w) superCtor(name string, args ...string) {
	one := fmt.Sprintf("  const %s._() : super._(%s);", name, strings.Join(args, ", "))
	if len(one) <= width {
		x.line("%s", one)
		return
	}
	x.line("  const %s._()", name)
	if two := fmt.Sprintf("    : super._(%s);", strings.Join(args, ", ")); len(two) <= width {
		x.line("%s", two)
		return
	}
	x.line("    : super._(")
	for _, a := range args {
		x.line("        %s,", a)
	}
	x.line("      );")
}

// classHead writes `final class Name extends Base {`, wrapped the way dart format wraps it: the
// clause moves to its own line, and a generic base that still does not fit is split too.
func (x *w) classHead(name, ext string) { x.declHead("final class "+name, "extends", ext) }

// declHead writes `<head> <kw> <type> {` (a class or extension type header).
func (x *w) declHead(head, kw, typ string) {
	if one := fmt.Sprintf("%s %s %s {", head, kw, typ); len(one) <= width {
		x.line("%s", one)
		return
	}
	x.line("%s", head)
	if two := fmt.Sprintf("    %s %s {", kw, typ); len(two) <= width {
		x.line("%s", two)
		return
	}
	if i := strings.IndexByte(typ, '<'); i > 0 && strings.HasSuffix(typ, ">") {
		x.line("    %s", kw)
		x.line("        %s<", typ[:i])
		x.line("          %s", typ[i+1:len(typ)-1])
		x.line("        > {")
		return
	}
	x.line("    %s", kw)
	x.line("        %s {", typ)
}

// assign writes `<lhs> = <rhs>;` on one line, or with the right side on the next.
func (x *w) assign(indent int, lhs, rhs string) {
	pad := strings.Repeat(" ", indent)
	if one := pad + lhs + " = " + rhs + ";"; len(one) <= width {
		x.line("%s", one)
		return
	}
	x.line("%s%s =", pad, lhs)
	x.line("%s    %s;", pad, rhs)
}

// assignCall writes `<lhs> = <head>args);` the way dart format does: on one line; else the call
// hugging the `=` with one argument per line; else the call on the next line; else both.
func (x *w) assignCall(lhs, head string, args []string) {
	call := head + strings.Join(args, ", ") + ")"
	if one := lhs + " = " + call + ";"; len(one) <= width {
		x.line("%s", one)
		return
	}
	if first := lhs + " = " + head; len(first) <= width {
		x.line("%s", first)
		for _, a := range args {
			x.line("  %s,", a)
		}
		x.line(");")
		return
	}
	if two := "    " + call + ";"; len(two) <= width {
		x.line("%s =", lhs)
		x.line("%s", two)
		return
	}
	x.line("%s =", lhs)
	x.line("    %s", head)
	for _, a := range args {
		x.line("      %s,", a)
	}
	x.line("    );")
}

// member writes `  <type> <name>;` (a field), moving the name down when it does not fit.
// assign2 writes `<head> <body>`, or the body on the next line (indent 6) when it does not fit.
func (x *w) assign2(head, body string) {
	if one := head + " " + body; len(one) <= width {
		x.line("%s", one)
		return
	}
	x.line("%s", head)
	x.line("      %s", body)
}

func (x *w) field(typ, name string) {
	if one := fmt.Sprintf("  final %s %s;", typ, name); len(one) <= width {
		x.line("%s", one)
		return
	}
	x.line("  final %s", typ)
	x.line("  %s;", name)
}

// entry writes `<pad>'key': value,`, moving the value down when it does not fit.
func (x *w) entry(indent int, key, value string) {
	pad := strings.Repeat(" ", indent)
	if one := fmt.Sprintf("%s%s: %s,", pad, key, value); len(one) <= width {
		x.line("%s", one)
		return
	}
	x.line("%s%s:", pad, key)
	x.line("%s    %s,", pad, value)
}

// tableRow writes one row of a data table: `'name': Info._(kind: ..., keys: [...]),`.
func (x *w) tableRow(name, info string, fields []string, keys []string) {
	ind := 2
	if one := fmt.Sprintf("  %s: %s(", name, info); len(one) <= width {
		x.line("%s", one)
	} else {
		x.line("  %s:", name)
		x.line("      %s(", info)
		ind = 6
	}
	pad := strings.Repeat(" ", ind)
	for _, fl := range fields {
		x.line("%s  %s,", pad, fl)
	}
	x.strList(ind+2, "keys: ", ",", keys)
	x.line("%s),", pad)
}

// strList writes `<prefix>['a', 'b']<suffix>` on one line, or one element per line when it does
// not fit (indent is the column the line starts at).
func (x *w) strList(indent int, prefix, suffix string, items []string) {
	pad := strings.Repeat(" ", indent)
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = dq(s)
	}
	one := pad + prefix + "[" + strings.Join(q, ", ") + "]" + suffix
	if len(one) <= width {
		x.line("%s", one)
		return
	}
	x.line("%s%s[", pad, prefix)
	for _, s := range q {
		x.line("%s  %s,", pad, s)
	}
	x.line("%s]%s", pad, suffix)
}

func levelClass(l string) string { return "DurianSignalLevel" + pascal(l) }

func spanKind(s contract.Span) string {
	if s.Kind == "" {
		return "internal"
	}
	return s.Kind
}

var metricBase = map[string]string{
	contract.MetricCounter:   "DurianSignalCounter",
	contract.MetricHistogram: "DurianSignalHistogram",
	contract.MetricGauge:     "DurianSignalGauge",
}

func (g *emitter) render() string {
	c := g.c
	x := &w{}
	x.b.WriteString(gen.Header(c, "// "))
	x.line("// ignore_for_file: type=lint")
	x.blank()
	x.line("// ---- shared base types ----")
	x.blank()
	// The date helper is used by a calendar date type only: without one it would be an unused element.
	helper := ""
	if g.hasDate {
		helper = dateHelper
	}
	x.b.WriteString(strings.Replace(baseCode, dateMarker, helper, 1))
	for _, l := range contract.Levels {
		x.blank()
		x.line("/// The level marker of %s events.", l)
		x.line("final class %s extends DurianSignalLevel {", levelClass(l))
		x.line("  const %s._() : super._(%s);", levelClass(l), dq(l))
		x.line("}")
	}

	x.blank()
	x.line("// ---- label value types ----")
	for _, n := range c.TypeOrder {
		x.blank()
		g.typeDecl(x, n, c.Types[n])
	}

	x.blank()
	x.line("// ---- metrics ----")
	for _, m := range c.MetricOrder {
		x.blank()
		g.labelsClass(x, pascal(m)+"Labels", "metrics."+m, "The labels of "+m+".", c.Metrics[m].LabelOrder, c.Metrics[m].Labels)
		x.blank()
		x.line("/// The %s %s.", c.Metrics[m].Kind, m)
		x.classHead(pascal(m)+"Metric", fmt.Sprintf("%s<%sLabels>", metricBase[c.Metrics[m].Kind], pascal(m)))
		x.superCtor(pascal(m)+"Metric", dq(m))
		x.line("}")
		x.blank()
		x.line("/// The %s %s.", c.Metrics[m].Kind, m)
		x.assign(0, "const "+g.consts[m], pascal(m)+"Metric._()")
	}
	x.blank()
	x.line("/// The data table of the metrics: kind, unit, budget, pod handling and the allowed")
	x.line("/// label keys.")
	x.line("const metricTable = <String, DurianSignalMetricInfo>{")
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		x.tableRow(dq(n), "DurianSignalMetricInfo._", []string{
			"kind: " + dq(m.Kind), "unit: " + dq(m.Unit), fmt.Sprintf("budget: %d", m.Budget),
			"acrossPods: " + dq(m.AcrossPod), fmt.Sprintf("perPod: %t", m.PerPod),
		}, m.LabelOrder)
	}
	x.line("};")

	x.blank()
	x.line("// ---- log events ----")
	for _, k := range c.LogOrder {
		e := c.Logs[k]
		x.blank()
		x.line("/// The event %s at level %s.", e.Event, e.Level)
		x.classHead(pascal(k)+"Event", "DurianSignalEvent<"+levelClass(e.Level)+">")
		x.superCtor(pascal(k)+"Event", dq(e.Event), dq(e.Level))
		x.line("}")
		x.blank()
		x.line("/// The event %s at level %s.", e.Event, e.Level)
		x.assign(0, "const "+camel(k)+"Event", pascal(k)+"Event._()")
	}
	x.blank()
	x.line("/// Every event name of the contract, in contract order.")
	x.strList(0, "const eventNames = <String>", ";", c.EventNames())

	x.blank()
	x.line("// ---- spans ----")
	for _, s := range c.SpanOrder {
		sp := c.Spans[s]
		x.blank()
		g.labelsClass(x, pascal(s)+"Attributes", "spans."+s, "The attributes of span "+s+".", sp.AttrOrder, sp.Attributes)
		x.blank()
		x.line("/// The span %s.", s)
		x.classHead(pascal(s)+"Span", "DurianSignalSpan<"+pascal(s)+"Attributes>")
		x.superCtor(pascal(s)+"Span", dq(s), dq(spanKind(sp)))
		x.line("}")
		x.blank()
		x.line("/// The span %s.", s)
		x.assign(0, "const "+camel(s)+"Span", pascal(s)+"Span._()")
	}
	x.blank()
	x.line("/// The data table of the spans: kind and allowed attribute keys.")
	x.line("const spanTable = <String, DurianSignalSpanInfo>{")
	for _, n := range c.SpanOrder {
		s := c.Spans[n]
		x.tableRow(dq(n), "DurianSignalSpanInfo._", []string{"kind: " + dq(spanKind(s))}, s.AttrOrder)
	}
	x.line("};")
	return strings.TrimRight(x.b.String(), "\n") + "\n"
}

func (g *emitter) typeDecl(x *w, name string, t contract.Type) {
	id := pascal(name)
	switch t.Kind {
	case contract.KindEnum:
		x.line("/// The enum %s.", name)
		x.line("enum %s implements DurianSignalWire {", id)
		vals := g.enumVals[name]
		for i, v := range t.Values {
			sep := ","
			if i == len(t.Values)-1 {
				sep = ";"
			}
			x.line("  %s(%s)%s", vals[i], dq(v), sep)
		}
		x.blank()
		x.line("  const %s(this.wire);", id)
		x.blank()
		x.line("  @override")
		x.line("  final String wire;")
		x.line("}")
	case contract.KindInt:
		g.boundType(x, id, name, fmt.Sprintf("The bounded number %s, %d..%d.", name, *t.Min, *t.Max), *t.Min, *t.Max, "")
	case contract.KindTime:
		max := 23
		if t.Unit == contract.UnitWeekday {
			max = 6
		}
		g.boundType(x, id, name, fmt.Sprintf("The time bucket %s (%s, 0..%d); it rolls over by itself.", name, t.Unit, max), 0, max, t.Unit)
	case contract.KindDate:
		x.line("/// The calendar date %s: a metric label with %d values live at once", name, t.WindowDays)
		x.line("/// (window_days).")
		x.declHead("final class "+id, "implements", "DurianSignalWire")
		x.line("  const %s._(this.wire);", id)
		x.blank()
		x.line("  /// The UTC calendar date of [t], as YYYY-MM-DD.")
		x.assign2(fmt.Sprintf("  factory %s.of(DateTime t) =>", id), fmt.Sprintf("%s._(_durianSignalDate(t));", id))
		x.blank()
		x.line("  /// How many dates are live at once.")
		x.line("  static const int windowDays = %d;", t.WindowDays)
		x.blank()
		x.line("  @override")
		x.line("  final String wire;")
		x.line("}")
	case contract.KindFree:
		x.line("// %s is a free value (a String): log fields and span attributes only.", name)
	}
}

func (g *emitter) boundType(x *w, id, name, doc string, min, max int, unit string) {
	x.line("/// %s", doc)
	x.line("///")
	x.line("/// A const [%s] outside the range is a compile error; at run time use", id)
	x.line("/// [%sSpec].of, which returns a result.", camel(name))
	x.declHead("extension type const "+id+"._(int value)", "implements", "int")
	cond := fmt.Sprintf("v >= %d && v <= %d", min, max)
	one := fmt.Sprintf("  const %s(int v) : assert(%s), value = v;", id, cond)
	if len(one) <= width {
		x.line("%s", one)
	} else {
		x.line("  const %s(int v)", id)
		x.line("    : assert(%s),", cond)
		x.line("      value = v;")
	}
	x.line("}")
	x.blank()
	x.line("/// The range of [%s]; of(n) returns a result.", id)
	x.assignCall("const "+camel(name)+"Spec", "DurianSignalBoundSpec<"+id+">._(",
		[]string{dq(name), fmt.Sprint(min), fmt.Sprint(max), dq(unit), id + ".new"})
}

func (g *emitter) labelsClass(x *w, id, path, doc string, names []string, types map[string]string) {
	x.line("/// %s", doc)
	x.classHead(id, "DurianSignalLabels")
	props := g.props[path]
	if len(names) == 0 {
		x.line("  const %s() : super._();", id)
		x.blank()
		x.line("  @override")
		x.line("  Map<String, Object> get entries => const {};")
		x.line("}")
		return
	}
	params := make([]string, len(props))
	for i, p := range props {
		params[i] = "required this." + p
	}
	if one := fmt.Sprintf("  const %s({%s}) : super._();", id, strings.Join(params, ", ")); len(one) <= width {
		x.line("%s", one)
	} else {
		x.line("  const %s({", id)
		for _, p := range params {
			x.line("    %s,", p)
		}
		x.line("  }) : super._();")
	}
	x.blank()
	for i, n := range names {
		x.field(g.typeRef[types[n]], props[i])
		if i < len(names)-1 {
			// fields stay together
		}
	}
	x.blank()
	x.line("  @override")
	items := make([]string, len(names))
	for i, n := range names {
		items[i] = fmt.Sprintf("%s: %s", dq(n), props[i])
	}
	if one := fmt.Sprintf("  Map<String, Object> get entries => {%s};", strings.Join(items, ", ")); len(one) <= width {
		x.line("%s", one)
	} else {
		x.line("  Map<String, Object> get entries => {")
		for i, n := range names {
			x.entry(4, dq(n), props[i])
		}
		x.line("  };")
	}
	x.line("}")
}

// baseCode is the shared base types, the same in every contract.
const baseCode = `/// A label value that has a wire form.
abstract interface class DurianSignalWire {
  String get wire;
}

/// The labels or attributes of a signal. Only the generated classes below extend it.
sealed class DurianSignalLabels {
  const DurianSignalLabels._();

  /// The wire name and value of each label, in contract order.
  Map<String, Object> get entries;
}

/// The result of making a bounded number: a value, or the reason it was refused.
sealed class DurianSignalResult<T> {
  const DurianSignalResult._();
}

/// A value.
final class DurianSignalOk<T> extends DurianSignalResult<T> {
  const DurianSignalOk(this.value) : super._();

  final T value;
}

/// The reason a value was refused.
final class DurianSignalErr<T> extends DurianSignalResult<T> {
  const DurianSignalErr(this.error) : super._();

  final String error;
}

/// The range of a bounded number or time bucket. Only this library can make one.
final class DurianSignalBoundSpec<T> {
  const DurianSignalBoundSpec._(
    this.name,
    this.min,
    this.max,
    this.unit,
    this._make,
  );

  final String name;
  final int min;
  final int max;

  /// hour_of_day or weekday for a time bucket, empty for a bounded number.
  final String unit;

  final T Function(int) _make;

  /// Makes a value, or says why [n] is refused (works in release builds).
  DurianSignalResult<T> of(int n) =>
      n < min || n > max
          ? DurianSignalErr<T>('$name: $n is outside $min..$max')
          : DurianSignalOk<T>(_make(n));

  /// The bucket of [t] (always in range for a time bucket).
  DurianSignalResult<T> atTime(DateTime t) => switch (unit) {
    'hour_of_day' => of(t.hour),
    'weekday' => of(t.weekday % 7),
    _ => DurianSignalErr<T>("$name: unit '$unit' is not a time bucket"),
  };
}

@@DATE-HELPER@@/// The level marker base. Only the markers below extend it.
sealed class DurianSignalLevel {
  const DurianSignalLevel._(this.level);

  final String level;
}

/// A log event: a name carrier whose type includes its level.
sealed class DurianSignalEvent<L extends DurianSignalLevel> {
  const DurianSignalEvent._(this.name, this.level);

  /// The event name (the value of the durian.cloud/event field).
  final String name;

  /// The level the type fixes (trace, debug, info, warn, error or fatal).
  final String level;
}

/// A counter name carrier; [L] is its labels class.
sealed class DurianSignalCounter<L extends DurianSignalLabels> {
  const DurianSignalCounter._(this.name);

  final String name;
}

/// A histogram name carrier.
sealed class DurianSignalHistogram<L extends DurianSignalLabels> {
  const DurianSignalHistogram._(this.name);

  final String name;
}

/// A gauge name carrier.
sealed class DurianSignalGauge<L extends DurianSignalLabels> {
  const DurianSignalGauge._(this.name);

  final String name;
}

/// A span name carrier; [A] is its attributes class.
sealed class DurianSignalSpan<A extends DurianSignalLabels> {
  const DurianSignalSpan._(this.name, this.kind);

  final String name;

  /// internal, client, server, producer or consumer.
  final String kind;
}

/// One row of metricTable.
final class DurianSignalMetricInfo {
  const DurianSignalMetricInfo._({
    required this.kind,
    required this.unit,
    required this.budget,
    required this.acrossPods,
    required this.perPod,
    required this.keys,
  });

  /// counter, histogram or gauge.
  final String kind;

  /// The stored name suffix (seconds, bytes), or empty.
  final String unit;

  /// The most series the metric may store per landscape (0: none stated).
  final int budget;

  /// sum, avg, max or last; empty for counters, histograms and per-pod gauges.
  final String acrossPods;

  /// True for a gauge that keeps its pod keys.
  final bool perPod;

  /// The allowed label keys, in contract order.
  final List<String> keys;
}

/// One row of spanTable.
final class DurianSignalSpanInfo {
  const DurianSignalSpanInfo._({required this.kind, required this.keys});

  final String kind;
  final List<String> keys;
}
`

// dateMarker is where baseCode takes dateHelper, which is emitted only when the contract has a calendar date type.
const dateMarker = "@@DATE-HELPER@@"

const dateHelper = `String _durianSignalDate(DateTime t) {
  final u = t.toUtc();
  final m = u.month.toString().padLeft(2, '0');
  final d = u.day.toString().padLeft(2, '0');
  return '${u.year.toString().padLeft(4, '0')}-$m-$d';
}

`
