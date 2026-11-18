package profile

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default tolerances (concept section 11.6).
const (
	TolFunction = 0.05 // functions and spans
	TolSpan     = 0.05
	TolTotal    = 0.15 // total CPU per request
	TolDotnet   = 0.30 // every row while the service is profiled with dotnet-trace
)

// ProfilerDotnetTrace is the profiler name that selects the 30 % tolerance.
const ProfilerDotnetTrace = "dotnet-trace"

// Override raises (or lowers) one limit for one service. Only config files carry them, and
// each one must say why.
type Override struct {
	Service  string  `yaml:"service"`
	Scenario string  `yaml:"scenario"` // empty: every scenario of the service
	Kind     string  `yaml:"kind"`     // function, span or total; empty: every kind
	Name     string  `yaml:"name"`     // function or span name; empty: every row of the kind
	File     string  `yaml:"file"`     // optional function source file (exact match)
	Line     int64   `yaml:"line"`     // optional one-based function declaration line
	Column   int64   `yaml:"column"`   // optional one-based declaration column
	Limit    float64 `yaml:"limit"`    // the tolerance, as a fraction (0.10 = 10 %)
	Why      string  `yaml:"why"`      // required
}

// Tolerances replace the default tolerances above; a zero field keeps the built-in value.
// obs.yaml thresholds.profiles sets them for the whole repo (cmd: Limits.Defaults).
type Tolerances struct {
	Function, Span, Total, DotnetTrace float64
}

// Limits is the config file (default e2e/profile-limits.yaml).
type Limits struct {
	Overrides []Override `yaml:"overrides"`
	// Defaults come from obs.yaml, not from the limits file. A per-service override still wins.
	Defaults Tolerances `yaml:"-"`
}

// LoadLimits reads the limits config. A missing file means no overrides.
func LoadLimits(path string) (*Limits, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Limits{}, nil
	}
	if err != nil {
		return nil, err
	}
	var l Limits
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&l); err != nil && err.Error() != "EOF" {
		return nil, inputErr(path, "%v", err)
	}
	for i, o := range l.Overrides {
		where := fmt.Sprintf("overrides[%d] (service %q)", i, o.Service)
		switch {
		case o.Service == "":
			return nil, inputErr(path, "%s: service is required", where)
		case strings.TrimSpace(o.Why) == "":
			return nil, inputErr(path, "%s: a limit override must say why (the why: field is empty)", where)
		case o.Limit <= 0 || o.Limit > 10:
			return nil, inputErr(path, "%s: limit %v is not a fraction between 0 and 10 (0.10 means 10 %%)", where, o.Limit)
		}
		switch o.Kind {
		case "", "function", "span", "total":
		default:
			return nil, inputErr(path, "%s: kind %q: want function, span or total", where, o.Kind)
		}
		if o.Line < 0 || o.Column < 0 || ((o.File != "" || o.Line != 0 || o.Column != 0) && o.Kind != "function") {
			return nil, inputErr(path, "%s: file/line/column selectors require kind: function and nonnegative locations", where)
		}
	}
	return &l, nil
}

// tolerance picks the tolerance of one row: the most specific override, else the default.
// why is the override's reason (empty for a default).
func (l *Limits) tolerance(b *Baseline, kind, name string) (tol float64, why string) {
	return l.rowTolerance(b, kind, name, "", 0, 0)
}

func (l *Limits) functionTolerance(b *Baseline, r Row) (float64, string) {
	return l.rowTolerance(b, "function", r.Name, r.File, r.Line, r.Column)
}

func (l *Limits) rowTolerance(b *Baseline, kind, name, file string, line, column int64) (tol float64, why string) {
	var d Tolerances
	if l != nil {
		d = l.Defaults
	}
	pick := func(set, builtin float64) float64 {
		if set > 0 {
			return set
		}
		return builtin
	}
	tol = pick(d.Function, TolFunction)
	switch kind {
	case "total":
		tol = pick(d.Total, TolTotal)
	case "span":
		tol = pick(d.Span, TolSpan)
	}
	if b.Profiler == ProfilerDotnetTrace {
		tol, why = pick(d.DotnetTrace, TolDotnet), "profiled with dotnet-trace"
	}
	best := -1
	if l == nil {
		return
	}
	for _, o := range l.Overrides {
		if o.Service != b.Service || (o.Scenario != "" && o.Scenario != b.Scenario) ||
			(o.Kind != "" && o.Kind != kind) || (o.Name != "" && o.Name != name) ||
			(o.File != "" && sourceFile(o.File) != sourceFile(file)) || (o.Line != 0 && o.Line != line) || (o.Column != 0 && o.Column != column) {
			continue
		}
		spec := 0
		if o.Scenario != "" {
			spec++
		}
		if o.Kind != "" {
			spec++
		}
		if o.Name != "" {
			spec++
		}
		if o.File != "" {
			spec++
		}
		if o.Line != 0 {
			spec++
		}
		if o.Column != 0 {
			spec++
		}
		if spec > best {
			best, tol, why = spec, o.Limit, "override: "+o.Why
		}
	}
	return
}
