// Package contract reads and checks observability/contract.yaml (concept section 3):
// bounded label types, one budget per metric, metric kinds, log events, span attributes.
//
// Load reads the file (unknown keys fail) and Validate applies the rules of section 11.4
// and the schema rules; the code generators (internal/gen) only ever see a contract that
// Validate accepted.
package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// Type kinds of a label (concept section 3.1).
const (
	KindEnum = "enum" // listed values
	KindInt  = "int"  // bounded number min..max
	KindTime = "time" // time bucket: hour_of_day (0..23) or weekday (0..6)
	KindDate = "date" // calendar date; a metric label only, with window_days
	KindFree = "free" // any value; log fields and span attributes only
	// KindEstimate is a size range somebody else decides (pods in a cluster): min, max and churn_per_day.
	// Allowed only on a source: metric; check counts its max.
	KindEstimate = "estimate"
)

// Metric kinds (concept section 4).
const (
	MetricCounter   = "counter"
	MetricHistogram = "histogram"
	MetricGauge     = "gauge"
)

// Time units of a time-bucket type.
const (
	UnitHourOfDay = "hour_of_day"
	UnitWeekday   = "weekday"
)

// Log levels an event may fix.
var Levels = []string{"trace", "debug", "info", "warn", "error", "fatal"}

// Span kinds.
var SpanKinds = []string{"internal", "client", "server", "producer", "consumer"}

// DefaultPodKeys is the platform's pod key list (concept section 4.1).
var DefaultPodKeys = []string{"k8s.pod.name", "k8s.pod.uid", "k8s.node.name", "service.instance.id"}

// DefaultInterval is the aggregation interval (concept section 4.1, owner ruling D157).
const DefaultInterval = "30s"

// DefaultBuckets is the bucket count (with +Inf) of a histogram that does not say:
// a 12-bucket histogram stores 14 series per label set (12 buckets + _sum + _count).
const DefaultBuckets = 12

// Type is one named label type.
type Type struct {
	Kind       string   `yaml:"kind"`
	Values     []string `yaml:"values,omitempty"`      // enum
	Min        *int     `yaml:"min,omitempty"`         // int
	Max        *int     `yaml:"max,omitempty"`         // int
	Unit       string   `yaml:"unit,omitempty"`        // time
	WindowDays int      `yaml:"window_days,omitempty"` // date
	// ChurnPerDay is the estimate's new values per day (a pod that is replaced is a new value).
	ChurnPerDay *int `yaml:"churn_per_day,omitempty"` // estimate
}

// Size is the number of values the type can take (0 for free).
func (t Type) Size() int {
	switch t.Kind {
	case KindEnum:
		return len(t.Values)
	case KindInt:
		if t.Min == nil || t.Max == nil || *t.Max < *t.Min {
			return 0
		}
		// max-min can overflow int: take the difference as unsigned and saturate at MaxWorst.
		d := uint64(int64(*t.Max)) - uint64(int64(*t.Min))
		if d >= uint64(MaxWorst) {
			return int(MaxWorst)
		}
		return int(d) + 1
	case KindTime:
		if t.Unit == UnitWeekday {
			return 7
		}
		return 24
	case KindDate:
		return t.WindowDays
	case KindEstimate:
		if t.Max == nil || *t.Max < 0 {
			return 0
		}
		return *t.Max
	}
	return 0
}

// Metric is one metric of the contract.
type Metric struct {
	// Source names who makes the metric when it is not ours (kube-state-metrics, cadvisor).
	// obs gen makes no types for it; its name is already the stored name.
	Source string `yaml:"source,omitempty"`
	// UsedBy says who reads the metric (box/alert/pod-crashloop, box/dash/kube-state-metrics).
	UsedBy    []string `yaml:"used_by,omitempty"`
	Kind      string   `yaml:"kind"`
	Unit      string   `yaml:"unit,omitempty"`    // seconds, bytes: the stored name suffix
	Budget    int      `yaml:"budget,omitempty"`  // max stored series per landscape
	Buckets   int      `yaml:"buckets,omitempty"` // histogram: buckets with +Inf (default 12)
	AcrossPod string   `yaml:"across_pods,omitempty"`
	PerPod    bool     `yaml:"per_pod,omitempty"`
	// RawLabels is the labels: block as written: a type name, or { follows: <label> }.
	RawLabels map[string]LabelSpec `yaml:"labels,omitempty"`
	// Labels is label name -> type name (empty for a label that follows another); filled from RawLabels.
	Labels map[string]string `yaml:"-"`
	// Follows is label name -> the label it follows: it adds no series of its own.
	Follows map[string]string `yaml:"-"`
	// LabelOrder keeps the labels in file order, so generated code is stable.
	LabelOrder []string `yaml:"-"`
	// Line is the metric's line in the contract file, LabelLine each label's (0 when built in code).
	Line      int            `yaml:"-"`
	LabelLine map[string]int `yaml:"-"`
}

