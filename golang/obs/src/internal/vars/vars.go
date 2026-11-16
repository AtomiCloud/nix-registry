// Package vars fills dashboard variables the way Grafana's Prometheus backend does
// ($__interval, $__interval_ms, $__rate_interval, $__rate_interval_ms, $__range,
// $__range_s, $__range_ms and the ${...} spellings, and the step it sends) and the
// template variables (one value as is, several values as a regex group).
//
// It is a port of Grafana promlib models/query.go (calculatePrometheusInterval,
// interpolateVariables, calculateRateInterval), measured against Grafana in the
// spike 7 prototype (160 of 160 panel queries equal). The LogsQL filling reuses the
// same macros; whether that equals the VictoriaLogs datasource's own
// ReplaceTemplateVariable (v0.34.0) is NOT TESTED (the nightly parity job does).
package vars

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/gtime"
)

// Case is one panel query and the Grafana request around it.
type Case struct {
	Expr          string
	FromMs, ToMs  int64
	MaxDataPoints int64
	IntervalMs    int64
	QueryInterval string              // the panel's "Min step" (`interval`)
	Scrape        string              // the datasource's timeInterval
	Vars          map[string][]string // template variable values
}

// Result is the filled query and the step Grafana sends with it.
type Result struct {
	Expr   string
	StepMs int64
}

func isVarInterval(s string) bool {
	switch s {
	case "$__interval", "${__interval}", "$__rate_interval", "${__rate_interval}":
		return true
	}
	return false
}

func calcInterval(from, to, maxDP int64, minI time.Duration) time.Duration {
	if maxDP == 0 {
		maxDP = 1500
	}
	ci := time.Duration((to - from) * int64(time.Millisecond) / maxDP)
	if ci < minI {
		return minI
	}
	return gtime.RoundInterval(ci)
}

func rateInterval(q time.Duration, scrape string) time.Duration {
	if scrape == "" {
		scrape = "15s"
	}
	s, err := gtime.ParseIntervalStringToTimeDuration(scrape)
	if err != nil {
		return 0
	}
	return time.Duration(int64(math.Max(float64(q+s), 4*float64(s))))
}

func promStep(c Case) time.Duration {
	qi := c.QueryInterval
	if isVarInterval(qi) {
		qi = ""
	}
	minI, err := gtime.GetIntervalFrom(c.Scrape, qi, c.IntervalMs, 15*time.Second)
	if err != nil {
		return 0
	}
	calc := calcInterval(c.FromMs, c.ToMs, c.MaxDataPoints, minI)
	safe := gtime.RoundInterval(time.Duration((c.ToMs - c.FromMs) * int64(time.Millisecond) / 11000))
	adj := safe
	if calc > safe {
		adj = calc
	}
	if c.QueryInterval == "$__rate_interval" || c.QueryInterval == "${__rate_interval}" {
		return rateInterval(adj, c.Scrape)
	}
	return adj
}

// Fill replaces the macros and the template variables in c.Expr.
func Fill(c Case) Result {
	step := promStep(c)
	queryInterval := time.Duration(c.IntervalMs) * time.Millisecond
	rangeMs := c.ToMs - c.FromMs
	rangeS := int64(math.Round(float64(rangeMs) / 1000))
	var ri time.Duration
	req := c.QueryInterval
	if req == "$__rate_interval" || req == "${__rate_interval}" {
		ri = step
	} else {
		if req == "$__interval" || req == "${__interval}" {
			req = step.String()
		}
		if req == "" {
			req = c.Scrape
		}
		ri = rateInterval(queryInterval, req)
	}
	e := fillTemplate(c.Expr, c.Vars)
	rep := []struct{ a, b string }{
		{"$__interval_ms", strconv.FormatInt(int64(step/time.Millisecond), 10)},
		{"$__interval", gtime.FormatInterval(step)},
		{"$__range_ms", strconv.FormatInt(rangeMs, 10)},
		{"$__range_s", strconv.FormatInt(rangeS, 10)},
		{"$__range", strconv.FormatInt(rangeS, 10) + "s"},
		{"$__rate_interval_ms", strconv.FormatInt(int64(ri/time.Millisecond), 10)},
		{"$__rate_interval", ri.String()},
		{"${__interval_ms}", strconv.FormatInt(int64(step/time.Millisecond), 10)},
		{"${__interval}", gtime.FormatInterval(step)},
		{"${__range_ms}", strconv.FormatInt(rangeMs, 10)},
		{"${__range_s}", strconv.FormatInt(rangeS, 10)},
		{"${__range}", strconv.FormatInt(rangeS, 10) + "s"},
		{"${__rate_interval_ms}", strconv.FormatInt(int64(ri/time.Millisecond), 10)},
		{"${__rate_interval}", ri.String()},
	}
	// The longer spellings first: `$__interval_ms` must not be eaten by `$__interval`.
	for _, r := range rep {
		e = strings.ReplaceAll(e, r.a, r.b)
	}
	return Result{Expr: e, StepMs: step.Milliseconds()}
}

var tplVar = regexp.MustCompile(`\$\{(\w+)(?::(\w+))?\}|\$(\w+)|\[\[(\w+)\]\]`)

// Names returns the template variable names an expression mentions (the built-in
// `__` macros excluded), sorted.
func Names(e string) []string {
	set := map[string]bool{}
	for _, m := range tplVar.FindAllStringSubmatch(e, -1) {
		n := m[1]
		if n == "" {
			n = m[3]
		}
		if n == "" {
			n = m[4]
		}
		if n == "" || strings.HasPrefix(n, "__") {
			continue
		}
		set[n] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// fillTemplate: Grafana's Prometheus multi-value format: one value as is, several as
// (a|b) with the regex characters escaped.
func fillTemplate(e string, vars map[string][]string) string {
	if len(vars) == 0 {
		return e
	}
	return tplVar.ReplaceAllStringFunc(e, func(m string) string {
		sm := tplVar.FindStringSubmatch(m)
		name := sm[1]
		if name == "" {
			name = sm[3]
		}
		if name == "" {
			name = sm[4]
		}
		vs, ok := vars[name]
		if !ok {
			return m
		}
		if len(vs) == 1 {
			return vs[0]
		}
		esc := make([]string, len(vs))
		for i, v := range vs {
			esc[i] = regexEscape(v)
		}
		return "(" + strings.Join(esc, "|") + ")"
	})
}

var regexSpecials = regexp.MustCompile(`[\\^$*+?.()|{}\[\]]`)

func regexEscape(s string) string {
	return regexSpecials.ReplaceAllStringFunc(s, func(x string) string { return `\\` + x })
}

// ParseDuration reads a Grafana duration (5m, 6h, 1d, 2w, 1M, 1y).
func ParseDuration(s string) (time.Duration, error) { return gtime.ParseDuration(s) }
