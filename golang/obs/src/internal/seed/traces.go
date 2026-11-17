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
	seen := map[string]bool{}
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
		// One definition per source metric keeps logs linked to one unambiguous request.
		if seen[td.From.Metric] {
			return nil, fmt.Errorf("traces[%d].from.metric %q has multiple definitions", i, td.From.Metric)
		}
		seen[td.From.Metric] = true
		for k, v := range td.Attributes {
			if _, err := attrValue(v); err != nil {
				return nil, fmt.Errorf("traces[%d].attributes.%s: %w", i, k, err)
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

func makeTrace(td TraceDef, where, res map[string]string, hi, lo, start uint64, r *rnd) *TTrace {
	t := &TTrace{}
	binary.BigEndian.PutUint64(t.TraceID[:8], hi)
	binary.BigEndian.PutUint64(t.TraceID[8:], lo)
	median := td.MedianS
	if median == 0 {
		median = 0.08
	}
	dur := max(uint64(median*math.Exp(0.5*r.normal())*1e9), 2000)
	status := 1
	if len(td.ErrorWhen) > 0 && matches(where, td.ErrorWhen) {
		status = 2
	}
	attrs, resource := map[string]TAttrVal{}, map[string]TAttrVal{}
	for k, v := range res {
		if k != "service.name" {
			resource[k] = TStr(v)
		}
	}
	for k, v := range where {
		attrs[k] = TStr(v)
		// Standard semantic-convention HTTP status codes are OTLP ints.
		if k == "http.response.status_code" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				attrs[k] = TInt(n)
			}
		}
	}
	for k, v := range td.Attributes {
		attrs[k], _ = attrValue(v)
	}
	if td.Route != "" {
		attrs["http.route"] = TStr(td.Route)
	}
	server := &TSpan{Name: td.Server, Kind: 2, Status: status, StartNs: start, DurNs: dur, Service: res["service.name"], Resource: resource, Attrs: attrs}
	binary.BigEndian.PutUint64(server.SpanID[:], r.next())
	t.Spans = []*TSpan{server}
	if td.Client != "" {
		name := td.Client
		for k, v := range where {
			name = strings.ReplaceAll(name, "{"+k+"}", v)
		}
		client := &TSpan{ParentID: server.SpanID, Name: name, Kind: 3, Status: status, StartNs: start + 1000, DurNs: dur * 8 / 10, Service: server.Service, Resource: resource, Attrs: attrs}
		binary.BigEndian.PutUint64(client.SpanID[:], r.next())
		t.Spans = append(t.Spans, client)
	}
	return t
}