// LabelSpec is one entry of a metric's labels: block: a type name, or { follows: <label> } for a
// label that always changes together with another label of the same metric (a pod's uid follows
// the pod) and so adds no series.
type LabelSpec struct {
	Type    string
	Follows string
}

// UnmarshalYAML reads `pod: pod` and `uid: { follows: pod }`.
func (l *LabelSpec) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		l.Type = n.Value
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value != "follows" {
				return fmt.Errorf("line %d: field %s not found in a label (a label is a type name or { follows: <label> })", k.Line, k.Value)
			}
			l.Follows = v.Value
		}
		if l.Follows == "" {
			return fmt.Errorf("line %d: a label map needs follows: <label>", n.Line)
		}
		return nil
	}
	return fmt.Errorf("line %d: a label is a type name or { follows: <label> }", n.Line)
}

// IsSource is true for an entry somebody else makes.
func (m Metric) IsSource() bool { return m.Source != "" }

// Event is one log event: a name and the level it fixes. Fields are free-form (concept 6).
type Event struct {
	Event string `yaml:"event"`
	Level string `yaml:"level"`
	// StreamLabels would make a VictoriaLogs stream field; concept 3.3 allows only the where-from set,
	// so this exists to refuse free / date values with a clear failure (concept 11.4).
	StreamLabels map[string]string `yaml:"stream_labels,omitempty"`
}

// Span is one span of the contract.
type Span struct {
	Kind       string            `yaml:"kind,omitempty"`       // default internal
	Attributes map[string]string `yaml:"attributes,omitempty"` // name -> type name
	AttrOrder  []string          `yaml:"-"`
}

// Target is one generated output: a language, where it goes, and its options.
type Target struct {
	Lang    string `yaml:"lang"`
	Out     string `yaml:"out"`               // folder, relative to the repo root (the folder holding observability/)
	Package string `yaml:"package,omitempty"` // go package / dart library
	Module  string `yaml:"namespace,omitempty"`
	// Interfaces is the import path (go), package (dart, bun) or namespace (dotnet) of the hand-written interfaces library the generated code builds on; empty means the published default.
	Interfaces string `yaml:"interfaces,omitempty"`
}

// Contract is the parsed contract.yaml.
type Contract struct {
	Service  string            `yaml:"service"`
	Modules  []string          `yaml:"modules,omitempty"`
	PodKeys  []string          `yaml:"pod_keys,omitempty"`
	Interval string            `yaml:"interval,omitempty"`
	Types    map[string]Type   `yaml:"types,omitempty"`
	Metrics  map[string]Metric `yaml:"metrics,omitempty"`
	Logs     map[string]Event  `yaml:"logs,omitempty"`
	Spans    map[string]Span   `yaml:"spans,omitempty"`
	// Platform lists metrics the platform makes itself (OTel auto instrumentation: HTTP server
	// histograms and the like) with the labels they carry, so queries may read them.
	Platform map[string][]string `yaml:"platform_metrics,omitempty"`
	Generate []Target            `yaml:"generate,omitempty"`

	// Hash is the sha256 (hex) of the contract file's bytes; every generated header carries it.
	Hash string `yaml:"-"`
	// File is the path the contract was read from.
	File string `yaml:"-"`

	TypeOrder []string `yaml:"-"`
	// TypeLine is each type's line in the contract file (absent for a type built in code).
	TypeLine    map[string]int `yaml:"-"`
	MetricOrder []string       `yaml:"-"`
	LogOrder    []string       `yaml:"-"`
	SpanOrder   []string       `yaml:"-"`
}

// Load reads and parses a contract file. It does not validate: call Validate.
func Load(path string) (*Contract, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, b)
}

// Parse parses contract bytes; name is only used in errors.
func Parse(name string, b []byte) (*Contract, error) {
	c := &Contract{File: name}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	sum := sha256.Sum256(b)
	c.Hash = hex.EncodeToString(sum[:])
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err == nil {
		c.fillOrder(&root)
	}
	c.normaliseLabels()
	c.fillMissingOrder()
	c.addIntrinsicFree()
	return c, nil
}

// addIntrinsicFree makes `free` usable as a type name without declaring it, as the concept's own
// example writes it (`attributes: { user_id: free }`): a use of an undeclared `free` declares a type
// named free of kind free (a plain string in every language).
func (c *Contract) addIntrinsicFree() {
	if _, declared := c.Types["free"]; declared {
		return
	}
	used := false
	for _, m := range c.Metrics {
		for _, t := range m.Labels {
			used = used || t == "free"
		}
	}
	for _, s := range c.Spans {
		for _, t := range s.Attributes {
			used = used || t == "free"
		}
	}
	for _, e := range c.Logs {
		for _, t := range e.StreamLabels {
			used = used || t == "free"
		}
	}
	if !used {
		return
	}
	if c.Types == nil {
		c.Types = map[string]Type{}
	}
	c.Types["free"] = Type{Kind: KindFree}
	c.TypeOrder = append(c.TypeOrder, "free")
}

