package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
)

// BaselineVersion is the file format version.
const BaselineVersion = 6

// MinFunctionShare is the smallest share of the baseline's CPU a function needs to be a row.
const MinFunctionShare = 0.03

// MinDeclarationShare keeps sampling noise in tiny declarations out of group sizes.
const MinDeclarationShare = MinFunctionShare / 2

// Row of a baseline: a function or a span with its median share and median sample count.
type Row struct {
	Name         string  `json:"name"`
	File         string  `json:"file,omitempty"`
	Line         int64   `json:"line,omitempty"`
	Column       int64   `json:"column,omitempty"`
	Declarations int     `json:"declarations,omitempty"` // qualifying group count, including 1.5%-3% declarations
	SourceOrder  int     `json:"source_order,omitempty"` // ordinal in the full group, sorted by line/column
	Share        float64 `json:"share"`
	N            int64   `json:"n"`
}

// FunctionGroup retains count and location context even when every baseline
// member is below the 3% row cutoff, so hot duplicates cannot bypass identity.
type FunctionGroup struct {
	Name    string        `json:"name"`
	File    string        `json:"file,omitempty"`
	Count   int           `json:"count"`
	Members []Declaration `json:"members,omitempty"`
}

// Declaration is a source location, including cold members used for pairing.
type Declaration struct {
	Line    int64 `json:"line,omitempty"`
	Column  int64 `json:"column,omitempty"`
	Counted bool  `json:"counted,omitempty"`
}

// Baseline is e2e/profile-baseline/<service>.<scenario>.json: median of 3 runs, a few KB,
// deterministic (no times, sorted rows, rounded shares).
type Baseline struct {
	Version            int      `json:"version"`
	Service            string   `json:"service"`
	Scenario           string   `json:"scenario"`
	Format             string   `json:"format"`
	Profiler           string   `json:"profiler,omitempty"` // e.g. dotnet-trace
	Runs               int      `json:"runs"`
	Own                []string `json:"own,omitempty"`
	SamplePeriodNs     float64  `json:"sample_period_ns,omitempty"`
	SourceRoot         string   `json:"source_root,omitempty"`
	SourceRootExplicit bool     `json:"source_root_explicit,omitempty"`
	// Samples is the median number of samples per run; Requests the median requests per run.
	Samples  int64 `json:"samples"`
	Requests int64 `json:"requests,omitempty"`
	// CPUPerRequestNs is the median OS CPU per request (0 when the runs had no run meta).
	CPUPerRequestNs float64         `json:"cpu_per_request_ns,omitempty"`
	Functions       []Row           `json:"functions"`
	FunctionGroups  []FunctionGroup `json:"function_groups,omitempty"`
	Spans           []Row           `json:"spans"`
}

func (b *Baseline) PrintSourceRoot(w io.Writer) {
	printSourceRoot(w, "baseline ", b.SourceRoot, b.SourceRootExplicit)
}

func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }

