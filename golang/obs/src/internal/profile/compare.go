package profile

import (
	"fmt"
	"io"
	"math"
	"sort"
)

// Noise is the sampling noise of a ratio of two counts: sqrt(1/n_base + 1/n_now).
func Noise(nBase, nNow float64) float64 {
	if nBase <= 0 || nNow <= 0 {
		return math.Inf(1)
	}
	return math.Sqrt(1/nBase + 1/nNow)
}

// Limit is max(tolerance, 3 x noise).
func Limit(tol, nBase, nNow float64) float64 { return math.Max(tol, 3*Noise(nBase, nNow)) }

// Result of one compared row.
type Result struct {
	Kind    string // function, span, total
	Name    string
	File    string
	Line    int64
	Column  int64
	Base    float64 // share, or ns per request for total
	Now     float64
	Change  float64 // now / base - 1
	Limit   float64
	Why     string // where a raised or fixed tolerance came from
	Slower  bool   // change above the limit
	Faster  bool   // change below minus the limit
	Enforce bool   // false: reported only
	Note    string
	Missing bool // the row has no samples in the new run
}

// Failed is true when the row makes the compare fail.
func (r Result) Failed() bool { return r.Enforce && (r.Slower || (r.Kind == "function" && r.Missing)) }

// Compare checks a new run against the baseline. Rows are the baseline's rows only; functions
// that are new in this run are listed as notes in Extra.
func Compare(b *Baseline, now *Summary, l *Limits) (rows []Result, extra []string) {
	if (b.SourceRootExplicit || now.SourceRootExplicit) && (b.SourceRootExplicit != now.SourceRootExplicit || b.SourceRoot != now.SourceRoot) {
		return []Result{{Kind: "input", Name: "source root", Enforce: true, Slower: true, Note: fmt.Sprintf("explicit source roots differ: baseline %q, now %q; use the baseline's --source-root or regenerate it with --update", b.SourceRoot, now.SourceRoot)}}, nil
	}
	if b.SamplePeriodNs != now.SamplePeriodNs {
		return []Result{{Kind: "input", Name: "sample period", Enforce: true, Slower: true, Note: fmt.Sprintf("sample period differs: baseline %g ns, now %g ns; use the actual --sample-period used for collection", b.SamplePeriodNs, now.SamplePeriodNs)}}, nil
	}
	var functions []FuncRow
	for _, f := range now.Functions {
		if f.Own {
			functions = append(functions, f)
		}
	}
	matched, ambiguous, used := matchFunctions(b.Functions, functions)
	bySpan := map[string]SpanRow{}
	for _, s := range now.Spans {
		bySpan[s.Name] = s
	}
	mk := func(kind, name string, base, nowShare float64, nBase, nNow int64) Result {
		tol, why := l.tolerance(b, kind, name)
		r := Result{Kind: kind, Name: name, Base: base, Now: nowShare, Enforce: true, Why: why}
		if nNow == 0 {
			r.Missing = true
			r.Change = -1
			r.Limit = tol
			r.Faster = true
			return r
		}
		r.Change = nowShare/base - 1
		r.Limit = Limit(tol, float64(nBase), float64(nNow))
		r.Slower = r.Change > r.Limit
		r.Faster = r.Change < -r.Limit
		return r
	}
	for bi, br := range b.Functions {
		var f FuncRow
		if ni, ok := matched[bi]; ok {
			f = functions[ni]
			used[ni] = true
		}
		r := mk("function", br.Name, br.Share, f.OwnShare, br.N, f.OwnN)
		// Function-specific overrides can select file and declaration line as well as name.
		tol, why := l.functionTolerance(b, br)
		r.File, r.Line, r.Column, r.Why = br.File, br.Line, br.Column, why
		if r.Missing {
			r.Limit = tol
			r.Faster = false
			r.Note = "baseline function has no matching samples; inspect the profile and regenerate the baseline with --update if intentional"
			if reason := ambiguous[bi]; reason != "" {
				r.Note = reason
			}
		} else {
			r.Limit = Limit(tol, float64(br.N), float64(f.OwnN))
			r.Slower, r.Faster = r.Change > r.Limit, r.Change < -r.Limit
		}
		rows = append(rows, r)
	}
	for _, br := range b.Spans {
		s := bySpan[br.Name]
		rows = append(rows, mk("span", br.Name, br.Share, s.Share, br.N, s.N))
	}
	// Total OS CPU per request.
	switch {
	case b.CPUPerRequestNs <= 0:
		extra = append(extra, "total CPU per request: not compared (the baseline has no OS CPU per request)")
	case now.CPUPerRequestNs <= 0:
		extra = append(extra, "total CPU per request: not compared (no run meta with requests and os_cpu_ns for this run)")
	default:
		tol, why := l.tolerance(b, "total", "")
		r := Result{Kind: "total", Name: "OS CPU per request", Base: b.CPUPerRequestNs, Now: now.CPUPerRequestNs, Why: why}
		r.Change = r.Now/r.Base - 1
		r.Limit = Limit(tol, float64(b.Samples), float64(now.Samples))
		r.Slower = r.Change > r.Limit
		r.Faster = r.Change < -r.Limit
		quiet, known, detail := now.Run.Quiet()
		r.Enforce = quiet
		switch {
		case !known:
			r.Note = "not enforced: no runner signal (" + detail + ")"
		case !quiet:
			r.Note = "not enforced: noisy runner, " + detail
		default:
			r.Note = "quiet runner, " + detail
		}
		rows = append(rows, r)
	}
	// Own functions that are hot now but are not rows of the baseline.
	var newer []FuncRow
	for ni, f := range functions {
		if f.OwnShare >= MinFunctionShare && !used[ni] {
			newer = append(newer, f)
		}
	}
	sort.Slice(newer, func(i, j int) bool {
		a, b := newer[i], newer[j]
		if a.OwnShare != b.OwnShare {
			return a.OwnShare > b.OwnShare
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	for _, f := range newer {
		extra = append(extra, fmt.Sprintf("function %s is %s of CPU now and has no row in the baseline", functionName(f.Name, f.File, f.Line, f.Column), pct(f.OwnShare)))
	}
	return rows, extra
}

// PrintCompare writes the table (what / baseline / now / change (limit)) and the failures.
// It returns the number of rows that failed.
func PrintCompare(w io.Writer, file string, rows []Result, extra []string) int {
	fmt.Fprintf(w, "%-9s %-44s %10s %10s  %s\n", "kind", "what", "baseline", "now", "change (limit)")
	failed := 0
	for _, r := range rows {
		if r.Kind == "input" {
			fmt.Fprintf(w, "FAIL %s %s: %s\n", file, r.Name, r.Note)
			failed++
			continue
		}
		base, now := pct(r.Base), pct(r.Now)
		if r.Kind == "total" {
			base, now = fmtNs(r.Base), fmtNs(r.Now)
		}
		if r.Missing && r.Kind == "function" {
			fmt.Fprintf(w, "%-9s %-44s %10s %10s  ERROR: %s\n", r.Kind, functionName(r.Name, r.File, r.Line, r.Column), base, "missing", r.Note)
			failed++
			continue
		}
		verdict := ""
		switch {
		case r.Slower && r.Enforce:
			verdict, failed = "  SLOWER", failed+1
		case r.Slower:
			verdict = "  slower (" + r.Note + ")"
		case r.Faster:
			verdict = "  faster"
		}
		if r.Missing {
			verdict = "  faster (no samples now)"
		}
		fmt.Fprintf(w, "%-9s %-44s %10s %10s  %+.1f%% (%.1f%%)%s\n", r.Kind, functionName(r.Name, r.File, r.Line, r.Column), base, now, r.Change*100, r.Limit*100, verdict)
	}
	for _, r := range rows {
		if r.Kind == "total" && r.Note != "" && !r.Slower {
			fmt.Fprintf(w, "note: total CPU per request: %s\n", r.Note)
		}
	}
	for _, e := range extra {
		fmt.Fprintf(w, "note: %s\n", e)
	}
	for _, r := range rows {
		if !r.Failed() {
			continue
		}
		if r.Kind == "input" {
			continue
		}
		if r.Kind == "function" && r.Missing {
			fmt.Fprintf(w, "FAIL %s function %q: %s; %s\n", file, functionName(r.Name, r.File, r.Line, r.Column), r.Note, "missing baseline row is not a speedup")
			continue
		}
		why := ""
		if r.Why != "" {
			why = "; tolerance from " + r.Why
		}
		if r.Kind == "total" {
			fmt.Fprintf(w, "FAIL %s %s: %s -> %s, %+.1f%% is above the limit of %.1f%%%s; %s\n", file, r.Name, fmtNs(r.Base), fmtNs(r.Now), r.Change*100, r.Limit*100, why, r.Note)
			continue
		}
		location := ""
		if r.File != "" || r.Line != 0 {
			location = "; source " + functionName(r.Name, r.File, r.Line, r.Column)
		}
		fmt.Fprintf(w, "FAIL %s %s %q: share %s -> %s, %+.1f%% is above the limit of %.1f%%%s%s\n", file, r.Kind, r.Name, pct(r.Base), pct(r.Now), r.Change*100, r.Limit*100, why, location)
	}
	return failed
}
