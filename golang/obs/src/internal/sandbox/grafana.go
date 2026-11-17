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
// name (a platform pins its uids: primordial-metrics ...), and one default per kind
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
