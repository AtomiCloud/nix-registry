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
