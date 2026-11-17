// SPDX-License-Identifier: AGPL-3.0-only
// Grafana expression/state port. Source read: grafana v13.3.0 (commit 752e3462).
// See gfexpr/LICENSE-grafana-AGPL and gfexpr/PROVENANCE.md for source and changes.
package alerts

// Grafana alert-rule emulator: per evaluation tick it runs the rule's queries
// (instant or range), then Grafana's own server-side expressions (math via the
// vendored pkg/expr/mathexp; reduce, threshold and recovery threshold copied
// from pkg/expr), turns the condition into per-instance results the way
// pkg/services/ngalert/eval does, and feeds them to a port of the
// pkg/services/ngalert/state state machine (Pending, Alerting, NoData, Error,
// Recovering via keep_firing_for, missing-series resolution).
// Source read: grafana v13.3.0 (commit 752e3462).

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"

	"github.com/AtomiCloud/obs/internal/alerts/gfexpr/mathexp"
	"github.com/AtomiCloud/obs/internal/alerts/gfexpr/mathexp/parse"
)

// ---------- rule file shapes (additions to spec.go's Alert) ----------

type ExprDef struct {
	Math      string        `yaml:"math"`
	Reduce    *ReduceDef    `yaml:"reduce"`
	Threshold *ThresholdDef `yaml:"threshold"`
}

type ReduceDef struct {
	Input       string   `yaml:"input"`
	Function    string   `yaml:"function"` // sum mean min max count last median
	Mode        string   `yaml:"mode"`     // "" (strict) | dropNN | replaceNN
	ReplaceWith *float64 `yaml:"replace_with"`
}

type ThresholdDef struct {
	Input   string    `yaml:"input"`
	Type    string    `yaml:"type"` // gt lt eq ne gte lte within_range outside_range within_range_included outside_range_included
	Params  []float64 `yaml:"params"`
	Recover *struct {
		Type   string    `yaml:"type"`
		Params []float64 `yaml:"params"`
	} `yaml:"recover"` // Grafana "recovery threshold" (hysteresis); only on the condition
}

type RecordDef struct {
	Metric string `yaml:"metric"`
	From   string `yaml:"from"`
}

// ---------- compiled rule ----------

const (
	stNormal = iota
	stAlerting
	stPending
	stNoData
	stError
	stRecovering
)

var stName = [...]string{"Normal", "Alerting", "Pending", "NoData", "Error", "Recovering"}

type thresh struct {
	typ    string
	params []float64
	invert bool // the recovery (unload) threshold is inverted, as in pkg/expr/threshold.go
}

func (t thresh) eval(v float64) bool { return t.raw(v) != t.invert }

func (t thresh) raw(v float64) bool {
	p := t.params
	switch t.typ {
	case "gt":
		return v > p[0]
	case "lt":
		return v < p[0]
	case "eq":
		return v == p[0]
	case "ne":
		return v != p[0]
	case "gte":
		return v >= p[0]
	case "lte":
		return v <= p[0]
	case "within_range":
		return v > p[0] && v < p[1]
	case "outside_range":
		return v < p[0] || v > p[1]
	case "within_range_included":
		return v >= p[0] && v <= p[1]
	case "outside_range_included":
		return v <= p[0] || v >= p[1]
	}
	return false
}

func newThresh(typ string, params []float64) (thresh, error) {
	need := 1
	switch typ {
	case "gt", "lt", "eq", "ne", "gte", "lte":
	case "within_range", "outside_range", "within_range_included", "outside_range_included":
		need = 2
	default:
		return thresh{}, fmt.Errorf("unknown threshold type %q", typ)
	}
	if len(params) < need {
		return thresh{}, fmt.Errorf("threshold %s needs %d params", typ, need)
	}
	return thresh{typ: typ, params: params}, nil
}

type cExpr struct {
	ref     string
	kind    string // math reduce threshold
	math    *mathexp.Expr
	input   string
	reducer mathexp.ReducerID
	mapper  mathexp.ReduceMapper
	th      thresh
	unload  *thresh
	deps    []string
}

