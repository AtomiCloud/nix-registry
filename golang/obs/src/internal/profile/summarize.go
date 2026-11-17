package profile

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// RunMeta is the sidecar a load run writes next to its profile (`<profile>.run.json`): what the
// profile cannot know. Requests and OSCPUNs give "OS CPU per request"; the other fields are the
// quiet-runner signal (see Quiet).
type RunMeta struct {
	Requests    int64    `json:"requests"`     // requests sent in the load window
	OSCPUNs     float64  `json:"os_cpu_ns"`    // process CPU (user+system) over the load window
	WallNs      float64  `json:"wall_ns"`      // wall time of the load window
	Cores       float64  `json:"cores"`        // cores the service was pinned to
	StealNs     *float64 `json:"steal_ns"`     // /proc/stat steal over the window, summed over those cores
	ThrottledNs *float64 `json:"throttled_ns"` // cgroup cpu.stat throttled_usec over the window, in ns
}

// QuietLimit is the share of the window's core time that may be lost to steal and throttling
// before the runner is called noisy.
const QuietLimit = 0.01

// Quiet is the quiet-runner signal: during the load window the service's cores lost at most
// 1 % of their time to hypervisor steal plus cgroup throttling. known is false when the run
// meta does not carry the fields (then the total-CPU row is reported, not enforced).
func (m RunMeta) Quiet() (quiet, known bool, detail string) {
	if m.StealNs == nil || m.ThrottledNs == nil || m.WallNs <= 0 || m.Cores <= 0 {
		return false, false, "run meta has no steal_ns, throttled_ns, wall_ns and cores"
	}
	lost := (*m.StealNs + *m.ThrottledNs) / (m.WallNs * m.Cores)
	detail = fmt.Sprintf("%.2f%% of core time lost to steal and throttling (quiet is at most %.0f%%)", lost*100, QuietLimit*100)
	return lost <= QuietLimit, true, detail
}

// ReadRunMeta reads a run meta file. A missing file is not an error for the caller to decide:
// ok is false then.
func ReadRunMeta(path string) (m RunMeta, ok bool, err error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, false, nil
	}
	if err != nil {
		return m, false, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, false, inputErr(path, "run meta: %v", err)
	}
	if m.Requests < 0 || m.OSCPUNs < 0 {
		return m, false, inputErr(path, "run meta: requests and os_cpu_ns must not be negative")
	}
	return m, true, nil
}

// FuncRow is one function of a summary. Shares are fractions of all CPU in the profile.
type FuncRow struct {
	Name      string  `json:"name"`
	File      string  `json:"file,omitempty"`
	SelfShare float64 `json:"self"`
	InclShare float64 `json:"inclusive"`
	// OwnShare is the CPU attributed to the function: its own time plus the time of callees
	// outside the service's own code (libraries, runtime) called from it. Only own-code,
	// non-closure functions have it. Compare rows use this share.
	OwnShare float64 `json:"own"`
	OwnN     int64   `json:"own_samples"`
	Own      bool    `json:"-"`
}

// SpanRow is one span: the share of CPU of samples taken inside it (children included).
type SpanRow struct {
	Name  string  `json:"name"`
	Share float64 `json:"share"`
	N     int64   `json:"samples"`
}

// Summary is what `obs profile summarize` prints and what baselines are built from.
type Summary struct {
	Format    string    `json:"format"`
	Samples   int64     `json:"samples"`
	Functions []FuncRow `json:"functions"`
	Spans     []SpanRow `json:"spans"`
	Run       *RunMeta  `json:"run,omitempty"`
	// CPUPerRequestNs is OS CPU per request: Run.OSCPUNs / Run.Requests (0 when unknown).
	CPUPerRequestNs float64 `json:"cpu_per_request_ns"`
}

// Options of Summarize.
type Options struct {
	// Own: regular expressions; a frame is the service's own code when its function name or
	// file matches one. Empty: every frame is own code.
	Own []string
}

type ownMatcher struct{ res []*regexp.Regexp }

func newOwn(patterns []string) (*ownMatcher, error) {
	m := &ownMatcher{}
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("--own %q: %v", p, err)
		}
		m.res = append(m.res, re)
	}
	return m, nil
}

func (m *ownMatcher) match(f Frame) bool {
	if len(m.res) == 0 {
		return true
	}
	for _, re := range m.res {
		if re.MatchString(f.Name) || (f.File != "" && re.MatchString(f.File)) {
			return true
		}
	}
	return false
}

type acc struct {
	self, incl, own float64
	ownN            float64
	file            string
	isOwn           bool
}

