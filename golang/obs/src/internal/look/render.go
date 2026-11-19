package look

import (
	"fmt"
	"strings"
	"time"
)

const marker = "<!-- obs-look -->"

// cleanup is the exact command that removes this run's sandbox: the project and the runtime
// it used, so a non-default --project or --runtime is not left running.
func cleanup(p *Plan) string {
	c := "obs sandbox down"
	proj := p.Project
	if p.Sandbox != nil && p.Sandbox.Project != "" {
		proj = p.Sandbox.Project
	}
	if proj != "" {
		c += " --project " + proj
	}
	if p.Runtime != "" {
		c += " --runtime " + p.Runtime
	}
	return c
}

// noDataItem names the checklist item about "No data" (C1 in the standard).
func noDataItem(items []string) string {
	for i, it := range items {
		if strings.Contains(strings.ToLower(it), "no data") {
			return fmt.Sprintf("C%d", i+1)
		}
	}
	return "C1"
}

// render writes the prompt for the calling agent. It is plain text, stable for a given
// plan (the golden test pins it), and carries every fact the agent needs.
func render(p *Plan) string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	if !p.OK {
		w("obs look: setup failed, nothing to review. obs look exits 0 on purpose; this is not a CI failure.")
		w("  what failed: %s", p.Error)
		for _, x := range p.Warnings {
			w("  note: %s", x)
		}
		w("  next: fix the cause above and run `obs look` again. If the sandbox is half up, `" + cleanup(p) + "` clears it (it removes only containers, volumes and the network labelled obs.sandbox=<project>).")
		return b.String()
	}
	if len(p.Dashboards) == 0 {
		w("obs look: no changed dashboards (against %s), nothing to review.", p.Base)
		for _, x := range p.Warnings {
			w("  note: %s", x)
		}
		return b.String()
	}
	w("obs look: review %d dashboard(s) in a browser", len(p.Dashboards))
	w("")
	w("You are the reviewer. obs look has set the stack up and calls no LLM; the screenshots and the judgement are yours.")
	w("")
	w("SANDBOX")
	w("  Grafana: %s (anonymous viewer, nothing to log in to)", p.Sandbox.Grafana)
	if p.Sandbox.From != 0 {
		w("  seed window: %s to %s (the URLs fix it; do not change the time range)",
			time.UnixMilli(p.Sandbox.From).UTC().Format(time.RFC3339), time.UnixMilli(p.Sandbox.To).UTC().Format(time.RFC3339))
	} else {
		w("  no seed was loaded: the stores are empty, so every panel will say No data (that is a finding only if the repo has a seed)")
	}
	if p.Base != "" {
		w("  changed dashboards are those that differ from %s", p.Base)
	}
	for _, x := range p.Warnings {
		w("  note: %s", x)
	}
	if p.Sandbox.LiveIncident != "" {
		w("  live incident: %s; inspect Grafana's native current Firing/Normal state in Alerting. The seed is a fixed snapshot: current state can change as it ages.", p.Sandbox.LiveIncident)
	} else if len(p.Sandbox.Incidents) > 0 {
		w("  no live incident: this is a historical seed (--seed-base or a reused seed). Grafana's native state is current; historical annotations are replay (computed by obs).")
	} else {
		w("  no incidents in this seed: Grafana's native state is current; no incident Firing state is expected.")
	}
	w("")
	w("STEPS")
	w("  1. For each dashboard below, open its URL in a browser with a viewport of %d x %d px.", p.Viewport.Width, p.Viewport.Height)
	w("     Wait until every panel has stopped loading (no spinners), then take a FULL-PAGE screenshot (the board is taller than the viewport)")
	w("     and save it at the given path. Keep the width at %d px.", p.Viewport.Width)
	w("  2. Open each screenshot and look at it. Judge it against EVERY checklist item: pass, fail or unclear.")
	w("     For a fail or unclear item, name the panel by title and say what you see. Do not judge from the JSON; judge from the image.")
	w("     If a panel needs a closer look, open its panel URL and save that shot too.")
	w("  3. Write ONE pull request comment for all dashboards, in the layout under COMMENT LAYOUT, with the images attached.")
	w("     If the pull request already has a comment that starts with %s, edit that one instead of adding a second.", marker)
	w("     GitHub has no image-upload API: paste or drag each PNG into the comment box in the browser (GitHub uploads it and inserts the link),")
	w("     or upload it where the repo keeps assets and link it. If you cannot attach images, say so in the comment and give the file names.")
	if p.Keep {
		w("  4. --keep leaves the stack running for a person after review. When they are done, `%s` stops it.", cleanup(p))
	} else {
		w("  4. When done, `%s` stops the stack (only the containers, volumes and network labelled obs.sandbox=%s).", cleanup(p), p.Sandbox.Project)
	}
	w("")
	w("DASHBOARDS")
	for i, d := range p.Dashboards {
		w("  %d. %s  (%s)", i+1, d.Title, d.File)
		w("     open: %s", d.URL)
		w("     save: %s", d.Shot)
		for _, incident := range p.Sandbox.Incidents {
			w("     incident marker to see: %s (%s to %s)", incident.Name, time.UnixMilli(incident.From).UTC().Format(time.RFC3339), time.UnixMilli(incident.To).UTC().Format(time.RFC3339))
			if len(incident.Fire) > 0 {
				w("     expected firing inside that window: %s", strings.Join(incident.Fire, ", "))
			}
			if len(incident.Quiet) > 0 {
				w("     expected normal for that incident: %s", strings.Join(incident.Quiet, ", "))
			}
			if incident.Name == p.Sandbox.LiveIncident {
				w("     LIVE: inspect Grafana's native current alert state for this incident; wait through each rule's pending duration and evaluation interval.")
				for _, rule := range p.Sandbox.LiveRules {
					provisioned := time.UnixMilli(rule.ProvisionedAt).UTC().Format(time.RFC3339)
					if rule.Expected == "Normal" {
						w("     live rule %s: expected Normal; provisioned %s (for=%s, interval=%s).", rule.UID, provisioned, rule.For, rule.Interval)
						if !rule.Supported {
							w("     snapshot prediction disagrees with Normal for %s; inspect the native state and record a finding.", rule.UID)
						}
						continue
					}
					if rule.Supported {
						w("     live rule %s: provisioned %s; expected Firing by about %s (a scheduled estimate: provisioned + for %s rounded up to whole intervals of %s, + one interval; Grafana staggers when it evaluates inside each interval, so the real change can come a few seconds later). Snapshot predicted to hold Firing until %s. Do not screenshot on the clock: wait until the alert-state panel shows Firing for %s, then screenshot before that time (prediction computed by obs, verify Grafana).", rule.UID, provisioned, time.UnixMilli(rule.FiringAt).UTC().Format(time.RFC3339), rule.For, rule.Interval, time.UnixMilli(rule.HoldUntil).UTC().Format(time.RFC3339), rule.UID)
					} else {
						w("     live rule %s: snapshot does not support the expected Firing window (for=%s, interval=%s). Record a finding; do not claim native Firing from replay annotations.", rule.UID, rule.For, rule.Interval)
					}
				}
			} else {
				w("     REPLAY: state-change annotations say 'replay (computed by obs)'; they come from obs's existing Grafana state-machine port, not Grafana's own history.")
			}
		}
		if len(d.Vars) > 0 {
			var vs []string
			for _, v := range d.Vars {
				vs = append(vs, fmt.Sprintf("$%s=%s (%s)", v.Name, strings.Join(v.Values, ","), v.Source))
			}
			w("     variables set in the URL: %s", strings.Join(vs, "; "))
			w("     every other variable keeps Grafana's default; check that each populates and changes the view")
		}
		if len(d.Panels) > 0 {
			var empty []string
			for _, pn := range d.Panels {
				if pn.ExpectedEmpty != "" {
					empty = append(empty, fmt.Sprintf("panel %d %q (%s)", pn.ID, pn.Title, pn.ExpectedEmpty))
				}
			}
			if len(empty) > 0 {
				w("     expected empty in the sandbox; mark %s n/a for these panels (no seed for the named signal was loaded): %s", noDataItem(p.Checklist), strings.Join(empty, "; "))
				w("     judge %s on the other panels, and judge every other item on these panels too", noDataItem(p.Checklist))
			}
			w("     panels (open one only when the board shot is not enough):")
			for _, pn := range d.Panels {
				mark := ""
				if pn.ExpectedEmpty != "" {
					mark = " [expected empty: " + pn.ExpectedEmpty + "]"
				}
				w("       - panel %d %q (%s)%s: %s -> %s", pn.ID, pn.Title, pn.Type, mark, pn.URL, pn.Shot)
			}
		}
	}
	w("")
	w("CHECKLIST (from %s)", p.ChecklistFile)
	if len(p.Checklist) == 0 {
		w("  none could be read (see the note above). Ask for the right --checklist path before judging; do not invent items.")
	}
	for i, it := range p.Checklist {
		w("  C%d. %s", i+1, it)
	}
	w("")
	w("COMMENT LAYOUT")
	w("  %s", marker)
	w("  ## Dashboard review (obs look)")
	w("  Sandbox seed window <from> to <to>; reviewed by <your name> from screenshots at %d px wide.", p.Viewport.Width)
	w("")
	w("  ### <dashboard title> (`<file>`)")
	w("  ![<dashboard title>](<image link>)")
	w("")
	w("  | Item | Verdict | Evidence |")
	w("  |---|---|---|")
	if len(p.Checklist) > 0 {
		w("  | C1 %s | pass / fail / unclear | panel title and what you see (one line) |", p.Checklist[0])
	} else {
		w("  | <checklist item> | pass / fail / unclear | panel title and what you see (one line) |")
	}
	w("  | ...one row per checklist item, every item, in order | | |")
	w("")
	w("  **Fix first:** <the fails, worst first, one line each; write `nothing` when every item passes>")
	w("")
	w("  (repeat the ### block for each dashboard)")
	return b.String()
}
