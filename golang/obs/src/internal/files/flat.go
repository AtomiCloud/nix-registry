package files

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// The flat alert tier file of the platform standard (docs/standards/observability/alerts.md, "The
// tier file"): `alerts/<slug>/<severity>.yaml` is ONE flat YAML map. The standard's chart
// (shipping.md, "What the chart adds to each alert") turns it into one GrafanaAlertRuleGroup CR
// holding one rule. obs builds that same rule here, so every command that reads alerts (check,
// query, alerts, test, look, live, sandbox) sees a flat file as the rule the chart renders. The CR
// form stays accepted unchanged.
//
// What obs cannot know: the landscape (each landscape has its own Grafana, obs checks one), the
// module, the repo URL (runbook_url) and the generic dashboard uid. Those labels and annotations
// are left out; the rule's query, condition, state handling, uid, title and severity are the chart's.

// FlatIdentity is where the rule uid's <platform>-<service> comes from.
type FlatIdentity struct {
	Platform, Service string
	// PlatformFrom and ServiceFrom say which file the value was read from ("" when unset).
	PlatformFrom, ServiceFrom string
}

// flatFields are the keys of the standard's table: the only keys a tier file may hold.
var flatFields = map[string]bool{"title": true, "datasource": true, "expr": true, "for": true, "summary": true, "description": true, "panel": true}

// flatRequired are the required ones, in the table's order.
var flatRequired = []string{"title", "datasource", "expr", "for", "summary", "description"}

// severities: the file name is the severity, with the title prefix the chart adds.
var severities = map[string]string{"critical": "🚨", "warning": "⚠️", "info": "ℹ️"}

var (
	flatDuration = regexp.MustCompile(`^([0-9]+(ms|s|m|h|d|w|y))+$`)
	// landscapeSel matches `landscape` used as a label or field name (a selector or a filter), never
	// as part of another word: each landscape's rules already read only that landscape's data.
	landscapeSel = regexp.MustCompile(`(^|[^A-Za-z0-9_.])"?(durian\.cloud\.)?landscape"?\s*(=~|!~|!=|=|:)`)
)

// isFlat tells a tier file from a CR: no kind, no apiVersion, and at least one key of the table.
func isFlat(m map[string]any) bool {
	if _, ok := m["kind"]; ok {
		return false
	}
	if _, ok := m["apiVersion"]; ok {
		return false
	}
	for k := range m {
		if flatFields[k] {
			return true
		}
	}
	return false
}

// resolveIdentity reads platform and service for the rule uid: `platform:` and `service:` of
// <dir>/obs.yaml, and the contract's `service:` (<dir>/contract.yaml) when obs.yaml has none.
// A missing or unreadable file is no value; obs.yaml's own errors are reported by its loader.
func resolveIdentity(dir string) FlatIdentity {
	var id FlatIdentity
	read := func(name string) map[string]any {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil
		}
		var m map[string]any
		if yaml.Unmarshal(b, &m) != nil {
			return nil
		}
		return m
	}
	str := func(m map[string]any, k string) string {
		s, _ := m[k].(string)
		return strings.TrimSpace(s)
	}
	obs, ct := read("obs.yaml"), read("contract.yaml")
	if v := str(obs, "platform"); v != "" {
		id.Platform, id.PlatformFrom = v, "obs.yaml platform"
	}
	if v := str(obs, "service"); v != "" {
		id.Service, id.ServiceFrom = v, "obs.yaml service"
	} else if v := str(ct, "service"); v != "" {
		id.Service, id.ServiceFrom = v, "contract.yaml service"
	}
	return id
}

func (s *Set) flatProblem(rel, rule, format string, args ...any) {
	s.Problems = append(s.Problems, Problem{File: rel, Rule: rule, Msg: fmt.Sprintf(format, args...)})
}

