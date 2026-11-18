package seed

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Incident changes the seed during Window and declares the expected alert IDs.
type Incident struct {
	Name   string `yaml:"-"`
	Story  string `yaml:"story"`
	Window struct {
		Start    string `yaml:"start"`
		Duration string `yaml:"duration"`
	} `yaml:"window"`
	Change []Change `yaml:"change"`
	Expect struct {
		Fire       []string `yaml:"fire"`
		Quiet      []string `yaml:"quiet"`
		FireWithin string   `yaml:"fire_within"`
		// ResolveWithin allows rollups and keepFiringFor to drain after the incident.
		ResolveWithin string `yaml:"resolve_within"`
	} `yaml:"expect"`
}

type Change struct {
	Metric   string            `yaml:"metric"`
	Where    map[string]string `yaml:"where"`
	Rate     string            `yaml:"rate"`
	LatencyX float64           `yaml:"latency_x"`
	ShareX   *float64          `yaml:"share_x"`
}

func (i *Incident) Contains(at time.Duration) bool {
	start, _ := time.ParseDuration(i.Window.Start)
	duration, _ := time.ParseDuration(i.Window.Duration)
	return at >= start && at < start+duration
}

// LoadIncidents reads only the seed's named incident files, with strict fields.
func LoadIncidents(s *Seed, dir string) ([]Incident, error) {
	var out []Incident
	seen := map[string]bool{}
	for _, name := range s.Incidents {
		if name == "" || name == "." || strings.ContainsAny(name, "/\\") || seen[name] || name == "baseline" {
			return nil, fmt.Errorf("seed.incidents: invalid or duplicate name %q", name)
		}
		seen[name] = true
		path := filepath.Join(dir, "incidents", name+".yaml")
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var i Incident
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&i); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		i.Name = name
		start, err := time.ParseDuration(i.Window.Start)
		if err != nil || start < 0 {
			return nil, fmt.Errorf("%s: window.start %q is invalid", path, i.Window.Start)
		}
		duration, err := time.ParseDuration(i.Window.Duration)
		if err != nil || duration <= 0 || start+duration > s.Duration() {
			return nil, fmt.Errorf("%s: window.duration %q exceeds seed length %s or is invalid", path, i.Window.Duration, s.Length)
		}
		for field, value := range map[string]string{"fire_within": i.Expect.FireWithin, "resolve_within": i.Expect.ResolveWithin} {
			if value == "" {
				continue
			}
			d, err := time.ParseDuration(value)
			if err != nil || d < 0 {
				return nil, fmt.Errorf("%s: expect.%s %q is invalid", path, field, value)
			}
		}
		for _, c := range i.Change {
			def, ok := s.Metrics[c.Metric]
			if !ok {
				return nil, fmt.Errorf("%s: change.metric %q not in seed", path, c.Metric)
			}
			matched := false
			for _, sr := range def.Series {
				matched = matched || matches(sr.Where, c.Where)
			}
			if !matched {
				return nil, fmt.Errorf("%s: change.metric %s where=%v matches no seed series", path, c.Metric, c.Where)
			}
			if c.Rate != "" {
				if r, err := ParseRate(c.Rate); err != nil || r < 0 || math.IsNaN(r) || math.IsInf(r, 0) {
					return nil, fmt.Errorf("%s: change.rate %q is invalid", path, c.Rate)
				}
			}
			if c.LatencyX < 0 || math.IsNaN(c.LatencyX) || math.IsInf(c.LatencyX, 0) || (c.ShareX != nil && (*c.ShareX < 0 || math.IsNaN(*c.ShareX) || math.IsInf(*c.ShareX, 0))) {
				return nil, fmt.Errorf("%s: change %s has invalid latency_x or share_x", path, c.Metric)
			}
		}
		if len(i.Expect.Fire)+len(i.Expect.Quiet) == 0 {
			return nil, fmt.Errorf("%s: expect needs fire or quiet alert IDs", path)
		}
		out = append(out, i)
	}
	return out, nil
}
