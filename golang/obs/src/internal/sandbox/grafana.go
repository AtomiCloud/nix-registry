package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/AtomiCloud/obs/internal/files"
)

// Datasource is one Grafana datasource of the sandbox.
type Datasource struct {
	Kind string // metrics, logs, traces, profiles
	UID  string
	Type string // Grafana plugin id
	URL  string // address inside the sandbox network
}

// Datasources lists the datasources to create: every concrete uid the repo's dashboards
// name (a platform pins its uids: fixture-metrics ...), and one default per kind
// (obs-metrics, obs-logs, obs-traces, obs-profiles) when no dashboard names one.
func Datasources(set *files.Set, project string) []Datasource {
	type kind struct{ name, typ, comp string }
	kinds := map[files.Kind]kind{
		files.Metrics: {"metrics", "prometheus", "vm"},
		files.Logs:    {"logs", PluginID, "vl"},
		files.Traces:  {"traces", "tempo", "tempo"},
	}
	seen := map[string]map[string]bool{}
	for _, q := range set.Queries {
		k, ok := kinds[q.Kind]
		if !ok || q.DSUID == "" || strings.HasPrefix(q.DSUID, "$") {
			continue
		}
		if seen[k.name] == nil {
			seen[k.name] = map[string]bool{}
		}
		seen[k.name][q.DSUID] = true
	}
	// Profile panels have no MetricsQL/LogsQL/TraceQL query, so files.Set.Queries
	// cannot carry their datasource references. Read those refs from the dashboards
	// the shared reader already found (including nested rows and target overrides).
	var profileRefs func(any)
	profileRefs = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if ds, ok := x["datasource"].(map[string]any); ok {
				typ, _ := ds["type"].(string)
				uid, _ := ds["uid"].(string)
				if strings.Contains(strings.ToLower(typ), "pyroscope") && uid != "" && !strings.HasPrefix(uid, "$") {
					if seen["profiles"] == nil {
						seen["profiles"] = map[string]bool{}
					}
					seen["profiles"][uid] = true
				}
			}
			for _, child := range x {
				profileRefs(child)
			}
		case []any:
			for _, child := range x {
				profileRefs(child)
			}
		}
	}
	for _, d := range set.Dashboards {
		b, err := os.ReadFile(filepath.Join(set.Dir, filepath.FromSlash(d.File)))
		if err != nil {
			continue
		} // ImportDashboards reports the file error.
		var raw any
		if json.Unmarshal(b, &raw) == nil {
			profileRefs(raw)
		}
	}
	pyro, _ := ByName("pyroscope")
	all := []kind{kinds[files.Metrics], kinds[files.Logs], kinds[files.Traces], {"profiles", "grafana-pyroscope-datasource", "pyroscope"}}
	var out []Datasource
	for _, k := range all {
		comp, _ := ByName(k.comp)
		if k.name == "profiles" {
			comp = pyro
		}
		addr := fmt.Sprintf("http://%s:%d", containerName(project, comp), comp.Port)
		var uids []string
		for u := range seen[k.name] {
			uids = append(uids, u)
		}
		sort.Strings(uids)
		if len(uids) == 0 {
			uids = []string{"obs-" + k.name}
		}
		for _, u := range uids {
			out = append(out, Datasource{Kind: k.name, UID: u, Type: k.typ, URL: addr})
		}
	}
	return out
}

func send(ctx context.Context, hc *http.Client, method, url string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	req.SetBasicAuth("admin", "admin")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, b, nil
}

// Provision creates or updates the datasources through Grafana's API (idempotent).
func Provision(ctx context.Context, hc *http.Client, grafana string, dss []Datasource) error {
	firstMetrics := true
	for _, d := range dss {
		def := map[string]any{
			"uid": d.UID, "name": d.UID, "type": d.Type, "access": "proxy", "url": d.URL,
			"isDefault": d.Kind == "metrics" && firstMetrics,
		}
		if d.Kind == "metrics" {
			firstMetrics = false
		}
		code, _, err := send(ctx, hc, http.MethodGet, grafana+"/api/datasources/uid/"+url.PathEscape(d.UID), nil)
		if err != nil {
			return err
		}
		method, target := http.MethodPost, grafana+"/api/datasources"
		if code == http.StatusOK {
			method, target = http.MethodPut, grafana+"/api/datasources/uid/"+url.PathEscape(d.UID)
		}
		code, b, err := send(ctx, hc, method, target, def)
		if err != nil {
			return err
		}
		if code/100 != 2 {
			return fmt.Errorf("grafana datasource %s: HTTP %d: %s", d.UID, code, strings.TrimSpace(string(b)))
		}
	}
	return nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// DashboardUID is the uid a dashboard has in the sandbox: its own, else one made from the
// file path (obs-dashboards-overview) so the URL is stable between runs.
func DashboardUID(rel, own string) string {
	if own != "" {
		return own
	}
	return "obs-" + strings.Trim(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSuffix(rel, ".json")), "-"), "-")
}

