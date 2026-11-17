package check

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/VictoriaMetrics/metricsql"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/files"
)

// valueMatcher finds `label op "value"` pairs in the unfilled query text. The label may be quoted
// (`"payment.provider"`), the value may use any of the three quote styles.
var valueMatcher = regexp.MustCompile("(\"(?:[^\"\\\\]|\\\\.)*\"|[A-Za-z_][A-Za-z0-9_.]*)\\s*(?:=~|!~|!=|=)\\s*(\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`)")

// varLabels returns the labels whose matcher value holds a dashboard variable ($x, ${x}, [[x]]):
// the viewer chooses the value, so it is not judged. Keys are the label names as the parser reports
// them (a quoted name without its quotes).
func varLabels(raw string) map[string]bool {
	m := map[string]bool{}
	for _, g := range valueMatcher.FindAllStringSubmatch(raw, -1) {
		if !strings.Contains(g[2], "$") && !strings.Contains(g[2], "[[") {
			continue
		}
		name := g[1]
		if strings.HasPrefix(name, `"`) {
			if u, err := strconv.Unquote(name); err == nil {
				name = u
			}
		}
		m[name] = true
	}
	return m
}

// labelScope is what a subtree's output series may carry: the labels of the contract metrics it
// selects, plus the labels its label functions made. judged is false when it selects no contract metric.
type labelScope struct {
	labels map[string]bool
	judged bool
}

func (s *labelScope) merge(o labelScope) {
	if s.labels == nil {
		s.labels = map[string]bool{}
	}
	for l := range o.labels {
		s.labels[l] = true
	}
	s.judged = s.judged || o.judged
}

func (s *labelScope) add(l string) {
	if s.labels == nil {
		s.labels = map[string]bool{}
	}
	s.labels[contract.Norm(l)] = true
}

// metricChecker walks one query.
type metricChecker struct {
	r       *Report
	q       *files.Query
	c       *contract.Contract
	skipVal map[string]bool
	seen    map[string]bool // metric names already reported
	names   []string        // every stored name a regex selector may match
}

// contractMetrics checks the names a MetricsQL query reads against the contract: each metric
// (exact or by regex), each label of each selector, each label a label function or a grouping
// reads, and the value of an enum or bounded label.
func contractMetrics(r *Report, q *files.Query, e metricsql.Expr, c *contract.Contract) {
	mc := &metricChecker{r: r, q: q, c: c, skipVal: varLabels(q.Expr), seen: map[string]bool{}, names: storedNames(c)}
	mc.walk(e)
}

// storedNames lists every series name a query may select: the contract's and the platform's.
func storedNames(c *contract.Contract) []string {
	out := c.StoredNameList()
	for k := range c.Platform {
		base := contract.StoredBase(k)
		for _, suf := range []string{"", "_total", "_seconds", "_seconds_bucket", "_seconds_sum", "_seconds_count", "_bytes", "_bucket", "_sum", "_count"} {
			out = append(out, base+suf)
		}
	}
	sort.Strings(out)
	return out
}

func (mc *metricChecker) walk(e metricsql.Expr) labelScope {
	var sc labelScope
	switch t := e.(type) {
	case *metricsql.MetricExpr:
		return mc.selector(t)
	case *metricsql.RollupExpr:
		return mc.walk(t.Expr)
	case *metricsql.FuncExpr:
		for _, a := range t.Args {
			sc.merge(mc.walk(a))
		}
		mc.labelFunc(t, &sc)
	case *metricsql.AggrFuncExpr:
		for _, a := range t.Args {
			sc.merge(mc.walk(a))
		}
		if strings.ToLower(t.Name) == "count_values" && len(t.Args) > 0 {
			if se, ok := t.Args[0].(*metricsql.StringExpr); ok {
				sc.add(se.S)
			}
		}
		mc.grouping(t.Modifier.Args, sc)
	case *metricsql.BinaryOpExpr:
		sc.merge(mc.walk(t.Left))
		sc.merge(mc.walk(t.Right))
		mc.grouping(t.GroupModifier.Args, sc)
		mc.grouping(t.JoinModifier.Args, sc)
	}
	return sc
}

