package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
)

// IncidentInfo records an applied window and its declared alert expectations.
// These are expectations for screenshot review, never measured alert states.
type IncidentInfo struct {
	Name  string   `json:"name"`
	Story string   `json:"story,omitempty"`
	From  int64    `json:"from_ms"`
	To    int64    `json:"to_ms"`
	Fire  []string `json:"fire,omitempty"`
	Quiet []string `json:"quiet,omitempty"`
}

// ReplayTransition is a historical state change computed by obs's existing
// Grafana state-machine port. It is never presented as native Grafana history.
type ReplayTransition struct {
	Incident string `json:"incident,omitempty"`
	Alert    string `json:"alert"`
	At       int64  `json:"at_ms"`
	From     string `json:"from"`
	To       string `json:"to"`
}

func AnnotateReplay(ctx context.Context, hc *http.Client, grafana string, uids []string, replay []ReplayTransition) error {
	sort.SliceStable(replay, func(i, j int) bool {
		if replay[i].At != replay[j].At {
			return replay[i].At < replay[j].At
		}
		if replay[i].Alert != replay[j].Alert {
			return replay[i].Alert < replay[j].Alert
		}
		return replay[i].To < replay[j].To
	})
	for _, uid := range uids {
		for _, tr := range replay {
			text := "replay (computed by obs): " + html.EscapeString(tr.Alert) + ": " + html.EscapeString(tr.From) + " -> " + html.EscapeString(tr.To)
			if tr.Incident != "" {
				text += " [incident " + html.EscapeString(tr.Incident) + "]"
			}
			code, b, err := send(ctx, hc, http.MethodPost, grafana+"/api/annotations", map[string]any{"dashboardUID": uid, "time": tr.At, "text": text, "tags": []string{"obs-alert-replay", tr.Alert}})
			if err != nil {
				return err
			}
			if code/100 != 2 {
				return fmt.Errorf("grafana replay %s on %s: HTTP %d: %s", tr.Alert, uid, code, b)
			}
		}
	}
	return nil
}

// ProvisionAlerts installs the exact queries and timing from the shared CR reader
// into a sandbox-only folder. The Grafana scheduler shows current state; it does
// not backfill historical state when loading historical samples.
func ProvisionAlerts(ctx context.Context, hc *http.Client, grafana, project string, set *files.Set) error {
	if len(set.Problems) > 0 {
		return fmt.Errorf("%s: %s", set.Problems[0].File, set.Problems[0].Text())
	}
	folder := "obs-" + project
	if len(set.Rules) == 0 {
		// A retained stack may still hold groups of an earlier checkout: with no rule left, none stays.
		return removeStaleGroups(ctx, hc, grafana, folder, nil)
	}
	code, b, err := send(ctx, hc, http.MethodGet, grafana+"/api/folders/"+url.PathEscape(folder), nil)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound {
		code, b, err = send(ctx, hc, http.MethodPost, grafana+"/api/folders", map[string]any{"uid": folder, "title": "obs sandbox " + project})
	}
	if err != nil {
		return err
	}
	if code/100 != 2 {
		return fmt.Errorf("grafana alert folder: HTTP %d: %s", code, b)
	}
	type group struct {
		interval int64
		rules    []any
	}
	groups := map[string]*group{}
	metricTarget := "obs-metrics"
	for _, ds := range Datasources(set, project) {
		if ds.Kind == "metrics" {
			metricTarget = ds.UID
			break
		}
	}
	seen := map[string]bool{}
	for _, rr := range set.Rules {
		r := rr.Definition
		uid := r.UID
		if uid == "" {
			uid = rr.Name
		}
		if uid == "" || seen[uid] {
			return fmt.Errorf("%s rule %q: missing or duplicate uid", rr.File, uid)
		}
		seen[uid] = true
		interval := time.Minute
		if rr.Interval != "" {
			interval, err = time.ParseDuration(rr.Interval)
			if err != nil || interval < time.Second {
				return fmt.Errorf("%s: invalid interval %q", rr.File, rr.Interval)
			}
		}
		name := rr.Name
		if name == "" {
			name = strings.TrimSuffix(rr.File, ".yaml")
		}
		g := groups[name]
		if g == nil {
			g = &group{interval: int64(interval / time.Second)}
			groups[name] = g
		}
		if g.interval != int64(interval/time.Second) {
			return fmt.Errorf("%s: group %s has inconsistent intervals", rr.File, name)
		}
		var data []any
		for _, q := range r.Data {
			data = append(data, map[string]any{"refId": q.RefID, "queryType": q.QueryType, "datasourceUid": q.DatasourceUID,
				"relativeTimeRange": map[string]int64{"from": q.RelativeTimeRange.From, "to": q.RelativeTimeRange.To}, "model": q.Model})
		}
		title := r.Title
		if title == "" {
			title = uid
		}
		noData, execErr := r.NoDataState, r.ExecErrState
		if noData == "" {
			noData = "NoData"
		}
		if execErr == "" {
			execErr = "Error"
		}
		forDuration := r.For
		if forDuration == "" {
			forDuration = "0s"
		}
		rule := map[string]any{"uid": uid, "title": title, "condition": r.Condition, "for": forDuration, "noDataState": noData, "execErrState": execErr,
			"folderUID": folder, "ruleGroup": name, "data": data, "labels": r.Labels}
		if rr.Flat {
			// A flat tier file's summary, description and panel link ride along, as the chart renders them.
			// (A CR's annotations were never provisioned in 0.5; that stays.)
			rule["annotations"] = r.Annotations
		}
		if r.KeepFiringFor != "" {
			rule["keep_firing_for"] = r.KeepFiringFor
		}
		if r.MissingSeriesEvalsToResolve != 0 {
			rule["missing_series_evals_to_resolve"] = r.MissingSeriesEvalsToResolve
		}
		if r.Record != nil {
			rule["record"] = map[string]any{"metric": r.Record.Metric, "from": r.Record.From, "target_datasource_uid": metricTarget}
		}
		g.rules = append(g.rules, rule)
	}
	var names []string
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g := groups[name]
		code, b, err := send(ctx, hc, http.MethodPut, grafana+"/api/v1/provisioning/folder/"+url.PathEscape(folder)+"/rule-groups/"+url.PathEscape(name),
			map[string]any{"title": name, "folderUid": folder, "interval": g.interval, "rules": g.rules})
		if err != nil {
			return err
		}
		if code/100 != 2 {
			return fmt.Errorf("grafana alert group %s: HTTP %d: %s", name, code, b)
		}
	}
	keep := map[string]bool{}
	for _, name := range names {
		keep[name] = true
	}
	return removeStaleGroups(ctx, hc, grafana, folder, keep)
}

