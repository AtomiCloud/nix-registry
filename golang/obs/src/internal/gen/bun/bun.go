// Package bun is the TypeScript generator of `obs gen` (concept section 3.4, goals/lib/bun-interfaces.md
// "Trace port"). It writes types and constants only, into one file, signals.ts:
//
//   - label value types: enum = string-literal union; bounded number up to maxUnion values = number-literal
//     union; larger bounded number = brand + spec (the hand-written bounded(spec, n) builds it); calendar
//     date = string brand + spec; free = string;
//   - one label type per metric, the MetricLabels map and the metricTable key table (the port's run-time
//     guard refuses keys that are not in it);
//   - log events as {name, level} constants plus the eventNames list (the port refuses other names);
//   - span attributes as the SpanAttrs map and spanTable (kind + allowed keys). Both are `type` aliases:
//     an interface does not fit the Tracer index-signature constraint;
//   - one name carrier (a string-literal constant) per metric and span.
//
// The file has no imports, so it type-checks alone. The output is a pure function of the contract and is
// laid out the way biome formats it (2 spaces, double quotes, semicolons, trailing commas, width 100).
package bun

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

const (
	// File is the one generated file, relative to the target's out folder.
	File = "signals.ts"
	// maxUnion is the largest bounded number that becomes a literal union; larger ones get a brand.
	maxUnion = 64
	width    = 100
)

func init() { gen.Register(generator{}) }

type generator struct{}

func (generator) Lang() string { return "bun" }

func (generator) Files(c *contract.Contract, _ gen.Options) ([]gen.File, error) {
	s, err := Render(c)
	if err != nil {
		return nil, err
	}
	return []gen.File{{Path: File, Content: []byte(s)}}, nil
}

// reserved are names a generated top-level declaration must not shadow.
var reserved = map[string]bool{
	"Object": true, "String": true, "Number": true, "Boolean": true, "Array": true, "Date": true,
	"Symbol": true, "Map": true, "Set": true, "Error": true, "Promise": true, "Record": true,
	"Partial": true, "Readonly": true, "Function": true, "BigInt": true, "RegExp": true,
}

var identRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// ---- tiny formatter -------------------------------------------------------------------------------

// node is a value or type expression: raw text, an object or an array.
type node interface{}

type raw string

type member struct {
	key string // already quoted when needed, with `?` for optional
	val node
}

type obj struct {
	members []member
	isType  bool // type literal: `;` separators; value: `,`
}

type arr struct{ items []node }

func (o obj) sep() string {
	if o.isType {
		return ";"
	}
	return ","
}

