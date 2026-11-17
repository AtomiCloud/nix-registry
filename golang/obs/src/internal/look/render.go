package look

import (
	"fmt"
	"strings"
	"time"
)

const marker = "<!-- obs-look -->"

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
		w("  next: fix the cause above and run `obs look` again. If the sandbox is half up, `obs sandbox down` clears it (it removes only containers, volumes and the network labelled obs.sandbox=<project>).")
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
	w("  4. When done, `obs sandbox down` stops the stack (only the containers, volumes and network labelled obs.sandbox=%s).", p.Sandbox.Project)
	w("")
	w("DASHBOARDS")
	for i, d := range p.Dashboards {
		w("  %d. %s  (%s)", i+1, d.Title, d.File)
		w("     open: %s", d.URL)
		w("     save: %s", d.Shot)
		if len(d.Vars) > 0 {
			var vs []string
			for _, v := range d.Vars {
				vs = append(vs, fmt.Sprintf("$%s=%s (%s)", v.Name, strings.Join(v.Values, ","), v.Source))
			}
			w("     variables set in the URL: %s", strings.Join(vs, "; "))
			w("     every other variable keeps Grafana's default; check that each populates and changes the view")
		}
		if len(d.Panels) > 0 {
			w("     panels (open one only when the board shot is not enough):")
			for _, pn := range d.Panels {
				w("       - panel %d %q (%s): %s -> %s", pn.ID, pn.Title, pn.Type, pn.URL, pn.Shot)
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
