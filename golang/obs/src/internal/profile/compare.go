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
func (r Result) Failed() bool { return r.Slower && r.Enforce }

// Compare checks a new run against the baseline. Rows are the baseline's rows only; functions
// that are new in this run are listed as notes in Extra.
func Compare(b *Baseline, now *Summary, l *Limits) (rows []Result, extra []string) {
	byFn := map[string]FuncRow{}
	for _, f := range now.Functions {
		if f.Own {
			byFn[f.Name] = f
		}
	}
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
	for _, br := range b.Functions {
		f := byFn[br.Name]
		rows = append(rows, mk("function", br.Name, br.Share, f.OwnShare, br.N, f.OwnN))
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
	inBase := map[string]bool{}
	for _, br := range b.Functions {
		inBase[br.Name] = true
	}
	var newer []FuncRow
	for _, f := range now.Functions {
		if f.Own && f.OwnShare >= MinFunctionShare && !inBase[f.Name] {
			newer = append(newer, f)
		}
	}
	sort.Slice(newer, func(i, j int) bool { return newer[i].OwnShare > newer[j].OwnShare })
	for _, f := range newer {
		extra = append(extra, fmt.Sprintf("function %s is %s of CPU now and has no row in the baseline", f.Name, pct(f.OwnShare)))
	}
	return rows, extra
}

// PrintCompare writes the table (what / baseline / now / change (limit)) and the failures.
// It returns the number of rows that failed.
func PrintCompare(w io.Writer, file string, rows []Result, extra []string) int {
	fmt.Fprintf(w, "%-9s %-44s %10s %10s  %s\n", "kind", "what", "baseline", "now", "change (limit)")
	failed := 0
	for _, r := range rows {
		base, now := pct(r.Base), pct(r.Now)
		if r.Kind == "total" {
			base, now = fmtNs(r.Base), fmtNs(r.Now)
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
		fmt.Fprintf(w, "%-9s %-44s %10s %10s  %+.1f%% (%.1f%%)%s\n", r.Kind, trunc(r.Name, 44), base, now, r.Change*100, r.Limit*100, verdict)
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
		why := ""
		if r.Why != "" {
			why = "; tolerance from " + r.Why
		}
		if r.Kind == "total" {
			fmt.Fprintf(w, "FAIL %s %s: %s -> %s, %+.1f%% is above the limit of %.1f%%%s; %s\n", file, r.Name, fmtNs(r.Base), fmtNs(r.Now), r.Change*100, r.Limit*100, why, r.Note)
			continue
		}
		fmt.Fprintf(w, "FAIL %s %s %q: share %s -> %s, %+.1f%% is above the limit of %.1f%%%s\n", file, r.Kind, r.Name, pct(r.Base), pct(r.Now), r.Change*100, r.Limit*100, why)
	}
	return failed
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "~"
}