// labelFunc judges the labels a label function READS (they must exist on its input) and adds the
// labels it MAKES to the scope of its result only.
func (mc *metricChecker) labelFunc(fe *metricsql.FuncExpr, sc *labelScope) {
	str := func(i int) (string, bool) {
		if i < len(fe.Args) {
			if se, ok := fe.Args[i].(*metricsql.StringExpr); ok {
				return se.S, true
			}
		}
		return "", false
	}
	var reads, makes []string
	pick := func(idx ...int) []string {
		var out []string
		for _, i := range idx {
			if s, ok := str(i); ok {
				out = append(out, s)
			}
		}
		return out
	}
	switch strings.ToLower(fe.Name) {
	case "label_replace":
		makes, reads = pick(1), pick(3)
	case "label_join":
		makes = pick(1)
		for i := 3; i < len(fe.Args); i++ {
			reads = append(reads, pick(i)...)
		}
	case "label_set":
		for i := 1; i < len(fe.Args); i += 2 {
			makes = append(makes, pick(i)...)
		}
	case "label_copy", "label_move":
		for i := 1; i+1 < len(fe.Args); i += 2 {
			reads = append(reads, pick(i)...)
			makes = append(makes, pick(i+1)...)
		}
	case "label_map", "label_match", "label_mismatch", "label_transform":
		reads = pick(1)
	case "label_uppercase", "label_lowercase", "label_del", "label_keep":
		for i := 1; i < len(fe.Args); i++ {
			reads = append(reads, pick(i)...)
		}
	}
	// What the input carries is the scope of the series argument only: labels made by an inner
	// label function count, labels made by this one (or by a sibling elsewhere) do not.
	var input labelScope
	if len(fe.Args) > 0 {
		input = mc.scopeOf(fe.Args[0])
	}
	for _, l := range reads {
		n := contract.Norm(l)
		if input.judged && !input.labels[n] {
			mc.r.add(Fail, mc.q.File, mc.q.Where, "contract-undeclared-label",
				"%s reads label %q, which the series it takes do not have in the contract%s", fe.Name, l, labelHint(n, input.labels))
		}
	}
	for _, l := range makes {
		sc.add(l)
	}
}

// scopeOf is walk without reporting twice: the labels the expression's output may carry.
func (mc *metricChecker) scopeOf(e metricsql.Expr) labelScope {
	quiet := &metricChecker{r: newReport(), q: mc.q, c: mc.c, skipVal: mc.skipVal, seen: map[string]bool{}, names: mc.names}
	return quiet.walk(e)
}

// grouping judges `by (...)`, `without (...)`, `on (...)`, `ignoring (...)`, `group_left (...)`.
func (mc *metricChecker) grouping(args []string, sc labelScope) {
	if !sc.judged {
		return
	}
	for _, a := range args {
		if a == "*" {
			continue
		}
		l := contract.Norm(a)
		if sc.labels[l] {
			continue
		}
		if isPodKey(mc.c, l) {
			mc.r.add(Fail, mc.q.File, mc.q.Where, "contract-pod-label", "grouping by %q: business metrics have no pod key in the store (concept 4.1)", a)
			continue
		}
		mc.r.add(Fail, mc.q.File, mc.q.Where, "contract-undeclared-label", "grouping label %q is not a label of the metrics this query reads%s", a, labelHint(l, sc.labels))
	}
}

// selector judges one selector (`name{a="b"}`, `{__name__=~"re", a="b"}`).
func (mc *metricChecker) selector(me *metricsql.MetricExpr) labelScope {
	var sc labelScope
	for _, lfs := range me.LabelFilterss {
		name, nameRe := "", ""
		for _, lf := range lfs {
			if lf.Label != "__name__" || lf.IsNegative {
				continue
			}
			if lf.IsRegexp {
				nameRe = lf.Value
			} else {
				name = lf.Value
			}
		}
		var matched []contract.StoredMetric
		single := false
		switch {
		case name != "":
			sm, ok := mc.c.Lookup(name)
			if !ok {
				if !mc.seen[name] {
					mc.seen[name] = true
					hint := ""
					if s := contract.Suggest(name, mc.names); s != "" {
						hint = fmt.Sprintf("; did you mean %q?", s)
					}
					mc.r.add(Fail, mc.q.File, mc.q.Where, "contract-undeclared-metric",
						"metric %q is not in the contract (observability/contract.yaml) and not a platform metric%s", name, hint)
				}
				continue
			}
			matched, single = []contract.StoredMetric{sm}, true
		case nameRe != "":
			re, err := regexp.Compile("^(?:" + nameRe + ")$")
			if err != nil {
				continue
			}
			for _, n := range mc.names {
				if re.MatchString(n) {
					if sm, ok := mc.c.Lookup(n); ok {
						matched = append(matched, sm)
					}
				}
			}
			if len(matched) == 0 {
				if !mc.seen["~"+nameRe] {
					mc.seen["~"+nameRe] = true
					mc.r.add(Fail, mc.q.File, mc.q.Where, "contract-undeclared-metric",
						"the metric regex %q matches no metric in the contract (observability/contract.yaml) or the platform metrics", nameRe)
				}
				continue
			}
		}
		allowed := mc.c.AllLabels()
		if len(matched) > 0 {
			allowed = map[string]bool{}
			for _, sm := range matched {
				for l := range sm.Labels {
					allowed[l] = true
				}
			}
			sc.judged = true
			sc.merge(labelScope{labels: allowed, judged: true})
		}
		for _, lf := range lfs {
			if lf.Label == "__name__" {
				continue
			}
			l := contract.Norm(lf.Label)
			if !allowed[l] {
				switch {
				case isPodKey(mc.c, l) && len(matched) > 0:
					mc.r.add(Fail, mc.q.File, mc.q.Where, "contract-pod-label",
						"label %q on metric %q: business metrics have no pod key in the store (concept 4.1); only per_pod metrics keep it", lf.Label, firstName(name, nameRe))
				case len(matched) > 0:
					mc.r.add(Fail, mc.q.File, mc.q.Where, "contract-undeclared-label",
						"label %q is not a label of metric %q in the contract%s", lf.Label, firstName(name, nameRe), labelHint(l, allowed))
				default:
					mc.r.add(Fail, mc.q.File, mc.q.Where, "contract-undeclared-label",
						"label %q is not a label of any contract metric%s", lf.Label, labelHint(l, allowed))
				}
				continue
			}
			if single && !matched[0].Platform && !mc.skipVal[lf.Label] {
				mc.valueCheck(matched[0].Metric, name, lf)
			}
		}
	}
	return sc
}

