package check

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/VictoriaMetrics/metricsql"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/files"
)

// loadContract reads <Dir>/contract.yaml when it exists. problems are contract failures that
// stop the name checks (an invalid contract names nothing reliably).
func loadContract(o *Options) (c *contract.Contract, problems []string) {
	if o.Contract != nil {
		return o.Contract, nil
	}
	if !o.hasContract() {
		return nil, nil
	}
	c, err := contract.Load(o.contractPath())
	if err != nil {
		return nil, []string{err.Error()}
	}
	if is := c.Validate(); len(is) > 0 {
		for _, i := range is {
			problems = append(problems, i.String())
		}
		return nil, problems
	}
	o.Contract = c
	return c, nil
}

// readsContractNames is true when the contract is read: a contract exists.
func (o *Options) readsContractNames() bool { return o.Contract != nil }

var dollarMatcher = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.]*)\s*(?:=~|!~|!=|=)\s*"[^"]*\$[^"]*"`)

// varLabels returns the labels whose matcher value holds a dashboard variable ($x, ${x}): their
// value is chosen by the viewer, so it is not judged.
func varLabels(raw string) map[string]bool {
	m := map[string]bool{}
	for _, g := range dollarMatcher.FindAllStringSubmatch(raw, -1) {
		m[g[1]] = true
	}
	return m
}

// contractMetrics checks the names a MetricsQL query reads against the contract: the metric,
// each label of each selector, each group-by label, and the value of an enum or bounded label.
func contractMetrics(r *Report, q *files.Query, e metricsql.Expr, c *contract.Contract) {
	skipVal := varLabels(q.Expr)
	var selected []contract.StoredMetric
	added := map[string]bool{} // labels made by label_replace and the like
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		if fe, ok := x.(*metricsql.FuncExpr); ok {
			switch strings.ToLower(fe.Name) {
			case "label_replace", "label_join", "label_set", "label_copy", "label_move", "label_map", "label_uppercase", "label_lowercase":
				if len(fe.Args) > 1 {
					if se, ok := fe.Args[1].(*metricsql.StringExpr); ok {
						added[contract.Norm(se.S)] = true
					}
				}
				if strings.ToLower(fe.Name) == "label_set" {
					for i := 1; i < len(fe.Args); i += 2 {
						if se, ok := fe.Args[i].(*metricsql.StringExpr); ok {
							added[contract.Norm(se.S)] = true
						}
					}
				}
			}
		}
	})
	seenMetric := map[string]bool{}
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		me, ok := x.(*metricsql.MetricExpr)
		if !ok {
			return
		}
		for _, lfs := range me.LabelFilterss {
			name := ""
			for _, lf := range lfs {
				if lf.Label == "__name__" && !lf.IsRegexp && !lf.IsNegative {
					name = lf.Value
				}
			}
			var sm contract.StoredMetric
			known := false
			if name != "" {
				sm, known = c.Lookup(name)
				if !known {
					if !seenMetric[name] {
						seenMetric[name] = true
						hint := ""
						if s := contract.Suggest(name, c.StoredNameList()); s != "" {
							hint = fmt.Sprintf("; did you mean %q?", s)
						}
						r.add(Fail, q.File, q.Where, "contract-undeclared-metric",
							"metric %q is not in the contract (observability/contract.yaml) and not a platform metric%s", name, hint)
					}
					continue
				}
				selected = append(selected, sm)
			}
			for _, lf := range lfs {
				if lf.Label == "__name__" {
					continue
				}
				l := contract.Norm(lf.Label)
				if added[l] {
					continue
				}
				if known {
					if !sm.Labels[l] {
						if per := isPodKey(c, l); per {
							r.add(Fail, q.File, q.Where, "contract-pod-label",
								"label %q on metric %q: business metrics have no pod key in the store (concept 4.1); only per_pod metrics keep it", lf.Label, name)
						} else {
							r.add(Fail, q.File, q.Where, "contract-undeclared-label",
								"label %q is not a label of metric %q in the contract%s", lf.Label, name, labelHint(l, sm.Labels))
						}
						continue
					}
					if !sm.Platform && !skipVal[lf.Label] {
						valueCheck(r, q, c, sm.Metric, name, lf)
					}
				} else if name == "" && !c.AllLabels()[l] {
					r.add(Fail, q.File, q.Where, "contract-undeclared-label", "label %q is not a label of any contract metric%s", lf.Label, labelHint(l, c.AllLabels()))
				}
			}
		}
	})
	// group-by modifiers: not tied to one selector, so judged against every label the selected metrics may carry
	allowed := map[string]bool{}
	for _, sm := range selected {
		for l := range sm.Labels {
			allowed[l] = true
		}
	}
	if len(selected) == 0 {
		allowed = c.AllLabels()
	}
	check := func(args []string) {
		for _, a := range args {
			l := contract.Norm(a)
			if allowed[l] || added[l] {
				continue
			}
			if isPodKey(c, l) {
				r.add(Fail, q.File, q.Where, "contract-pod-label", "grouping by %q: business metrics have no pod key in the store (concept 4.1)", a)
				continue
			}
			r.add(Fail, q.File, q.Where, "contract-undeclared-label", "grouping label %q is not a label of the metrics this query reads%s", a, labelHint(l, allowed))
		}
	}
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		switch t := x.(type) {
		case *metricsql.AggrFuncExpr:
			check(t.Modifier.Args)
		case *metricsql.BinaryOpExpr:
			check(t.GroupModifier.Args)
			check(t.JoinModifier.Args)
		}
	})
}

func isPodKey(c *contract.Contract, norm string) bool {
	for _, k := range c.EffectivePodKeys() {
		if contract.Norm(k) == norm {
			return true
		}
	}
	return false
}

func labelHint(l string, set map[string]bool) string {
	var all []string
	for k := range set {
		all = append(all, k)
	}
	sort.Strings(all)
	if s := contract.Suggest(l, all); s != "" {
		return fmt.Sprintf("; did you mean %q?", s)
	}
	return ""
}

var simpleAlt = regexp.MustCompile(`^[A-Za-z0-9_.\-|]+$`)

// valueCheck judges the value of an enum or bounded-number label: an equality or a plain
// `a|b` regex alternation must list only allowed values.
func valueCheck(r *Report, q *files.Query, c *contract.Contract, m contract.Metric, metric string, lf metricsql.LabelFilter) {
	t, ok := c.LabelType(m, contract.Norm(lf.Label))
	if !ok {
		return
	}
	vals := []string{lf.Value}
	if lf.IsRegexp {
		if !simpleAlt.MatchString(lf.Value) {
			return
		}
		vals = strings.Split(lf.Value, "|")
	}
	for _, v := range vals {
		if v == "" {
			continue
		}
		switch t.Kind {
		case contract.KindEnum:
			if !has(t.Values, v) {
				r.add(Fail, q.File, q.Where, "contract-bad-enum-value",
					"label %q of metric %q has value %q, which is not allowed; allowed: %s%s", lf.Label, metric, v, strings.Join(t.Values, ", "), valueHint(v, t.Values))
			}
		case contract.KindInt:
			n, err := strconv.Atoi(v)
			if err != nil || n < *t.Min || n > *t.Max {
				r.add(Fail, q.File, q.Where, "contract-bad-enum-value",
					"label %q of metric %q has value %q, outside the bounded number %d..%d", lf.Label, metric, v, *t.Min, *t.Max)
			}
		case contract.KindTime:
			n, err := strconv.Atoi(v)
			hi := 23
			if t.Unit == contract.UnitWeekday {
				hi = 6
			}
			if err != nil || n < 0 || n > hi {
				r.add(Fail, q.File, q.Where, "contract-bad-enum-value",
					"label %q of metric %q has value %q, outside the time bucket 0..%d", lf.Label, metric, v, hi)
			}
		}
	}
}

func valueHint(v string, vals []string) string {
	if s := contract.Suggest(v, vals); s != "" {
		return fmt.Sprintf("; did you mean %q?", s)
	}
	return ""
}

func has(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

// contractLogs checks a LogsQL query against the contract: the event filter's values against the
// contract events (concept 11.1), and each stream tag against the stream fields (concept 3.3).
func contractLogs(r *Report, q *files.Query, parsed *vl.Query, c *contract.Contract, filters []string) {
	events := c.EventNames()
	sf := c.StreamFields()
	for _, f := range filters {
		if tag, ok := strings.CutPrefix(f, "_stream:"); ok && !sf[tag] {
			r.add(Fail, q.File, q.Where, "contract-undeclared-stream-field",
				"stream field %q is not a stream field: the stream fields are the where-from set (%s)%s", tag, "durian.cloud.landscape, .platform, .service, .module, service.name, service.namespace, deployment.environment.name, k8s.namespace.name", streamHint(tag, sf))
		}
	}
	for _, ff := range parsed.FieldFilters(eventField) {
		if ff.InStream {
			continue // the event inside _stream:{} is refused by the logevent rules, not here
		}
		switch ff.Op {
		case "exact", "in":
			for _, v := range ff.Values {
				if !has(events, v) {
					r.add(Fail, q.File, q.Where, "contract-undeclared-event",
						"event %q is not in the contract (contract/events.json: %s)%s", v, strings.Join(events, ", "), eventHint(v, events))
				}
			}
		case "regexp":
			re, err := regexp.Compile(ff.Pattern)
			if err != nil {
				continue
			}
			match := false
			for _, e := range events {
				if re.MatchString(e) {
					match = true
				}
			}
			if !match {
				r.add(Fail, q.File, q.Where, "contract-event-regexp-no-match",
					"event regex %q matches no contract event (%s)", ff.Pattern, strings.Join(events, ", "))
			}
		}
	}
}

func eventHint(v string, events []string) string {
	if s := contract.Suggest(v, events); s != "" {
		return fmt.Sprintf("; did you mean %q?", s)
	}
	return ""
}

func streamHint(tag string, sf map[string]bool) string {
	var all []string
	for k := range sf {
		all = append(all, k)
	}
	sort.Strings(all)
	if s := contract.Suggest(tag, all); s != "" {
		return fmt.Sprintf("; did you mean %q?", s)
	}
	return ""
}
