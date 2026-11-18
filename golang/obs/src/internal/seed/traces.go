package seed

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// TraceDef makes a server span and optional client child for each source event.
// Attributes retain OTLP scalar types; resource keys retain their dotted names.
type TraceDef struct {
	From       LogSource         `yaml:"from"`
	Server     string            `yaml:"server"`
	Route      string            `yaml:"route"`
	Client     string            `yaml:"client"`
	ErrorWhen  map[string]string `yaml:"error_when"`
	MedianS    float64           `yaml:"median_s"`
	Attributes map[string]any    `yaml:"attributes"`
}

func (s *Seed) traceDefs() ([]TraceDef, error) {
	if s.Traces.Kind == 0 {
		return nil, nil
	}
	b, err := yaml.Marshal(&s.Traces)
	if err != nil {
		return nil, err
	}
	var defs []TraceDef
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err := d.Decode(&defs); err != nil {
		return nil, fmt.Errorf("traces: %w", err)
	}
	for i, td := range defs {
		if _, ok := s.Metrics[td.From.Metric]; !ok {
			return nil, fmt.Errorf("traces[%d].from.metric %q is not in metrics", i, td.From.Metric)
		}
		if td.Server == "" {
			return nil, fmt.Errorf("traces[%d].server is required", i)
		}
		if td.MedianS < 0 || math.IsNaN(td.MedianS) || math.IsInf(td.MedianS, 0) {
			return nil, fmt.Errorf("traces[%d].median_s %g must be finite and positive", i, td.MedianS)
		}
		for k, v := range td.Attributes {
			if _, err := attrValue(v); err != nil {
				return nil, fmt.Errorf("traces[%d].attributes.%s: %w", i, k, err)
			}
		}
	}
	// Different routes may use the same metric. Refuse only definitions that
	// overlap on a seeded series, which would assign two traces to one log event.
	for name, m := range s.Metrics {
		for _, series := range m.Series {
			matched := -1
			for i, td := range defs {
				if td.From.Metric == name && matches(series.Where, td.From.Where) {
					if matched >= 0 {
						return nil, fmt.Errorf("traces[%d] overlaps traces[%d] for metric %q series %v", i, matched, name, series.Where)
					}
					matched = i
				}
			}
		}
	}
	return defs, nil
}

func attrValue(v any) (TAttrVal, error) {
	switch v := v.(type) {
	case string:
		return TStr(v), nil
	case int:
		return TInt(int64(v)), nil
	case uint64:
		if v <= math.MaxInt64 {
			return TInt(int64(v)), nil
		}
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return TFloat(v), nil
		}
	case bool:
		return TBool(v), nil
	}
	return TAttrVal{}, fmt.Errorf("attribute %v must be an OTLP string, int, double or bool", v)
}

// traceShape is everything a trace definition fixes for one seeded series: span
// names, status and attribute maps. Every trace of the series shares the maps
// (nothing mutates a span's maps), so a long seed holds one copy per series, not
// one per span.
type traceShape struct {
	td              TraceDef
	status          int
	service, client string
	attrs, resource map[string]TAttrVal
}

func newTraceShape(td TraceDef, where, res map[string]string) *traceShape {
	sh := &traceShape{td: td, status: 1, service: res["service.name"], attrs: map[string]TAttrVal{}, resource: map[string]TAttrVal{}}
	if len(td.ErrorWhen) > 0 && matches(where, td.ErrorWhen) {
		sh.status = 2
	}
	for k, v := range res {
		if k != "service.name" {
			sh.resource[k] = TStr(v)
		}
	}
	for k, v := range where {
		sh.attrs[k] = TStr(v)
		// Standard semantic-convention HTTP status codes are OTLP ints.
		if k == "http.response.status_code" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				sh.attrs[k] = TInt(n)
			}
		}
	}
	for k, v := range td.Attributes {
		sh.attrs[k], _ = attrValue(v)
	}
	if td.Route != "" {
		sh.attrs["http.route"] = TStr(td.Route)
	}
	if td.Client != "" {
		sh.client = td.Client
		for k, v := range where {
			sh.client = strings.ReplaceAll(sh.client, "{"+k+"}", v)
		}
	}
	return sh
}

func makeTrace(td TraceDef, where, res map[string]string, hi, lo, start uint64, latencyX float64, r *rnd) *TTrace {
	return newTraceShape(td, where, res).trace(hi, lo, start, latencyX, r)
}

// trace makes one trace. latencyX scales span durations inside an incident window
// (the same latency_x that scales a histogram metric); 1 keeps the baseline
// duration bit for bit.
func (sh *traceShape) trace(hi, lo, start uint64, latencyX float64, r *rnd) *TTrace {
	t := &TTrace{}
	binary.BigEndian.PutUint64(t.TraceID[:8], hi)
	binary.BigEndian.PutUint64(t.TraceID[8:], lo)
	median := sh.td.MedianS
	if median == 0 {
		median = 0.08
	}
	dur := max(uint64(median*latencyX*math.Exp(0.5*r.normal())*1e9), 2000)
	server := &TSpan{Name: sh.td.Server, Kind: 2, Status: sh.status, StartNs: start, DurNs: dur, Service: sh.service, Resource: sh.resource, Attrs: sh.attrs}
	binary.BigEndian.PutUint64(server.SpanID[:], r.next())
	t.Spans = []*TSpan{server}
	if sh.client != "" {
		client := &TSpan{ParentID: server.SpanID, Name: sh.client, Kind: 3, Status: sh.status, StartNs: start + 1000, DurNs: dur * 8 / 10, Service: server.Service, Resource: sh.resource, Attrs: sh.attrs}
		binary.BigEndian.PutUint64(client.SpanID[:], r.next())
		t.Spans = append(t.Spans, client)
	}
	return t
}

// TraceDefs returns the seed's checked trace definitions (nil when it has none).
func (s *Seed) TraceDefs() ([]TraceDef, error) { return s.traceDefs() }

// Matches reports whether a series' where-labels hold every pair of where.
func Matches(series, where map[string]string) bool { return matches(series, where) }
