// SPDX-License-Identifier: AGPL-3.0-only
// Grafana expression/state port. Source read: grafana v13.3.0 (commit 752e3462).
// See gfexpr/LICENSE-grafana-AGPL and gfexpr/PROVENANCE.md for source and changes.
package alerts

// Light per-tick evaluation for rules made only of instant queries, math (operators
// only), threshold and reduce: the same rules as evalTick (rules.go) on plain values,
// without building Grafana data frames. OBS_VERIFY_FAST=1 runs both and counts any
// difference (reported at the end of a check).

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"

	"github.com/AtomiCloud/obs/internal/alerts/gfexpr/mathexp"
)

var (
	verifyFast     = os.Getenv("OBS_VERIFY_FAST") != ""
	noFast         = os.Getenv("OBS_NO_FAST") != ""
	FastTicks      atomic.Int64
	FullTicks      atomic.Int64
	VerifiedTicks  atomic.Int64
	VerifyMismatch atomic.Int64
	verifyExamples atomic.Int64
)

func (r *cRule) canLight() bool {
	for _, q := range r.a.Queries {
		if q.Range != "" {
			return false
		}
	}
	for _, e := range r.exprs {
		if e.kind == "math" && !e.math.Light() {
			return false
		}
	}
	return true
}

func (r *cRule) evalTickAuto(t time.Time, qd map[string]qTick, loaded map[string]bool) []evalResult {
	if !r.light || noFast {
		FullTicks.Add(1)
		return r.evalTick(t, qd, loaded)
	}
	FastTicks.Add(1)
	fast := r.evalTickLight(qd, loaded)
	if verifyFast {
		full := r.evalTick(t, qd, loaded)
		VerifiedTicks.Add(1)
		if !sameResults(fast, full) {
			VerifyMismatch.Add(1)
			if verifyExamples.Add(1) <= 5 {
				fmt.Fprintf(os.Stderr, "VERIFY MISMATCH %s at %s:\n  fast %s\n  full %s\n", r.a.ID, t.Format(time.RFC3339), fmtRes(fast), fmtRes(full))
			}
		}
	}
	return fast
}

func fmtRes(rs []evalResult) string {
	var parts []string
	for _, x := range rs {
		parts = append(parts, fmt.Sprintf("%s=%s(%v,%v)", x.key, stName[x.state], x.value, x.hasVal))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func sameResults(a, b []evalResult) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]evalResult{}
	for _, x := range b {
		m[x.key] = x
	}
	for _, x := range a {
		y, ok := m[x.key]
		if !ok || x.state != y.state || x.hasVal != y.hasVal || x.err != y.err {
			return false
		}
		if x.hasVal && !(x.value == y.value || (math.IsNaN(x.value) && math.IsNaN(y.value))) {
			return false
		}
	}
	return true
}

func (r *cRule) evalTickLight(qd map[string]qTick, loaded map[string]bool) []evalResult {
	vars := mathexp.LVars{}
	noData := map[string][]string{}
	condErr := map[string]error{}
	errOrigin := map[string]string{}
	for _, ref := range r.queries {
		q := qd[ref]
		if q.err != nil {
			condErr[ref] = q.err
			errOrigin[ref] = ref
			continue
		}
		vals := make([]mathexp.LVal, 0, len(q.nums))
		for i := range q.nums {
			v := q.nums[i].v
			vals = append(vals, mathexp.LVal{Kind: mathexp.LNumber, Labels: q.nums[i].labels, V: &v})
		}
		if len(vals) == 0 {
			ds := queryUID(r.a.Queries[ref])
			noData[ds] = append(noData[ds], ref)
			vals = []mathexp.LVal{{Kind: mathexp.LNoData}}
		}
		vars[ref] = vals
	}
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
		out, err := e.execLight(vars, loaded)
		if err != nil {
			condErr[e.ref] = err
			continue
		}
		vars[e.ref] = out
	}
	if err := condErr[r.cond]; err != nil {
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
	if len(cres) == 0 {
		return []evalResult{{key: "{}", state: stNoData}}
	}
	out := make([]evalResult, 0, len(cres))
	seen := map[string]bool{}
	for _, v := range cres {
		var er evalResult
		switch v.Kind {
		case mathexp.LNumber:
			er.labels = v.Labels
			er.state, er.value, er.hasVal = stateFromVal(v.V)
		case mathexp.LScalar:
			er.state, er.value, er.hasVal = stateFromVal(v.V)
		case mathexp.LNoData:
			er.state = stNoData
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

func (e *cExpr) execLight(vars mathexp.LVars, loaded map[string]bool) ([]mathexp.LVal, error) {
	switch e.kind {
	case "math":
		return e.math.EvalLight(vars)
	case "reduce":
		var out []mathexp.LVal
		for _, v := range vars[e.input] {
			switch v.Kind {
			case mathexp.LNumber:
				val := v.V
				if e.mapper != nil {
					val = e.mapper.MapInput(val)
					if val == nil {
						continue
					}
				}
				out = append(out, mathexp.LVal{Kind: mathexp.LNumber, Labels: v.Labels, V: val})
			case mathexp.LNoData:
				out = append(out, mathexp.LVal{Kind: mathexp.LNoData})
			default:
				return out, fmt.Errorf("can only reduce type series, got type scalar")
			}
		}
		return out, nil
	case "threshold":
		in := vars[e.input]
		th := func(t thresh, vs []mathexp.LVal) []mathexp.LVal {
			out := make([]mathexp.LVal, 0, len(vs))
			for _, v := range vs {
				nv := mathexp.LVal{Kind: v.Kind, Labels: v.Labels}
				if v.Kind != mathexp.LNoData && v.V != nil {
					f := b2f(t.eval(*v.V))
					nv.V = &f
				}
				if v.Kind == mathexp.LNoData {
					nv = mathexp.LVal{Kind: mathexp.LNoData}
				}
				out = append(out, nv)
			}
			return out
		}
		if e.unload == nil {
			return th(e.th, in), nil
		}
		if len(in) == 0 || (len(in) == 1 && in[0].Kind == mathexp.LNoData) {
			return []mathexp.LVal{{Kind: mathexp.LNoData}}, nil
		}
		if len(loaded) == 0 {
			return th(e.th, in), nil
		}
		var ld, ul []mathexp.LVal
		for _, v := range in {
			if loaded[lblKey(v.Labels)] {
				ld = append(ld, v)
			} else {
				ul = append(ul, v)
			}
		}
		if len(ld) == 0 {
			return th(e.th, in), nil
		}
		if len(ul) == 0 {
			return th(*e.unload, in), nil
		}
		return append(th(e.th, ul), th(*e.unload, ld)...), nil
	}
	return nil, fmt.Errorf("bad expr kind %s", e.kind)
}
