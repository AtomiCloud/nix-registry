// Package dotnet generates contract-specific types, specs, carriers and key tables.
// Shared bounded values, results, level markers and port contracts come from the
// interfaces library, whose import is configurable per generation target.
// Compiler fixtures use a minimal stub from the goal, not the real library.
package dotnet

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

func init() { gen.Register(generator{}) }

type generator struct{}

func (generator) Lang() string { return "dotnet" }

// Files returns Signals.g.cs.
func (generator) Files(c *contract.Contract, o gen.Options) ([]gen.File, error) {
	ns := o.Namespace
	if ns == "" {
		ns = ident(c.Service) + ".Signals"
	}
	interfaces := o.Interfaces
	if interfaces == "" {
		interfaces = "AtomiCloud.Diene.Interfaces"
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`).MatchString(interfaces) {
		return nil, fmt.Errorf("interfaces %q must be a C# namespace", interfaces)
	}
	g := &emitter{c: c, ns: ns, interfaces: interfaces, packageID: o.PackageID, declared: map[string]string{}}
	if err := g.plan(); err != nil {
		return nil, err
	}
	return []gen.File{{Path: "Signals.g.cs", Content: []byte(g.render())}}, nil
}

type emitter struct {
	c          *contract.Contract
	ns         string
	interfaces string
	packageID  string
	declared   map[string]string // top-level identifier -> what declared it

	typeRef  map[string]string   // contract type -> the C# type of a label value
	enumVals map[string][]string // enum type -> member identifiers
	props    map[string][]string // "metrics.x" / "spans.x" -> property identifiers, in label order
	carrier  map[string][]string // "metrics" / "logs" / "spans" -> member identifiers of Names / Events / Spans
}

// The interfaces ABI uses Int64 for every accepted contract bound.
func (g *emitter) intType() string { return "long" }

// Base types the generated file declares, so a contract name may not take them.
var baseTypes = []string{
	"IDurianSignalWire", "IDurianSignalLabels", "IDurianSignalLevel", "IDurianSignalIntSpec",
	"IDurianSignalDateSpec", "DurianSignalLabelAttribute", "DurianSignalResult", "DurianSignalInt",
	"DurianSignalDate", "DurianSignalEvent", "DurianSignalCounter", "DurianSignalHistogram",
	"DurianSignalGauge", "DurianSignalSpan", "DurianSignalMetricInfo", "DurianSignalSpanInfo",
	"DurianSignalLevelTrace", "DurianSignalLevelDebug", "DurianSignalLevelInfo",
	"DurianSignalLevelWarn", "DurianSignalLevelError", "DurianSignalLevelFatal",
	"DurianSignalContract", "Names", "Events", "Spans", "SignalTables", "Int", "Result", "Event",
	"TraceLevel", "DebugLevel", "InfoLevel", "WarnLevel", "ErrorLevel", "FatalLevel",
}

// Names a generated type may never take: it would shadow a type the file uses.
var shadowing = map[string]bool{
	"String": true, "Object": true, "Int32": true, "Boolean": true, "Double": true, "Attribute": true,
	"Enum": true, "Exception": true, "Type": true, "Dictionary": true, "IReadOnlyList": true,
	"IReadOnlyDictionary": true, "Array": true, "DateOnly": true, "DateTimeOffset": true, "System": true,
}

// Members a generated class or record already has.
var reservedMember = map[string]bool{
	"Wire": true, "Valid": true, "All": true, "ToString": true, "Equals": true, "GetHashCode": true,
	"GetType": true, "MemberwiseClone": true, "Finalize": true, "ReferenceEquals": true,
	"EqualityContract": true, "Clone": true, "PrintMembers": true, "Deconstruct": true,
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

// ident makes a C# identifier: PascalCase; a name that starts with a digit gets a V.
func ident(s string) string {
	p := gen.Pascal(clean(s))
	if p == "" {
		return "Value"
	}
	if p[0] >= '0' && p[0] <= '9' {
		return "V" + p
	}
	return p
}

// declare records a top-level identifier; two contract names that make one identifier fail.
func (g *emitter) declare(id, what string) error {
	if shadowing[id] {
		return fmt.Errorf("%s makes the C# name %q, which would shadow a type the generated code uses; rename it", what, id)
	}
	if prev, dup := g.declared[id]; dup {
		return fmt.Errorf("%s and %s both make the C# name %q; rename one", prev, what, id)
	}
	g.declared[id] = what
	return nil
}

// members makes the identifiers of names (enum values, labels, ...) and fails on a clash.
func members(path, at string, names []string, reserved map[string]bool, self string) ([]string, error) {
	seen := map[string]string{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		m := ident(n)
		if reserved[m] || m == self {
			m += "_"
		}
		if prev, dup := seen[m]; dup {
			return nil, fmt.Errorf("%s: %q and %q both make the C# member %q; rename one (%s)", path, prev, n, m, at)
		}
		seen[m] = n
		out = append(out, m)
	}
	return out, nil
}

// plan resolves every identifier and fails on a clash, so writing cannot fail.
func (g *emitter) plan() error {
	c := g.c
	g.typeRef = map[string]string{}
	g.enumVals = map[string][]string{}
	g.props = map[string][]string{}
	g.carrier = map[string][]string{}
	for _, b := range baseTypes {
		g.declared[b] = "a generated base type"
	}
	for _, n := range c.TypeOrder {
		t := c.Types[n]
		from := "types." + n
		var err error
		switch t.Kind {
		case contract.KindEnum:
			id := ident(n)
			if err = g.declare(id, from); err != nil {
				return err
			}
			g.typeRef[n] = id
			if g.enumVals[n], err = members(from, c.TypeAt(n), t.Values, reservedMember, id); err != nil {
				return err
			}
		case contract.KindInt, contract.KindTime:
			if err = g.declare(ident(n)+"Spec", from); err != nil {
				return err
			}
			g.typeRef[n] = "Int<" + ident(n) + "Spec>"
		case contract.KindDate:
			if err = g.declare(ident(n)+"Spec", from); err != nil {
				return err
			}
			g.typeRef[n] = "DurianSignalDate<" + ident(n) + "Spec>"
		case contract.KindFree:
			g.typeRef[n] = "string"
		}
	}
	for _, m := range c.MetricOrder {
		p := "metrics." + m
		if err := g.declare(ident(m)+"Labels", p); err != nil {
			return err
		}
		ps, err := members(p+".labels", c.At(c.Metrics[m].Line), c.Metrics[m].LabelOrder, reservedMember, ident(m)+"Labels")
		if err != nil {
			return err
		}
		g.props[p] = ps
	}
	for _, s := range c.SpanOrder {
		p := "spans." + s
		if err := g.declare(ident(s)+"Attributes", p); err != nil {
			return err
		}
		ps, err := members(p+".attributes", c.Rel(), c.Spans[s].AttrOrder, reservedMember, ident(s)+"Attributes")
		if err != nil {
			return err
		}
		g.props[p] = ps
	}
	// static members of Names, Events and Spans
	// (a member can not take the name of its enclosing class, so the container's own name is the self name)
	for _, set := range []struct {
		path, self string
		names      []string
	}{{"metrics", "Names", c.MetricOrder}, {"spans", "Spans", c.SpanOrder}, {"logs", "Events", c.LogOrder}} {
		ms, err := members(set.path, c.Rel(), set.names, reservedMember, set.self)
		if err != nil {
			return err
		}
		g.carrier[set.path] = ms
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
	s := x.b.String()
	if !strings.HasSuffix(s, "\n\n") {
		x.b.WriteByte('\n')
	}
}

// cs quotes a string as a C# literal.
func cs(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func strArray(ss []string) string {
	if len(ss) == 0 {
		return "new string[] { }"
	}
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = cs(s)
	}
	return "new string[] { " + strings.Join(q, ", ") + " }"
}

func levelIdent(l string) string { return ident(l) + "Level" }

func (g *emitter) render() string {
	c := g.c
	x := &w{}
	x.b.WriteString(gen.Header(c, "// "))
	x.line("// <auto-generated />")
	x.line("#nullable enable")
	x.line("#pragma warning disable CS1591 // generated members are documented by the contract")
	x.blank()
	x.line("using System;")
	x.line("using System.Collections.Generic;")
	x.line("using %s;", g.interfaces)
	x.blank()
	x.line("namespace %s;", g.ns)
	x.blank()

	x.blank()
	x.line("// ---- label value types ----")
	for _, n := range c.TypeOrder {
		x.blank()
		g.typeDecl(x, n, c.Types[n])
	}

	x.blank()
	x.line("// ---- label records ----")
	for _, m := range c.MetricOrder {
		x.blank()
		g.record(x, ident(m)+"Labels", "metrics."+m, c.Metrics[m].LabelOrder, c.Metrics[m].Labels)
	}
	for _, s := range c.SpanOrder {
		x.blank()
		g.record(x, ident(s)+"Attributes", "spans."+s, c.Spans[s].AttrOrder, c.Spans[s].Attributes)
	}

	x.blank()
	x.line("// ---- name carriers ----")
	if len(c.MetricOrder) > 0 {
		x.blank()
		x.line("/// <summary>The metrics of the contract.</summary>")
		x.line("public static class Names")
		x.line("{")
		for i, m := range c.MetricOrder {
			x.line("    public static readonly %s<%sLabels> %s = new(%s);", metricBase[c.Metrics[m].Kind], ident(m), g.carrier["metrics"][i], cs(m))
		}
		x.line("}")
	}
	if len(c.LogOrder) > 0 {
		x.blank()
		x.line("/// <summary>The log events of the contract; each carrier's type fixes its level.</summary>")
		x.line("public static class Events")
		x.line("{")
		for i, k := range c.LogOrder {
			e := c.Logs[k]
			x.line("    public static readonly Event<%s> %s = new(%s);", levelIdent(e.Level), g.carrier["logs"][i], cs(e.Event))
		}
		x.line("}")
	}
	if len(c.SpanOrder) > 0 {
		x.blank()
		x.line("/// <summary>The spans of the contract.</summary>")
		x.line("public static class Spans")
		x.line("{")
		for i, s := range c.SpanOrder {
			x.line("    public static readonly DurianSignalSpan<%sAttributes> %s = new(%s, %s);", ident(s), g.carrier["spans"][i], cs(s), cs(spanKind(c.Spans[s])))
		}
		x.line("}")
	}

	x.blank()
	x.line("// ---- data tables ----")
	x.blank()
	x.line("/// <summary>The data tables: allowed label keys per metric and span, every event name and its level.</summary>")
	x.line("public static class SignalTables")
	x.line("{")
	x.line("    /// <summary>The package-qualified contract id and hash used at boot registration.</summary>")
	x.line("    public static readonly DurianSignalContract Contract = new(%s, %s);", cs(gen.RegistrationID(c, gen.Options{PackageID: g.packageID})), cs("sha256:"+c.Hash))
	x.blank()
	x.line("    public static readonly IReadOnlyDictionary<string, DurianSignalMetricInfo> Metrics =")
	x.line("        new Dictionary<string, DurianSignalMetricInfo>")
	x.line("        {")
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		var values, ranges []string
		for _, l := range m.LabelOrder {
			t := c.Types[m.Labels[l]]
			switch t.Kind {
			case contract.KindEnum:
				values = append(values, fmt.Sprintf("[%s] = %s", cs(l), strArray(t.Values)))
			case contract.KindInt:
				ranges = append(ranges, fmt.Sprintf("[%s] = new long[] { %d, %d }", cs(l), *t.Min, *t.Max))
			case contract.KindTime:
				hi := 23
				if t.Unit == contract.UnitWeekday {
					hi = 6
				}
				ranges = append(ranges, fmt.Sprintf("[%s] = new long[] { 0, %d }", cs(l), hi))
			}
		}
		extra := ""
		if len(values) > 0 {
			extra += ",\n                Values: new Dictionary<string, string[]> { " + strings.Join(values, ", ") + " }"
		}
		if len(ranges) > 0 {
			extra += ",\n                Ranges: new Dictionary<string, long[]> { " + strings.Join(ranges, ", ") + " }"
		}
		x.line("            [%s] = new(%s, %s, %d, %s, %s, %s%s),", cs(n), cs(m.Kind), cs(m.Unit), m.Budget, cs(m.AcrossPod), boolLit(m.PerPod), strArray(m.LabelOrder), extra)
	}
	x.line("        };")
	x.blank()
	x.line("    public static readonly IReadOnlyDictionary<string, DurianSignalSpanInfo> Spans =")
	x.line("        new Dictionary<string, DurianSignalSpanInfo>")
	x.line("        {")
	for _, n := range c.SpanOrder {
		x.line("            [%s] = new(%s, %s),", cs(n), cs(spanKind(c.Spans[n])), strArray(c.Spans[n].AttrOrder))
	}
	x.line("        };")
	x.blank()
	x.line("    public static readonly IReadOnlyList<string> EventNames = %s;", strArray(c.EventNames()))
	x.blank()
	x.line("    /// <summary>Event name to level: Port.Install takes it, and the port refuses any other name or level.</summary>")
	x.line("    public static readonly IReadOnlyDictionary<string, string> EventLevels =")
	x.line("        new Dictionary<string, string>")
	x.line("        {")
	for _, k := range c.LogOrder {
		x.line("            [%s] = %s,", cs(c.Logs[k].Event), cs(c.Logs[k].Level))
	}
	x.line("        };")
	x.line("}")
	return strings.TrimRight(x.b.String(), "\n") + "\n"
}

func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

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

func (g *emitter) typeDecl(x *w, name string, t contract.Type) {
	id := ident(name)
	switch t.Kind {
	case contract.KindEnum:
		vals := g.enumVals[name]
		x.line("/// <summary>The enum %s. The constructor is private: only these members exist.</summary>", name)
		x.line("public sealed class %s : IDurianSignalWire", id)
		x.line("{")
		x.line("    private %s(string wire)", id)
		x.line("    {")
		x.line("        Wire = wire;")
		x.line("    }")
		x.blank()
		for i, v := range t.Values {
			x.line("    public static readonly %s %s = new(%s);", id, vals[i], cs(v))
		}
		x.blank()
		x.line("    public static readonly IReadOnlyList<%s> All = new %s[] { %s };", id, id, strings.Join(vals, ", "))
		x.blank()
		x.line("    public string Wire { get; }")
		x.blank()
		x.line("    public bool Valid => true;")
		x.blank()
		x.line("    public override string ToString() => Wire;")
		x.line("}")
	case contract.KindInt:
		x.line("/// <summary>The bounded number %s, %d..%d.</summary>", name, *t.Min, *t.Max)
		g.intSpec(x, id, name, *t.Min, *t.Max, "")
	case contract.KindTime:
		max := 23
		if t.Unit == contract.UnitWeekday {
			max = 6
		}
		x.line("/// <summary>The time bucket %s (%s, 0..%d); it rolls over by itself, so the series count stays flat.</summary>", name, t.Unit, max)
		g.intSpec(x, id, name, 0, max, t.Unit)
	case contract.KindDate:
		x.line("/// <summary>The calendar date %s: a metric label with %d values live at once (window_days).</summary>", name, t.WindowDays)
		x.line("public readonly struct %sSpec : IDurianSignalDateSpec", id)
		x.line("{")
		x.line("    public static string Name => %s;", cs(name))
		x.blank()
		x.line("    public static %s WindowDays => %d;", g.intType(), t.WindowDays)
		x.line("}")
	case contract.KindFree:
		x.line("// %s is a free value (a string): log fields and span attributes only.", name)
	}
}

func (g *emitter) intSpec(x *w, id, name string, min, max int, unit string) {
	x.line("public readonly struct %sSpec : IDurianSignalIntSpec", id)
	x.line("{")
	x.line("    public static string Name => %s;", cs(name))
	x.blank()
	x.line("    public static %s Min => %d;", g.intType(), min)
	x.blank()
	x.line("    public static %s Max => %d;", g.intType(), max)
	x.blank()
	x.line("    public static string Unit => %s;", cs(unit))
	x.line("}")
}

func (g *emitter) record(x *w, id, path string, names []string, types map[string]string) {
	what := "labels"
	if strings.HasPrefix(path, "spans.") {
		what = "attributes"
	}
	x.line("/// <summary>The %s of %s.</summary>", what, strings.SplitN(path, ".", 2)[1])
	if len(names) == 0 {
		x.line("public sealed record %s : IDurianSignalLabels;", id)
		return
	}
	props := g.props[path]
	x.line("public sealed record %s(", id)
	for i, n := range names {
		sep := ","
		if i == len(names)-1 {
			sep = ""
		}
		x.line("    [property: DurianSignalLabel(%s)] %s %s%s", cs(n), g.typeRef[types[n]], props[i], sep)
	}
	x.line(") : IDurianSignalLabels;")
}
