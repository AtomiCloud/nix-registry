package main

import (
	"fmt"
	"strings"
)

// DiffRules documents the breaking-change rules applied within one (module, version).
var DiffRules = map[string]string{
	"D101": "a problem was removed (deprecate it; remove it only in a new version)",
	"D102": "a problem's status changed",
	"D103": "a problem's recoverable flag changed",
	"D104": "a problem's title changed",
	"D105": "a public data field was removed or made private",
	"D106": "a field's type (or format, or item type, or named type) changed",
	"D107": "a field changed between optional and required",
	"D108": "a new public field is required (additions must be optional: old servers do not send it)",
	"D109": "an enum's value set changed (values are frozen within a version)",
	"D111": "a problem's retryAfter changed",
	"D112": "the module changed (a renamed module is a removed module)",
}

// DiffResult is the outcome of comparing two versions of one definition.
type DiffResult struct {
	Breaking []Diag
	Allowed  []string
	NewVer   bool
}

// Diff compares the resolved old and new modules.
func Diff(old, cur *Module) DiffResult {
	var r DiffResult
	brk := func(code string, p Pos, f string, a ...any) {
		r.Breaking = append(r.Breaking, Diag{code, p, fmt.Sprintf(f, a...)})
	}
	ok := func(f string, a ...any) { r.Allowed = append(r.Allowed, fmt.Sprintf(f, a...)) }

	if old.Def.Module != cur.Def.Module {
		brk("D112", Pos{File: cur.Def.File, Line: 1, Col: 1}, "module %q became %q", old.Def.Module, cur.Def.Module)
		return r
	}
	if old.Def.Version != cur.Def.Version {
		r.NewVer = true
		ok("new contract version %s (was %s): no compatibility constraints; keep the %s file while %s is served", cur.Def.Version, old.Def.Version, old.Def.Version, old.Def.Version)
		return r
	}
	curByID := map[string]*Problem{}
	for _, p := range cur.Problems {
		curByID[p.ID] = p
	}
	oldByID := map[string]*Problem{}
	for _, p := range old.Problems {
		oldByID[p.ID] = p
		np, found := curByID[p.ID]
		if !found {
			brk("D101", Pos{File: cur.Def.File, Line: 1, Col: 1}, "problem %q was removed", p.ID)
			continue
		}
		if p.Status != np.Status {
			brk("D102", np.Pos, "problem %q: status %d -> %d", p.ID, p.Status, np.Status)
		}
		if p.Recoverable != np.Recoverable {
			brk("D103", np.Pos, "problem %q: recoverable %v -> %v", p.ID, p.Recoverable, np.Recoverable)
		}
		if p.Title != np.Title {
			brk("D104", np.Pos, "problem %q: title %q -> %q", p.ID, p.Title, np.Title)
		}
		if p.RetryAfter != np.RetryAfter {
			brk("D111", np.Pos, "problem %q: retryAfter %q -> %q", p.ID, p.RetryAfter, np.RetryAfter)
		}
		if p.DetailSrc != np.DetailSrc || p.LogSrc != np.LogSrc {
			ok("problem %q: detail/log template changed (not part of the contract)", p.ID)
		}
		if p.Deprecated != np.Deprecated {
			ok("problem %q: deprecation changed", p.ID)
		}
		diffFields(old, cur, "problem "+p.ID, p.Data, np.Data, true, brk, ok)
	}
	for _, p := range cur.Problems {
		if _, found := oldByID[p.ID]; !found {
			ok("problem %q added", p.ID)
		}
	}
	return r
}

func diffFields(old, cur *Module, where string, of, nf []*Field, top bool,
	brk func(string, Pos, string, ...any), ok func(string, ...any)) {
	nByName := map[string]*Field{}
	for _, f := range nf {
		nByName[f.Name] = f
	}
	oByName := map[string]*Field{}
	for _, f := range of {
		oByName[f.Name] = f
		n, found := nByName[f.Name]
		if f.Private {
			if found && !n.Private {
				// private -> public is an addition of a public field
				if !n.Optional {
					brk("D108", n.Pos, "%s: field %q became public and is required", where, f.Name)
				} else {
					ok("%s: field %q became public (optional)", where, f.Name)
				}
			} else {
				ok("%s: private field %q changed (not part of the contract)", where, f.Name)
			}
			continue
		}
		if !found || n.Private {
			brk("D105", posOr(n, cur), "%s: public field %q removed or made private", where, f.Name)
			continue
		}
		if f.Optional != n.Optional {
			brk("D107", n.Pos, "%s: field %q optional %v -> %v", where, f.Name, f.Optional, n.Optional)
		}
		if why := typeChange(old, cur, f.Type, n.Type, map[string]bool{}); why != "" {
			code := "D106"
			if strings.HasPrefix(why, "enum ") {
				code = "D109"
			}
			brk(code, n.Pos, "%s: field %q: %s", where, f.Name, why)
		}
	}
	for _, n := range nf {
		if _, found := oByName[n.Name]; found || n.Private {
			continue
		}
		if !n.Optional {
			brk("D108", n.Pos, "%s: new field %q is required", where, n.Name)
		} else {
			ok("%s: optional field %q added", where, n.Name)
		}
	}
}

func posOr(f *Field, m *Module) Pos {
	if f != nil {
		return f.Pos
	}
	return Pos{File: m.Def.File, Line: 1, Col: 1}
}

// typeChange returns a reason when the wire type changed; "" when compatible.
func typeChange(old, cur *Module, a, b FieldType, seen map[string]bool) string {
	if a.Kind != b.Kind || a.Format != b.Format {
		return fmt.Sprintf("type %s -> %s", typeSig(a), typeSig(b))
	}
	switch a.Kind {
	case KArray:
		if a.Items == nil || b.Items == nil {
			return ""
		}
		if w := typeChange(old, cur, *a.Items, *b.Items, seen); w != "" {
			return "array items: " + w
		}
	case KRef:
		if a.Ref != b.Ref {
			return fmt.Sprintf("named type %s -> %s", a.Ref, b.Ref)
		}
		if seen[a.Ref] {
			return ""
		}
		seen[a.Ref] = true
		ot, nt := old.TypeByName(a.Ref), cur.TypeByName(b.Ref)
		if ot == nil || nt == nil {
			return ""
		}
		if ot.IsEnum != nt.IsEnum {
			return fmt.Sprintf("%s changed between enum and object", a.Ref)
		}
		if ot.IsEnum {
			if strings.Join(sorted(ot.Values), ",") != strings.Join(sorted(nt.Values), ",") {
				return fmt.Sprintf("enum %s values %v -> %v (D109: frozen within a version)", a.Ref, ot.Values, nt.Values)
			}
			return ""
		}
		var reasons []string
		diffFields(old, cur, "type "+a.Ref, ot.Fields, nt.Fields, false,
			func(code string, _ Pos, f string, x ...any) { reasons = append(reasons, code+" "+fmt.Sprintf(f, x...)) },
			func(string, ...any) {})
		if len(reasons) > 0 {
			return strings.Join(reasons, "; ")
		}
	}
	return ""
}

func sorted(xs []string) []string {
	out := append([]string(nil), xs...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