// ImportDashboards loads every dashboard of the folder (overwrite, the General folder).
func ImportDashboards(ctx context.Context, hc *http.Client, grafana string, set *files.Set) (int, error) {
	return importDashboards(ctx, hc, grafana, set, "")
}

func importDashboards(ctx context.Context, hc *http.Client, grafana string, set *files.Set, project string) (int, error) {
	n := 0
	for _, d := range set.Dashboards {
		b, err := os.ReadFile(filepath.Join(set.Dir, filepath.FromSlash(d.File)))
		if err != nil {
			return n, err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return n, fmt.Errorf("%s: %w", d.File, err)
		}
		own, _ := m["uid"].(string)
		m["uid"] = DashboardUID(d.File, own)
		m["id"] = nil
		if project != "" {
			enableIncidentAnnotations(m)
			if len(set.Rules) > 0 {
				addAlertStatePanel(m, project)
			}
		}
		code, body, err := send(ctx, hc, http.MethodPost, grafana+"/api/dashboards/db",
			map[string]any{"dashboard": m, "overwrite": true, "message": "obs sandbox up"})
		if err != nil {
			return n, err
		}
		if code/100 != 2 {
			return n, fmt.Errorf("grafana dashboard %s: HTTP %d: %s", d.File, code, strings.TrimSpace(string(body)))
		}
		n++
	}
	return n, nil
}

func enableIncidentAnnotations(dashboard map[string]any) {
	annotations, _ := dashboard["annotations"].(map[string]any)
	if annotations == nil {
		annotations = map[string]any{}
		dashboard["annotations"] = annotations
	}
	list, _ := annotations["list"].([]any)
	for _, raw := range list {
		if q, ok := raw.(map[string]any); ok && q["builtIn"] == float64(1) {
			q["enable"] = true
			return
		}
	}
	annotations["list"] = append(list, map[string]any{"builtIn": 1, "datasource": map[string]string{"type": "grafana", "uid": "-- Grafana --"}, "enable": true, "hide": true, "iconColor": "rgba(255, 96, 96, 1)", "name": "Annotations & Alerts", "type": "dashboard"})
}

// addAlertStatePanel uses Grafana 13.3.0's native alertlist plugin, including
// inactive (normal) rules. Source: public/app/plugins/panel/alertlist/types.ts at
// v13.3.0. The panel queries Grafana, never obs's computed replay annotations.
func addAlertStatePanel(dashboard map[string]any, project string) int64 {
	maxID, bottom := int64(0), float64(0)
	var walk func([]any)
	walk = func(panels []any) {
		for _, raw := range panels {
			p, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := p["id"].(float64); ok {
				maxID = max(maxID, int64(id))
			}
			if g, ok := p["gridPos"].(map[string]any); ok {
				y, _ := g["y"].(float64)
				h, _ := g["h"].(float64)
				bottom = max(bottom, y+h)
			}
			children, _ := p["panels"].([]any)
			walk(children)
		}
	}
	panels, _ := dashboard["panels"].([]any)
	walk(panels)
	id := maxID + 1
	dashboard["panels"] = append(panels, map[string]any{"id": id, "title": "Sandbox alert state (Grafana live)", "type": "alertlist",
		"gridPos": map[string]any{"x": 0, "y": bottom, "w": 24, "h": 8},
		"options": map[string]any{"folder": map[string]any{"uid": "obs-" + project, "title": "obs sandbox " + project}, "dashboardAlerts": false,
			"showInactiveAlerts": true, "showInstances": true, "maxItems": 100, "sortOrder": 1, "groupMode": "default", "groupBy": []string{}, "viewMode": "list",
			"stateFilter": map[string]bool{"firing": true, "pending": true, "normal": true, "recovering": true, "noData": true, "error": true}}})
	return id
}
