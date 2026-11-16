package check

import "regexp"

// labelValuesRe matches a template variable query `label_values(metric, label)` or `label_values(label)`.
var labelValuesRe = regexp.MustCompile(`^\s*label_values\(\s*([^,)]+?)\s*(?:,\s*([^)]+?)\s*)?\)\s*$`)

// unsupportedFnRe reads the function name out of VictoriaMetrics' parser error for a
// function it does not implement (a Prometheus-only function).
var unsupportedFnRe = regexp.MustCompile(`unsupported function "([^"]+)"`)
