// Package look is the setup half of `obs look`: it brings up (or reuses) the sandbox,
// finds the changed dashboards and works out, for each one, the Grafana URL to open, the
// viewport and where to save the screenshot. It then renders a prompt for the calling
// agent, who opens the browser, takes the screenshots, judges them against the visual
// checklist and writes the pull request comment. obs look itself calls no LLM, reads no
// key, spends nothing and posts nothing (owner ruling, 17 Nov 2026).
package look

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/sandbox"
	"github.com/AtomiCloud/obs/internal/seed"
)

// Options is one `obs look` run.
type Options struct {
	Dir       string   // the observability folder
	SeedPath  string   // default <Dir>/seed.yaml (only used to fill query variables)
	Base      string   // git ref the changed dashboards are measured against
	All       bool     // every dashboard, not only the changed ones
	Files     []string // explicit dashboard files (relative to Dir); skips git
	Checklist string   // the visual checklist markdown
	Out       string   // where the screenshots go (as printed in the prompt)
	Width     int
	Height    int
	Project   string                                           // sandbox project (the obs.sandbox label), as printed in the cleanup command
	Runtime   string                                           // container runtime name (docker or podman), as printed in the cleanup command
	Git       func(dir string, args ...string) (string, error) // default: the git binary
}

// Viewport is the browser window size.
type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Var is one template variable with the value the URL sets.
type Var struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
	Source string   `json:"source"` // why this value: current, first option, seed, datasource, default
}

// Panel is one panel of a dashboard.
type Panel struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
	URL   string `json:"url"`
	Shot  string `json:"screenshot"`
	// ExpectedEmpty is set when every datasource of the panel is traces or profiles: the sandbox has no
	// trace or profile seed, so No data is expected there (the reviewer marks the No-data item n/a).
	ExpectedEmpty string `json:"expected_empty,omitempty"` // traces or profiles
}

// Dashboard is one dashboard to review.
type Dashboard struct {
	File   string  `json:"file"`
	Title  string  `json:"title"`
	UID    string  `json:"uid"`
	URL    string  `json:"url"`
	Shot   string  `json:"screenshot"`
	Vars   []Var   `json:"variables"`
	Panels []Panel `json:"panels"`
}

// Plan is everything the calling agent needs. It is also the --json output.
type Plan struct {
	OK            bool          `json:"ok"`
	Error         string        `json:"error,omitempty"`
	Warnings      []string      `json:"warnings,omitempty"`
	Base          string        `json:"base,omitempty"`
	Sandbox       *sandbox.Info `json:"sandbox,omitempty"`
	Project       string        `json:"project,omitempty"` // the sandbox project and runtime of this run: the cleanup command names them
	Runtime       string        `json:"runtime,omitempty"`
	Viewport      Viewport      `json:"viewport"`
	ChecklistFile string        `json:"checklist_file"`
	Checklist     []string      `json:"checklist"`
	Dashboards    []Dashboard   `json:"dashboards"`
	Prompt        string        `json:"prompt"`
}

// Setup does the work. It never fails the process: a failure is Plan.OK=false and Plan.Error.
// up brings up or reuses the sandbox (the caller wires it to sandbox.Up).
func Setup(ctx context.Context, o Options, up func(context.Context) (*sandbox.Info, error)) *Plan {
	if o.Width == 0 {
		o.Width = 1600
	}
	if o.Height == 0 {
		o.Height = 1000
	}
	if o.Out == "" {
		o.Out = "obs-look"
	}
	if o.Git == nil {
		o.Git = gitRun
	}
	p := &Plan{Base: o.Base, Viewport: Viewport{o.Width, o.Height}, Project: o.Project, Runtime: o.Runtime, ChecklistFile: o.Checklist, Checklist: []string{}, Dashboards: []Dashboard{}}
	defer func() { p.Prompt = render(p) }()

	items, err := ReadChecklist(o.Checklist)
	if err != nil {
		p.Warnings = append(p.Warnings, err.Error())
	}
	p.Checklist = append(p.Checklist, items...)

	files, err := changed(o)
	if err != nil {
		p.Error = "cannot tell which dashboards changed: " + err.Error()
		return p
	}
	if len(files) == 0 {
		p.OK = true
		return p
	}
	info, err := up(ctx)
	if err != nil {
		p.Error = "sandbox setup failed: " + err.Error()
		return p
	}
	p.Sandbox = info
	var ds *seed.Dataset
	if info.From != 0 {
		sp := o.SeedPath
		if sp == "" {
			sp = filepath.Join(o.Dir, "seed.yaml")
		}
		if sd, err := seed.Load(sp); err != nil {
			p.Warnings = append(p.Warnings, "query variables are not filled from the seed: "+err.Error())
		} else if ds, err = seed.Build(sd, time.UnixMilli(info.From).UTC()); err != nil {
			p.Warnings = append(p.Warnings, "query variables are not filled from the seed: "+err.Error())
		}
	}
	for _, f := range files {
		d, err := buildDashboard(o, info, ds, f)
		if err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s: skipped: %v", f, err))
			continue
		}
		p.Dashboards = append(p.Dashboards, *d)
	}
	p.OK = true
	return p
}

