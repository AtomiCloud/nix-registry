// Package obstest is the rule `obs test` adds to the three runs it combines (`obs query --live`,
// `obs alerts`, `obs links`): every incident is caught by at least one alert.
//
// "Caught" uses the expectations `obs alerts` already judges. An incident is caught when at least
// one alert listed in its expect.fire passed its fire verdict: the alert fired inside the incident
// window (and within fire_within, and not outside the window plus resolve_within). An incident is
// uncaught when expect.fire is empty (only quiet alerts, or none), when none of the alerts in
// expect.fire passed, or when the folder has no alert rules at all. An alert that fires without
// being listed is not a catch: it already fails as an unexpected fire in `obs alerts`.
package obstest

import (
	"fmt"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/alerts"
	"github.com/AtomiCloud/obs/internal/seed"
)

// Rule is the id of the finding, printed in brackets like every other rule id.
const Rule = "incident-uncaught"

// Finding is one incident no alert caught.
type Finding struct {
	File     string // incidents/<name>.yaml, relative to the observability folder
	Incident string
	Window   string // [start,end) in the seed's time line
	Reason   string
}

// Uncaught returns the incidents no alert caught, in the order of incs. verdicts are the verdicts of
// `obs alerts` for the same incidents (Run is the incident name); noRules is true when the folder has
// no alert rules (then no alert ran and nothing could catch anything).
func Uncaught(incs []seed.Incident, verdicts []alerts.Verdict, noRules bool) []Finding {
	var out []Finding
	for _, inc := range incs {
		f := Finding{File: "incidents/" + inc.Name + ".yaml", Incident: inc.Name, Window: window(inc)}
		switch {
		case noRules:
			f.Reason = "the folder has no alert rules, so nothing can catch it; add an alert and list its uid in expect.fire"
		case len(inc.Expect.Fire) == 0:
			f.Reason = "expect.fire is empty: no alert is expected to fire in this window; add the alert that should catch it to expect.fire"
		default:
			var missed []string
			caught := false
			for _, v := range verdicts {
				if v.Run != inc.Name || v.Expect != "fire" {
					continue
				}
				if v.Pass {
					caught = true
					break
				}
				missed = append(missed, fmt.Sprintf("%s (%s)", v.Alert, firstClause(v.Why)))
			}
			if caught {
				continue
			}
			f.Reason = "no alert in expect.fire fired inside the window: " + strings.Join(missed, ", ")
		}
		out = append(out, f)
	}
	return out
}

// Line is the finding as one output line.
func (f Finding) Line() string {
	return fmt.Sprintf("FAIL %s incident %s window %s: [%s] %s", f.File, f.Incident, f.Window, Rule, f.Reason)
}

func window(inc seed.Incident) string {
	s, _ := time.ParseDuration(inc.Window.Start)
	d, _ := time.ParseDuration(inc.Window.Duration)
	return fmt.Sprintf("[%s,%s)", s, s+d)
}

// firstClause is a verdict's reason up to its first ";": "quiet", "fired before the incident window".
func firstClause(why string) string {
	if i := strings.Index(why, ";"); i >= 0 {
		why = why[:i]
	}
	return why
}
