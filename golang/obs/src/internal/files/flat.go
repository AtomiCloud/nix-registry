package files

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/obsyaml"
	"github.com/VictoriaMetrics/metricsql"
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

// isLandscapeName tells the label or field a landscape is stored under (concept section 2).
func isLandscapeName(n string) bool { return n == "landscape" || n == "durian.cloud.landscape" }

// landscapeUse reads the expression with the engines' own parsers and returns the first label
// matcher (metrics) or field filter (logs) on the landscape, e.g. `landscape=~"r.*"` or
// `landscape:raichu`. Text that only contains the word (a quoted message, a label value, a metric
// name) is not a selector. An expression that does not parse returns "": the checker reports it.
func landscapeUse(datasource, expr string) string {
	switch datasource {
	case "metrics":
		e, err := metricsql.Parse(expr)
		if err != nil {
			return ""
		}
		found := ""
		metricsql.VisitAll(e, func(x metricsql.Expr) {
			me, ok := x.(*metricsql.MetricExpr)
			if !ok || found != "" {
				return
			}
			for _, group := range me.LabelFilterss {
				for _, lf := range group {
					if isLandscapeName(lf.Label) {
						op := "="
						switch {
						case lf.IsRegexp && lf.IsNegative:
							op = "!~"
						case lf.IsRegexp:
							op = "=~"
						case lf.IsNegative:
							op = "!="
						}
						found = fmt.Sprintf("%s%s%q", lf.Label, op, lf.Value)
						return
					}
				}
			}
		})
		return found
	case "logs":
		q, err := vl.Parse(expr, time.Now())
		if err != nil {
			return ""
		}
		filters, _, _ := q.Fields()
		for _, f := range filters {
			if isLandscapeName(strings.TrimPrefix(f, "_stream:")) {
				return f + " filter"
			}
		}
	}
	return ""
}

// boolModifier reports whether a PromQL expression compares with the `bool` modifier, which the
// standard forbids (any series the expression returns fires, even with value 0: the comparison
// must filter). Read from the parse tree, so the word inside a string or label value is not one.
func boolModifier(expr string) bool {
	e, err := metricsql.Parse(expr)
	if err != nil {
		return false
	}
	found := false
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		if b, ok := x.(*metricsql.BinaryOpExpr); ok && b.Bool {
			found = true
		}
	})
	return found
}

// emojiRanges are the code points of emoji and pictographs, plus the joiner and the variation
// selector that make a symbol render as one. The chart adds 🚨, ⚠️ or ℹ️ from the file name, so a
// title that carries its own shows it twice.
var emojiRanges = [][2]rune{
	{0x1F000, 0x1FAFF}, {0x2600, 0x27BF}, {0x2300, 0x23FF}, {0x2B00, 0x2BFF}, {0x25A0, 0x25FF},
	{0x2190, 0x21FF}, {0x2934, 0x2935}, {0x3030, 0x3030}, {0x303D, 0x303D}, {0x3297, 0x3299},
	{0x203C, 0x203C}, {0x2049, 0x2049}, {0x2122, 0x2122}, {0x2139, 0x2139}, {0x24C2, 0x24C2},
	{0xFE0F, 0xFE0F}, {0x200D, 0x200D}, {0xE0020, 0xE007F},
}

func emojiIn(title string) (rune, bool) {
	for _, r := range title {
		for _, rg := range emojiRanges {
			if r >= rg[0] && r <= rg[1] {
				return r, true
			}
		}
	}
	return 0, false
}

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

// resolveIdentity reads platform and service for the rule uid. Both come from <dir>/obs.yaml
// through the obs.yaml loader (obsyaml.Load, the one every command uses, so whatever file the loader
// finds for a folder is the one read here): `platform:` and `service:`. When obs.yaml has no
// `service:`, the contract's `service:` (<dir>/contract.yaml) is used. A missing, unreadable or
// invalid file is no value; the loader's own error is reported by the command that loads it.
func resolveIdentity(dir string) FlatIdentity {
	var id FlatIdentity
	f, _ := obsyaml.Load(dir)
	if f != nil {
		if f.Platform != "" {
			id.Platform, id.PlatformFrom = f.Platform, f.Path+" platform"
		}
		if f.Service != "" {
			id.Service, id.ServiceFrom = f.Service, f.Path+" service"
		}
	}
	if id.Service == "" {
		if b, err := os.ReadFile(filepath.Join(dir, "contract.yaml")); err == nil {
			var m struct {
				Service string `yaml:"service"`
			}
			if yaml.Unmarshal(b, &m) == nil && strings.TrimSpace(m.Service) != "" {
				id.Service, id.ServiceFrom = strings.TrimSpace(m.Service), filepath.Join(dir, "contract.yaml")+" service"
			}
		}
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
	if f, ok := str["for"]; ok {
		if _, err := ParseFor(f); err != nil {
			s.flatProblem(rel, "flat-alert-for", "for %v", err)
		}
	}
	if e, ok := str["expr"]; ok {
		if u := landscapeUse(str["datasource"], e); u != "" {
			s.flatProblem(rel, "flat-alert-landscape", "expr selects landscape (%s): each landscape's rules already read only that landscape's data, so expr never selects it", u)
		}
		if str["datasource"] == "metrics" && boolModifier(e) {
			s.flatProblem(rel, "flat-alert-bool", "expr uses the bool modifier: any series the expr returns fires, even with value 0, so let the comparison filter (drop bool)")
		}
	}
	if t, ok := str["title"]; ok {
		if r, bad := emojiIn(t); bad {
			s.flatProblem(rel, "flat-alert-title-emoji", "title %q holds the emoji %q: the chart adds the severity emoji from the file name, so a title carries none", t, string(r))
		}
	}
	panel := ""
	if v, ok := m["panel"]; ok {
		switch n := v.(type) {
		case nil:
			s.flatProblem(rel, "flat-alert-panel", "panel is null: write an integer panel id of dashboards/overview.json, or leave the key out")
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
	last := &s.Rules[len(s.Rules)-1]
	last.Flat = true
	// The standard's runbook is the sibling alerts/<slug>/runbook.md; obs cannot build the URL
	// (no repo URL), so the file stands for the runbook_url annotation.
	if _, err := os.Stat(filepath.Join(s.Dir, "alerts", slug, "runbook.md")); err == nil {
		last.RunbookFile = "alerts/" + slug + "/runbook.md"
	}
}

// Text is the problem as a one-line message: with its rule id in front when it has one.
func (p Problem) Text() string {
	if p.Rule == "" {
		return p.Msg
	}
	return "[" + p.Rule + "] " + p.Msg
}
