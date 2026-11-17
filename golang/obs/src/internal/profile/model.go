// Package profile reads CPU profiles (pprof, Chrome .cpuprofile, speedscope), summarises them
// (CPU per function, per span, OS CPU per request), stores a small baseline (median of 3 runs)
// and compares a new run against it (concept section 11.6).
package profile

import (
	"fmt"
	"strings"
)

// Frame is one stack frame: the function name and the file it lives in (either may be empty).
type Frame struct {
	Name string
	File string
}

// Sample is one CPU sample. Stack is leaf first, root last. Count is the number of samples
// the entry stands for (1 for .cpuprofile and speedscope, the "samples" value for pprof);
// Weight is the CPU weight (nanoseconds for pprof, 1 per sample or the speedscope weight).
// Span is set when the reader found a span label on the sample (pprof label span_name).
type Sample struct {
	Stack  []Frame
	Count  float64
	Weight float64
	Span   string
}

// Profile is a parsed profile: only what the summary needs.
type Profile struct {
	Format  string
	Samples []Sample
}

// Formats obs profile reads.
const (
	FormatPprof      = "pprof"
	FormatCPUProfile = "cpuprofile"
	FormatSpeedscope = "speedscope"
)

// Span frames: Bun runs each span body inside a generated function span__<name>; Dart uses
// one UserTag per span (frame named UserTag:<name>). Both end up as frames in the profile.
const (
	spanFramePrefix = "span__"
	userTagPrefix   = "UserTag:"
)

// spanOfFrame returns the span name when the frame is a span frame.
func spanOfFrame(name string) (string, bool) {
	if s, ok := strings.CutPrefix(name, spanFramePrefix); ok && s != "" {
		return s, true
	}
	if s, ok := strings.CutPrefix(name, userTagPrefix); ok && s != "" {
		return s, true
	}
	// Some Bun/JS profilers print the generated function with a receiver or module prefix.
	if i := strings.LastIndex(name, "."+spanFramePrefix); i >= 0 {
		if s := name[i+1+len(spanFramePrefix):]; s != "" {
			return s, true
		}
	}
	return "", false
}

// isClosure says whether a frame is a closure, lambda or compiler-generated wrapper. Closures
// are never rows of a compare: their CPU belongs to the function that contains them.
func isClosure(name string) bool {
	switch {
	case name == "", name == "(anonymous)", name == "<anonymous>":
		return true
	case strings.Contains(name, "<anonymous closure>"), strings.Contains(name, "[Closure:"):
		return true // Dart
	case strings.Contains(name, "<>c__"), strings.Contains(name, "lambda_method"),
		strings.Contains(name, ">b__"), strings.Contains(name, "<>c.<"):
		return true // .NET: display classes, lambdas
	case strings.HasSuffix(name, "-fm"): // Go method value wrapper
		return true
	}
	// Go: pkg.Func.func1, pkg.Func.func1.2, pkg.Func.gowrap1, pkg.(*T).M.deferwrap1
	for _, marker := range []string{".func", ".gowrap", ".deferwrap"} {
		if i := strings.LastIndex(name, marker); i >= 0 {
			rest := strings.TrimPrefix(name[i:], marker)
			if rest == "" {
				continue
			}
			ok := true
			for _, part := range strings.Split(rest, ".") {
				if part == "" || strings.Trim(part, "0123456789") != "" {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}

// Input errors are returned with context; exit code 2 in the command.
func inputErr(file, format string, a ...any) error {
	return fmt.Errorf("%s: %s", file, fmt.Sprintf(format, a...))
}
