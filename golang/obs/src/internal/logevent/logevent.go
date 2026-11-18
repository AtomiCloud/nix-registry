// Package logevent applies the contract's free-form log alert rules to the
// VictoriaLogs parsed AST. It never tokenizes LogsQL with regular expressions.
package logevent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/engines"
	"github.com/VictoriaMetrics/VictoriaLogs/lib/logstorage"
)

const Field = "durian.cloud/event"

type Events interface{ EventNames() []string }
type Analysis struct {
	Expr            string
	Events, Assumed []string
	Skipped         bool
}

// Analyze validates the event selectors and removes filters on free-form field
// values, preserving event, stream and time predicates. nil means no contract;
// in that case the original query is preserved and only the M1 checks apply.
func Analyze(expr string, events Events) (Analysis, error) {
	return AnalyzeAt(expr, events, time.Now())
}

func AnalyzeAt(expr string, events Events, at time.Time) (Analysis, error) {
	out := Analysis{Expr: expr}
	engines.Init()
	q, err := logstorage.ParseQueryAtTimestamp(expr, at.UnixNano())
	if err != nil {
		return out, err
	}
	if _, err := q.ObsStatsLabels(); err != nil {
		return out, err
	}
	if events == nil {
		out.Skipped = true
		return out, nil
	}
	declared := append([]string{}, events.EventNames()...)
	names := append([]string{}, declared...)
	sort.Strings(names)
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	hasEvent := false
	matched := map[string]bool{}
	// Per-leaf names describe validated selectors for display. The actual AST
	// (including native phrase/prefix matching and negation) is retained. Row
	// pruning must derive its own conservative bounds from that rewritten AST.
	var visit func(logstorage.ObsFilterTree, bool) (string, error)
	visit = func(n logstorage.ObsFilterTree, neg bool) (string, error) {
		if n.Kind == "stream" {
			for _, field := range n.Values {
				if field == Field {
					return "", fmt.Errorf("event field %q inside _stream:{} is not a stream field", Field)
				}
			}
			return n.Text, nil
		}
		if n.Kind == "or" && containsEvent(n) {
			return "", fmt.Errorf("event field %q inside an or with another filter is refused; use in(...) or :~ instead", Field)
		}
		if n.Field == Field {
			hasEvent = true
			var matches []string
			switch n.Kind {
			case "exact", "phrase":
				if len(n.Values) != 1 || !known[n.Values[0]] {
					value := ""
					if len(n.Values) > 0 {
						value = n.Values[0]
					}
					return "", fmt.Errorf("event %q is not in the contract%s", value, hint(value, declared))
				}
				matches = append(matches, n.Values[0])
			case "prefix":
				prefix := ""
				if len(n.Values) > 0 {
					prefix = n.Values[0]
				}
				for _, name := range names {
					if strings.HasPrefix(name, prefix) {
						matches = append(matches, name)
					}
				}
				if len(matches) == 0 {
					return "", fmt.Errorf("event prefix %q matches no contract event (contract/events.json: %s)%s", prefix+"*", strings.Join(declared, ", "), hint(prefix, declared))
				}
			case "regexp":
				re, err := regexp.Compile(n.Pattern)
				if err != nil {
					return "", err
				}
				for _, name := range names {
					if re.MatchString(name) {
						matches = append(matches, name)
					}
				}
				if len(matches) == 0 {
					return "", fmt.Errorf("event regex %q matches no contract event", n.Pattern)
				}
			case "in":
				for _, name := range n.Values {
					if known[name] {
						matches = append(matches, name)
					}
				}
				if len(matches) == 0 {
					return "", fmt.Errorf("event in(%s) matches no contract event", strings.Join(n.Values, ", "))
				}
			default:
				return "", fmt.Errorf("event filter form not checkable: %s; use :=, phrase, prefix, :~ or in(...)", n.Text)
			}
			selected := map[string]bool{}
			for _, name := range matches {
				selected[name] = true
			}
			for _, name := range names {
				if selected[name] != neg {
					matched[name] = true
				}
			}
			return n.Text, nil
		}
		if n.Kind == "time" {
			return n.Text, nil
		}
		if n.Kind == "and" || n.Kind == "or" || n.Kind == "not" {
			// A negated unknown-value predicate is itself assumed true. Replacing only
			// its child with true would incorrectly turn it into a constant false.
			if !containsKept(n) {
				if n.Text != "*" && n.Text != "" {
					out.Assumed = append(out.Assumed, n.Text)
				}
				return "*", nil
			}
			var children []string
			for _, c := range n.Children {
				text, err := visit(c, neg != (n.Kind == "not"))
				if err != nil {
					return "", err
				}
				children = append(children, "("+text+")")
			}
			if n.Kind == "not" {
				return "!" + children[0], nil
			}
			op := " "
			if n.Kind == "or" {
				op = " or "
			}
			return strings.Join(children, op), nil
		}
		if n.Text != "*" && n.Text != "" {
			out.Assumed = append(out.Assumed, n.Text)
		}
		return "*", nil
	}
	rewritten, err := q.ObsAlertTransform(func(n logstorage.ObsFilterTree) (string, error) { return visit(n, false) }, func(fn string, fields []string) error {
		if len(fields) > 0 {
			return fmt.Errorf("stats function %s reads log field values %v; seed events carry no field values", fn, fields)
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if !hasEvent {
		return out, fmt.Errorf("a log alert must filter on the event field %q (:=, phrase, prefix, negated, :~ or in(...))", Field)
	}
	out.Expr = rewritten.String()
	for name := range matched {
		out.Events = append(out.Events, name)
	}
	sort.Strings(out.Events)
	sort.Strings(out.Assumed)
	out.Assumed = unique(out.Assumed)
	return out, nil
}

func containsEvent(n logstorage.ObsFilterTree) bool {
	if n.Field == Field {
		return true
	}
	for _, c := range n.Children {
		if containsEvent(c) {
			return true
		}
	}
	return false
}
func containsKept(n logstorage.ObsFilterTree) bool {
	if n.Field == Field || n.Kind == "time" || n.Kind == "stream" {
		return true
	}
	for _, c := range n.Children {
		if containsKept(c) {
			return true
		}
	}
	return false
}
func unique(xs []string) []string {
	var out []string
	for _, s := range xs {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func hint(name string, names []string) string {
	best, distance := "", 4
	for _, s := range names {
		d := editDistance(name, s)
		if d < distance {
			best, distance = s, d
		}
	}
	if best != "" {
		return fmt.Sprintf("; did you mean %q", best)
	}
	return ""
}
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// ValidateSeed refuses an undeclared seed event before any engine is started.
func ValidateSeed(names []string, events Events) error {
	if events == nil {
		return nil
	}
	known := map[string]bool{}
	all := events.EventNames()
	for _, name := range all {
		known[name] = true
	}
	for _, name := range names {
		if !known[name] {
			return fmt.Errorf("seed log event %q is not in the contract%s", name, hint(name, all))
		}
	}
	return nil
}
