package seed

import (
	"strconv"

	"gopkg.in/yaml.v3"
)

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
			// yaml.v3 emits integral float64 map values as integer-looking scalars.
			// An explicit tag preserves the OTLP double kind through this round trip.
			for name, value := range td.Attributes {
				if f, ok := value.(float64); ok {
					td.Attributes[name] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(f, 'g', -1, 64), Style: yaml.TaggedStyle}
				}
			}
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
