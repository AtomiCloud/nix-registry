// SPDX-License-Identifier: AGPL-3.0-only
// Grafana expression/state port. Source read: grafana v13.3.0 (commit 752e3462).
// See gfexpr/LICENSE-grafana-AGPL and gfexpr/PROVENANCE.md for source and changes.
// Package alerts evaluates GrafanaAlertRuleGroup CRs on the embedded engines.
package alerts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

type Label = seed.Label
type Series = seed.Series
type Dataset = seed.Dataset
type MetricDef = seed.MetricDef
type Incident = seed.Incident

type Run struct {
	Name       string
	Incident   *Incident
	Start, End time.Time
}

type Alert struct {
	ID, File, Title, Condition, For, KeepFiringFor, NoDataState, ExecErrState, Interval string
	Queries                                                                             map[string]Query
	Expressions                                                                         map[string]ExprDef
	MissingSeriesEvalsToResolve                                                         int
	Labels                                                                              map[string]string
	Record                                                                              *RecordDef
	LoadErr                                                                             string
}

type Query struct {
	Datasource, UID, Expr, Range string
	From, To                     time.Duration // Grafana relativeTimeRange, distinct from metrics range queries
}

type Project struct {
	Dir             string
	Seed            seed.Seed
	Incidents       []Incident
	Alerts, Records []Alert
	Files           map[string][]byte
	Gap             time.Duration
}

func loadProject(dir, seedPath string) (*Project, error) {
	set, err := files.Load(dir)
	if err != nil {
		return nil, err
	}
	if len(set.Problems) > 0 {
		return nil, fmt.Errorf("%s: %s", set.Problems[0].File, set.Problems[0].Msg)
	}
	if len(set.Rules) == 0 {
		return nil, fmt.Errorf("%s/alerts: no GrafanaAlertRuleGroup rules", dir)
	}
	if seedPath == "" {
		seedPath = filepath.Join(dir, "seed.yaml")
	}
	sd, err := seed.Load(seedPath)
	if err != nil {
		return nil, err
	}
	incs, err := seed.LoadIncidents(sd, dir)
	if err != nil {
		return nil, err
	}
	p := &Project{Dir: dir, Seed: *sd, Incidents: incs, Files: map[string][]byte{}, Gap: 24 * time.Hour}
	p.Files["seed.yaml"], err = os.ReadFile(seedPath)
	if err != nil {
		return nil, err
	}
	for _, i := range incs {
		k := "incidents/" + i.Name + ".yaml"
		p.Files[k], err = os.ReadFile(filepath.Join(dir, k))
		if err != nil {
			return nil, err
		}
	}
	ids := map[string]bool{}
	for _, rr := range set.Rules {
		r := rr.Definition
		id := r.UID
		if id == "" {
			id = rr.Name
		} // same fallback identity as the shared query reader
		if id == "" {
			return nil, fmt.Errorf("%s rule %q: uid or metadata.name is required", rr.File, r.Title)
		}
		if ids[id] {
			return nil, fmt.Errorf("%s rule %s: duplicate uid", rr.File, id)
		}
		ids[id] = true
		a := Alert{ID: id, File: rr.File, Title: r.Title, Condition: r.Condition, For: r.For, KeepFiringFor: r.KeepFiringFor, NoDataState: r.NoDataState, ExecErrState: r.ExecErrState, Interval: rr.Interval, Labels: r.Labels, MissingSeriesEvalsToResolve: r.MissingSeriesEvalsToResolve, Queries: map[string]Query{}, Expressions: map[string]ExprDef{}}
		refs := map[string]bool{}
		for _, q := range r.Data {
			if q.RefID == "" || refs[q.RefID] {
				return nil, fmt.Errorf("%s rule %s: invalid or duplicate refId %q", a.File, id, q.RefID)
			}
			refs[q.RefID] = true
			// Reuse the same datasource classifier as obs query.
			typ, uid := "", q.DatasourceUID
			if ds, ok := q.Model["datasource"].(map[string]any); ok {
				typ, _ = ds["type"].(string)
				if uid == "" {
					uid, _ = ds["uid"].(string)
				}
			}
			kind, err := files.Classify(typ, uid)
			if err != nil {
				return nil, fmt.Errorf("%s rule %s query %s: %w", a.File, id, q.RefID, err)
			}
			if kind == files.Other {
				d, err := expression(q.Model)
				if err != nil {
					return nil, fmt.Errorf("%s rule %s query %s: %w", a.File, id, q.RefID, err)
				}
				a.Expressions[q.RefID] = d
				continue
			}
			if kind == files.Loki || kind == files.Traces {
				return nil, fmt.Errorf("%s rule %s query %s: datasource %s is unavailable in obs alerts (logs use LogsQL; Tempo can plug into DatasourceEngine in a later release)", a.File, id, q.RefID, kind)
			}
			expr, _ := q.Model["expr"].(string)
			if expr == "" {
				return nil, fmt.Errorf("%s rule %s query %s: model.expr is empty", a.File, id, q.RefID)
			}
			if q.RelativeTimeRange.From < 0 || q.RelativeTimeRange.To < 0 || q.RelativeTimeRange.To > q.RelativeTimeRange.From {
				return nil, fmt.Errorf("%s rule %s query %s: invalid relativeTimeRange %d..%d", a.File, id, q.RefID, q.RelativeTimeRange.From, q.RelativeTimeRange.To)
			}
			query := Query{Datasource: kind.String(), UID: uid, Expr: expr, From: time.Duration(q.RelativeTimeRange.From) * time.Second, To: time.Duration(q.RelativeTimeRange.To) * time.Second}
			instant, _ := q.Model["instant"].(bool)
			rangeQuery, _ := q.Model["range"].(bool)
			qt, _ := q.Model["queryType"].(string)
			if kind == files.Metrics && !instant && (rangeQuery || qt == "range" || q.QueryType == "range") {
				query.Range = (query.From - query.To).String()
			}
			if query.From > p.Gap {
				p.Gap = query.From + time.Hour
			}
			a.Queries[q.RefID] = query
		}
		if r.Record != nil {
			a.Record = &RecordDef{Metric: r.Record.Metric, From: r.Record.From}
			if a.Record.Metric == "" {
				return nil, fmt.Errorf("%s rule %s: record.metric is empty", a.File, id)
			}
			if a.Record.From == "" {
				a.Record.From = a.Condition
			}
			p.Records = append(p.Records, a)
		} else {
			p.Alerts = append(p.Alerts, a)
		}
		// A CR can carry more than one alert. Cache only this rule's normalized input,
		// so editing one sibling does not invalidate unrelated alert results.
		b, err := json.Marshal(a)
		if err != nil {
			return nil, err
		}
		sub := "alerts/"
		if a.Record != nil {
			sub = "recording/"
		}
		p.Files[sub+id+".json"] = b
	}
	for _, i := range p.Incidents {
		seen := map[string]bool{}
		for _, id := range append(append([]string{}, i.Expect.Fire...), i.Expect.Quiet...) {
			if !ids[id] || seen[id] {
				return nil, fmt.Errorf("incidents/%s.yaml: expect invalid or duplicate alert %q", i.Name, id)
			}
			seen[id] = true
		}
	}
	sort.Slice(p.Alerts, func(i, j int) bool { return p.Alerts[i].ID < p.Alerts[j].ID })
	return p, nil
}

