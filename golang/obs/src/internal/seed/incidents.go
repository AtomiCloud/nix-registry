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
	Name  string `yaml:"-"`
	Story string `yaml:"story"`
	// Disturbance marks a deliberate non-incident disturbance (only quiet expectations: it proves the
	// alerts stay quiet). The reason is required. `obs test` leaves it out of [incident-uncaught] and
	// prints it as a note; `obs alerts` ignores it.
	Disturbance string `yaml:"disturbance"`
	Window      struct {
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

// LoadIncidents reads only the seed's named incident files, with strict fields. An incident must
// expect at least one alert to fire or stay quiet (what `obs alerts` demands).
func LoadIncidents(s *Seed, dir string) ([]Incident, error) { return loadIncidents(s, dir, false) }

// LoadIncidentsForTest is LoadIncidents for `obs test`: an incident with an empty expectation
// (`expect: {}`) loads, so the rule "every incident is caught by an alert" can report it as
// [incident-uncaught] instead of the run stopping with an input error.
func LoadIncidentsForTest(s *Seed, dir string) ([]Incident, error) {
	return loadIncidents(s, dir, true)
}

func loadIncidents(s *Seed, dir string, allowEmptyExpect bool) ([]Incident, error) {
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
		if err := checkDisturbance(path, b, i.Expect.Fire); err != nil {
			return nil, err
		}
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
		if !allowEmptyExpect && len(i.Expect.Fire)+len(i.Expect.Quiet) == 0 {
			return nil, fmt.Errorf("%s: expect needs fire or quiet alert IDs", path)
		}
		out = append(out, i)
	}
	return out, nil
}

// checkDisturbance validates the DECODED `disturbance` value (after YAML merges `<<:`), so a merged empty
// reason fails like a direct one. An absent key is fine. A present key needs a non-blank reason, and only a
// quiet-only incident may carry it (an incident with expect.fire is an error). Errors name file:line of the key
// (of the `<<` key when the value comes from a merge).
func checkDisturbance(path string, doc []byte, fire []string) error {
	var decoded map[string]any
	if err := yaml.Unmarshal(doc, &decoded); err != nil {
		return nil // the strict decode above reports the real error
	}
	val, present := decoded["disturbance"]
	if !present {
		return nil
	}
	line := disturbanceLine(doc)
	if s, _ := val.(string); strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s:%d: disturbance needs a non-empty reason (what the incident proves the alerts ignore); remove the key or write the reason", path, line)
	}
	if len(fire) > 0 {
		return fmt.Errorf("%s:%d: disturbance is only for incidents with no fire expectation (expect.fire lists %s); remove the disturbance key, or move the fire expectations to a separate incident", path, line, strings.Join(fire, ", "))
	}
	return nil
}

// disturbanceLine is the line of the top-level `disturbance` key, else of the `<<` merge key that supplies it.
func disturbanceLine(doc []byte) int {
	var root yaml.Node
	if err := yaml.Unmarshal(doc, &root); err != nil || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return 1
	}
	m := root.Content[0]
	merge := 0
	for k := 0; k+1 < len(m.Content); k += 2 {
		switch m.Content[k].Value {
		case "disturbance":
			return m.Content[k].Line
		case "<<":
			if merge == 0 {
				merge = m.Content[k].Line
			}
		}
	}
	if merge > 0 {
		return merge
	}
	return 1
}
