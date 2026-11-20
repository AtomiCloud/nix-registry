// Package files reads the repo's observability folder: alert files
// (`alerts/<slug>/<severity>.yaml`, one GrafanaAlertRuleGroup CR per file) and
// dashboards (`dashboards/**/*.json`), and finds the seed file.
package files

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Query is one query of an alert rule or a dashboard panel.
type Query struct {
	File    string // path relative to the observability dir
	Where   string // `rule crash-logs-warning query A`, `panel 4 "Title" target A`
	IsAlert bool
	Kind    Kind
	Expr    string
	// Alerts: the rule's relative time range in seconds ("from" of relativeTimeRange).
	RangeSecs int64
	// ToSecs is the "to" of relativeTimeRange: the window ends this many seconds before the evaluation.
	ToSecs    int64
	QueryType string // the model's queryType (stats, instant, range ...)
	// Dashboards.
	Dash          *Dashboard
	PanelType     string
	Interval      string // the panel's min step
	MaxDataPoints int64
	// Problem is set when the query could not be classified; the checker reports it.
	Problem string
	DSType  string
	DSUID   string
	// Model is an alert query's raw model (nil for dashboards): datasource options the
	// alert checks need beyond the expression, such as Tempo's metricsQueryType and step.
	Model map[string]any
}

// Dashboard is the parts of a dashboard JSON obs needs.
type Dashboard struct {
	File  string
	Title string
	From  string // time.from, e.g. now-6h
	To    string
	Vars  []Variable
}

// Variable is one template variable.
type Variable struct {
	Name    string
	Type    string // query, custom, interval, textbox, constant, datasource
	Query   string
	Options []string
	Current string
	// Multi is true when the dashboard lets a viewer pick several values or "All".
	Multi bool
}

// Problem is a file that could not be read as what it claims to be.
type Problem struct {
	File string
	Msg  string
	// Rule is the rule id a finding carries; empty means the generic "file" rule.
	Rule string
}

// Set is everything read from the folder.
type Set struct {
	Dir        string
	Queries    []Query
	Dashboards []*Dashboard
	Problems   []Problem
	Alerts     int    // alert files read
	Rules      []Rule // complete alert definitions, including Grafana expressions

	ident FlatIdentity // platform and service of the rule uid of a flat tier file
}

// Load reads dir. A missing dashboards/ or alerts/ folder is fine; nothing at all is not.
func Load(dir string) (*Set, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", dir)
	}
	s := &Set{Dir: dir, ident: resolveIdentity(dir)}
	for _, sub := range []string{"alerts", "dashboards"} {
		root := filepath.Join(dir, sub)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		var paths []string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			switch {
			case sub == "alerts" && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")):
				paths = append(paths, p)
			case sub == "dashboards" && strings.HasSuffix(p, ".json"):
				paths = append(paths, p)
			}
			return nil
		})
		sort.Strings(paths)
		for _, p := range paths {
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			b, err := os.ReadFile(p)
			if err != nil {
				s.Problems = append(s.Problems, Problem{File: rel, Msg: err.Error()})
				continue
			}
			if sub == "alerts" {
				s.Alerts++
				s.readAlert(rel, b)
			} else {
				s.readDashboard(rel, b)
			}
		}
	}
	return s, nil
}

type alertDoc struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Interval string      `yaml:"interval"`
		Rules    []alertRule `yaml:"rules"`
	} `yaml:"spec"`
}

type alertRule struct {
	UID                         string            `yaml:"uid"`
	Title                       string            `yaml:"title"`
	Condition                   string            `yaml:"condition"`
	For                         string            `yaml:"for"`
	KeepFiringFor               string            `yaml:"keepFiringFor"`
	NoDataState                 string            `yaml:"noDataState"`
	ExecErrState                string            `yaml:"execErrState"`
	MissingSeriesEvalsToResolve int               `yaml:"missingSeriesEvalsToResolve"`
	Labels                      map[string]string `yaml:"labels"`
	Annotations                 map[string]string `yaml:"annotations"`
	Record                      *struct {
		Metric string `yaml:"metric"`
		From   string `yaml:"from"`
	} `yaml:"record"`
	Data []alertData `yaml:"data"`
}

// alertData is one query or expression of a rule.
type alertData struct {
	RefID             string `yaml:"refId"`
	QueryType         string `yaml:"queryType"`
	DatasourceUID     string `yaml:"datasourceUid"`
	RelativeTimeRange struct {
		From int64 `yaml:"from"`
		To   int64 `yaml:"to"`
	} `yaml:"relativeTimeRange"`
	Model map[string]any `yaml:"model"`
}

