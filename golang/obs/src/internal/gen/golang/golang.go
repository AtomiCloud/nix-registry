// Package golang is the Go generator of `obs gen` (concept section 3.4).
//
// It writes ONE file, signals.go, of types and constants only:
//
//   - the shared base types, prefixed DurianSignal (owner ruling D88 B12): level markers, an event
//     carrier that fixes its level in its type, one name carrier per metric kind and one for spans;
//   - one label value type per contract type: an enum is a struct with an unexported field (only
//     this package can make a value), a bounded number and a time bucket are structs built by a
//     constructor that returns an error, a calendar date is built from a time.Time;
//   - one labels struct per metric and one attributes struct per span (fields carry the wire
//     name in an `obs:"..."` tag, which the OTel adapter reads);
//   - one name variable per signal and a data table of allowed keys.
//
// There are no per-metric functions. The hole the compiler cannot close is the zero value
// (Provider{}): the port refuses it at run time and the forbidigo lint closes it in CI.
package golang

import (
	"bytes"
	"fmt"
	"go/format"
	"strconv"
	"strings"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

func init() { gen.Register(generator{}) }

type generator struct{}

func (generator) Lang() string { return "go" }

func (generator) Files(c *contract.Contract, o gen.Options) ([]gen.File, error) {
	pkg := o.Package
	if pkg == "" {
		pkg = "signals"
	}
	w := &writer{c: c, declared: map[string]string{}}
	src := w.render(pkg)
	if w.err != nil {
		return nil, w.err
	}
	out, err := format.Source([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("generated Go does not parse: %w\n%s", err, src)
	}
	return []gen.File{{Path: "signals.go", Content: out}}, nil
}

type writer struct {
	bytes.Buffer
	c        *contract.Contract
	declared map[string]string // identifier -> what declared it
	err      error
}

func (w *writer) f(format string, a ...any) { fmt.Fprintf(&w.Buffer, format, a...) }

// decl records a top-level identifier and fails on a collision.
func (w *writer) decl(id, what string) {
	if prev, dup := w.declared[id]; dup && w.err == nil {
		w.err = fmt.Errorf("identifier %s is declared twice: by %s and by %s; rename one in the contract", id, prev, what)
	}
	w.declared[id] = what
}

var levelIdent = map[string]string{"trace": "Trace", "debug": "Debug", "info": "Info", "warn": "Warn", "error": "Error", "fatal": "Fatal"}

func (w *writer) render(pkg string) string {
	c := w.c
	w.f("%s\n", strings.TrimRight(gen.Header(c, "// "), "\n"))
	w.f("\n// Package %s holds the typed signals of service %s: types and constants only.\n", pkg, strconv.Quote(c.Service))
	w.f("package %s\n\n", pkg)
	w.f("import (\n\t\"fmt\"\n\t\"time\"\n)\n\n")

	// ---- shared base types
	w.f("// ---- shared base types ----\n\n")
	for _, l := range contract.Levels {
		id := "DurianSignalLevel" + levelIdent[l]
		w.decl(id, "base type")
		w.f("// %s is the level marker of %s events.\ntype %s struct{}\n\n", id, l, id)
	}
	w.decl("DurianSignalLevel", "base type")
	w.f("// DurianSignalLevel is implemented by the level markers only.\ntype DurianSignalLevel interface{ durianSignalLevel() string }\n\n")
	for _, l := range contract.Levels {
		w.f("func (DurianSignalLevel%s) durianSignalLevel() string { return %q }\n", levelIdent[l], l)
	}
	w.f(`
// DurianSignalEvent is a log event: a name carrier whose type includes its level. Only this
// package can make one with a name.
type DurianSignalEvent[L DurianSignalLevel] struct{ name string }

// Name is the event name (the value of the durian.cloud/event field).
func (e DurianSignalEvent[L]) Name() string { return e.name }

// Level is the level the type fixes.
func (DurianSignalEvent[L]) Level() string { var l L; return l.durianSignalLevel() }

// DurianSignalCounter is a counter name carrier; Labels is its labels struct.
type DurianSignalCounter[Labels any] struct{ name string }

// Name is the metric name as written in the contract.
func (m DurianSignalCounter[Labels]) Name() string { return m.name }

// DurianSignalHistogram is a histogram name carrier.
type DurianSignalHistogram[Labels any] struct{ name string }

// Name is the metric name as written in the contract.
func (m DurianSignalHistogram[Labels]) Name() string { return m.name }

// DurianSignalGauge is a gauge name carrier.
type DurianSignalGauge[Labels any] struct{ name string }

// Name is the metric name as written in the contract.
func (m DurianSignalGauge[Labels]) Name() string { return m.name }

// DurianSignalSpan is a span name carrier; Attrs is its attributes struct.
type DurianSignalSpan[Attrs any] struct{ name, kind string }

// Name is the span name.
func (s DurianSignalSpan[Attrs]) Name() string { return s.name }

// Kind is the span kind (internal, client, server, producer or consumer).
func (s DurianSignalSpan[Attrs]) Kind() string { return s.kind }

// DurianSignalMetricInfo is one row of MetricTable.
type DurianSignalMetricInfo struct {
	Kind       string
	Unit       string
	Budget     int
	AcrossPods string // sum, avg, max or last; empty for counters, histograms and per-pod gauges
	PerPod     bool
	Keys       []string // the allowed label keys, in contract order
}

// DurianSignalSpanInfo is one row of SpanTable.
type DurianSignalSpanInfo struct {
	Kind string
	Keys []string
}

// DurianSignalNoLabels is the labels struct of a metric with no labels.
type DurianSignalNoLabels struct{}

// durianSignalRange is the error of a bounded number out of range.
func durianSignalRange(name string, n, lo, hi int) error {
	return fmt.Errorf("%%s: %%d is outside %%d..%%d", name, n, lo, hi)
}

var _ = time.Time{}
`)
	w.decl("DurianSignalNoLabels", "base type")

	// ---- label value types
	w.f("\n// ---- label value types ----\n")
	for _, n := range c.TypeOrder {
		w.typeDecl(n, c.Types[n])
	}

	// ---- metrics
	w.f("\n// ---- metrics ----\n")
	for _, n := range c.MetricOrder {
		w.metric(n, c.Metrics[n])
	}
	w.f("\n// MetricTable is the data table of the metrics: kind, unit, budget, pod handling and the allowed label keys.\n")
	w.f("var MetricTable = map[string]DurianSignalMetricInfo{\n")
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		w.f("\t%q: {Kind: %q, Unit: %q, Budget: %d, AcrossPods: %q, PerPod: %v, Keys: %s},\n", n, m.Kind, m.Unit, m.Budget, m.AcrossPod, m.PerPod, strList(m.LabelOrder))
	}
	w.f("}\n")

	// ---- log events
	w.f("\n// ---- log events ----\n\n")
	for _, k := range c.LogOrder {
		e := c.Logs[k]
		id := gen.Pascal(k)
		w.decl(id, "log event "+k)
		w.f("// %s is the event %s at level %s.\nvar %s = DurianSignalEvent[DurianSignalLevel%s]{name: %q}\n\n", id, e.Event, e.Level, id, levelIdent[e.Level], e.Event)
	}
	w.f("// Events lists every event name of the contract, in contract order.\nvar Events = %s\n", strList(c.EventNames()))

	// ---- spans
	w.f("\n// ---- spans ----\n")
	for _, n := range c.SpanOrder {
		w.span(n, c.Spans[n])
	}
	w.f("\n// SpanTable is the data table of the spans: kind and allowed attribute keys.\n")
	w.f("var SpanTable = map[string]DurianSignalSpanInfo{\n")
	for _, n := range c.SpanOrder {
		s := c.Spans[n]
		w.f("\t%q: {Kind: %q, Keys: %s},\n", n, spanKind(s), strList(s.AttrOrder))
	}
	w.f("}\n")
	return w.String()
}

func spanKind(s contract.Span) string {
	if s.Kind == "" {
		return "internal"
	}
	return s.Kind
}

func strList(a []string) string {
	q := make([]string, len(a))
	for i, s := range a {
		q[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(q, ", ") + "}"
}

func (w *writer) typeDecl(n string, t contract.Type) {
	id := gen.Pascal(n)
	w.decl(id, "type "+n)
	switch t.Kind {
	case contract.KindEnum:
		w.f("\n// %s is the enum %s. The field is unexported: only this package can make a value.\n", id, n)
		w.f("type %s struct{ v string }\n\n", id)
		w.f("// String is the wire value.\nfunc (e %s) String() string { return e.v }\n\n", id)
		w.f("var (\n")
		for _, v := range t.Values {
			vid := id + gen.Pascal(v)
			w.decl(vid, "value "+v+" of "+n)
			w.f("\t%s = %s{%q}\n", vid, id, v)
		}
		w.f(")\n")
	case contract.KindInt:
		w.f("\n// %s is the bounded number %s, %d..%d. New%s refuses a value outside the range.\n", id, n, *t.Min, *t.Max, id)
		w.f("type %s struct{ v int }\n\n", id)
		w.f("const (\n\t%sMin = %d\n\t%sMax = %d\n)\n\n", id, *t.Min, id, *t.Max)
		w.f("// New%s makes a value, or returns an error when n is out of range.\n", id)
		w.f("func New%s(n int) (%s, error) {\n\tif n < %sMin || n > %sMax {\n\t\treturn %s{}, durianSignalRange(%q, n, %sMin, %sMax)\n\t}\n\treturn %s{n}, nil\n}\n\n", id, id, id, id, id, n, id, id, id)
		w.f("// Int is the number.\nfunc (b %s) Int() int { return b.v }\n", id)
	case contract.KindTime:
		lo, hi, from := 0, 23, "t.Hour()"
		if t.Unit == contract.UnitWeekday {
			hi, from = 6, "int(t.Weekday())"
		}
		w.f("\n// %s is the time bucket %s (%s, %d..%d); it rolls over by itself, so the series count stays flat.\n", id, n, t.Unit, lo, hi)
		w.f("type %s struct{ v int }\n\n", id)
		w.f("// New%s makes a value, or returns an error when n is out of range.\n", id)
		w.f("func New%s(n int) (%s, error) {\n\tif n < %d || n > %d {\n\t\treturn %s{}, durianSignalRange(%q, n, %d, %d)\n\t}\n\treturn %s{n}, nil\n}\n\n", id, id, lo, hi, id, n, lo, hi, id)
		w.f("// %sAt is the bucket of a time (always in range).\nfunc %sAt(t time.Time) %s { return %s{%s} }\n\n", id, id, id, id, from)
		w.f("// Int is the number.\nfunc (b %s) Int() int { return b.v }\n", id)
	case contract.KindDate:
		w.f("\n// %s is the calendar date %s: a metric label with %d values live at once (window_days).\n", id, n, t.WindowDays)
		w.f("type %s struct{ v string }\n\n", id)
		w.f("// New%s makes a value from a time (its UTC calendar date, YYYY-MM-DD).\nfunc New%s(t time.Time) %s { return %s{t.UTC().Format(\"2006-01-02\")} }\n\n", id, id, id, id)
		w.f("// String is the wire value.\nfunc (d %s) String() string { return d.v }\n", id)
	case contract.KindFree:
		// free is a plain string at use sites; no type is generated
		delete(w.declared, id)
	}
}

// goType is the Go type of a contract type name at a use site.
func (w *writer) goType(typ string) string {
	if w.c.Types[typ].Kind == contract.KindFree {
		return "string"
	}
	return gen.Pascal(typ)
}

func (w *writer) fields(order []string, m map[string]string) {
	used := map[string]string{}
	for _, l := range order {
		fid := gen.Pascal(l)
		if prev, dup := used[fid]; dup && w.err == nil {
			w.err = fmt.Errorf("labels %q and %q both become the Go field %s", prev, l, fid)
		}
		used[fid] = l
		w.f("\t%s %s `obs:%q`\n", fid, w.goType(m[l]), l)
	}
}

func (w *writer) metric(n string, m contract.Metric) {
	id := gen.Pascal(n)
	lid := id + "Labels"
	w.decl(id, "metric "+n)
	w.decl(lid, "labels of metric "+n)
	w.f("\n// %s holds the labels of %s.\ntype %s struct {\n", lid, n, lid)
	w.fields(m.LabelOrder, m.Labels)
	w.f("}\n\n")
	carrier := map[string]string{contract.MetricCounter: "DurianSignalCounter", contract.MetricHistogram: "DurianSignalHistogram", contract.MetricGauge: "DurianSignalGauge"}[m.Kind]
	w.f("// %s is the %s %s.\nvar %s = %s[%s]{name: %q}\n", id, m.Kind, n, id, carrier, lid, n)
}

func (w *writer) span(n string, s contract.Span) {
	id := gen.Pascal(n)
	aid := id + "Attrs"
	w.decl(id, "span "+n)
	w.decl(aid, "attributes of span "+n)
	w.f("\n// %s holds the attributes of span %s.\ntype %s struct {\n", aid, n, aid)
	w.fields(s.AttrOrder, s.Attributes)
	w.f("}\n\n")
	w.f("// %s is the span %s.\nvar %s = DurianSignalSpan[%s]{name: %q, kind: %q}\n", id, n, id, aid, n, spanKind(s))
}