// ReadChecklist returns the bullet items of the "Visual checklist" section of the
// dashboard standard: the lines that start with "- " under the first heading that
// mentions "checklist", up to the next heading or rule.
func ReadChecklist(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("checklist %s: %v (pass --checklist)", path, err)
	}
	var items []string
	in := false
	var cur *string
	for _, ln := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "#"):
			if in && len(items) > 0 {
				return items, nil
			}
			in = strings.Contains(strings.ToLower(t), "checklist")
			cur = nil
		case in && t == "---":
			return items, nil
		case in && strings.HasPrefix(t, "- "):
			items = append(items, strings.TrimSpace(strings.TrimPrefix(t, "- ")))
			cur = &items[len(items)-1]
		case in && cur != nil && t != "" && (strings.HasPrefix(ln, "  ") || strings.HasPrefix(ln, "\t")):
			*cur += " " + t // a wrapped bullet
		case in && t == "":
			cur = nil
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("checklist %s: no bullet items under a heading that mentions \"checklist\" (pass --checklist)", path)
	}
	return items, nil
}

type dashJSON struct {
	UID        string `json:"uid"`
	Title      string `json:"title"`
	Panels     []panelJSON
	Templating struct {
		List []varJSON `json:"list"`
	} `json:"templating"`
}

type panelJSON struct {
	ID         int64           `json:"id"`
	Title      string          `json:"title"`
	Type       string          `json:"type"`
	Datasource json.RawMessage `json:"datasource"`
	Targets    []struct {
		Datasource json.RawMessage `json:"datasource"`
	} `json:"targets"`
	Panels []panelJSON `json:"panels"`
}

