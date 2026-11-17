// Package tempo embeds Tempo 3.1.1's TraceQL parser and engine. Storage is an
// in-memory adapter with Tempo's vParquet5 nested-set and typed-column semantics.
package tempo

import (
	"bytes"
	"encoding/hex"
	"github.com/grafana/tempo/pkg/traceql"
	"github.com/grafana/tempo/pkg/util"
	"sort"
)

// ValidateTraceID follows Tempo's hexadecimal ID decoder, including left padding.
func ValidateTraceID(id string) error { _, err := util.HexStringToTraceID(id); return err }

// Lookup is an ID lookup, not TraceQL. Empty traces are missing, matching Tempo's
// HTTP 200 {"trace":{}} behavior for unknown IDs.
func (e *TempoEngine) Lookup(id string) ([]TraceHit, error) {
	b, err := util.HexStringToTraceID(id)
	if err != nil {
		return nil, err
	}
	for _, t := range e.traces {
		if bytes.Equal(t.id[:], b) && len(t.spans) > 0 {
			h := TraceHit{TraceID: hex.EncodeToString(t.id[:])}
			for _, s := range t.spans {
				h.SpanIDs = append(h.SpanIDs, hex.EncodeToString(s.ID()))
			}
			sort.Strings(h.SpanIDs)
			return []TraceHit{h}, nil
		}
	}
	return nil, nil
}

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
	// Projections are the attributes a query reads only to carry them forward
	// (select(), by(), a metrics function's attribute): Tempo fetches them in the
	// second pass, so they are not filters. An attribute that is also filtered
	// is fetched in the first pass and is listed in Attributes only.
	Projections []Attribute
	Metrics     bool
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
	// First pass (filters) is read before the second pass (projections), so an
	// attribute that is filtered anywhere is never reported as a projection.
	read := func(cs []traceql.Condition, projection bool) {
		for _, c := range cs {
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
				if projection {
					out.Projections = append(out.Projections, x)
				}
			}
		}
	}
	for _, req := range reqs {
		read(req.Conditions, false)
	}
	for _, req := range reqs {
		read(req.SecondPassConditions, true)
	}
	byName := func(s []Attribute) {
		sort.Slice(s, func(i, j int) bool { return s[i].String() < s[j].String() })
	}
	byName(out.Attributes)
	byName(out.Projections)
	return out, nil
}