// removeStaleGroups deletes the rule groups in obs's sandbox folder that the checkout no longer has.
// Grafana replaces the group a PUT addresses, not the folder, so a retained stack keeps evaluating a
// removed group (and shows it in the live alert-state panel) until it is deleted. The folder is made by
// obs for this sandbox only; groups of any other folder are never listed for deletion.
func removeStaleGroups(ctx context.Context, hc *http.Client, grafana, folder string, keep map[string]bool) error {
	code, b, err := send(ctx, hc, http.MethodGet, grafana+"/api/v1/provisioning/alert-rules", nil)
	if err != nil {
		return err
	}
	if code/100 != 2 {
		return fmt.Errorf("grafana alert rules: HTTP %d: %s", code, b)
	}
	var listed []struct {
		FolderUID string `json:"folderUID"`
		RuleGroup string `json:"ruleGroup"`
	}
	if err := json.Unmarshal(b, &listed); err != nil {
		return fmt.Errorf("grafana alert rules: unexpected answer: %w", err)
	}
	stale := map[string]bool{}
	for _, r := range listed {
		if r.FolderUID == folder && r.RuleGroup != "" && !keep[r.RuleGroup] {
			stale[r.RuleGroup] = true
		}
	}
	var names []string
	for name := range stale {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		code, b, err := send(ctx, hc, http.MethodDelete, grafana+"/api/v1/provisioning/folder/"+url.PathEscape(folder)+"/rule-groups/"+url.PathEscape(name), nil)
		if err != nil {
			return err
		}
		if code/100 != 2 && code != http.StatusNotFound {
			return fmt.Errorf("grafana removing alert group %s: HTTP %d: %s", name, code, b)
		}
	}
	return nil
}

// AnnotateIncidents creates dashboard-scoped region annotations, using Grafana's
// built-in annotations query. The text labels fire/quiet as declared expectations.
func AnnotateIncidents(ctx context.Context, hc *http.Client, grafana string, uids []string, incidents []IncidentInfo) error {
	for _, uid := range uids {
		for _, inc := range incidents {
			text := "incident " + html.EscapeString(inc.Name) + ": " + html.EscapeString(inc.Story)
			if len(inc.Fire) > 0 {
				text += "<br>Expected firing in this window: " + html.EscapeString(strings.Join(inc.Fire, ", "))
			}
			if len(inc.Quiet) > 0 {
				text += "<br>Expected normal: " + html.EscapeString(strings.Join(inc.Quiet, ", "))
			}
			code, b, err := send(ctx, hc, http.MethodPost, grafana+"/api/annotations", map[string]any{"dashboardUID": uid, "time": inc.From, "timeEnd": inc.To, "text": text, "tags": []string{"obs-incident", inc.Name}})
			if err != nil {
				return err
			}
			if code/100 != 2 {
				return fmt.Errorf("grafana incident %s on %s: HTTP %d: %s", inc.Name, uid, code, b)
			}
		}
	}
	return nil
}
