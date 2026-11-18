// Package seed reads the seed file and makes the data a store would hold: metric
// samples as the OTLP -> VictoriaMetrics path stores them and log rows as
// VictoriaLogs stores them from OTLP (concept section 11.2).
package seed

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Seed is the seed file. `incidents` (read by `obs alerts`) and `traces` (read by `obs query --live`
// and `obs links`) are not used by metric and log seeding; they are kept here as raw nodes.
type Seed struct {
	Length     string            `yaml:"length"`
	RandomSeed uint64            `yaml:"random_seed"`
	Resource   map[string]string `yaml:"resource"`
	// StreamFields names extra resource attributes that are log stream fields on this
	// platform (the bounded where-from set always is).
	StreamFields []string             `yaml:"stream_fields"`
	Traffic      Traffic              `yaml:"traffic"`
	Metrics      map[string]MetricDef `yaml:"metrics"`
	Logs         []LogDef             `yaml:"logs"`
	Traces       yaml.Node            `yaml:"traces"`
	Incidents    []string             `yaml:"incidents"`
}

type Traffic struct {
	Requests string `yaml:"requests"` // "3/s"
	Shape    string `yaml:"shape"`    // flat | daily_wave
}

type MetricDef struct {
	Type    string      `yaml:"type"` // counter | histogram
	Unit    string      `yaml:"unit"` // s for seconds histograms
	Buckets []float64   `yaml:"buckets"`
	Latency *LatencyDef `yaml:"latency"`
	Series  []SeriesDef `yaml:"series"`
}

type LatencyDef struct {
	Median float64 `yaml:"median"`
	Sigma  float64 `yaml:"sigma"`
}

type SeriesDef struct {
	Where map[string]string `yaml:"where"`
	Share float64           `yaml:"share"`
}

type LogDef struct {
	Event string    `yaml:"event"`
	Level string    `yaml:"level"`
	Line  string    `yaml:"line"`
	From  LogSource `yaml:"from"`
}

type LogSource struct {
	Metric string            `yaml:"metric"`
	Where  map[string]string `yaml:"where"`
}

// Load reads and checks a seed file. Unknown keys are an error.
func Load(path string) (*Seed, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Seed
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func (s *Seed) validate() error {
	if _, err := time.ParseDuration(s.Length); err != nil || s.Length == "" {
		return fmt.Errorf("length %q: not a duration like 4h", s.Length)
	}
	if _, err := ParseRate(s.Traffic.Requests); err != nil {
		return fmt.Errorf("traffic.requests: %w", err)
	}
	for name, m := range s.Metrics {
		switch m.Type {
		case "", "counter":
		case "histogram":
			if len(m.Buckets) == 0 {
				return fmt.Errorf("metrics.%s: a histogram needs buckets", name)
			}
		default:
			return fmt.Errorf("metrics.%s.type %q: not one of [counter, histogram]", name, m.Type)
		}
	}
	for i, l := range s.Logs {
		if l.Event == "" {
			return fmt.Errorf("logs[%d]: event is required", i)
		}
		if _, ok := s.Metrics[l.From.Metric]; !ok {
			return fmt.Errorf("logs[%d] (%s): from.metric %q is not in metrics", i, l.Event, l.From.Metric)
		}
	}
	return nil
}

// Duration is the seed length.
func (s *Seed) Duration() time.Duration {
	d, _ := time.ParseDuration(s.Length)
	return d
}

// ParseRate reads "3/s", "30/m", "100/h" or a bare number (per second).
func ParseRate(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("a rate like 3/s is required")
	}
	num, unit, ok := strings.Cut(s, "/")
	v, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil {
		return 0, fmt.Errorf("bad rate %q", s)
	}
	if !ok {
		return v, nil
	}
	switch strings.TrimSpace(unit) {
	case "s":
		return v, nil
	case "m", "min":
		return v / 60, nil
	case "h":
		return v / 3600, nil
	}
	return 0, fmt.Errorf("bad rate unit in %q", s)
}