// Summarize computes CPU per function (self, inclusive, own), per span and per request.
func Summarize(p *Profile, meta *RunMeta, o Options) (*Summary, error) {
	own, err := newOwn(o.Own)
	if err != nil {
		return nil, err
	}
	var total, totalN float64
	fn := map[string]*acc{}
	spanW := map[string]float64{}
	spanN := map[string]float64{}
	get := func(f Frame) *acc {
		a := fn[f.Name]
		if a == nil {
			a = &acc{file: f.File}
			fn[f.Name] = a
		}
		return a
	}
	for _, s := range p.Samples {
		if len(s.Stack) == 0 {
			continue
		}
		total += s.Weight
		totalN += s.Count
		// Self: the leaf. Inclusive: every distinct function on the stack once (recursion counts once).
		get(s.Stack[0]).self += s.Weight
		seen := map[string]bool{}
		spans := map[string]bool{}
		if s.Span != "" {
			spans[s.Span] = true
		}
		for _, f := range s.Stack {
			if !seen[f.Name] {
				seen[f.Name] = true
				get(f).incl += s.Weight
			}
			if sp, ok := spanOfFrame(f.Name); ok {
				spans[sp] = true
			}
		}
		for sp := range spans {
			spanW[sp] += s.Weight
			spanN[sp] += s.Count
		}
		// Own: the nearest own-code, non-closure, non-span frame going from the leaf to the root.
		for _, f := range s.Stack {
			if _, isSpan := spanOfFrame(f.Name); isSpan || isClosure(f.Name) || !own.match(f) {
				continue
			}
			a := get(f)
			a.own += s.Weight
			a.ownN += s.Count
			a.isOwn = true
			break
		}
	}
	if total <= 0 {
		return nil, fmt.Errorf("the profile has no CPU weight")
	}
	sum := &Summary{Format: p.Format, Samples: int64(totalN + 0.5), Run: meta}
	for name, a := range fn {
		sum.Functions = append(sum.Functions, FuncRow{
			Name: name, File: a.file, SelfShare: a.self / total, InclShare: a.incl / total,
			OwnShare: a.own / total, OwnN: int64(a.ownN + 0.5), Own: a.isOwn,
		})
	}
	sort.Slice(sum.Functions, func(i, j int) bool {
		a, b := sum.Functions[i], sum.Functions[j]
		if a.OwnShare != b.OwnShare {
			return a.OwnShare > b.OwnShare
		}
		if a.SelfShare != b.SelfShare {
			return a.SelfShare > b.SelfShare
		}
		return a.Name < b.Name
	})
	for name, w := range spanW {
		sum.Spans = append(sum.Spans, SpanRow{Name: name, Share: w / total, N: int64(spanN[name] + 0.5)})
	}
	sort.Slice(sum.Spans, func(i, j int) bool {
		if sum.Spans[i].Share != sum.Spans[j].Share {
			return sum.Spans[i].Share > sum.Spans[j].Share
		}
		return sum.Spans[i].Name < sum.Spans[j].Name
	})
	if meta != nil && meta.Requests > 0 && meta.OSCPUNs > 0 {
		sum.CPUPerRequestNs = meta.OSCPUNs / float64(meta.Requests)
	}
	return sum, nil
}

// Print writes the summary as text: top functions, spans, OS CPU per request.
func (s *Summary) Print(w io.Writer, top int) {
	fmt.Fprintf(w, "profile (%s): %d samples\n", s.Format, s.Samples)
	if s.CPUPerRequestNs > 0 {
		fmt.Fprintf(w, "OS CPU per request: %s (%d requests)\n", fmtNs(s.CPUPerRequestNs), s.Run.Requests)
	} else {
		fmt.Fprintln(w, "OS CPU per request: unknown (no run meta with requests and os_cpu_ns)")
	}
	fmt.Fprintf(w, "\n%-8s %-9s %-9s  %s\n", "self", "inclusive", "own", "function")
	for i, f := range s.Functions {
		if top > 0 && i >= top {
			fmt.Fprintf(w, "... %d more functions (use --top 0 for all)\n", len(s.Functions)-top)
			break
		}
		own := "-"
		if f.Own {
			own = pct(f.OwnShare)
		}
		fmt.Fprintf(w, "%-8s %-9s %-9s  %s\n", pct(f.SelfShare), pct(f.InclShare), own, f.Name)
	}
	if len(s.Spans) > 0 {
		fmt.Fprintf(w, "\n%-8s %-8s  %s\n", "share", "samples", "span")
		for _, sp := range s.Spans {
			fmt.Fprintf(w, "%-8s %-8d  %s\n", pct(sp.Share), sp.N, sp.Name)
		}
	} else {
		fmt.Fprintln(w, "\nno spans in this profile (no span_name label, no span__ or UserTag frames)")
	}
}

func pct(x float64) string { return fmt.Sprintf("%.1f%%", x*100) }

func fmtNs(ns float64) string {
	switch {
	case ns >= 1e9:
		return fmt.Sprintf("%.2f s", ns/1e9)
	case ns >= 1e6:
		return fmt.Sprintf("%.3f ms", ns/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.1f us", ns/1e3)
	}
	return fmt.Sprintf("%.0f ns", ns)
}
