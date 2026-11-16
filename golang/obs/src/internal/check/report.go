// Package check holds the rules of `obs query --static` and `obs query --live`.
package check

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Level of a finding.
type Level int

const (
	Fail Level = iota
	Warn
	Note
)

func (l Level) String() string { return [...]string{"FAIL", "warn", "note"}[l] }

// Finding names the file, the rule or panel and the offending token.
type Finding struct {
	Level Level
	File  string
	Where string
	Rule  string // the check that fired, e.g. logsql-syntax
	Msg   string
}

// Report collects findings and counts.
type Report struct {
	Findings []Finding
	Counts   map[string]int // queries by kind
}

func newReport() *Report { return &Report{Counts: map[string]int{}} }

func (r *Report) add(l Level, file, where, rule, format string, a ...any) {
	r.Findings = append(r.Findings, Finding{Level: l, File: file, Where: where, Rule: rule, Msg: fmt.Sprintf(format, a...)})
}

// Failures is the number of FAIL findings.
func (r *Report) Failures() int { return r.count(Fail) }

// Warnings is the number of warn findings.
func (r *Report) Warnings() int { return r.count(Warn) }

func (r *Report) count(l Level) int {
	n := 0
	for _, f := range r.Findings {
		if f.Level == l {
			n++
		}
	}
	return n
}

// Print writes every finding, failures first, in a stable order.
func (r *Report) Print(w io.Writer) {
	fs := append([]Finding{}, r.Findings...)
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Level != fs[j].Level {
			return fs[i].Level < fs[j].Level
		}
		if fs[i].File != fs[j].File {
			return fs[i].File < fs[j].File
		}
		return fs[i].Where < fs[j].Where
	})
	for _, f := range fs {
		loc := f.File
		if f.Where != "" {
			loc += " " + f.Where
		}
		fmt.Fprintf(w, "%-4s %s: [%s] %s\n", f.Level, loc, f.Rule, strings.TrimSpace(f.Msg))
	}
}

// Summary is the one-line result.
func (r *Report) Summary(cmd string) string {
	kinds := make([]string, 0, len(r.Counts))
	for k := range r.Counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var parts []string
	total := 0
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%d %s", r.Counts[k], k))
		total += r.Counts[k]
	}
	res := "ok"
	if r.Failures() > 0 {
		res = "FAILED"
	}
	return fmt.Sprintf("%s: %s: %d queries (%s), %d failures, %d warnings", cmd, res, total, strings.Join(parts, ", "), r.Failures(), r.Warnings())
}