type varJSON struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Query      any    `json:"query"`
	IncludeAll bool   `json:"includeAll"`
	Multi      bool   `json:"multi"`
	Options    []struct {
		Value any `json:"value"`
	} `json:"options"`
	Current struct {
		Value any `json:"value"`
	} `json:"current"`
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func buildDashboard(o Options, info *sandbox.Info, ds *seed.Dataset, rel string) (*Dashboard, error) {
	b, err := os.ReadFile(filepath.Join(o.Dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	var dj dashJSON
	if err := json.Unmarshal(b, &dj); err != nil {
		return nil, fmt.Errorf("not valid dashboard JSON: %v", err)
	}
	var raw struct {
		Panels []panelJSON `json:"panels"`
	}
	_ = json.Unmarshal(b, &raw)
	uid := sandbox.DashboardUID(rel, dj.UID)
	title := dj.Title
	if title == "" {
		title = rel
	}
	base := slug(strings.TrimSuffix(rel, ".json"))
	d := &Dashboard{File: rel, Title: title, UID: uid, Shot: filepath.ToSlash(filepath.Join(o.Out, base+".png"))}
	for _, v := range dj.Templating.List {
		if vv := fillVar(v, info, ds); vv != nil {
			d.Vars = append(d.Vars, *vv)
		}
	}
	dsVars := map[string]string{} // datasource variable name -> plugin type it lists
	for _, v := range dj.Templating.List {
		if v.Type == "datasource" {
			dsVars[v.Name] = strings.ToLower(str(v.Query))
		}
	}
	q := func(extra ...string) string {
		parts := []string{"orgId=1"}
		if info.From != 0 {
			parts = append(parts, fmt.Sprintf("from=%d", info.From), fmt.Sprintf("to=%d", info.To))
		}
		for _, v := range d.Vars {
			for _, val := range v.Values {
				parts = append(parts, "var-"+v.Name+"="+url.QueryEscape(val))
			}
		}
		return strings.Join(append(parts, extra...), "&")
	}
	path := "/d/" + url.PathEscape(uid)
	if s := slug(title); s != "" {
		path += "/" + s
	}
	d.URL = info.Grafana + path + "?" + q("kiosk")
	var walk func(ps []panelJSON)
	walk = func(ps []panelJSON) {
		for _, p := range ps {
			if p.Type != "row" {
				d.Panels = append(d.Panels, Panel{
					ID: p.ID, Title: p.Title, Type: p.Type, ExpectedEmpty: expectedEmpty(p, dsVars),
					URL:  info.Grafana + path + "?" + q(fmt.Sprintf("viewPanel=panel-%d", p.ID), "kiosk"),
					Shot: filepath.ToSlash(filepath.Join(o.Out, fmt.Sprintf("%s-panel-%d.png", base, p.ID))),
				})
			}
			walk(p.Panels)
		}
	}
	walk(raw.Panels)
	sort.SliceStable(d.Panels, func(i, j int) bool { return d.Panels[i].ID < d.Panels[j].ID })
	return d, nil
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if q, ok := t["query"].(string); ok {
			return q
		}
	case nil:
		return ""
	case float64, bool:
		return fmt.Sprint(t)
	}
	return ""
}

var labelValuesRe = regexp.MustCompile(`label_values\(\s*(?:([^,]+?)\s*,\s*)?([a-zA-Z_][a-zA-Z0-9_.]*)\s*\)`)

// fillVar picks the value the URL sets for one variable: the dashboard's own current
// value, else the datasource of the sandbox, else the first option or the first value on
// the seed (all values when the variable can be All), else nothing (Grafana decides).
func fillVar(v varJSON, info *sandbox.Info, ds *seed.Dataset) *Var {
	if v.Type == "datasource" {
		return fillDatasourceVar(v, info)
	}
	out := &Var{Name: v.Name}
	switch cur := v.Current.Value.(type) {
	case string:
		if cur != "" {
			out.Values, out.Source = []string{cur}, "current"
		}
	case []any:
		for _, x := range cur {
			if s := str(x); s != "" {
				out.Values = append(out.Values, s)
			}
		}
		if len(out.Values) > 0 {
			out.Source = "current"
		}
	}
	if len(out.Values) > 0 {
		return out
	}
	query := str(v.Query)
	switch v.Type {
	case "custom":
		for _, s := range strings.Split(query, ",") {
			if s = strings.TrimSpace(s); s != "" {
				return &Var{Name: v.Name, Values: []string{s}, Source: "first option"}
			}
		}
	case "interval":
		if len(v.Options) > 0 {
			return &Var{Name: v.Name, Values: []string{str(v.Options[0].Value)}, Source: "first option"}
		}
		return &Var{Name: v.Name, Values: []string{"5m"}, Source: "default"}
	case "textbox", "constant":
		if query != "" {
			return &Var{Name: v.Name, Values: []string{query}, Source: "default"}
		}
		return nil
	case "query":
		if v.IncludeAll || v.Multi {
			if v.IncludeAll {
				return &Var{Name: v.Name, Values: []string{"$__all"}, Source: "all values"}
			}
		}
		if m := labelValuesRe.FindStringSubmatch(query); m != nil && ds != nil {
			metric, label := "", strings.TrimSpace(m[2])
			if m[1] != "" {
				metric = strings.TrimSpace(m[1])
				if i := strings.IndexAny(metric, "{"); i >= 0 {
					metric = metric[:i]
				}
			}
			if vs := ds.LabelValues(metric, label, ds.Start.UnixMilli(), ds.End.UnixMilli()); len(vs) > 0 {
				return &Var{Name: v.Name, Values: []string{vs[0]}, Source: "first value on the seed"}
			}
		}
	}
	return nil
}

// datasourceKinds maps the plugin type a datasource variable lists to the sandbox's datasource kind.
var datasourceKinds = map[string]string{"prometheus": "metrics", "victoriametrics-logs-datasource": "logs", "tempo": "traces", "grafana-pyroscope-datasource": "profiles"}

// fillDatasourceVar resolves a datasource variable against the sandbox first. A saved current
// uid is kept only when the sandbox provisioned it (a stale uid from another Grafana would send
// every panel to a datasource that does not exist here); otherwise the sandbox's own datasource
// of that kind is used.
func fillDatasourceVar(v varJSON, info *sandbox.Info) *Var {
	provisioned := map[string]bool{}
	for _, us := range info.Datasources {
		for _, u := range us {
			provisioned[u] = true
		}
	}
	var cur []string
	switch c := v.Current.Value.(type) {
	case string:
		cur = []string{c}
	case []any:
		for _, x := range c {
			cur = append(cur, str(x))
		}
	}
	keep := len(cur) > 0
	for _, u := range cur {
		if u == "" || !provisioned[u] {
			keep = false
		}
	}
	if keep {
		return &Var{Name: v.Name, Values: cur, Source: "current"}
	}
	if us := info.Datasources[datasourceKinds[strings.ToLower(str(v.Query))]]; len(us) > 0 {
		return &Var{Name: v.Name, Values: []string{us[0]}, Source: "sandbox datasource"}
	}
	return nil
}

// dsRef reads a datasource reference (an object {type, uid} or a bare string uid).
func dsRef(raw json.RawMessage, dsVars map[string]string) (typ, uid string) {
	if len(raw) == 0 {
		return "", ""
	}
	var o struct{ Type, UID string }
	if json.Unmarshal(raw, &o) != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return "", ""
		}
		o.UID = s
	}
	typ, uid = o.Type, o.UID
	name := strings.Trim(uid, "$[]{}")
	if strings.HasPrefix(uid, "$") || strings.HasPrefix(uid, "[[") {
		if t, ok := dsVars[name]; ok {
			return t, uid // the variable lists one plugin type: that is the type
		}
	}
	return typ, uid
}