func expression(model map[string]any) (ExprDef, error) {
	// JSON tags match Grafana's expression model, including reducer settings and
	// unloadEvaluator (recovery threshold in pkg/expr/threshold.go).
	var m struct {
		Type       string `json:"type"`
		Expression string `json:"expression"`
		Reducer    string `json:"reducer"`
		Settings   struct {
			Mode             string   `json:"mode"`
			ReplaceWithValue *float64 `json:"replaceWithValue"`
		} `json:"settings"`
		Conditions []struct {
			Evaluator struct {
				Type   string    `json:"type"`
				Params []float64 `json:"params"`
			} `json:"evaluator"`
			UnloadEvaluator *struct {
				Type   string    `json:"type"`
				Params []float64 `json:"params"`
			} `json:"unloadEvaluator"`
		} `json:"conditions"`
	}
	b, _ := json.Marshal(model)
	if err := json.Unmarshal(b, &m); err != nil {
		return ExprDef{}, err
	}
	switch m.Type {
	case "math":
		return ExprDef{Math: m.Expression}, nil
	case "reduce":
		return ExprDef{Reduce: &ReduceDef{Input: m.Expression, Function: m.Reducer, Mode: m.Settings.Mode, ReplaceWith: m.Settings.ReplaceWithValue}}, nil
	case "threshold":
		if len(m.Conditions) != 1 {
			return ExprDef{}, fmt.Errorf("threshold needs exactly one condition; got %d", len(m.Conditions))
		}
		c := m.Conditions[0]
		d := &ThresholdDef{Input: m.Expression, Type: c.Evaluator.Type, Params: c.Evaluator.Params}
		if c.UnloadEvaluator != nil {
			d.Recover = &struct {
				Type   string    `yaml:"type"`
				Params []float64 `yaml:"params"`
			}{c.UnloadEvaluator.Type, c.UnloadEvaluator.Params}
		}
		return ExprDef{Threshold: d}, nil
	}
	return ExprDef{}, fmt.Errorf("expression type %q: want math, reduce or threshold", m.Type)
}

func storedName(s string) string  { return strings.ReplaceAll(s, ".", "_") }
func labelsKey(ls []Label) string { b, _ := json.Marshal(ls); return string(b) }
func sortLabels(ls []Label) []Label {
	sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
	return ls
}
func mustDur(s string) time.Duration { d, _ := time.ParseDuration(s); return d }
func ptrs(is []Incident) []*Incident {
	out := make([]*Incident, len(is))
	for i := range is {
		out[i] = &is[i]
	}
	return out
}
