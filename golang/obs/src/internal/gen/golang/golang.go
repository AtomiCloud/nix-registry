// Package golang generates contract-specific types, specs, carriers and key tables.
// Shared bounded values, results, level markers and port contracts come from the
// interfaces library, whose import is configurable per generation target.
// Compiler fixtures use a minimal stub from the goal, not the real library.
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
	interfaces := o.Interfaces
	if interfaces == "" {
		interfaces = "github.com/AtomiCloud/diene.go-interfaces/obs"
	}
	w := &writer{c: c, interfaces: interfaces, packageID: o.PackageID, contractID: o.ContractID, declared: map[string]string{}}
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
	c          *contract.Contract
	interfaces string
	packageID  string
	contractID string
	declared   map[string]string // identifier -> what declared it
	err        error
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
	w.f("import (\n\tobs %q\n", w.interfaces)
	hasDate := false
	for _, t := range c.Types {
		hasDate = hasDate || t.Kind == contract.KindDate
	}
	if hasDate {
		w.f("\t\"time\"\n")
	}
	w.f(")\n\n")
	for _, id := range []string{"obs", "MetricTable", "SpanTable", "Events", "DurianSignalGauge", "DurianSignalCounter", "DurianSignalHistogram", "Event", "EventTable"} {
		w.decl(id, "shared interfaces name")
	}
	w.f("// Event is the interfaces library's level-typed event carrier.\ntype Event[L obs.Level] = obs.Event[L]\n\n")
	for _, level := range contract.Levels {
		id := levelIdent[level]
		w.decl(id, "shared event level")
		w.f("type %s = obs.%sLevel\n", id, id)
	}

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
	w.f("\n// MetricTable is the data table of the metrics: kind, unit, budget, pod handling, the allowed label keys,\n")
	w.f("// the generated labels type, and the allowed values (enums) and ranges (bounded numbers) the port checks.\n")
	w.f("var MetricTable = map[string]obs.DurianSignalMetricInfo{\n")
	for _, n := range c.MetricOrder {
		m := c.Metrics[n]
		w.f("\t%q: {\n\t\tKind: %q, Unit: %q, Budget: %d, AcrossPods: %q, PerPod: %v, Keys: %s,\n", n, m.Kind, m.Unit, m.Budget, m.AcrossPod, m.PerPod, strList(m.LabelOrder))
		w.f("\t\tLabels: %sLabels{},\n", gen.Pascal(n))
		var values, ranges []string
		for _, l := range m.LabelOrder {
			t := c.Types[m.Labels[l]]
			switch t.Kind {
			case contract.KindEnum:
				values = append(values, fmt.Sprintf("%q: %s", l, strings.TrimPrefix(strList(t.Values), "[]string")))
			case contract.KindInt:
				ranges = append(ranges, fmt.Sprintf("%q: {%d, %d}", l, *t.Min, *t.Max))
			case contract.KindTime:
				hi := 23
				if t.Unit == contract.UnitWeekday {
					hi = 6
				}
				ranges = append(ranges, fmt.Sprintf("%q: {0, %d}", l, hi))
			}
		}
		if len(values) > 0 {
			w.f("\t\tValues: map[string][]string{%s},\n", strings.Join(values, ", "))
		}
		if len(ranges) > 0 {
			w.f("\t\tRanges: map[string][2]int64{%s},\n", strings.Join(ranges, ", "))
		}
		w.f("\t},\n")
	}
	w.f("}\n")

	// ---- log events
	w.f("\n// ---- log events ----\n\n")
	for _, k := range c.LogOrder {
		e := c.Logs[k]
		id := gen.Pascal(k)
		w.decl(id, "log event "+k)
		w.f("// %s is the event %s at level %s.\nvar %s = obs.NewEvent[obs.%sLevel](%q)\n\n", id, e.Event, e.Level, id, levelIdent[e.Level], e.Event)
	}
	w.f("// Events lists every event name of the contract, in contract order.\nvar Events = %s\n", strList(c.EventNames()))
	w.f("\n// EventTable is the data table of the events: name -> level. The port refuses any other name or level.\n")
	w.f("var EventTable = map[string]string{\n")
	for _, k := range c.LogOrder {
		e := c.Logs[k]
		w.f("\t%q: %q,\n", e.Event, e.Level)
	}
	w.f("}\n")

	// ---- spans
	w.f("\n// ---- spans ----\n")
	for _, n := range c.SpanOrder {
		w.span(n, c.Spans[n])
	}
	w.f("\n// SpanTable is the data table of the spans: kind and allowed attribute keys.\n")
	w.f("var SpanTable = map[string]obs.DurianSignalSpanInfo{\n")
	for _, n := range c.SpanOrder {
		s := c.Spans[n]
		w.f("\t%q: {Kind: %q, Keys: %s},\n", n, spanKind(s), strList(s.AttrOrder))
	}
	w.f("}\n")
	w.f("\n// init merges this contract's tables at boot, before the first signal.\n")
	w.f("// Duplicate names, changed contracts and late registration poison the port until restart.\n")
	w.f("func init() {\n\t_ = obs.Register(obs.DurianSignalTables{Contract: %q, Hash: %q, Metrics: MetricTable, Events: EventTable, Spans: SpanTable})\n}\n", gen.RegistrationID(c, gen.Options{PackageID: w.packageID, ContractID: w.contractID}), "sha256:"+c.Hash)
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