// fillOrder records the file order of every map, so generated code is stable and readable.
func (c *Contract) fillOrder(root *yaml.Node) {
	if len(root.Content) == 0 {
		return
	}
	m := root.Content[0]
	if m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, val := m.Content[i].Value, m.Content[i+1]
		switch key {
		case "types":
			c.TypeOrder = keys(val)
			c.TypeLine = map[string]int{}
			for j := 0; j+1 < len(val.Content); j += 2 {
				c.TypeLine[val.Content[j].Value] = val.Content[j].Line
			}
		case "metrics":
			c.MetricOrder = keys(val)
			for j := 0; j+1 < len(val.Content); j += 2 {
				name := val.Content[j].Value
				mt := c.Metrics[name]
				mt.LabelOrder = subKeys(val.Content[j+1], "labels")
				mt.Line = val.Content[j].Line
				mt.LabelLine = subKeyLines(val.Content[j+1], "labels")
				c.Metrics[name] = mt
			}
		case "logs":
			c.LogOrder = keys(val)
		case "spans":
			c.SpanOrder = keys(val)
			for j := 0; j+1 < len(val.Content); j += 2 {
				name := val.Content[j].Value
				sp := c.Spans[name]
				sp.AttrOrder = subKeys(val.Content[j+1], "attributes")
				c.Spans[name] = sp
			}
		}
	}
}

func keys(n *yaml.Node) []string {
	var out []string
	if n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, n.Content[i].Value)
	}
	return out
}

// normaliseLabels fills Labels and Follows from the labels: block as written.
func (c *Contract) normaliseLabels() {
	for n, m := range c.Metrics {
		if len(m.RawLabels) == 0 {
			continue
		}
		m.Labels = map[string]string{}
		for l, spec := range m.RawLabels {
			if spec.Follows != "" {
				if m.Follows == nil {
					m.Follows = map[string]string{}
				}
				m.Follows[l] = spec.Follows
				m.Labels[l] = ""
				continue
			}
			m.Labels[l] = spec.Type
		}
		c.Metrics[n] = m
	}
}

func subKeyLines(n *yaml.Node, key string) map[string]int {
	out := map[string]int{}
	if n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != key || n.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		m := n.Content[i+1]
		for j := 0; j+1 < len(m.Content); j += 2 {
			out[m.Content[j].Value] = m.Content[j].Line
		}
	}
	return out
}

func subKeys(n *yaml.Node, key string) []string {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return keys(n.Content[i+1])
		}
	}
	return nil
}

// fillMissingOrder makes the order lists total (a contract built in code has none).
func (c *Contract) fillMissingOrder() {
	c.TypeOrder = complete(c.TypeOrder, mapKeys(c.Types))
	c.MetricOrder = complete(c.MetricOrder, mapKeys(c.Metrics))
	c.LogOrder = complete(c.LogOrder, mapKeys(c.Logs))
	c.SpanOrder = complete(c.SpanOrder, mapKeys(c.Spans))
	for n, m := range c.Metrics {
		m.LabelOrder = complete(m.LabelOrder, mapKeys(m.Labels))
		c.Metrics[n] = m
	}
	for n, s := range c.Spans {
		s.AttrOrder = complete(s.AttrOrder, mapKeys(s.Attributes))
		c.Spans[n] = s
	}
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// complete keeps order, drops names not in all, and appends the missing ones sorted.
func complete(order, all []string) []string {
	have := map[string]bool{}
	for _, a := range all {
		have[a] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, o := range order {
		if have[o] && !seen[o] {
			out = append(out, o)
			seen[o] = true
		}
	}
	for _, a := range all {
		if !seen[a] {
			out = append(out, a)
		}
	}
	return out
}

// EffectivePodKeys is pod_keys, or the platform default.
func (c *Contract) EffectivePodKeys() []string {
	if len(c.PodKeys) > 0 {
		return c.PodKeys
	}
	return DefaultPodKeys
}

// EffectiveInterval is interval, or the ruled default of 30 s.
func (c *Contract) EffectiveInterval() string {
	if c.Interval != "" {
		return c.Interval
	}
	return DefaultInterval
}

// EventNames returns the log event names in contract order.
func (c *Contract) EventNames() []string {
	out := make([]string, 0, len(c.LogOrder))
	for _, k := range c.LogOrder {
		out = append(out, c.Logs[k].Event)
	}
	return out
}

// LoadValid loads a contract and fails when it has any validation issue (for generators' tests).
func LoadValid(path string) (*Contract, error) {
	c, err := Load(path)
	if err != nil {
		return nil, err
	}
	if is := c.Validate(); len(is) > 0 {
		return nil, fmt.Errorf("%s: %s", path, is[0])
	}
	return c, nil
}
