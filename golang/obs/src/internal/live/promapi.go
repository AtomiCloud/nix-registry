package live

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"time"
)

// Sample is one value at one time.
type Sample struct {
	T int64 // unix seconds
	V float64
}

// Vec is one series of an instant query.
type Vec struct {
	Labels map[string]string
	V      float64
}

// Mat is one series of a range query.
type Mat struct {
	Labels  map[string]string
	Samples []Sample
}

type promResp struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	ErrorType string `json:"errorType"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func parseSample(p []any) (Sample, bool) {
	if len(p) != 2 {
		return Sample{}, false
	}
	ts, ok := p[0].(float64)
	s, ok2 := p[1].(string)
	if !ok || !ok2 {
		return Sample{}, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) {
		return Sample{}, false
	}
	return Sample{T: int64(ts), V: v}, true
}

func (c *Client) decodeProm(b []byte, ctx string) (*promResp, error) {
	var r promResp
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: not a query API answer (obs prints no part of it)", ctx)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("%s: the store answered status %q: %s %s", ctx, c.storeText(r.Status), c.storeText(r.ErrorType), c.storeText(r.Error))
	}
	return &r, nil
}

// Instant runs a MetricsQL / PromQL instant query at at.
func (c *Client) Instant(ctx context.Context, base, q string, at time.Time) ([]Vec, error) {
	v := url.Values{"query": {q}, "time": {strconv.FormatInt(at.Unix(), 10)}}
	b, err := c.Get(ctx, base, "/api/v1/query", v)
	if err != nil {
		return nil, err
	}
	r, err := c.decodeProm(b, "query "+q)
	if err != nil {
		return nil, err
	}
	var out []Vec
	for _, x := range r.Data.Result {
		s, ok := parseSample(x.Value)
		if ok {
			out = append(out, Vec{Labels: x.Metric, V: s.V})
		}
	}
	return out, nil
}

// Range runs a range query; missing steps are not in Samples (that is how gaps show).
func (c *Client) Range(ctx context.Context, base, q string, start, end time.Time, step time.Duration) ([]Mat, error) {
	v := url.Values{"query": {q}, "start": {strconv.FormatInt(start.Unix(), 10)}, "end": {strconv.FormatInt(end.Unix(), 10)}, "step": {strconv.Itoa(int(step.Seconds())) + "s"}}
	b, err := c.Get(ctx, base, "/api/v1/query_range", v)
	if err != nil {
		return nil, err
	}
	r, err := c.decodeProm(b, "query_range "+q)
	if err != nil {
		return nil, err
	}
	var out []Mat
	for _, x := range r.Data.Result {
		m := Mat{Labels: x.Metric}
		for _, p := range x.Values {
			if s, ok := parseSample(p); ok {
				m.Samples = append(m.Samples, s)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// InstantAt runs an instant query on any store that answers the Prometheus query API shape at
// path (VictoriaMetrics /api/v1/query, VictoriaLogs /select/logsql/stats_query).
func (c *Client) InstantAt(ctx context.Context, base, path, q string, at time.Time) ([]Vec, error) {
	v := url.Values{"query": {q}, "time": {strconv.FormatInt(at.Unix(), 10)}}
	b, err := c.Get(ctx, base, path, v)
	if err != nil {
		return nil, err
	}
	r, err := c.decodeProm(b, "query "+q)
	if err != nil {
		return nil, err
	}
	var out []Vec
	for _, x := range r.Data.Result {
		if s, ok := parseSample(x.Value); ok {
			out = append(out, Vec{Labels: x.Metric, V: s.V})
		}
	}
	return out, nil
}

// TempoRange runs a TraceQL metrics query and returns every sample of every series.
func (c *Client) TempoRange(ctx context.Context, base, q string, start, end time.Time, step time.Duration) ([]float64, error) {
	v := url.Values{"q": {q}, "start": {strconv.FormatInt(start.Unix(), 10)}, "end": {strconv.FormatInt(end.Unix(), 10)}, "step": {strconv.Itoa(int(step.Seconds())) + "s"}}
	b, err := c.Get(ctx, base, "/api/metrics/query_range", v)
	if err != nil {
		return nil, err
	}
	var r struct {
		Series []struct {
			Samples []struct {
				Value *float64 `json:"value"` // null: a step with no value, which is not a zero
			} `json:"samples"`
		} `json:"series"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("tempo metrics query %s: not the answer expected (obs prints no part of it)", q)
	}
	var out []float64
	for _, s := range r.Series {
		for _, x := range s.Samples {
			if x.Value != nil && !math.IsNaN(*x.Value) {
				out = append(out, *x.Value)
			}
		}
	}
	return out, nil
}

// Series lists the series that match selector and had samples between from and to
// (/api/v1/series). It returns the raw answer; assert.ParseSeries reads it.
func (c *Client) Series(ctx context.Context, base, selector string, from, to time.Time) ([]byte, error) {
	v := url.Values{"match[]": {selector}, "start": {strconv.FormatInt(from.Unix(), 10)}, "end": {strconv.FormatInt(to.Unix(), 10)}}
	return c.Get(ctx, base, "/api/v1/series", v)
}
