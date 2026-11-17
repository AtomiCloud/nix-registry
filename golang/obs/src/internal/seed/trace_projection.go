package seed

import "gopkg.in/yaml.v3"

// WithMetrics projects a seed onto retained metric definitions. Validate trace
// definitions against the original metrics before dropping their unused sources;
// retained definitions still generate the same trace and log IDs as the full seed.
// Callers must retain the source metrics of every retained log definition.
func (s Seed) WithMetrics(metrics map[string]MetricDef) (Seed, error) {
	defs, err := s.traceDefs()
	if err != nil {
		return Seed{}, err
	}
	s.Metrics = metrics
	var kept []TraceDef
	for _, td := range defs {
		if _, ok := metrics[td.From.Metric]; ok {
			kept = append(kept, td)
		}
	}
	if len(kept) == 0 {
		s.Traces = yaml.Node{}
	} else if err := s.Traces.Encode(kept); err != nil {
		return Seed{}, err
	}
	return s, nil
}
