package alerts

import (
	"os"
	"slices"
	"sort"
	"time"

	"github.com/VictoriaMetrics/VictoriaLogs/lib/logstorage"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/logevent"
	"github.com/AtomiCloud/obs/internal/seed"
)

// logEventSets maps a rewritten LogsQL alert expression to the contract events its
// filters can pass. A nil value means "any event" (see rootEvents).
type logEventSets map[string][]string

// rootEvents is the set of contract events a row can carry and still pass the
// rewritten query expr into its stats pipe, or nil ("any event": load all).
//
// It does not reuse logevent.Analysis.Events: that union also takes event
// predicates inside stats if(...) (other events still feed the unconditional
// functions and grouped zero rows) and negates only the event leaf under a NOT over
// mixed predicates (!(_stream:{...} event:=e) passes e in other streams). Here:
//   - a pipe that reads the event field (stats if, by, a filter pipe VictoriaLogs did
//     not fold into the root filter) means any event;
//   - otherwise every filter tree the adapter visits (root filter, global_filter,
//     pre-stats filter pipes; stats if() filters without the event field) is
//     evaluated per event with three-valued logic: an event leaf is true or false,
//     every other leaf (stream, time, field values, regex, subquery) may be either.
//     An event is kept when every restricting tree can be true for it.
func rootEvents(expr string, events logevent.Events) []string {
	if events == nil {
		return nil
	}
	names := events.EventNames()
	q, err := vl.Parse(expr, time.Now())
	if err != nil {
		return nil
	}
	_, pipes, wildcard := q.Fields()
	if wildcard || slices.Contains(pipes, logevent.Field) {
		return nil
	}
	lq, err := logstorage.ParseQueryAtTimestamp(expr, time.Now().UnixNano())
	if err != nil {
		return nil
	}
	if lq.ObsNeedsFullSeed() {
		return nil
	}
	possible := map[string]bool{}
	for _, n := range names {
		possible[n] = true
	}
	_, err = lq.ObsAlertTransform(func(tree logstorage.ObsFilterTree) (string, error) {
		for _, n := range names {
			if canT, _ := eventTruth(tree, n); !canT {
				possible[n] = false
			}
		}
		return tree.Text, nil
	}, func(string, []string) error { return nil })
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range names {
		if possible[n] {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// eventTruth reports whether filter tree t can be true, and can be false, for a row
// whose event is ev (every other field unknown).
func eventTruth(t logstorage.ObsFilterTree, ev string) (canT, canF bool) {
	switch t.Kind {
	case "and":
		canT = true
		for _, c := range t.Children {
			ct, cf := eventTruth(c, ev)
			canT, canF = canT && ct, canF || cf
		}
		return canT, canF
	case "or":
		canF = true
		for _, c := range t.Children {
			ct, cf := eventTruth(c, ev)
			canT, canF = canT || ct, canF && cf
		}
		return canT, canF
	case "not":
		if len(t.Children) != 1 {
			return true, true
		}
		ct, cf := eventTruth(t.Children[0], ev)
		return cf, ct
	case "exact", "in":
		if t.Field == logevent.Field {
			m := slices.Contains(t.Values, ev)
			return m, !m
		}
	}
	return true, true
}

// neededLogEvents is the lazy-load set for logs: the seed events some rule's log
// query can match. nil means "load every event" (a query with an unknown set).
// The seed draws each event's rows from its own random stream (seed.BuildIncident),
// so dropping one event's rows changes no other row, sample or timestamp.
func neededLogEvents(rules []*Alert, sets logEventSets) map[string]bool {
	if os.Getenv("OBS_NO_PRUNE") != "" {
		return nil
	}
	need := map[string]bool{}
	for _, a := range rules {
		for _, q := range a.Queries {
			if q.Datasource != "logs" {
				continue
			}
			evs, ok := sets[q.Expr]
			if !ok || len(evs) == 0 {
				return nil
			}
			for _, e := range evs {
				need[e] = true
			}
		}
	}
	return need
}

// keepLogs returns the seed log definitions whose event is in need (a new slice).
func keepLogs(logs []seed.LogDef, need map[string]bool) (kept []seed.LogDef, dropped int) {
	for _, l := range logs {
		if need[l.Event] {
			kept = append(kept, l)
		} else {
			dropped++
		}
	}
	return kept, dropped
}
