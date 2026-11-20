// OBS CHANGE (Apache-2.0 section 4 modification notice).
//
// This file is NOT part of VictoriaLogs v1.54.0 (commit 6433dae586cc15921b4b9c50a805a49553c9d761).
// It was added by the obs tool (AtomiCloud/nix-registry, golang/obs) and is the only
// file in this vendored tree that differs from the tag. The tree-drift test in obs
// (src/internal/engines/vl, TestThirdPartyTreeMatchesTag) fails if any other file
// changes. The file exports a field-name walker, because the parsed query keeps its
// filters unexported.

package logstorage

import (
	"fmt"
	"sort"

	"github.com/VictoriaMetrics/VictoriaLogs/lib/prefixfilter"
)

// ObsFieldNames returns the field names a parsed query reads.
//
// filters holds the field of every filter before the first pipe (a stream filter
// contributes "_stream" plus one "_stream:<tag>" entry per stream tag it names).
// pipes holds the fields the whole pipe chain needs from the stored rows; it is
// empty and wildcard is true when the chain keeps every field (no `stats`, `fields`
// or similar pipe narrows it).
func (q *Query) ObsFieldNames() (filters []string, pipes []string, wildcard bool) {
	set := map[string]struct{}{}
	collect := func(f filter) {
		if f == nil {
			return
		}
		visitFilterRecursive(f, func(leaf filter) bool {
			switch t := leaf.(type) {
			case *filterAnd, *filterOr, *filterNot:
				return false
			case *filterStream:
				set["_stream"] = struct{}{}
				if t.f != nil {
					for _, af := range t.f.orFilters {
						for _, tf := range af.tagFilters {
							set["_stream:"+tf.tagName] = struct{}{}
						}
					}
				}
				return false
			}
			var pf prefixfilter.Filter
			leaf.updateNeededFields(&pf)
			for _, a := range pf.GetAllowFilters() {
				set[a] = struct{}{}
			}
			return false
		})
	}
	collect(q.opts.globalFilter)
	collect(q.f)
	for k := range set {
		filters = append(filters, k)
	}
	sort.Strings(filters)

	pf := getNeededColumns(q.pipes)
	for _, a := range pf.GetAllowFilters() {
		if a == "*" || (len(a) > 0 && a[len(a)-1] == '*') {
			wildcard = true
			continue
		}
		pipes = append(pipes, a)
	}
	sort.Strings(pipes)
	return filters, pipes, wildcard
}

// ObsFilterTree is a read-only view of a parsed filter. It exposes syntax and
// original spelling, not the evaluator's unexported mutable filter objects.
type ObsFilterTree struct {
	Kind, Field, Text, Pattern string
	Values                     []string
	Children                   []ObsFilterTree
}

func obsFilterTree(f filter) ObsFilterTree {
	n := ObsFilterTree{Kind: "other"}
	if f == nil {
		return n
	}
	n.Text = f.String()
	switch t := f.(type) {
	case *filterAnd:
		n.Kind = "and"
		for _, f := range t.filters {
			n.Children = append(n.Children, obsFilterTree(f))
		}
	case *filterOr:
		n.Kind = "or"
		for _, f := range t.filters {
			n.Children = append(n.Children, obsFilterTree(f))
		}
	case *filterNot:
		n.Kind = "not"
		n.Children = []ObsFilterTree{obsFilterTree(t.f)}
	case *filterTime:
		n.Kind = "time"
		n.Field = "_time"
	case *filterStream:
		n.Kind = "stream"
		n.Field = "_stream"
		if t.f != nil {
			for _, a := range t.f.orFilters {
				for _, f := range a.tagFilters {
					n.Values = append(n.Values, f.tagName)
				}
			}
		}
	case *filterGeneric:
		n.Field = t.fieldName
		switch v := t.f.(type) {
		case *filterExact:
			n.Kind = "exact"
			n.Values = []string{v.value}
		case *filterPhrase:
			n.Kind = "phrase"
			n.Values = []string{v.phrase}
		case *filterPrefix:
			n.Kind = "prefix"
			n.Values = []string{v.prefix}
		case *filterExactPrefix:
			n.Kind = "prefix"
			n.Values = []string{v.prefix}
		case *filterIn:
			n.Kind = "in"
			n.Values = append([]string(nil), v.values.values...)
			if v.values.q != nil {
				n.Kind = "subquery"
			}
		case *filterRegexp:
			n.Kind = "regexp"
			n.Pattern = v.re.String()
		}
	}
	return n
}

// ObsNeedsFullSeed identifies extra query sources using VictoriaLogs' own
// recursive visitor. It covers union/join and filter/conditional subqueries;
// opaque row-producing forms also disable pruning conservatively.
func (q *Query) ObsNeedsFullSeed() bool {
	extra := false
	q.visitSubqueries(func(sub *Query) {
		if sub != q {
			extra = true
		}
	})
	for _, p := range q.pipes {
		if slot := obsAlertPipeCondition(p); obsAlertOpaquePipe(p) || (slot != nil && *slot != nil) {
			extra = true
		}
	}
	return extra
}