// refKind says what a datasource reference holds in the sandbox: "traces" and "profiles"
// (no seed for either), "data" (metrics or logs, seeded) or "" (expressions, mixed, unknown).
func refKind(typ, uid string) string {
	t, u := strings.ToLower(typ), strings.ToLower(uid)
	switch {
	case strings.Contains(t, "pyroscope"), strings.Contains(t, "phlare"):
		return "profiles"
	case (t == "" || t == "datasource") && (strings.Contains(u, "pyroscope") || strings.Contains(u, "profil")):
		return "profiles"
	case t == "mixed" || t == "grafana" || t == "__expr__" || u == "__expr__" || strings.HasPrefix(u, "-- "):
		return ""
	}
	k, err := files.Classify(typ, uid)
	switch {
	case err != nil:
		return ""
	case k == files.Traces:
		return "traces"
	case k == files.Metrics || k == files.Logs || k == files.Loki:
		return "data"
	}
	return ""
}

// expectedEmpty says "traces" or "profiles" when every datasource of the panel is one of those
// (the sandbox seeds only metrics and logs), "traces and profiles" for both, else "".
func expectedEmpty(p panelJSON, dsVars map[string]string) string {
	kinds := map[string]bool{}
	add := func(raw json.RawMessage) {
		if k := refKind(dsRef(raw, dsVars)); k != "" {
			kinds[k] = true
		}
	}
	add(p.Datasource)
	for _, t := range p.Targets {
		add(t.Datasource)
	}
	switch {
	case kinds["data"]:
		return ""
	case kinds["traces"] && kinds["profiles"]:
		return "traces and profiles"
	case kinds["traces"]:
		return "traces"
	case kinds["profiles"]:
		return "profiles"
	}
	return ""
}
