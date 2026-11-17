package check

import (
	"sort"
	"strings"

	"github.com/VictoriaMetrics/metricsql"
)

// isCmp reports the comparison operators.
func isCmp(op string) bool {
	switch op {
	case "==", "!=", ">", "<", ">=", "<=":
		return true
	}
	return false
}

// hasSelector reports whether e reads any series.
func hasSelector(e metricsql.Expr) bool {
	found := false
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		if _, ok := x.(*metricsql.MetricExpr); ok {
			found = true
		}
	})
	return found
}

// stripThresholds removes the comparison a Grafana alert writes into its expression
// (`rate(x[5m]) > 10`): a comparison filters rows out, so on a quiet seed it returns
// nothing even when every name in it is right. The live check runs the left side.
// It returns the rewritten tree and how many comparisons it removed.
func stripThresholds(e metricsql.Expr) (metricsql.Expr, int) {
	n := 0
	var walk func(metricsql.Expr) metricsql.Expr
	walk = func(e metricsql.Expr) metricsql.Expr {
		switch t := e.(type) {
		case *metricsql.BinaryOpExpr:
			t.Left, t.Right = walk(t.Left), walk(t.Right)
			if isCmp(t.Op) && !t.Bool {
				if !hasSelector(t.Right) {
					n++
					return t.Left
				}
				if !hasSelector(t.Left) {
					n++
					return t.Right
				}
			}
		case *metricsql.FuncExpr:
			for i := range t.Args {
				t.Args[i] = walk(t.Args[i])
			}
			// Absence is a threshold: judge its underlying data on a quiet seed.
			if len(t.Args) == 1 {
				switch strings.ToLower(t.Name) {
				case "absent":
					n++
					return t.Args[0]
				case "absent_over_time":
					n++
					t.Name = "count_over_time"
				}
			}
		case *metricsql.AggrFuncExpr:
			for i := range t.Args {
				t.Args[i] = walk(t.Args[i])
			}
		case *metricsql.RollupExpr:
			t.Expr = walk(t.Expr)
		}
		return e
	}
	out := walk(e)
	return out, n
}

// metricNames returns the metric names and label names a MetricsQL expression reads.
func metricNames(e metricsql.Expr) (metrics, labels []string) {
	m, l := map[string]bool{}, map[string]bool{}
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		switch t := x.(type) {
		case *metricsql.MetricExpr:
			for _, lfs := range t.LabelFilterss {
				for _, lf := range lfs {
					if lf.Label == "__name__" {
						if !lf.IsRegexp && !lf.IsNegative {
							m[lf.Value] = true
						}
						continue
					}
					l[lf.Label] = true
				}
			}
		case *metricsql.AggrFuncExpr:
			for _, a := range t.Modifier.Args {
				l[a] = true
			}
		}
	})
	return sortedKeys(m), sortedKeys(l)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// unsupportedFuncs returns the functions in e that VictoriaMetrics does not know
// (Prometheus-only functions): FROM SOURCE, metricsql's own function tables.
func unsupportedFuncs(e metricsql.Expr) []string {
	set := map[string]bool{}
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		fe, ok := x.(*metricsql.FuncExpr)
		if !ok {
			return
		}
		name := strings.ToLower(fe.Name)
		if metricsql.IsRollupFunc(name) || metricsql.IsTransformFunc(name) || metricsql.IsAggrFunc(name) {
			return
		}
		set[fe.Name] = true
	})
	return sortedKeys(set)
}

// windowlessRollups returns the rollup functions called on a selector with no
// `[window]` (the window then comes from the query step).
func windowlessRollups(e metricsql.Expr) []string {
	set := map[string]bool{}
	metricsql.VisitAll(e, func(x metricsql.Expr) {
		fe, ok := x.(*metricsql.FuncExpr)
		if !ok || !metricsql.IsRollupFunc(strings.ToLower(fe.Name)) {
			return
		}
		for _, a := range fe.Args {
			if _, bare := a.(*metricsql.MetricExpr); bare {
				set[fe.Name] = true
			}
		}
	})
	return sortedKeys(set)
}