// Conditional transforms also carry filters, not only stats/filter pipes.
func obsAlertPipeCondition(p pipe) **ifFilter {
	switch t := p.(type) {
	case *pipeExtract:
		return &t.iff
	case *pipeExtractRegexp:
		return &t.iff
	case *pipeUnpackJSON:
		return &t.iff
	case *pipeUnpackLogfmt:
		return &t.iff
	case *pipeUnpackSyslog:
		return &t.iff
	case *pipeUnroll:
		return &t.iff
	case *pipeFormat:
		return &t.iff
	case *pipeSetStreamFields:
		return &t.iff
	case *pipeReplace:
		return &t.iff
	case *pipeReplaceRegexp:
		return &t.iff
	case *pipeCollapseNums:
		return &t.iff
	}
	return nil
}

func obsAlertOpaquePipe(p pipe) bool {
	switch t := p.(type) {
	case *pipeUnion:
		return t.q == nil
	case *pipeJoin:
		return t.q == nil
	case *pipeStreamContext, *pipeGenerateSequence:
		return true
	}
	return false
}

// ObsAlertTransform walks parsed filters before aggregation. The caller owns
// policy; this adapter only exposes the AST and reconstructs a parsed query.
// Filters after stats apply to computed values and are retained unchanged.
// statsFields receives the stats function text and the stored fields it reads.
// Subqueries are checked from the leaves upward using the native visitor.
func (q *Query) ObsAlertTransform(transform func(ObsFilterTree) (string, error), statsFields func(string, []string) error) (*Query, error) {
	qc := q.Clone(q.timestamp)
	// Collect before rewriting: replacing a free-form in(subquery) predicate can
	// otherwise erase a subtree before its event/stats policy has been checked.
	var all []*Query
	seen := map[*Query]bool{}
	qc.visitSubqueries(func(sub *Query) {
		if !seen[sub] {
			seen[sub] = true
			all = append(all, sub)
		}
	})
	for i := len(all) - 1; i >= 0; i-- {
		if _, err := all[i].obsAlertTransformOne(transform, statsFields); err != nil {
			return nil, err
		}
	}
	return qc, nil
}

func (q *Query) obsAlertTransformOne(transform func(ObsFilterTree) (string, error), statsFields func(string, []string) error) (*Query, error) {
	qc := q
	rewrite := func(f filter) (filter, error) {
		s, err := transform(obsFilterTree(f))
		if err != nil {
			return nil, err
		}
		if s == "" {
			s = "*"
		}
		parsed, err := ParseQueryAtTimestamp(s, q.timestamp)
		if err != nil {
			return nil, err
		}
		if len(parsed.pipes) > 0 {
			return nil, fmt.Errorf("rewritten filter contains pipes")
		}
		return parsed.f, nil
	}
	var err error
	qc.f, err = rewrite(qc.f)
	if err != nil {
		return nil, err
	}
	if qc.opts.globalFilter != nil {
		qc.opts.globalFilter, err = rewrite(qc.opts.globalFilter)
		if err != nil {
			return nil, err
		}
	}
	seenStats := false
	for i, p := range qc.pipes {
		if obsAlertOpaquePipe(p) {
			return nil, fmt.Errorf("log alert pipe form not checkable: %s", p.String())
		}
		hasSubquery := false
		p.visitSubqueries(func(*Query) { hasSubquery = true })
		if hasSubquery {
			switch p.(type) {
			case *pipeUnion, *pipeJoin:
				if seenStats {
					return nil, fmt.Errorf("log alert pipe form not checkable after stats: %s", p.String())
				}
			case *pipeStats, *pipeFilter:
			default:
				if obsAlertPipeCondition(p) == nil {
					return nil, fmt.Errorf("log alert pipe form not checkable: %s", p.String())
				}
			}
		}
		if !seenStats {
			if slot := obsAlertPipeCondition(p); slot != nil && *slot != nil {
				f, err := rewrite((*slot).f)
				if err != nil {
					return nil, err
				}
				*slot = newIfFilter(f)
			}
		}
		switch ps := p.(type) {
		case *pipeStats:
			if !seenStats {
				// Flatten the parsed switch form into its equivalent conditional
				// functions so rewritten conditions are also printed by String().
				ps.entries = nil
				for j := range ps.funcs {
					f := &ps.funcs[j]
					var pf prefixfilter.Filter
					f.f.updateNeededFields(&pf)
					if err := statsFields(f.f.String(), pf.GetAllowFilters()); err != nil {
						return nil, err
					}
					if f.iff != nil {
						ff, err := rewrite(f.iff.f)
						if err != nil {
							return nil, err
						}
						f.iff = newIfFilter(ff)
					}
					ps.entries = append(ps.entries, f)
				}
			}
			seenStats = true
		case *pipeFilter:
			if !seenStats {
				ff, err := rewrite(ps.f)
				if err != nil {
					return nil, err
				}
				qc.pipes[i] = &pipeFilter{f: ff}
			}
		}
	}
	return qc, nil
}

