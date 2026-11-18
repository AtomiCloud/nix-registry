// Package obstest is the rule `obs test` adds to the three runs it combines (`obs query --live`,
// `obs alerts`, `obs links`): every incident is caught by at least one alert.
//
// "Caught" uses the expectations `obs alerts` already judges. An incident is caught when at least
// one alert listed in its expect.fire passed its fire verdict: the alert fired inside the incident
// window (and within fire_within, and not outside the window plus resolve_within). An incident is
// uncaught when expect.fire is empty (only quiet alerts, or none), when none of the alerts in
// expect.fire passed, or when the folder has no alert rules at all. An alert that fires without
// being listed is not a catch: it already fails as an unexpected fire in `obs alerts`.
//
// A deliberate non-incident disturbance (only quiet expectations: it proves the alerts stay quiet)
// carries `disturbance: "<reason>"` in its incident file. It is excluded from [incident-uncaught]
// and printed as a note. An incident without the marker and with no catching alert stays red.
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
		if inc.Disturbance != "" {
			continue
		}
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
				missed = append(missed, fmt.Sprintf("%s (%s)", v.Alert, failedCondition(v.Why)))
			}
			if caught {
				continue
			}
			f.Reason = "no alert passed its fire expectation: " + strings.Join(missed, ", ")
		}
		out = append(out, f)
	}
	return out
}

// Note is an incident marked as a deliberate disturbance (printed, not failed).
type Note struct {
	File     string
	Incident string
	Reason   string
}

// Disturbances returns the incidents marked `disturbance:`, in the order of incs.
func Disturbances(incs []seed.Incident) []Note {
	var out []Note
	for _, inc := range incs {
		if inc.Disturbance != "" {
			out = append(out, Note{File: "incidents/" + inc.Name + ".yaml", Incident: inc.Name, Reason: inc.Disturbance})
		}
	}
	return out
}

// Line is the note as one output line.
func (n Note) Line() string {
	return fmt.Sprintf("note: [disturbance] %s %s: %s", n.File, n.Incident, n.Reason)
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

// failedCondition is a verdict's reason without the incident-window clause `obs alerts` always appends
// last ("; incident [10m0s,20m0s), fire_within=5m0s"): "quiet", "fired at +12m0s, firing 29 min;
// firing outside window [10m0s,25m0s) at +25m0s". Every failed condition stays in the text.
func failedCondition(why string) string {
	if i := strings.LastIndex(why, "; incident ["); i >= 0 {
		why = why[:i]
	}
	return why
}