type cRule struct {
	a        *Alert
	queries  []string // refs, sorted
	exprs    []*cExpr // dependency order
	cond     string
	forD     time.Duration
	keepD    time.Duration
	interval time.Duration
	noData   string
	execErr  string
	missingN int64
	record   *RecordDef
	usesHyst bool
	light    bool
}

func dsUID(ds string) string {
	switch ds {
	case "metrics":
		return "vm"
	case "logs":
		return "vlogs"
	case "traces":
		return "tempo"
	}
	return ds
}

func compileRule(a *Alert) (*cRule, error) {
	if a.Condition == "" && a.Record == nil {
		return nil, fmt.Errorf("condition is empty")
	}
	if a.NoDataState != "" && a.NoDataState != "NoData" && a.NoDataState != "OK" && a.NoDataState != "Alerting" && a.NoDataState != "KeepLast" {
		return nil, fmt.Errorf("noDataState %q is invalid", a.NoDataState)
	}
	if a.ExecErrState != "" && a.ExecErrState != "Error" && a.ExecErrState != "OK" && a.ExecErrState != "Alerting" && a.ExecErrState != "KeepLast" {
		return nil, fmt.Errorf("execErrState %q is invalid", a.ExecErrState)
	}
	if a.MissingSeriesEvalsToResolve < 0 {
		return nil, fmt.Errorf("missingSeriesEvalsToResolve must be positive")
	}
	if a.LoadErr != "" {
		return nil, fmt.Errorf("%s", a.LoadErr)
	}
	r := &cRule{a: a, noData: a.NoDataState, execErr: a.ExecErrState, missingN: 2, record: a.Record}
	if r.noData == "" {
		r.noData = "NoData"
	}
	if r.execErr == "" {
		r.execErr = "Error"
	}
	if a.MissingSeriesEvalsToResolve > 0 {
		r.missingN = int64(a.MissingSeriesEvalsToResolve)
	}
	var err error
	if r.forD, err = time.ParseDuration(orDefault(a.For, "0s")); err != nil {
		return nil, err
	}
	if r.keepD, err = time.ParseDuration(orDefault(a.KeepFiringFor, "0s")); err != nil {
		return nil, err
	}
	if r.interval, err = time.ParseDuration(orDefault(a.Interval, "1m")); err != nil {
		return nil, err
	}
	if r.interval <= 0 || r.forD < 0 || r.keepD < 0 {
		return nil, fmt.Errorf("interval must be positive, for and keepFiringFor nonnegative")
	}
	for ref := range a.Queries {
		r.queries = append(r.queries, ref)
	}
	sort.Strings(r.queries)
	defs := map[string]ExprDef{}
	for k, v := range a.Expressions {
		defs[k] = v
	}
	cond := a.Condition
	if r.record != nil && cond == "" {
		cond = r.record.From
	}
	if _, isQ := a.Queries[cond]; !isQ {
		if _, isE := defs[cond]; !isE {
			// legacy: the condition is a math expression
			defs["__cond"] = ExprDef{Math: cond}
			cond = "__cond"
		}
	}
	r.cond = cond
	built := map[string]*cExpr{}
	for ref, d := range defs {
		e := &cExpr{ref: ref}
		switch {
		case d.Math != "":
			e.kind = "math"
			e.math, err = mathexp.New(d.Math)
			if err != nil {
				return nil, fmt.Errorf("%s: math %q: %w", ref, d.Math, err)
			}
			for _, v := range e.math.VarNames {
				e.deps = append(e.deps, v)
			}
		case d.Reduce != nil:
			e.kind = "reduce"
			e.input = d.Reduce.Input
			e.reducer = mathexp.ReducerID(d.Reduce.Function)
			if _, err := mathexp.GetReduceFunc(e.reducer); err != nil {
				return nil, fmt.Errorf("%s: %w", ref, err)
			}
			switch d.Reduce.Mode {
			case "", "strict":
			case "dropNN":
				e.mapper = mathexp.DropNonNumber{}
			case "replaceNN":
				v := 0.0
				if d.Reduce.ReplaceWith != nil {
					v = *d.Reduce.ReplaceWith
				}
				e.mapper = mathexp.ReplaceNonNumberWithValue{Value: v}
			default:
				return nil, fmt.Errorf("%s: reduce mode %q", ref, d.Reduce.Mode)
			}
			e.deps = []string{e.input}
		case d.Threshold != nil:
			e.kind = "threshold"
			e.input = d.Threshold.Input
			if e.th, err = newThresh(d.Threshold.Type, d.Threshold.Params); err != nil {
				return nil, fmt.Errorf("%s: %w", ref, err)
			}
			if rc := d.Threshold.Recover; rc != nil {
				if ref != cond {
					return nil, fmt.Errorf("recovery threshold '%s' is only allowed to be the alert condition", ref)
				}
				u, err := newThresh(rc.Type, rc.Params)
				if err != nil {
					return nil, fmt.Errorf("%s recover: %w", ref, err)
				}
				u.invert = true
				e.unload = &u
				r.usesHyst = true
			}
			e.deps = []string{e.input}
		default:
			return nil, fmt.Errorf("expression %s: one of math, reduce, threshold", ref)
		}
		built[ref] = e
	}
	// topological order
	seen := map[string]int{}
	var visit func(string) error
	visit = func(ref string) error {
		if _, ok := a.Queries[ref]; ok {
			return nil
		}
		e, ok := built[ref]
		if !ok {
			return fmt.Errorf("unknown ref $%s", ref)
		}
		switch seen[ref] {
		case 1:
			return fmt.Errorf("cycle at %s", ref)
		case 2:
			return nil
		}
		seen[ref] = 1
		for _, d := range e.deps {
			if err := visit(d); err != nil {
				return err
			}
		}
		seen[ref] = 2
		r.exprs = append(r.exprs, e)
		return nil
	}
	refs := make([]string, 0, len(built))
	for k := range built {
		refs = append(refs, k)
	}
	sort.Strings(refs)
	for _, k := range refs {
		if err := visit(k); err != nil {
			return nil, err
		}
	}
	if _, ok := a.Queries[cond]; !ok && built[cond] == nil {
		return nil, fmt.Errorf("condition %q is not a query or expression", cond)
	}
	r.light = r.canLight()
	return r, nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// ---------- per-tick data ----------

// qTick holds what one query returned at one tick.
type qTick struct {
	err     error
	nums    []instVal      // instant: one value per series at this tick
	series  []sortedSeries // range: points inside the window
	isRange bool
}

type evalResult struct {
	labels   data.Labels
	key      string
	state    int // stNormal, stAlerting, stNoData, stError
	err      string
	value    float64
	hasVal   bool
	resultFP string // query labels before rule-label overrides (hysteresis identity)
}

func lblKey(l data.Labels) string {
	if len(l) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func toDataLabels(ls []Label, keepName bool) data.Labels {
	out := data.Labels{}
	for _, l := range ls {
		if l.Name == "__name__" && !keepName {
			continue
		}
		out[l.Name] = l.Value
	}
	return out
}

// KeepNameLabel: whether instance labels keep __name__ (decided by the referee run).
var KeepNameLabel = true

func f64p(v float64) *float64 { return &v }

// evalTick computes Grafana's eval.Results for one tick.
func (r *cRule) evalTick(t time.Time, qd map[string]qTick, loaded map[string]bool) []evalResult {
	vars := mathexp.Vars{}
	noData := map[string][]string{} // ds uid -> refs
	condErr := map[string]error{}
	errOrigin := map[string]string{} // ref -> datasource query whose error it carries
	for _, ref := range r.queries {
		q := qd[ref]
		if q.err != nil {
			condErr[ref] = q.err
			errOrigin[ref] = ref
			vars[ref] = mathexp.Results{Error: q.err}
			continue
		}
		var vals mathexp.Values
		if q.isRange {
			for _, s := range q.series {
				ser := mathexp.NewSeries(ref, toDataLabels(s.labels, KeepNameLabel), len(s.ts))
				for i, k := range s.ts {
					ser.SetPoint(i, time.UnixMilli(k).UTC(), f64p(s.vs[i]))
				}
				vals = append(vals, ser)
			}
		} else {
			for _, s := range q.nums {
				n := mathexp.NewNumber(ref, s.labels)
				n.SetValue(f64p(s.v))
				vals = append(vals, n)
			}
		}
		if len(vals) == 0 {
			ds := queryUID(r.a.Queries[ref])
			noData[ds] = append(noData[ds], ref)
			vals = mathexp.Values{mathexp.NewNoData()}
		}
		vars[ref] = mathexp.Results{Values: vals}
	}
	// expressions; an error in a dependency propagates as a dependency error
	for _, e := range r.exprs {
		var depErr error
		for _, d := range e.deps {
			if err := condErr[d]; err != nil {
				depErr = err
				errOrigin[e.ref] = errOrigin[d]
				break
			}
		}
		if depErr != nil {
			condErr[e.ref] = depErr
			continue
		}
		res, err := e.exec(t, vars, loaded)
		if err != nil {
			condErr[e.ref] = err
			continue
		}
		vars[e.ref] = res
	}
	if err := condErr[r.cond]; err != nil {
		// ngalert/state/cache.go: an Error state (exec_err_state Error) gets the failing
		// query's ref_id and datasource_uid as labels
		var l data.Labels
		if o := errOrigin[r.cond]; o != "" && r.execErr == "Error" {
			l = data.Labels{"datasource_uid": queryUID(r.a.Queries[o]), "ref_id": o}
		}
		return []evalResult{{labels: l, key: lblKey(l), state: stError, err: err.Error()}}
	}
	if len(noData) > 0 {
		var out []evalResult
		uids := make([]string, 0, len(noData))
		for u := range noData {
			uids = append(uids, u)
		}
		sort.Strings(uids)
		for _, u := range uids {
			l := data.Labels{"datasource_uid": u, "ref_id": strings.Join(noData[u], ",")}
			out = append(out, evalResult{labels: l, key: lblKey(l), state: stNoData})
		}
		return out
	}
	cres := vars[r.cond]
	if len(cres.Values) == 0 {
		return []evalResult{{key: "{}", state: stNoData}}
	}
	var out []evalResult
	seen := map[string]bool{}
	for _, v := range cres.Values {
		var er evalResult
		switch x := v.(type) {
		case mathexp.Number:
			er.labels = x.GetLabels()
			er.state, er.value, er.hasVal = stateFromVal(x.GetFloat64Value())
		case mathexp.Scalar:
			er.state, er.value, er.hasVal = stateFromVal(x.GetFloat64Value())
		case mathexp.NoData:
			er.state = stNoData
		case mathexp.Series:
			er.labels = x.GetLabels()
			er.state = stError
			er.err = "looks like time series data, only reduced data can be alerted on."
		default:
			er.state = stError
			er.err = fmt.Sprintf("invalid result type %T", v)
		}
		er.key = lblKey(er.labels)
		if seen[er.key] {
			return []evalResult{{labels: er.labels, key: er.key, state: stError, err: "frame cannot uniquely be identified by its labels: has duplicate results with labels " + er.key}}
		}
		seen[er.key] = true
		out = append(out, er)
	}
	return out
}

func stateFromVal(v *float64) (int, float64, bool) {
	if v == nil {
		return stNoData, 0, false
	}
	if *v == 0 {
		return stNormal, 0, true
	}
	return stAlerting, *v, true
}

func (e *cExpr) exec(now time.Time, vars mathexp.Vars, loaded map[string]bool) (mathexp.Results, error) {
	switch e.kind {
	case "math":
		return e.math.Execute(e.ref, vars, nil)
	case "reduce":
		var out mathexp.Results
		for i, val := range vars[e.input].Values {
			switch v := val.(type) {
			case mathexp.Series:
				n, err := v.Reduce(e.ref, e.reducer, e.mapper)
				if err != nil {
					return out, err
				}
				out.Values = append(out.Values, n)
			case mathexp.Number:
				value := v.GetFloat64Value()
				if e.mapper != nil {
					value = e.mapper.MapInput(value)
					if value == nil {
						continue
					}
				}
				c := mathexp.NewNumber(e.ref, v.GetLabels())
				c.SetValue(value)
				out.Values = append(out.Values, c)
				_ = i
			case mathexp.NoData:
				out.Values = append(out.Values, v.New())
			default:
				return out, fmt.Errorf("can only reduce type series, got type %v", val.Type())
			}
		}
		return out, nil
	case "threshold":
		in := vars[e.input]
		if e.unload == nil {
			return thresholdExec(e.ref, e.th, in)
		}
		// hysteresis (recovery threshold)
		if in.IsNoData() {
			return mathexp.Results{Values: mathexp.Values{mathexp.NewNoData()}}, nil
		}
		if len(loaded) == 0 {
			return thresholdExec(e.ref, e.th, in)
		}
		var ld, ul mathexp.Values
		for _, v := range in.Values {
			if loaded[lblKey(v.GetLabels())] {
				ld = append(ld, v)
			} else {
				ul = append(ul, v)
			}
		}
		if len(ld) == 0 {
			return thresholdExec(e.ref, e.th, in)
		}
		if len(ul) == 0 {
			return thresholdExec(e.ref, *e.unload, in)
		}
		a, err := thresholdExec(e.ref, e.th, mathexp.Results{Values: ul})
		if err != nil {
			return a, err
		}
		b, err := thresholdExec(e.ref, *e.unload, mathexp.Results{Values: ld})
		if err != nil {
			return b, err
		}
		return mathexp.Results{Values: append(a.Values, b.Values...)}, nil
	}
	return mathexp.Results{}, fmt.Errorf("bad expr kind %s", e.kind)
}

func thresholdExec(ref string, th thresh, in mathexp.Results) (mathexp.Results, error) {
	ev := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		if th.eval(*v) {
			return f64p(1)
		}
		return f64p(0)
	}
	out := mathexp.Results{Values: make(mathexp.Values, 0, len(in.Values))}
	for _, val := range in.Values {
		switch v := val.(type) {
		case mathexp.Series:
			s := mathexp.NewSeries(ref, v.GetLabels(), v.Len())
			for i := range v.Len() {
				t, x := v.GetPoint(i)
				s.SetPoint(i, t, ev(x))
			}
			out.Values = append(out.Values, s)
		case mathexp.Number:
			c := mathexp.NewNumber(ref, v.GetLabels())
			c.SetValue(ev(v.GetFloat64Value()))
			out.Values = append(out.Values, c)
		case mathexp.Scalar:
			out.Values = append(out.Values, mathexp.NewScalar(ref, ev(v.GetFloat64Value())))
		case mathexp.NoData:
			out.Values = append(out.Values, mathexp.NewNoData())
		default:
			return out, fmt.Errorf("unsupported format of the input data, got type %v", val.Type())
		}
	}
	return out, nil
}

var _ = parse.TypeNoData
var _ = math.NaN

// ---------- state machine (port of ngalert/state) ----------

type instState struct {
	labels   data.Labels
	state    int
	reason   string
	startsAt time.Time
	lastEval time.Time
	resultFP string
}

type ruleStates struct {
	r      *cRule
	states map[string]*instState
}

func newRuleStates(r *cRule) *ruleStates { return &ruleStates{r: r, states: map[string]*instState{}} }

// loaded: result fingerprints of Alerting/Pending states with no reason (hysteresis reader).
func (rs *ruleStates) loaded() map[string]bool {
	if !rs.r.usesHyst {
		return nil
	}
	m := map[string]bool{}
	for _, s := range rs.states {
		if s.reason != "" {
			continue
		}
		if s.state == stAlerting || s.state == stPending {
			m[s.resultFP] = true
		}
	}
	return m
}

type Transition struct {
	T        int64  `json:"t"`
	Instance string `json:"instance"`
	From     string `json:"from"`
	To       string `json:"to"`
	Reason   string `json:"reason,omitempty"`
}

func fmtState(s int, reason string) string {
	if reason == "" {
		return stName[s]
	}
	return stName[s] + " (" + reason + ")"
}

// Port of Grafana v13.3.0 pkg/services/ngalert/state/manager.go:
// Manager.setNextStateForRule maps whole-rule NoData/Error to existing instances.
func (rs *ruleStates) process(t time.Time, results []evalResult) []Transition {
	r := rs.r
	if len(r.a.Labels) > 0 {
		decorated := make([]evalResult, len(results))
		for i, res := range results {
			res.resultFP = res.key
			labels := data.Labels{}
			for k, v := range res.labels {
				labels[k] = v
			}
			for k, v := range r.a.Labels {
				labels[k] = v
			}
			res.labels, res.key = labels, lblKey(labels)
			decorated[i] = res
		}
		results = decorated
	}
	var trs []Transition
	allNoData := true
	allErr := len(results) > 0
	for _, x := range results {
		if x.state != stNoData {
			allNoData = false
		}
		if x.state != stError {
			allErr = false
		}
	}
	done := false
	if allNoData && (r.noData == "Alerting" || r.noData == "OK" || r.noData == "KeepLast") && len(rs.states) > 0 {
		res := evalResult{state: stNoData, key: "{}"}
		if len(results) > 0 {
			res = results[0]
		}
		for _, s := range rs.states {
			trs = append(trs, rs.transition(s, t, res)...)
		}
		done = true
	}
	if !done && allErr && (r.execErr == "Alerting" || r.execErr == "OK" || r.execErr == "KeepLast") && len(rs.states) > 0 {
		for _, s := range rs.states {
			trs = append(trs, rs.transition(s, t, results[0])...)
		}
		done = true
	}
	if !done {
		for _, res := range results {
			s := rs.states[res.key]
			if s == nil {
				s = &instState{labels: res.labels, state: stNormal, startsAt: t, lastEval: t}
				rs.states[res.key] = s
			}
			trs = append(trs, rs.transition(s, t, res)...)
		}
	}
	// Grafana v13.3.0 pkg/services/ngalert/state/manager.go:
	// Manager.processMissingSeriesStates and stateIsStale. NoData/Error resolve after
	// one missing tick; ordinary instances after missingSeriesEvalsToResolve.
	// missing series
	for k, s := range rs.states {
		if s.lastEval.Equal(t) {
			continue
		}
		n := r.missingN
		if s.state == stError || s.state == stNoData || (s.state == stPending && (s.reason == "Error" || s.reason == "NoData")) {
			n = 1
		}
		if t.Sub(s.lastEval) >= time.Duration(n)*r.interval {
			old, oldR := s.state, s.reason
			s.state, s.reason = stNormal, "MissingSeries"
			trs = append(trs, Transition{T: t.UnixMilli(), Instance: k, From: fmtState(old, oldR), To: fmtState(s.state, s.reason)})
			delete(rs.states, k)
		}
	}
	return trs
}

// Port of Grafana v13.3.0 pkg/services/ngalert/state/state.go: State.transition,
// resultError, resultNoData and resultStateReason. Pending is respected for the
// synthetic NoData/Error instances (ignorePendingForNoDataAndError=false).
func (rs *ruleStates) transition(s *instState, t time.Time, res evalResult) []Transition {
	r := rs.r
	s.lastEval = t
	s.resultFP = res.key
	if res.resultFP != "" {
		s.resultFP = res.resultFP
	}
	old, oldR := s.state, s.reason
	switch res.state {
	case stNormal:
		resultNormal(s, r, t)
	case stAlerting:
		resultAlerting(s, r, t)
	case stError:
		switch r.execErr {
		case "Alerting":
			resultAlerting(s, r, t)
		case "Error":
			switch s.state {
			case stError:
			case stPending:
				if t.Sub(s.startsAt) >= r.forD {
					s.state, s.startsAt = stError, t
				}
			default:
				if s.state != stRecovering && r.forD > 0 {
					s.state, s.startsAt = stPending, t
				} else {
					s.state, s.startsAt = stError, t
				}
			}
		case "OK":
			resultNormal(s, r, t)
		case "KeepLast":
			resultKeepLast(s, r, t)
		}
	case stNoData:
		switch r.noData {
		case "Alerting":
			resultAlerting(s, r, t)
		case "NoData":
			switch s.state {
			case stNoData:
			case stPending:
				if t.Sub(s.startsAt) >= r.forD {
					s.state, s.startsAt = stNoData, t
				}
			default:
				if s.state != stRecovering && r.forD > 0 {
					s.state, s.startsAt = stPending, t
				} else {
					s.state, s.startsAt = stNoData, t
				}
			}
		case "OK":
			resultNormal(s, r, t)
		case "KeepLast":
			resultKeepLast(s, r, t)
		}
	}
	s.reason = ""
	if s.state != res.state && res.state != stNormal && res.state != stAlerting {
		s.reason = stName[res.state]
		if (res.state == stError && r.execErr == "KeepLast") || (res.state == stNoData && r.noData == "KeepLast") {
			s.reason = stName[res.state] + ", KeepLast"
		}
	}
	if old != s.state || oldR != s.reason {
		k := res.key
		for kk, v := range rs.states {
			if v == s {
				k = kk
			}
		}
		return []Transition{{T: t.UnixMilli(), Instance: k, From: fmtState(old, oldR), To: fmtState(s.state, s.reason)}}
	}
	return nil
}

// Grafana v13.3.0 pkg/services/ngalert/state/state.go: resultNormal.
// Normal clears Pending; Alerting enters Recovering when keepFiringFor > 0;
// Recovering becomes Normal when that interval expires.
func resultNormal(s *instState, r *cRule, t time.Time) {
	switch {
	case s.state == stNormal:
	case s.state == stRecovering:
		if t.Sub(s.startsAt) >= r.keepD {
			s.state, s.startsAt = stNormal, t
		}
	case s.state == stAlerting && r.keepD > 0:
		s.state, s.startsAt = stRecovering, t
	default:
		s.state, s.startsAt = stNormal, t
	}
}

// Grafana v13.3.0 pkg/services/ngalert/state/state.go: resultAlerting.
// A new violation enters Pending for the rule's for interval; a relapse from
// Recovering returns directly to Alerting without repeating Pending.
func resultAlerting(s *instState, r *cRule, t time.Time) {
	switch s.state {
	case stAlerting:
	case stPending:
		if t.Sub(s.startsAt) >= r.forD {
			s.state, s.startsAt = stAlerting, t
		}
	default:
		if s.state != stRecovering && r.forD > 0 {
			s.state, s.startsAt = stPending, t
		} else {
			s.state, s.startsAt = stAlerting, t
		}
	}
}

// Grafana v13.3.0 pkg/services/ngalert/state/state.go: resultKeepLast.
// Pending may mature during an error/no-data tick; a Recovering instance drains.
func resultKeepLast(s *instState, r *cRule, t time.Time) {
	switch s.state {
	case stAlerting:
		resultAlerting(s, r, t)
	case stPending:
		if t.Sub(s.startsAt) >= r.forD {
			resultAlerting(s, r, t)
		}
	default:
		resultNormal(s, r, t)
	}
}

type instVal struct {
	labels data.Labels
	v      float64
}
