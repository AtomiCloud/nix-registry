package profile

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"

	pp "github.com/google/pprof/profile"
)

// Read parses a profile file in the given format (pprof, cpuprofile or speedscope).
// Gzip-compressed JSON is accepted for the two JSON formats; pprof is gzip or raw protobuf.
func Read(path, format string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p *Profile
	switch format {
	case FormatPprof:
		p, err = readPprof(path, data)
	case FormatCPUProfile:
		p, err = readCPUProfile(path, gunzipIfNeeded(data))
	case FormatSpeedscope:
		p, err = readSpeedscope(path, gunzipIfNeeded(data))
	default:
		return nil, fmt.Errorf("unknown --format %q: want pprof, cpuprofile or speedscope", format)
	}
	if err != nil {
		return nil, err
	}
	p.Format = format
	return p, nil
}

func gunzipIfNeeded(data []byte) []byte {
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		if zr, err := gzip.NewReader(bytes.NewReader(data)); err == nil {
			if b, err := io.ReadAll(zr); err == nil {
				return b
			}
		}
	}
	return data
}

// ---- pprof ---------------------------------------------------------------------------

func readPprof(path string, data []byte) (*Profile, error) {
	pr, err := pp.ParseData(data)
	if err != nil {
		return nil, inputErr(path, "not a pprof profile: %v", err)
	}
	if len(pr.SampleType) == 0 {
		return nil, inputErr(path, "pprof profile has no sample type")
	}
	// Count: the "samples" value; weight: "cpu" (nanoseconds) when present, else the same count.
	countIdx, weightIdx := -1, -1
	for i, st := range pr.SampleType {
		switch st.Type {
		case "samples":
			if countIdx < 0 {
				countIdx = i
			}
		case "cpu":
			if weightIdx < 0 {
				weightIdx = i
			}
		}
	}
	if countIdx < 0 {
		countIdx = 0
	}
	if weightIdx < 0 {
		weightIdx = countIdx
	}
	out := &Profile{}
	for _, s := range pr.Sample {
		if countIdx >= len(s.Value) || weightIdx >= len(s.Value) {
			continue
		}
		if s.Value[countIdx] <= 0 && s.Value[weightIdx] <= 0 {
			continue
		}
		sm := Sample{Count: float64(s.Value[countIdx]), Weight: float64(s.Value[weightIdx])}
		if v := s.Label["span_name"]; len(v) > 0 {
			sm.Span = v[0]
		}
		for _, loc := range s.Location {
			// Line[0] is the innermost (inlined) function; the last line is the caller it was inlined into.
			for _, ln := range loc.Line {
				if ln.Function == nil {
					continue
				}
				sm.Stack = append(sm.Stack, Frame{Name: ln.Function.Name, File: ln.Function.Filename, Line: ln.Function.StartLine})
			}
		}
		out.Samples = append(out.Samples, sm)
	}
	if len(out.Samples) == 0 {
		return nil, inputErr(path, "pprof profile has no samples")
	}
	return out, nil
}

// ---- Chrome .cpuprofile --------------------------------------------------------------

type cpuProfileDoc struct {
	Nodes []struct {
		ID        int `json:"id"`
		CallFrame struct {
			FunctionName string `json:"functionName"`
			URL          string `json:"url"`
			LineNumber   *int64 `json:"lineNumber"` // Chrome uses zero-based declaration lines
		} `json:"callFrame"`
		HitCount int   `json:"hitCount"`
		Children []int `json:"children"`
		Parent   *int  `json:"parent"`
	} `json:"nodes"`
	Samples []int `json:"samples"`
}

func readCPUProfile(path string, data []byte) (*Profile, error) {
	var doc cpuProfileDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, inputErr(path, "not a .cpuprofile (JSON): %v", err)
	}
	if len(doc.Nodes) == 0 {
		return nil, inputErr(path, ".cpuprofile has no nodes")
	}
	parent := map[int]int{}
	byID := map[int]int{}
	for i, n := range doc.Nodes {
		byID[n.ID] = i
		for _, c := range n.Children {
			parent[c] = n.ID
		}
		if n.Parent != nil {
			parent[n.ID] = *n.Parent
		}
	}
	stackOf := func(id int) []Frame {
		var st []Frame
		for steps := 0; steps < 100000; steps++ {
			i, ok := byID[id]
			if !ok {
				break
			}
			cf := doc.Nodes[i].CallFrame
			if cf.FunctionName != "(root)" {
				var line int64
				if cf.LineNumber != nil && *cf.LineNumber >= 0 {
					line = *cf.LineNumber + 1
				}
				st = append(st, Frame{Name: cf.FunctionName, File: cf.URL, Line: line})
			}
			p, ok := parent[id]
			if !ok {
				break
			}
			id = p
		}
		return st
	}
	isIdle := func(st []Frame) bool { return len(st) > 0 && st[0].Name == "(idle)" }
	counts := map[int]float64{}
	if len(doc.Samples) > 0 {
		for _, id := range doc.Samples {
			counts[id]++
		}
	} else {
		for _, n := range doc.Nodes {
			if n.HitCount > 0 {
				counts[n.ID] = float64(n.HitCount)
			}
		}
	}
	ids := make([]int, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := &Profile{}
	for _, id := range ids {
		st := stackOf(id)
		if len(st) == 0 || isIdle(st) {
			continue // the root node, or the thread was idle: not CPU
		}
		out.Samples = append(out.Samples, Sample{Stack: st, Count: counts[id], Weight: counts[id]})
	}
	if len(out.Samples) == 0 {
		return nil, inputErr(path, ".cpuprofile has no CPU samples")
	}
	return out, nil
}

