// Package tempo embeds Tempo 3.1.1's TraceQL parser and engine. Storage is an
// in-memory adapter with Tempo's vParquet5 nested-set and typed-column semantics.
package tempo

import (
	"github.com/grafana/tempo/pkg/traceql"
	"sort"
)

const Version = "3.1.1 (commit 9db7df79d2af)"

// Attribute is a non-intrinsic attribute read by a query. Scope is span,
// resource or empty (TraceQL's unscoped lookup in both).
type Attribute struct{ Scope, Name string }

func (a Attribute) String() string {
	if a.Scope == "" {
		return "." + a.Name
	}
	return a.Scope + "." + a.Name
}

type Query struct {
	Attributes []Attribute
	Metrics    bool
}

// Parse validates with Tempo, then reads its storage requests with optimizations
// disabled so a name in a folded-away expression is still reported.
func Parse(q string) (*Query, error) {
	ast, reqs, err := traceql.CompileFetchSpanRequests(q, traceql.WithSkipOptimization(traceql.TransformationAll))
	if err != nil {
		return nil, err
	}
	out := &Query{Metrics: len(ast.BatchSpanProcessor) > 0}
	seen := map[Attribute]bool{}
	for _, req := range reqs {
		for _, c := range append(req.Conditions, req.SecondPassConditions...) {
			a := c.Attribute
			if a.Intrinsic != traceql.IntrinsicNone {
				continue
			}
			scope := ""
			switch a.Scope {
			case traceql.AttributeScopeSpan:
				scope = "span"
			case traceql.AttributeScopeResource:
				scope = "resource"
			}
			x := Attribute{scope, a.Name}
			if !seen[x] {
				seen[x] = true
				out.Attributes = append(out.Attributes, x)
			}
		}
	}
	sort.Slice(out.Attributes, func(i, j int) bool { return out.Attributes[i].String() < out.Attributes[j].String() })
	return out, nil
}