// ObsStatsLabels returns the `by (...)` labels of the last `stats` pipe, the way
// the /select/logsql/stats_query endpoint gets them. It fails when the query has
// no `stats` pipe. It does not change q.
func (q *Query) ObsStatsLabels() ([]string, error) {
	return q.Clone(q.timestamp).GetStatsLabels()
}

// ObsWithoutTrailingFilters returns a copy of q in which the `filter` pipes that
// come after the last `stats` pipe are removed (a threshold written as
// `| stats ... | filter hits:>5`). dropped is the number of removed pipes. A query
// with no `stats` pipe, or with another kind of pipe after it, comes back
// unchanged with dropped 0.
func (q *Query) ObsWithoutTrailingFilters() (qOut *Query, dropped int) {
	idx := getLastPipeStatsIdx(q.pipes)
	if idx < 0 || idx == len(q.pipes)-1 {
		return q, 0
	}
	for _, p := range q.pipes[idx+1:] {
		if _, ok := p.(*pipeFilter); !ok {
			return q, 0
		}
	}
	qc := q.Clone(q.timestamp)
	n := len(qc.pipes) - (idx + 1)
	qc.pipes = qc.pipes[:idx+1]
	return qc, n
}

// ObsFieldFilter is one filter on a single field, as ObsFieldFilters reports it.
type ObsFieldFilter struct {
	Op       string   // "exact" (`:=`), "phrase" (`:"x"`), "prefix" (`:x*` or `:=x*`), "in" (`in(...)`), "regexp" (`:~`) or "other"
	Values   []string // exact and phrase: the one value; prefix: the prefix; in: its values
	Pattern  string   // regexp: the pattern
	Negated  bool     // under an odd number of `not` / `-`
	UnderOr  bool     // inside an `or` that has other branches
	InStream bool     // the field is a `_stream:{...}` tag, not a field filter
}

// ObsFieldFilters returns every filter on field before the first pipe. A stream filter that
// names field as a tag contributes one entry with InStream set.
func (q *Query) ObsFieldFilters(field string) []ObsFieldFilter {
	var out []ObsFieldFilter
	var walk func(f filter, neg, underOr bool)
	walk = func(f filter, neg, underOr bool) {
		switch t := f.(type) {
		case nil:
		case *filterAnd:
			for _, c := range t.filters {
				walk(c, neg, underOr)
			}
		case *filterOr:
			many := underOr || len(t.filters) > 1
			for _, c := range t.filters {
				walk(c, neg, many)
			}
		case *filterNot:
			walk(t.f, !neg, underOr)
		case *filterStream:
			if t.f == nil {
				return
			}
			for _, af := range t.f.orFilters {
				for _, tf := range af.tagFilters {
					if tf.tagName == field {
						out = append(out, ObsFieldFilter{Op: "other", Negated: neg, UnderOr: underOr, InStream: true})
					}
				}
			}
		case *filterGeneric:
			if t.fieldName != field {
				return
			}
			ff := ObsFieldFilter{Op: "other", Negated: neg, UnderOr: underOr}
			switch x := t.f.(type) {
			case *filterExact:
				ff.Op, ff.Values = "exact", []string{x.value}
			case *filterIn:
				ff.Op, ff.Values = "in", append([]string(nil), x.values.values...)
			case *filterPhrase:
				ff.Op, ff.Values = "phrase", []string{x.phrase}
			case *filterPrefix:
				ff.Op, ff.Values = "prefix", []string{x.prefix}
			case *filterExactPrefix:
				ff.Op, ff.Values = "prefix", []string{x.prefix}
			case *filterRegexp:
				ff.Op, ff.Pattern = "regexp", x.re.String()
			}
			out = append(out, ff)
		}
	}
	walk(q.opts.globalFilter, false, false)
	walk(q.f, false, false)
	return out
}

// ObsSetConcurrency sets the query's worker count, as `options(concurrency=n)` written
// before the query does (it only lowers the CPU count; parallel readers follow it).
// obs alerts runs many small stats queries from parallel rule workers; one worker
// per query avoids a set of per-worker stats buffers on every query.
func (q *Query) ObsSetConcurrency(n uint) { q.opts.concurrency = n }

// ObsTrailingFilterFields returns the fields read by the `filter` pipes that directly follow the last
// `stats` pipe (the pipes ObsWithoutTrailingFilters removes). A field that is also a `by (...)` label of
// that stats pipe is a retained group key filtered after the aggregation.
func (q *Query) ObsTrailingFilterFields() []string {
	idx := getLastPipeStatsIdx(q.pipes)
	if idx < 0 {
		return nil
	}
	set := map[string]struct{}{}
	for _, p := range q.pipes[idx+1:] {
		pf, ok := p.(*pipeFilter)
		if !ok {
			break
		}
		visitFilterRecursive(pf.f, func(leaf filter) bool {
			switch leaf.(type) {
			case *filterAnd, *filterOr, *filterNot:
				return false
			}
			var nf prefixfilter.Filter
			leaf.updateNeededFields(&nf)
			for _, a := range nf.GetAllowFilters() {
				set[a] = struct{}{}
			}
			return false
		})
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