// ---- speedscope ----------------------------------------------------------------------

type speedscopeDoc struct {
	Shared struct {
		Frames []struct {
			Name string `json:"name"`
			File string `json:"file"`
			Line int64  `json:"line"`
		} `json:"frames"`
	} `json:"shared"`
	Profiles []speedscopeProfile `json:"profiles"`
}

type speedscopeProfile struct {
	Type       string    `json:"type"`
	Name       string    `json:"name"`
	Unit       string    `json:"unit"`
	StartValue float64   `json:"startValue"`
	EndValue   float64   `json:"endValue"`
	Samples    [][]int   `json:"samples"`
	Weights    []float64 `json:"weights"`
	Events     []struct {
		Type  string  `json:"type"`
		Frame int     `json:"frame"`
		At    float64 `json:"at"`
	} `json:"events"`
}

// TraceEvent's thread-time export uses these pseudo-frames for off-CPU or uncertain
// native time. Do not classify arbitrary application methods named Wait as blocked.
func waitStack(stack []Frame) bool {
	for _, f := range stack {
		switch f.Name {
		case "(idle)", "BLOCKED_TIME", "AWAIT_TIME", "UNMANAGED_CODE_TIME":
			return true
		}
	}
	return false
}

func readSpeedscope(path string, data []byte) (*Profile, error) {
	var doc speedscopeDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, inputErr(path, "not a speedscope file (JSON): %v", err)
	}
	if len(doc.Shared.Frames) == 0 || len(doc.Profiles) == 0 {
		return nil, inputErr(path, "speedscope file has no shared.frames or no profiles")
	}
	out := &Profile{}
	for pi, pr := range doc.Profiles {
		// Normalize time units before combining threads. Empty units occur in older
		// sampled fixtures; "none" retains speedscope's arbitrary weight units.
		scale := 1.0
		switch pr.Unit {
		case "nanoseconds":
			scale = 1e-9
		case "microseconds":
			scale = 1e-6
		case "milliseconds":
			scale = 1e-3
		case "", "none", "seconds":
		default:
			return nil, inputErr(path, "profiles[%d]: unsupported CPU unit %q", pi, pr.Unit)
		}
		stackFrames := func(stack []int) ([]Frame, error) {
			var frames []Frame
			for i := len(stack) - 1; i >= 0; i-- {
				fi := stack[i]
				if fi < 0 || fi >= len(doc.Shared.Frames) {
					return nil, inputErr(path, "profiles[%d] names frame %d, but there are %d frames", pi, fi, len(doc.Shared.Frames))
				}
				f := doc.Shared.Frames[fi]
				frames = append(frames, Frame{Name: f.Name, File: f.File, Line: f.Line})
			}
			return frames, nil
		}
		if pr.Type == "evented" {
			if len(pr.Events) == 0 {
				return nil, inputErr(path, "profiles[%d] evented profile has no events", pi)
			}
			stack := []int{}
			last := pr.StartValue
			for ei, ev := range pr.Events {
				if math.IsInf(ev.At, 0) || ev.At < last || ev.At > pr.EndValue {
					return nil, inputErr(path, "profiles[%d] event %d: invalid time %g (previous %g, end %g)", pi, ei, ev.At, last, pr.EndValue)
				}
				frames, err := stackFrames(stack)
				if err != nil {
					return nil, err
				}
				// Every positive interval is charged to the stack open during it,
				// never once per frame: nested frames must not double count CPU.
				if ev.At > last && len(frames) > 0 && !waitStack(frames) {
					out.Samples = append(out.Samples, Sample{Stack: frames, Count: 1, Weight: (ev.At - last) * scale})
				}
				if ev.Frame < 0 || ev.Frame >= len(doc.Shared.Frames) {
					return nil, inputErr(path, "profiles[%d] event %d: invalid frame %d", pi, ei, ev.Frame)
				}
				switch ev.Type {
				case "O":
					stack = append(stack, ev.Frame)
				case "C":
					if len(stack) == 0 || stack[len(stack)-1] != ev.Frame {
						return nil, inputErr(path, "profiles[%d] event %d: closing frame %d does not match the open stack", pi, ei, ev.Frame)
					}
					stack = stack[:len(stack)-1]
				default:
					return nil, inputErr(path, "profiles[%d] event %d: type %q, want O or C", pi, ei, ev.Type)
				}
				last = ev.At
			}
			if len(stack) != 0 {
				return nil, inputErr(path, "profiles[%d] evented profile has unclosed frames", pi)
			}
			continue
		}
		if pr.Type != "sampled" {
			return nil, inputErr(path, "profiles[%d] has type %q: want sampled or evented speedscope profiles", pi, pr.Type)
		}
		if len(pr.Weights) != 0 && len(pr.Weights) != len(pr.Samples) {
			return nil, inputErr(path, "profiles[%d]: %d samples but %d weights", pi, len(pr.Samples), len(pr.Weights))
		}
		for si, stack := range pr.Samples {
			w := 1.0
			if len(pr.Weights) > 0 {
				w = pr.Weights[si]
			}
			if w <= 0 || len(stack) == 0 {
				continue
			}
			sm := Sample{Count: 1, Weight: w * scale}
			// speedscope stacks are root first: reverse to leaf first.
			var err error
			sm.Stack, err = stackFrames(stack)
			if err != nil {
				return nil, err
			}
			if waitStack(sm.Stack) {
				continue
			}
			out.Samples = append(out.Samples, sm)
		}
	}
	if len(out.Samples) == 0 {
		return nil, inputErr(path, "speedscope file has no samples")
	}
	return out, nil
}