// memberWords is the words of an enum value as an identifier suffix: every character that is not in an
// identifier is a word break (<none> -> None), so the value only ever changes the identifier, never the
// wire value. The type name is in front, so a leading digit is fine; a value with no letter or digit at
// all is Value. Two values that make one identifier are refused by decl, naming both.
func memberWords(v string) string {
	if p := gen.Pascal(gen.Clean(v)); p != "" {
		return p
	}
	return "Value"
}

func (w *writer) typeDecl(n string, t contract.Type) {
	id := gen.Pascal(n)
	w.decl(id, "type "+n)
	switch t.Kind {
	case contract.KindEnum:
		w.f("\n// %s is the enum %s. The field is unexported: only this package can make a value.\n", id, n)
		w.f("type %s struct{ v string }\n\n", id)
		w.f("// String is the wire value.\nfunc (e %s) String() string { return e.v }\n\n", id)
		w.f("// Valid is false for the zero value %s{}: a value only this package can make is set.\nfunc (e %s) Valid() bool { return e.v != \"\" }\n\n", id, id)
		w.f("var (\n")
		for _, v := range t.Values {
			vid := id + memberWords(v)
			w.decl(vid, fmt.Sprintf("value %q of type %s (%s)", v, n, w.c.TypeAt(n)))
			w.f("\t%s = %s{%q}\n", vid, id, v)
		}
		w.f(")\n")
	case contract.KindInt, contract.KindTime:
		lo, hi, unit := 0, 23, t.Unit
		if t.Kind == contract.KindInt {
			lo, hi = *t.Min, *t.Max
			unit = ""
		} else if unit == contract.UnitWeekday {
			hi = 6
		}
		w.decl(id+"Spec", "range spec of type "+n)
		w.f("\n// %sSpec is the range of %s; use obs.NewInt or obs.AtTime.\ntype %sSpec struct{}\n\n", id, n, id)
		w.f("func (%sSpec) Name() string { return %q }\n", id, n)
		w.f("func (%sSpec) Min() int64 { return %d }\n", id, lo)
		w.f("func (%sSpec) Max() int64 { return %d }\n", id, hi)
		w.f("func (%sSpec) Unit() string { return %q }\n", id, unit)
		w.f("\n// %s is the interfaces library's bounded value for this spec.\ntype %s = obs.Int[%sSpec]\n", id, id, id)
	case contract.KindDate:
		w.f("\n// %s is the calendar date %s: a metric label with %d values live at once (window_days).\n", id, n, t.WindowDays)
		w.decl("New"+id, "constructor of type "+n)
		w.f("type %s struct{ v string }\n\n", id)
		w.f("// New%s makes a value from a time (its UTC calendar date, YYYY-MM-DD).\nfunc New%s(t time.Time) %s { return %s{t.UTC().Format(\"2006-01-02\")} }\n\n", id, id, id, id)
		w.f("// String is the wire value.\nfunc (d %s) String() string { return d.v }\n\n", id)
		w.f("// Valid is false for the zero value %s{}.\nfunc (d %s) Valid() bool { return d.v != \"\" }\n", id, id)
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
	w.f("// %s is the %s %s.\nvar %s = obs.New%s[%s](%q)\n", id, m.Kind, n, id, strings.TrimPrefix(carrier, "DurianSignal"), lid, n)
}

func (w *writer) span(n string, s contract.Span) {
	id := gen.Pascal(n)
	aid := id + "Attrs"
	w.decl(id, "span "+n)
	w.decl(aid, "attributes of span "+n)
	w.f("\n// %s holds the attributes of span %s.\ntype %s struct {\n", aid, n, aid)
	w.fields(s.AttrOrder, s.Attributes)
	w.f("}\n\n")
	w.f("// %s is the span %s.\nvar %s = obs.NewSpan[%s](%q, %q)\n", id, n, id, aid, n, spanKind(s))
}
