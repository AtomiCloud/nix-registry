package live

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/AtomiCloud/obs/internal/look"
)

// LookOptions of Look.
type LookOptions struct {
	Checklist     string // the visual checklist markdown (default docs/standards/grafana-dashboards/index.md)
	Match         string // keep dashboards whose title, folder or tag contains this (case-insensitive)
	Out           string // where the reviewer saves screenshots
	Width, Height int
}

// LookDashboard is one dashboard the target's Grafana already has.
type LookDashboard struct {
	Title  string   `json:"title"`
	UID    string   `json:"uid"`
	Folder string   `json:"folder,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	URL    string   `json:"url"`
	Shot   string   `json:"screenshot"`
}

// LookResult is what `obs live look` prints; it is also the --json form. It never fails.
type LookResult struct {
	OK             bool            `json:"ok"`
	Error          string          `json:"error,omitempty"`
	Target         string          `json:"target"`
	Grafana        string          `json:"grafana"`
	GrafanaVersion string          `json:"grafana_version,omitempty"`
	Warnings       []string        `json:"warnings,omitempty"`
	ChecklistFile  string          `json:"checklist_file"`
	Checklist      []string        `json:"checklist"`
	Dashboards     []LookDashboard `json:"dashboards"`
	Prompt         string          `json:"prompt"`
}

// Look lists the dashboards the target's Grafana already has. obs installs nothing into a real
// cluster: dashboards get there only through the platform chart.
func Look(ctx context.Context, c *Client, t *Target, o LookOptions) *LookResult {
	if o.Out == "" {
		o.Out = "obs-look"
	}
	r := &LookResult{Target: t.Name, Grafana: t.Grafana, ChecklistFile: o.Checklist, Checklist: []string{}, Dashboards: []LookDashboard{}}
	defer func() { r.Prompt = renderLook(r, o) }()
	if items, err := look.ReadChecklist(o.Checklist); err != nil {
		r.Warnings = append(r.Warnings, err.Error())
	} else {
		r.Checklist = items
	}
	if t.Grafana == "" {
		r.Error = "the target has no grafana URL (targets." + t.Name + ".grafana in obs.yaml)"
		return r
	}
	var health struct {
		Version string `json:"version"`
	}
	if err := c.GetJSON(ctx, t.Grafana, "/api/health", nil, &health); err != nil {
		r.Error = err.Error()
		return r
	}
	r.GrafanaVersion = health.Version
	var found []struct {
		UID, Title, URL, FolderTitle string
		Tags                         []string
	}
	q := url.Values{"type": {"dash-db"}, "limit": {"5000"}}
	if err := c.GetJSON(ctx, t.Grafana, "/api/search", q, &found); err != nil {
		r.Error = err.Error()
		return r
	}
	m := strings.ToLower(o.Match)
	for _, d := range found {
		hay := strings.ToLower(d.Title + "\x00" + d.FolderTitle + "\x00" + strings.Join(d.Tags, "\x00"))
		if m != "" && !strings.Contains(hay, m) {
			continue
		}
		r.Dashboards = append(r.Dashboards, LookDashboard{
			Title: d.Title, UID: d.UID, Folder: d.FolderTitle, Tags: d.Tags,
			URL:  t.Grafana + d.URL + "?from=now-6h&to=now",
			Shot: fmt.Sprintf("%s/%s.png", strings.TrimRight(o.Out, "/"), d.UID),
		})
	}
	r.OK = true
	return r
}

func renderLook(r *LookResult, o LookOptions) string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	if !r.OK {
		w("obs live look: not run for target %s, nothing to review. obs live look exits 0 on purpose; this is not a CI failure.", r.Target)
		w("  what failed: %s", r.Error)
		for _, x := range r.Warnings {
			w("  note: %s", x)
		}
		return b.String()
	}
	w("obs live look: review %d dashboard(s) already on %s (Grafana %s) in a browser", len(r.Dashboards), r.Target, r.GrafanaVersion)
	w("")
	w("You are the reviewer. obs live look installs nothing, changes nothing and calls no LLM: it lists only what the platform chart already deployed to this cluster. The screenshots and the judgement are yours.")
	w("A dashboard that is not merged and deployed yet is not here: check it with `obs look` on the seed, or deploy the chart to your local cluster and use `--target local`.")
	w("")
	w("GRAFANA: %s (log in yourself; obs never reads or stores a login)", r.Grafana)
	w("")
	w("DASHBOARDS (real data, last 6 hours; do not change the time range)")
	for _, d := range r.Dashboards {
		folder := ""
		if d.Folder != "" {
			folder = " [" + d.Folder + "]"
		}
		w("  - %s%s", d.Title, folder)
		w("    %s", d.URL)
		w("    screenshot: %s", d.Shot)
	}
	if len(r.Dashboards) == 0 {
		w("  (none match)")
	}
	w("")
	w("VISUAL CHECKLIST (%s)", r.ChecklistFile)
	for _, it := range r.Checklist {
		w("  - %s", it)
	}
	if len(r.Checklist) == 0 {
		w("  (no checklist read; pass --checklist)")
	}
	for _, x := range r.Warnings {
		w("note: %s", x)
	}
	w("")
	w("For each dashboard: open the URL, wait for the panels to load, take a screenshot, then go through the checklist. Real data has real gaps and spikes: judge whether the dashboard reads well on them, not whether it matches the seed. Report what you found per dashboard.")
	return b.String()
}