func flat(n node) string {
	switch v := n.(type) {
	case raw:
		return string(v)
	case arr:
		parts := make([]string, len(v.items))
		for i, it := range v.items {
			parts[i] = flat(it)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case obj:
		if len(v.members) == 0 {
			return "{}"
		}
		parts := make([]string, len(v.members))
		for i, m := range v.members {
			parts[i] = m.key + ": " + flat(m.val)
		}
		return "{ " + strings.Join(parts, v.sep()+" ") + " }"
	}
	panic("bun: unknown node")
}

// render writes `prefix + node + suffix` at an indent, breaking the node when it does not fit.
func render(b *strings.Builder, indent int, prefix string, n node, suffix string) {
	pad := strings.Repeat("  ", indent)
	f := flat(n)
	_, isObj := n.(obj)
	if len(pad)+len(prefix)+len(f)+len(suffix) <= width && !(isObj && strings.Contains(f, "\n")) {
		b.WriteString(pad + prefix + f + suffix + "\n")
		return
	}
	switch v := n.(type) {
	case arr:
		b.WriteString(pad + prefix + "[\n")
		for _, it := range v.items {
			render(b, indent+1, "", it, ",")
		}
		b.WriteString(pad + "]" + suffix + "\n")
	case obj:
		b.WriteString(pad + prefix + "{\n")
		for _, m := range v.members {
			render(b, indent+1, m.key+": ", m.val, v.sep())
		}
		b.WriteString(pad + "}" + suffix + "\n")
	default:
		// A raw node that does not fit (a long union is handled by union()).
		b.WriteString(pad + prefix + f + suffix + "\n")
	}
}

// union writes `export type Name = a | b | ...`, one member per line when it does not fit.
func union(b *strings.Builder, name string, members []string) {
	one := "export type " + name + " = " + strings.Join(members, " | ") + ";"
	if len(one) <= width {
		b.WriteString(one + "\n")
		return
	}
	b.WriteString("export type " + name + " =\n")
	for i, m := range members {
		end := ""
		if i == len(members)-1 {
			end = ";"
		}
		b.WriteString("  | " + m + end + "\n")
	}
}

// q is a TypeScript string literal in single quotes: the registry formatter (prettier) writes them
// that way, so a committed generated file stays equal to a fresh generate.
func q(s string) string {
	v := strconv.Quote(s) // escapes \\ and control characters
	v = v[1 : len(v)-1]
	v = strings.ReplaceAll(v, `\"`, `"`)
	v = strings.ReplaceAll(v, `'`, `\'`)
	return "'" + v + "'"
}

func key(s string) string {
	if identRe.MatchString(s) {
		return s
	}
	return q(s)
}

func strArr(ss []string) arr {
	a := arr{}
	for _, s := range ss {
		a.items = append(a.items, raw(q(s)))
	}
	return a
}

// ---- generation -----------------------------------------------------------------------------------

type names struct{ seen map[string]string }

func (n *names) claim(ident, what string) error {
	if reserved[ident] {
		return fmt.Errorf("%s: %q would shadow a TypeScript global; rename it in the contract", what, ident)
	}
	if ident == "" || (ident[0] >= '0' && ident[0] <= '9') {
		return fmt.Errorf("%s: %q is not a valid TypeScript name", what, ident)
	}
	if prev, dup := n.seen[ident]; dup {
		return fmt.Errorf("%s and %s both generate the TypeScript name %s; rename one in the contract", prev, what, ident)
	}
	n.seen[ident] = what
	return nil
}

// Render returns the text of signals.ts for a valid contract.
func Render(c *contract.Contract) (string, error) {
	nm := &names{seen: map[string]string{}}
	var b strings.Builder
	b.WriteString(gen.Header(c, "// "))

	typeName := map[string]string{} // contract type -> TS type
	b.WriteString("\n// Label value types.\n")
	for _, tn := range c.TypeOrder {
		t := c.Types[tn]
		id := gen.Pascal(tn)
		if err := nm.claim(id, "type "+tn); err != nil {
			return "", err
		}
		typeName[tn] = id
		switch t.Kind {
		case contract.KindEnum:
			ms := make([]string, len(t.Values))
			for i, v := range t.Values {
				ms[i] = q(v)
			}
			union(&b, id, ms)
		case contract.KindInt:
			lo, hi := *t.Min, *t.Max
			if t.Size() <= maxUnion {
				var ms []string
				for i := lo; i <= hi; i++ {
					ms = append(ms, strconv.Itoa(i))
				}
				union(&b, id, ms)
			} else {
				spec := id + "Spec"
				if err := nm.claim(spec, "type "+tn+" (spec)"); err != nil {
					return "", err
				}
				fmt.Fprintf(&b, "export type %s = number & { readonly __durianBounded: %s };\n", id, q(id))
				render(&b, 0, "export const "+spec+" = ", obj{members: []member{
					{"name", raw(q(id))}, {"min", raw(strconv.Itoa(lo))}, {"max", raw(strconv.Itoa(hi))},
				}}, " as const;")
			}
		case contract.KindTime:
			hi := 23
			if t.Unit == contract.UnitWeekday {
				hi = 6
			}
			ms := make([]string, 0, hi+1)
			for i := 0; i <= hi; i++ {
				ms = append(ms, strconv.Itoa(i))
			}
			union(&b, id, ms)
		case contract.KindDate:
			spec := id + "Spec"
			if err := nm.claim(spec, "type "+tn+" (spec)"); err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "export type %s = string & { readonly __durianDate: %s };\n", id, q(id))
			render(&b, 0, "export const "+spec+" = ", obj{members: []member{
				{"name", raw(q(id))}, {"windowDays", raw(strconv.Itoa(t.WindowDays))},
			}}, " as const;")
		case contract.KindFree:
			fmt.Fprintf(&b, "export type %s = string;\n", id)
		default:
			return "", fmt.Errorf("type %s: unknown kind %q", tn, t.Kind)
		}
	}

	labelsOf := func(order []string, m map[string]string, opt bool) obj {
		o := obj{isType: true}
		for _, l := range order {
			k := key(l)
			if opt {
				k += "?"
			}
			o.members = append(o.members, member{k, raw(typeName[m[l]])})
		}
		return o
	}

	if len(c.MetricOrder) > 0 {
		b.WriteString("\n// Metrics: one label type per metric, the name -> labels map and the allowed-key table.\n")
		mapT := obj{isType: true}
		table := obj{}
		for _, mn := range c.MetricOrder {
			m := c.Metrics[mn]
			id := gen.Pascal(mn) + "Labels"
			if err := nm.claim(id, "metric "+mn+" (labels)"); err != nil {
				return "", err
			}
			if len(m.LabelOrder) == 0 {
				fmt.Fprintf(&b, "export type %s = Record<string, never>;\n", id)
			} else {
				render(&b, 0, "export type "+id+" = ", labelsOf(m.LabelOrder, m.Labels, false), ";")
			}
			mapT.members = append(mapT.members, member{key(mn), raw(id)})
			table.members = append(table.members, member{key(mn), obj{members: []member{
				{"kind", raw(q(m.Kind))}, {"keys", strArr(m.LabelOrder)},
			}}})
		}
		render(&b, 0, "export type MetricLabels = ", mapT, ";")
		render(&b, 0, "export const metricTable = ", table, " as const;")
		b.WriteString("\n// Metric name carriers (keys of MetricLabels).\n")
		for _, mn := range c.MetricOrder {
			id := gen.Pascal(mn)
			if err := nm.claim(id, "metric "+mn); err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "export const %s = %s as const;\n", id, q(mn))
		}
	}

	if len(c.LogOrder) > 0 {
		b.WriteString("\n// Log events: a name and the level it fixes. Fields are free-form.\n")
		var evs []string
		for _, ln := range c.LogOrder {
			e := c.Logs[ln]
			id := gen.Pascal(ln)
			if err := nm.claim(id, "log "+ln); err != nil {
				return "", err
			}
			render(&b, 0, "export const "+id+" = ", obj{members: []member{
				{"name", raw(q(e.Event))}, {"level", raw(q(e.Level))},
			}}, " as const;")
			evs = append(evs, e.Event)
		}
		render(&b, 0, "export const eventNames = ", strArr(evs), " as const;")
	}

	if len(c.SpanOrder) > 0 {
		b.WriteString("\n// Spans: attribute types are `type` aliases (an interface does not fit the Tracer constraint).\n")
		mapT := obj{isType: true}
		table := obj{}
		for _, sn := range c.SpanOrder {
			s := c.Spans[sn]
			id := gen.Pascal(sn) + "Attrs"
			if err := nm.claim(id, "span "+sn+" (attributes)"); err != nil {
				return "", err
			}
			if len(s.AttrOrder) == 0 {
				fmt.Fprintf(&b, "export type %s = Record<string, never>;\n", id)
			} else {
				render(&b, 0, "export type "+id+" = ", labelsOf(s.AttrOrder, s.Attributes, true), ";")
			}
			kind := s.Kind
			if kind == "" {
				kind = "internal"
			}
			mapT.members = append(mapT.members, member{key(sn), raw(id)})
			table.members = append(table.members, member{key(sn), obj{members: []member{
				{"kind", raw(q(kind))}, {"keys", strArr(s.AttrOrder)},
			}}})
		}
		render(&b, 0, "export type SpanAttrs = ", mapT, ";")
		render(&b, 0, "export const spanTable = ", table, " as const;")
		b.WriteString("\n// Span name carriers (keys of SpanAttrs).\n")
		for _, sn := range c.SpanOrder {
			id := gen.Pascal(sn)
			if err := nm.claim(id, "span "+sn); err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "export const %s = %s as const;\n", id, q(sn))
		}
	}
	return b.String(), nil
}