// Rule is a Grafana-managed rule read by Load, shared with obs alerts. File and
// Interval come from the containing CR, all other fields are Grafana's schema.
type Rule struct {
	File, Interval, Name string
	Definition           alertRule
	// Flat is true for a rule built from a flat tier file; RunbookFile is the sibling
	// alerts/<slug>/runbook.md of such a rule when it exists (relative to the observability folder).
	Flat        bool
	RunbookFile string
}

func (s *Set) readAlert(rel string, b []byte) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var d alertDoc
	if err := dec.Decode(&d); err != nil {
		// A file that reads as a flat tier file (top-level tier keys, no kind) says so with a rule id, so
		// the commands that skip unreadable files (obs live incidents) stop on it instead.
		rule := ""
		if looksFlat(b) {
			rule = "flat-alert-yaml"
		}
		s.Problems = append(s.Problems, Problem{File: rel, Rule: rule, Msg: "not valid YAML: " + err.Error()})
		return
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		rule, msg := "", "more than one YAML document: one GrafanaAlertRuleGroup CR per file"
		// Judge the first document by what it parsed to: a flat tier followed by another document (say a
		// ConfigMap) is a flat tier with trailing documents, not a CR.
		var first map[string]any
		if yaml.NewDecoder(bytes.NewReader(b)).Decode(&first) == nil && isFlat(first) {
			rule, msg = "flat-alert-documents", "more than one YAML document: a tier file is one flat map"
		} else if looksFlat(b) {
			rule, msg = "flat-alert-documents", "more than one YAML document: a tier file is one flat map"
		}
		s.Problems = append(s.Problems, Problem{File: rel, Rule: rule, Msg: msg})
		return
	}
	if d.Kind == "" && d.APIVersion == "" {
		var m map[string]any
		if yaml.Unmarshal(b, &m) == nil && isFlat(m) {
			s.readFlat(rel, m)
			return
		}
	}
	if d.Kind != "GrafanaAlertRuleGroup" {
		s.Problems = append(s.Problems, Problem{File: rel, Msg: fmt.Sprintf("kind %q: want GrafanaAlertRuleGroup", d.Kind)})
		return
	}
	if len(d.Spec.Rules) == 0 {
		s.Problems = append(s.Problems, Problem{File: rel, Msg: "spec.rules is empty"})
		return
	}
	for _, r := range d.Spec.Rules {
		s.addRule(rel, d.Metadata.Name, d.Spec.Interval, r)
	}
}

// addRule records one rule of the file rel (group name) and the queries it holds.
func (s *Set) addRule(rel, name, interval string, r alertRule) {
	s.Rules = append(s.Rules, Rule{File: rel, Interval: interval, Name: name, Definition: r})
	id := r.UID
	if id == "" {
		id = name
	}
	for _, q := range r.Data {
		dsType, dsUID := "", q.DatasourceUID
		if m, ok := q.Model["datasource"].(map[string]any); ok {
			if t, ok := m["type"].(string); ok {
				dsType = t
			}
			if u, ok := m["uid"].(string); ok && dsUID == "" {
				dsUID = u
			}
		}
		if t, ok := q.Model["type"].(string); ok && dsUID == "__expr__" {
			dsType = "__expr__"
			_ = t // math / reduce / threshold: Grafana expressions, nothing to parse here
		}
		qt := q.QueryType
		if t, ok := q.Model["queryType"].(string); ok && t != "" {
			qt = t
		}
		expr, _ := q.Model["expr"].(string)
		if expr == "" && (dsType == "tempo" || strings.Contains(dsUID, "tempo") || strings.Contains(dsUID, "traces")) {
			expr, _ = q.Model["query"].(string)
		}
		qu := Query{
			File: rel, IsAlert: true, Expr: expr, RangeSecs: q.RelativeTimeRange.From, ToSecs: q.RelativeTimeRange.To,
			QueryType: qt, DSType: dsType, DSUID: dsUID,
			Model: q.Model,
			Where: fmt.Sprintf("rule %s query %s", id, q.RefID),
		}
		kind, err := Classify(dsType, dsUID)
		qu.Kind = kind
		if err != nil {
			qu.Problem = err.Error()
		}
		if kind == Other && err == nil {
			continue // an expression: nothing to parse in this milestone
		}
		if expr == "" && err == nil && kind != Loki {
			qu.Problem = "model.expr is empty"
		}
		s.Queries = append(s.Queries, qu)
	}
}

type dashDoc struct {
	Title      string                        `json:"title"`
	Time       struct{ From, To string }     `json:"time"`
	Panels     []panelDoc                    `json:"panels"`
	Rows       []struct{ Panels []panelDoc } `json:"rows"`
	Datasource any                           `json:"datasource"`
	Templating struct {
		List []struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			Query   any    `json:"query"`
			Options []struct {
				Value any `json:"value"`
			} `json:"options"`
			Current struct {
				Value any `json:"value"`
			} `json:"current"`
			Multi      bool `json:"multi"`
			IncludeAll bool `json:"includeAll"`
		} `json:"list"`
	} `json:"templating"`
}