func median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// BuildBaseline takes the summaries of exactly three runs and keeps, per row, the median.
// A function is a row when its median own share is at least 3 % (own-code functions only,
// closures are never rows); a span is a row when it has samples in at least two of the three runs.
func BuildBaseline(service, scenario, profiler string, own []string, runs []*Summary) (*Baseline, error) {
	if len(runs) != 3 {
		return nil, fmt.Errorf("a baseline is the median of exactly 3 runs; got %d", len(runs))
	}
	if len(own) == 0 {
		return nil, fmt.Errorf("a baseline requires at least one --own expression for the service's own code")
	}
	b := &Baseline{Version: BaselineVersion, Service: service, Scenario: scenario, Format: runs[0].Format,
		Profiler: profiler, Runs: 3, Own: own, Functions: []Row{}, Spans: []Row{}, SamplePeriodNs: runs[0].SamplePeriodNs, SourceRoot: runs[0].SourceRoot, SourceRootExplicit: runs[0].SourceRootExplicit}
	for _, r := range runs {
		if (b.SourceRootExplicit || r.SourceRootExplicit) && (b.SourceRootExplicit != r.SourceRootExplicit || b.SourceRoot != r.SourceRoot) {
			return nil, fmt.Errorf("the 3 runs have different explicit source roots")
		}
		if r.SamplePeriodNs != b.SamplePeriodNs {
			return nil, fmt.Errorf("the 3 runs have different sample periods (%g and %g ns)", b.SamplePeriodNs, r.SamplePeriodNs)
		}
		if r.Format != runs[0].Format {
			return nil, fmt.Errorf("the 3 runs are in different formats (%s and %s)", runs[0].Format, r.Format)
		}
	}
	var samples, requests, cpu []float64
	for _, r := range runs {
		samples = append(samples, float64(r.Samples))
		if r.Run != nil {
			requests = append(requests, float64(r.Run.Requests))
		}
		cpu = append(cpu, r.CPUPerRequestNs)
	}
	b.Samples = int64(median(samples) + 0.5)
	if len(requests) == 3 {
		b.Requests = int64(median(requests) + 0.5)
	}
	if c := median(cpu); c > 0 && cpu[0] > 0 && cpu[1] > 0 && cpu[2] > 0 {
		b.CPUPerRequestNs = math.Round(c)
	}
	// Reserve exact locations, then align remaining members in source order. A count
	// change among duplicates cannot be assigned a trustworthy median.
	declarations := map[functionKey][]FuncRow{}
	var ownRuns []map[functionKey][]FuncRow
	var rawRuns []map[functionKey][]FuncRow
	for _, r := range runs {
		var functions []FuncRow
		for _, f := range r.Functions {
			if f.Own && f.OwnShare > 0 {
				functions = append(functions, f)
			}
		}
		groups := map[functionKey][]FuncRow{}
		raw := map[functionKey][]FuncRow{}
		for k, indices := range groupedFunctions(functions) {
			for _, i := range indices {
				raw[k] = append(raw[k], functions[i])
			}
			if _, ok := declarations[k]; !ok {
				declarations[k] = raw[k]
			}
		}
		for k, indices := range groupedFunctions(functions, MinDeclarationShare) {
			for _, i := range indices {
				groups[k] = append(groups[k], functions[i])
			}
		}
		ownRuns = append(ownRuns, groups)
		rawRuns = append(rawRuns, raw)
	}
	sshare := map[string][]float64{}
	sn := map[string][]float64{}
	for _, r := range runs {
		for _, s := range r.Spans {
			sshare[s.Name] = append(sshare[s.Name], s.Share)
			sn[s.Name] = append(sn[s.Name], float64(s.N))
		}
	}
	pad := func(xs []float64) []float64 {
		for len(xs) < 3 {
			xs = append(xs, 0)
		}
		return xs
	}
	for k, group := range declarations {
		expected := 0
		for _, f := range group {
			if f.OwnShare >= MinDeclarationShare {
				expected++
			}
		}
		var groupCounts []float64
		for _, r := range ownRuns {
			groupCounts = append(groupCounts, float64(len(r[k])))
			if n := len(r[k]); n != expected && (n > 1 || expected > 1) {
				return nil, fmt.Errorf("%s across the baseline runs", ambiguousDuplicate(k, expected, n))
			}
		}
		var members []Declaration
		for _, f := range group {
			members = append(members, Declaration{f.Line, f.Column, f.OwnShare >= MinDeclarationShare})
		}
		var pairings []map[int]int
		for _, r := range rawRuns {
			pairings = append(pairings, pairDeclarations(members, r[k]))
		}
		count := int(median(groupCounts))
		for order, f := range group {
			var shares, counts []float64
			for i, r := range rawRuns {
				var share, count float64
				if ni, ok := pairings[i][order]; ok {
					share, count = r[k][ni].OwnShare, float64(r[k][ni].OwnN)
				}
				shares = append(shares, share)
				counts = append(counts, count)
			}
			m := median(shares)
			if m >= MinFunctionShare {
				row := Row{Name: f.Name, File: f.File, Line: f.Line, Column: f.Column, Share: round6(m), N: int64(median(counts) + .5)}
				if len(group) > 1 {
					row.Declarations = count
					row.SourceOrder = order
				}
				b.Functions = append(b.Functions, row)
			}
		}
		if count > 0 {
			b.FunctionGroups = append(b.FunctionGroups, FunctionGroup{Name: k.name, File: k.file, Count: count, Members: members})
		}
	}
	for name, xs := range sshare {
		if len(xs) < 2 {
			continue
		}
		m := median(pad(xs))
		b.Spans = append(b.Spans, Row{Name: name, Share: round6(m), N: int64(median(pad(sn[name])) + 0.5)})
	}
	sortRows(b.Functions)
	sort.Slice(b.FunctionGroups, func(i, j int) bool {
		a, c := b.FunctionGroups[i], b.FunctionGroups[j]
		if a.Name != c.Name {
			return a.Name < c.Name
		}
		return a.File < c.File
	})
	sortRows(b.Spans)
	return b, nil
}

func sortRows(r []Row) {
	sort.Slice(r, func(i, j int) bool {
		if r[i].Share != r[j].Share {
			return r[i].Share > r[j].Share
		}
		if r[i].Name != r[j].Name {
			return r[i].Name < r[j].Name
		}
		if r[i].File != r[j].File {
			return r[i].File < r[j].File
		}
		if r[i].Line != r[j].Line {
			return r[i].Line < r[j].Line
		}
		return r[i].Column < r[j].Column
	})
}

// Marshal is the committed form: indented, trailing newline.
func (b *Baseline) Marshal() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(b)
	return buf.Bytes()
}

// BaselinePath is e2e/profile-baseline/<service>.<scenario>.json under dir.
func BaselinePath(dir, service, scenario string) string {
	return dir + "/" + service + "." + scenario + ".json"
}

// LoadBaseline reads and checks a baseline file.
func LoadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b Baseline
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return nil, inputErr(path, "baseline: %v", err)
	}
	if b.Version != BaselineVersion {
		return nil, inputErr(path, "baseline version %d: this obs reads version %d; regenerate from 3 profiles with --own and --update", b.Version, BaselineVersion)
	}
	if b.Service == "" || b.Scenario == "" {
		return nil, inputErr(path, "baseline has no service or scenario")
	}
	return &b, nil
}
