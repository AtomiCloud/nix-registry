package alerts

import (
	"os"

	"github.com/AtomiCloud/obs/internal/seed"
)

// logEventSets maps a rewritten LogsQL alert expression to the contract events its
// filter can match (logevent.Analysis.Events, a superset over the rewritten filter
// tree). A nil value means "any event": no contract, or no event list.
type logEventSets map[string][]string

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