func firstName(name, re string) string {
	if name != "" {
		return name
	}
	return "~" + re
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

// literalAlt reports whether a regex is only literal values joined by `|` (no metacharacters).
func literalAlt(re string) bool {
	return re != "" && regexp.QuoteMeta(strings.ReplaceAll(re, "|", "")) == strings.ReplaceAll(re, "|", "")
}

// valueCheck judges the value of an enum, bounded-number or time-bucket label. An equality, or a
// regex that is a plain `a|b` list, must name only allowed values; any other regex must match at
// least one allowed value (VictoriaMetrics regexes are anchored).
func (mc *metricChecker) valueCheck(m contract.Metric, metric string, lf metricsql.LabelFilter) {
	t, ok := mc.c.LabelType(m, contract.Norm(lf.Label))
	if !ok {
		return
	}
	if lf.IsRegexp && lf.IsNegative {
		return
	}
	allowed, finite := valueSet(t)
	if !finite {
		return
	}
	bad := func(format string, a ...any) {
		mc.r.add(Fail, mc.q.File, mc.q.Where, "contract-bad-enum-value", format, a...)
	}
	switch {
	case !lf.IsRegexp:
		if lf.Value != "" && !allowed[lf.Value] {
			bad("label %q of metric %q has value %q, which is not allowed; allowed: %s%s", lf.Label, metric, lf.Value, describe(t), valueHint(lf.Value, t))
		}
	case literalAlt(lf.Value):
		for _, v := range strings.Split(lf.Value, "|") {
			if v != "" && !allowed[v] {
				bad("label %q of metric %q has value %q, which is not allowed; allowed: %s%s", lf.Label, metric, v, describe(t), valueHint(v, t))
			}
		}
	default:
		re, err := regexp.Compile("^(?:" + lf.Value + ")$")
		if err != nil {
			return
		}
		for v := range allowed {
			if re.MatchString(v) {
				return
			}
		}
		bad("label %q of metric %q: the regex %q matches no allowed value; allowed: %s", lf.Label, metric, lf.Value, describe(t))
	}
}

const maxValueSet = 100000

// valueSet is the set of values a label type allows, when it is small enough to list.
func valueSet(t contract.Type) (map[string]bool, bool) {
	m := map[string]bool{}
	switch t.Kind {
	case contract.KindEnum:
		for _, v := range t.Values {
			m[v] = true
		}
	case contract.KindInt:
		if t.Size() > maxValueSet {
			return nil, false
		}
		for i := *t.Min; i <= *t.Max; i++ {
			m[strconv.Itoa(i)] = true
		}
	case contract.KindTime:
		hi := 23
		if t.Unit == contract.UnitWeekday {
			hi = 6
		}
		for i := 0; i <= hi; i++ {
			m[strconv.Itoa(i)] = true
		}
	default:
		return nil, false
	}
	return m, true
}

func describe(t contract.Type) string {
	switch t.Kind {
	case contract.KindEnum:
		return strings.Join(t.Values, ", ")
	case contract.KindInt:
		return fmt.Sprintf("the bounded number %d..%d", *t.Min, *t.Max)
	case contract.KindTime:
		if t.Unit == contract.UnitWeekday {
			return "the time bucket 0..6"
		}
		return "the time bucket 0..23"
	}
	return ""
}

func valueHint(v string, t contract.Type) string {
	if t.Kind != contract.KindEnum {
		return ""
	}
	if s := contract.Suggest(v, t.Values); s != "" {
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
