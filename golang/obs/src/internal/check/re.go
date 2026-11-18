package check

import (
	"regexp"
	"strings"
)

// parseLabelValues reads a template variable query `label_values(metric, label)` or `label_values(label)`.
// The arguments are split at top-level commas only, so braces, parentheses, brackets and quotes protect a
// comma (a selector with several matchers, a comma inside a quoted value). The label is the last argument
// (quotes removed); the metric is the text of the first argument before its selector, "" when there is none.
func parseLabelValues(q string) (metric, label string, ok bool) {
	q = strings.TrimSpace(q)
	const head = "label_values("
	if !strings.HasPrefix(q, head) || !strings.HasSuffix(q, ")") {
		return "", "", false
	}
	body := q[len(head) : len(q)-1]
	var args []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case quote != 0:
			if c == '\\' && quote != '`' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '{' || c == '(' || c == '[':
			depth++
		case c == '}' || c == ')' || c == ']':
			depth--
		case c == ',' && depth == 0:
			args = append(args, body[start:i])
			start = i + 1
		}
	}
	if quote != 0 || depth != 0 {
		return "", "", false
	}
	args = append(args, body[start:])
	for i := range args {
		args[i] = strings.TrimSpace(args[i])
	}
	switch len(args) {
	case 1:
		label = args[0]
	case 2:
		metric, label = args[0], args[1]
		if i := strings.IndexByte(metric, '{'); i >= 0 {
			metric = strings.TrimSpace(metric[:i])
		}
	default:
		return "", "", false
	}
	label = strings.Trim(label, "\"'`")
	if label == "" {
		return "", "", false
	}
	return metric, label, true
}

// unsupportedFnRe reads the function name out of VictoriaMetrics' parser error for a
// function it does not implement (a Prometheus-only function).
var unsupportedFnRe = regexp.MustCompile(`unsupported function "([^"]+)"`)