// readFlat validates one flat tier file and, when it is clean, appends the rule the chart renders.
func (s *Set) readFlat(rel string, m map[string]any) {
	before := len(s.Problems)
	parts := strings.Split(rel, "/")
	var slug, sev string
	if len(parts) == 3 && parts[0] == "alerts" {
		slug, sev = parts[1], strings.TrimSuffix(strings.TrimSuffix(parts[2], ".yaml"), ".yml")
	}
	if _, ok := severities[sev]; !ok || (!strings.HasSuffix(parts[len(parts)-1], ".yaml")) {
		s.flatProblem(rel, "flat-alert-file-name", "a tier file is alerts/<slug>/critical.yaml, warning.yaml or info.yaml; the file name is the severity (got %q)", rel)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if flatFields[k] {
			continue
		}
		if k == "severity" {
			s.flatProblem(rel, "flat-alert-field-unknown", "key %q: there is no severity field; the file name is the severity", k)
			continue
		}
		s.flatProblem(rel, "flat-alert-field-unknown", "key %q is not a tier file field (title, datasource, expr, for, summary, description, panel)", k)
	}
	str := map[string]string{}
	for _, k := range flatRequired {
		v, present := m[k]
		if !present || v == nil {
			s.flatProblem(rel, "flat-alert-field-missing", "required field %q is missing", k)
			continue
		}
		t, ok := v.(string)
		if !ok {
			s.flatProblem(rel, "flat-alert-field-type", "field %q must be a string, got %v", k, v)
			continue
		}
		if strings.TrimSpace(t) == "" {
			s.flatProblem(rel, "flat-alert-field-missing", "required field %q is empty", k)
			continue
		}
		str[k] = t
	}
	if t, ok := str["title"]; ok {
		if n := utf8.RuneCountInString(t); n > 48 {
			s.flatProblem(rel, "flat-alert-title-length", "title %q is %d characters; the notification title may hold at most 48", t, n)
		}
	}
	if d, ok := str["datasource"]; ok && d != "metrics" && d != "logs" {
		s.flatProblem(rel, "flat-alert-datasource", "datasource %q: want metrics (PromQL) or logs (LogsQL)", d)
	}
	if f, ok := str["for"]; ok && !flatDuration.MatchString(f) {
		s.flatProblem(rel, "flat-alert-for", "for %q is not a duration such as 0m, 10m or 2h", f)
	}
	if e, ok := str["expr"]; ok {
		if loc := landscapeSel.FindStringSubmatchIndex(e); loc != nil {
			s.flatProblem(rel, "flat-alert-landscape", "expr selects landscape (%q): each landscape's rules already read only that landscape's data, so expr never selects it", strings.TrimSpace(e[loc[0]:loc[1]]))
		}
	}
	panel := ""
	if v, ok := m["panel"]; ok && v != nil {
		switch n := v.(type) {
		case int:
			panel = fmt.Sprint(n)
		case int64:
			panel = fmt.Sprint(n)
		case uint64:
			panel = fmt.Sprint(n)
		default:
			s.flatProblem(rel, "flat-alert-panel", "panel must be an integer (a panel id of dashboards/overview.json), got %v", v)
		}
	}
	id := s.ident
	if id.Platform == "" || id.Service == "" {
		missing := "platform is"
		fix := "`platform:` in <observability>/obs.yaml"
		if id.Platform != "" {
			missing, fix = "service is", "the contract's `service:` (<observability>/contract.yaml) or `service:` in <observability>/obs.yaml"
		} else if id.Service == "" {
			missing, fix = "platform and service are", "`platform:` in <observability>/obs.yaml and the contract's `service:` (or `service:` in obs.yaml)"
		}
		s.flatProblem(rel, "flat-alert-identity", "the rule uid is <platform>-<service>-<slug>-<severity>, but the %s not known: write %s", missing, fix)
	}
	if len(s.Problems) > before {
		return
	}
	uid := id.Platform + "-" + id.Service + "-" + slug + "-" + sev
	if n := len(uid); n > 40 {
		s.flatProblem(rel, "flat-alert-uid-length", "rule uid %q is %d characters; Grafana's limit is 40 (platform %s, service %s, slug %s, severity %s)", uid, n, id.Platform, id.Service, slug, sev)
		return
	}
	dsUID, model := "metrics", map[string]any{"expr": str["expr"], "instant": true}
	if str["datasource"] == "logs" {
		dsUID, model = "logs", map[string]any{"expr": str["expr"], "queryType": "stats"}
	}
	ann := map[string]string{"summary": str["summary"], "description": str["description"]}
	if panel != "" {
		ann["__dashboardUid__"] = id.Platform + "-" + id.Service + "-overview"
		ann["__panelId__"] = panel
	}
	r := alertRule{
		UID: uid, Title: severities[sev] + " " + str["title"], Condition: "B", For: str["for"],
		NoDataState: "OK", ExecErrState: "Error",
		Labels:      map[string]string{"severity": sev, "platform": id.Platform, "service": id.Service},
		Annotations: ann,
	}
	a := alertData{RefID: "A", DatasourceUID: dsUID, Model: model}
	if str["datasource"] == "logs" {
		a.QueryType = "stats"
	}
	a.RelativeTimeRange.From, a.RelativeTimeRange.To = 600, 0
	b := alertData{RefID: "B", DatasourceUID: "__expr__", Model: map[string]any{"type": "math", "expression": "is_number($A) || is_nan($A) || is_inf($A)"}}
	r.Data = []alertData{a, b}
	s.addRule(rel, uid, "1m", r)
}

// Text is the problem as a one-line message: with its rule id in front when it has one.
func (p Problem) Text() string {
	if p.Rule == "" {
		return p.Msg
	}
	return "[" + p.Rule + "] " + p.Msg
}