type panelDoc struct {
	ID            int64       `json:"id"`
	Type          string      `json:"type"`
	Title         string      `json:"title"`
	Datasource    any         `json:"datasource"`
	Interval      string      `json:"interval"`
	MaxDataPoints int64       `json:"maxDataPoints"`
	Panels        []panelDoc  `json:"panels"`
	Targets       []targetDoc `json:"targets"`
}

type targetDoc struct {
	RefID      string `json:"refId"`
	Datasource any    `json:"datasource"`
	Expr       string `json:"expr"`
	Expression string `json:"expression"`
	Query      string `json:"query"` // Tempo's TraceQL query model
	QueryType  string `json:"queryType"`
	Hide       bool   `json:"hide"`
}

// dsRef reads a datasource reference: an object {type, uid}, a bare uid string, or nil.
func dsRef(v any) (typ, uid string) {
	switch t := v.(type) {
	case string:
		return "", t
	case map[string]any:
		typ, _ = t["type"].(string)
		uid, _ = t["uid"].(string)
	}
	return
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if q, ok := t["query"].(string); ok {
			return q
		}
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
	return ""
}

func (s *Set) readDashboard(rel string, b []byte) {
	var d dashDoc
	if err := json.Unmarshal(b, &d); err != nil {
		s.Problems = append(s.Problems, Problem{File: rel, Msg: "not valid dashboard JSON: " + err.Error()})
		return
	}
	dash := &Dashboard{File: rel, Title: d.Title, From: d.Time.From, To: d.Time.To}
	if dash.From == "" {
		dash.From = "now-6h"
	}
	if dash.To == "" {
		dash.To = "now"
	}
	dsVarType := map[string]string{}
	for _, v := range d.Templating.List {
		vr := Variable{Name: v.Name, Type: v.Type, Query: asString(v.Query), Current: asString(v.Current.Value), Multi: v.Multi || v.IncludeAll}
		for _, o := range v.Options {
			vr.Options = append(vr.Options, asString(o.Value))
		}
		dash.Vars = append(dash.Vars, vr)
		if v.Type == "datasource" {
			dsVarType[v.Name] = vr.Query
		}
	}
	s.Dashboards = append(s.Dashboards, dash)
	dDSType, dDSUID := dsRef(d.Datasource)
	var walk func(ps []panelDoc)
	walk = func(ps []panelDoc) {
		for _, p := range ps {
			walk(p.Panels)
			pType, pUID := dsRef(p.Datasource)
			for i, t := range p.Targets {
				expr := t.Expr
				if expr == "" {
					expr = t.Expression
				}
				if expr == "" {
					expr = t.Query
				}
				ref := t.RefID
				if ref == "" {
					ref = fmt.Sprint(i + 1)
				}
				dsType, dsUID := dsRef(t.Datasource)
				if dsType == "" && dsUID == "" {
					dsType, dsUID = pType, pUID
				}
				if dsUID == "-- Mixed --" || (dsType == "" && dsUID == "") {
					dsType, dsUID = dDSType, dDSUID
				}
				// A datasource chosen by a template variable: its `query` holds the plugin type.
				if name := strings.Trim(dsUID, "${}"); strings.HasPrefix(dsUID, "$") {
					if t, ok := dsVarType[name]; ok && dsType == "" {
						dsType = t
					}
				}
				qu := Query{
					File: rel, Expr: expr, QueryType: t.QueryType, Dash: dash,
					PanelType: p.Type, Interval: p.Interval, MaxDataPoints: p.MaxDataPoints,
					DSType: dsType, DSUID: dsUID,
					Where: fmt.Sprintf("panel %d %q target %s", p.ID, p.Title, ref),
				}
				kind, err := Classify(dsType, dsUID)
				if expr == "" && (kind != Traces || t.QueryType == "") {
					continue
				}
				qu.Kind = kind
				if err != nil {
					qu.Problem = err.Error()
					if dsType == "" && strings.HasPrefix(dsUID, "$") {
						qu.Problem = fmt.Sprintf("datasource %q is a template variable, but templating.list has no datasource variable of that name; define it (type datasource) or name the datasource", dsUID)
					}
				}
				if kind == Other && err == nil {
					continue
				}
				s.Queries = append(s.Queries, qu)
			}
		}
	}
	walk(d.Panels)
	for _, r := range d.Rows {
		walk(r.Panels)
	}
}
