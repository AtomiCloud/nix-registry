// SPDX-License-Identifier: AGPL-3.0-only
// Grafana expression/state port. Source read: grafana v13.3.0 (commit 752e3462).
// See gfexpr/LICENSE-grafana-AGPL and gfexpr/PROVENANCE.md for source and changes.
package mathexp

// Light evaluation (obs addition, not Grafana code): the same walk, union and operator
// rules as exp.go (walkBinary, union, biScalarNumber, walkUnary), on plain values
// instead of data frames, for expressions made only of numbers, scalars, variables,
// unary and binary operators. Anything else (functions, series) reports ok=false and
// the caller uses Execute. obs can verify both paths agree (OBS_VERIFY_FAST=1).

import (
	"github.com/grafana/grafana-plugin-sdk-go/data"

	"github.com/AtomiCloud/obs/internal/alerts/gfexpr/mathexp/parse"
)

const (
	LScalar int8 = iota
	LNumber
	LNoData
)

type LVal struct {
	Kind   int8
	Labels data.Labels
	V      *float64
}

type LVars map[string][]LVal

// Light reports whether the expression only uses nodes the light path supports.
func (e *Expr) Light() bool { return lightNode(e.Root) }

func lightNode(n parse.Node) bool {
	switch x := n.(type) {
	case *parse.ScalarNode, *parse.VarNode:
		return true
	case *parse.BinaryNode:
		return lightNode(x.Args[0]) && lightNode(x.Args[1])
	case *parse.UnaryNode:
		return lightNode(x.Arg)
	}
	return false
}

func (e *Expr) EvalLight(vars LVars) (out []LVal, err error) {
	defer func() {
		if r := recover(); r != nil {
			if er, ok := r.(error); ok {
				err = er
				return
			}
			panic(r)
		}
	}()
	return lightWalk(e.Root, vars)
}

func lightWalk(n parse.Node, vars LVars) ([]LVal, error) {
	switch x := n.(type) {
	case *parse.ScalarNode:
		f := x.Float64
		return []LVal{{Kind: LScalar, V: &f}}, nil
	case *parse.VarNode:
		return vars[x.Name], nil
	case *parse.UnaryNode:
		a, err := lightWalk(x.Arg, vars)
		if err != nil {
			return nil, err
		}
		out := make([]LVal, 0, len(a))
		for _, v := range a {
			switch v.Kind {
			case LNoData:
				out = append(out, LVal{Kind: LNoData})
			default:
				nv := LVal{Kind: v.Kind, Labels: v.Labels}
				if v.V != nil {
					f, err := unaryOp(x.OpStr, *v.V)
					if err != nil {
						return nil, err
					}
					nv.V = &f
				}
				out = append(out, nv)
			}
		}
		return out, nil
	case *parse.BinaryNode:
		a, err := lightWalk(x.Args[0], vars)
		if err != nil {
			return nil, err
		}
		b, err := lightWalk(x.Args[1], vars)
		if err != nil {
			return nil, err
		}
		var out []LVal
		for _, u := range lightUnion(a, b) {
			A, B := u[0], u[1]
			switch {
			case A.Kind == LNoData:
				out = append(out, A)
			case B.Kind == LNoData:
				out = append(out, B)
			case A.Kind == LScalar && B.Kind == LScalar:
				if A.V == nil || B.V == nil {
					out = append(out, LVal{Kind: LScalar})
					continue
				}
				f, err := binaryOp(x.OpStr, *A.V, *B.V)
				if err != nil {
					return nil, err
				}
				out = append(out, LVal{Kind: LScalar, V: &f})
			default: // at least one Number: biScalarNumber, number first unless A is the scalar
				nv := LVal{Kind: LNumber, Labels: u[2].Labels}
				if A.V != nil && B.V != nil {
					f, err := binaryOp(x.OpStr, *A.V, *B.V)
					if err != nil {
						return nil, err
					}
					nv.V = &f
				}
				out = append(out, nv)
			}
		}
		return out, nil
	}
	panic(errUnsupported)
}

type lightErr string

func (e lightErr) Error() string { return string(e) }

const errUnsupported = lightErr("light: unsupported node")

// lightUnion is union() from exp.go on plain values; the third element carries the labels.
func lightUnion(a, b []LVal) [][3]LVal {
	var out [][3]LVal
	if len(a) == 0 || len(b) == 0 {
		return out
	}
	if len(a) == 1 || len(b) == 1 {
		if a[0].Kind == LNoData || b[0].Kind == LNoData {
			return [][3]LVal{{a[0], b[0], {}}}
		}
	}
	for _, x := range a {
		for _, y := range b {
			var labels data.Labels
			al, bl := x.Labels, y.Labels
			switch {
			case al.Equals(bl) || len(al) == 0 || len(bl) == 0:
				l := al
				if len(al) == 0 {
					l = bl
				}
				labels = l
			case len(al) == len(bl):
				continue
			case al.Contains(bl):
				labels = al
			case bl.Contains(al):
				labels = bl
			default:
				continue
			}
			out = append(out, [3]LVal{x, y, {Labels: labels}})
		}
	}
	if len(out) == 0 && len(a) == 1 && len(b) == 1 {
		out = append(out, [3]LVal{a[0], b[0], {}})
	}
	return out
}
