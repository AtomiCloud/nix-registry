package tempo

// Tempo's two metrics HTTP endpoints as the Grafana Tempo datasource calls them for
// an alert query (grafana-tempo-datasource v13.3.0 pkg/tempo/traceql_query.go:172-212):
// /api/metrics/query_range and /api/metrics/query. Each request first goes through
// what Tempo 3.1.1's query-frontend handler does to it at the server's time.Now()
// (the end cutoff, validation and step alignment, Frontend below); the caller passes
// that "now". The responses are Tempo's own protobuf messages, so the caller sees
// OTLP-typed series labels exactly as the plugin decodes them. Exemplars are never
// computed (the engine runs with no exemplar budget); the alerts layer refuses range
// queries that would get them.

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/grafana/tempo/pkg/tempopb"
	"github.com/grafana/tempo/pkg/traceql"
	"gopkg.in/yaml.v3"
)

// ErrEndMustBeGreaterThanStart is the frontend's validation error
// (modules/frontend/metrics_query_validation.go:14, metrics_query_handler.go:231-233).
var ErrEndMustBeGreaterThanStart = errors.New("end must be greater than start")

// ErrQueryWindowWithinEndCutoff is the frontend's answer (HTTP 400) when the end
// cutoff removes the whole window (modules/frontend/metrics_query_range_handler.go:29).
var ErrQueryWindowWithinEndCutoff = errors.New("query window falls entirely within query_end_cutoff")

// Frontend is the part of Tempo 3.1.1's query_frontend config that shapes a metrics
// request. DefaultFrontend has Tempo's defaults.
type Frontend struct {
	// QueryEndCutoff is query_frontend.query_end_cutoff: requests never read data
	// newer than now minus this (modules/frontend/config.go:53-54, default :154).
	QueryEndCutoff time.Duration
	// MaxDuration is query_frontend.metrics.max_duration (config.go:130,
	// metrics_query_range_sharder.go:47); 0 turns the check off. The per-tenant
	// override (overrides MaxMetricsDuration) is not modelled.
	MaxDuration time.Duration
	// MaxIntervals is query_frontend.metrics.max_intervals (config.go:89, :152); 0 off.
	MaxIntervals uint64
}

// DefaultFrontend is Tempo 3.1.1's RegisterFlagsAndApplyDefaults
// (modules/frontend/config.go:128-154).
var DefaultFrontend = Frontend{QueryEndCutoff: 30 * time.Second, MaxDuration: 24 * time.Hour, MaxIntervals: 10_000}

// QueryRange answers /api/metrics/query_range with an explicit step (the caller
// applies traceql.DefaultQueryRangeStep when the request has none, as
// pkg/api/http.go:685-698 does) at the query-frontend's time now.
func (e *TempoEngine) QueryRange(fe Frontend, now time.Time, q string, start, end time.Time, step time.Duration) (*tempopb.QueryRangeResponse, error) {
	req, err := fe.rangeRequest(now, q, start, end, step)
	if err != nil {
		return nil, err
	}
	// The sharder aligns the request once more for its jobs
	// (metrics_query_range_sharder.go:111), which only adds a leading bucket ending
	// at req.Start; the combiner is built from the handler's request (:157-165) and
	// drops every sample at or before its start (IntervalMapperQueryRange.isTsValid,
	// pkg/traceql/engine_metrics.go:2083-2085), so running req as it is gives the
	// same answer.
	return e.metricsReq(req)
}

// QueryInstant answers /api/metrics/query the way Tempo 3.1.1's frontend does at
// time now: rewrite to a query_range request with Step = End - Start and instant set,
// pull the end in by the cutoff and validate (modules/frontend/metrics_query_handler.go:139-155),
// run and combine it, then keep each series' first sample (translateQueryRangeToInstant, :250-267).
func (e *TempoEngine) QueryInstant(fe Frontend, now time.Time, q string, start, end time.Time) (*tempopb.QueryInstantResponse, error) {
	req, err := fe.instantRequest(now, q, start, end)
	if err != nil {
		return nil, err
	}
	rr, err := e.metricsReq(req)
	if err != nil {
		return nil, err
	}
	out := &tempopb.QueryInstantResponse{Metrics: rr.Metrics, Status: rr.Status, Message: rr.Message}
	for _, s := range rr.Series {
		if len(s.Samples) == 0 {
			continue
		}
		out.Series = append(out.Series, &tempopb.InstantSeries{Labels: s.Labels, Value: s.Samples[0].Value})
	}
	return out, nil
}

// instantRequest is the instant handler's request rewrite, in Tempo's order
// (metrics_query_handler.go:139-155): Step = End - Start, clampInstantQueryEnd
// (:224-245: subtract the fewest whole seconds that put the end at or before
// now - cutoff, and fail when nothing is left), Step = End - Start again, validate.
func (fe Frontend) instantRequest(now time.Time, q string, start, end time.Time) (*tempopb.QueryRangeRequest, error) {
	req := &tempopb.QueryRangeRequest{Query: q, Start: uint64(start.UnixNano()), End: uint64(end.UnixNano())}
	req.Step = req.End - req.Start
	req.SetInstant(true)
	if !end.After(start) {
		return nil, ErrEndMustBeGreaterThanStart
	}
	if cutoff := now.Add(-fe.QueryEndCutoff); !end.Before(cutoff) {
		reduce := time.Duration(math.Ceil(end.Sub(cutoff).Seconds())) * time.Second
		end = end.Add(-reduce)
		if !end.After(start) {
			return nil, ErrQueryWindowWithinEndCutoff
		}
		req.End = uint64(end.UnixNano())
	}
	req.Step = req.End - req.Start
	if err := fe.validate(req); err != nil {
		return nil, err
	}
	return req, nil
}

// rangeRequest is the range handler's preparation, in Tempo's order
// (metrics_query_range_handler.go:143-160): clamp the end to now - cutoff for
// validation (clampQueryEndForValidation, :199-213), validate, AlignRequest (start
// down to the step and one step earlier, end up to the step), then clamp again and,
// when that moved the end, round it down to the step (clampQueryEnd, :215-232).
func (fe Frontend) rangeRequest(now time.Time, q string, start, end time.Time, step time.Duration) (*tempopb.QueryRangeRequest, error) {
	if step <= 0 {
		return nil, errors.New("step required")
	}
	req := &tempopb.QueryRangeRequest{Query: q, Start: uint64(start.UnixNano()), End: uint64(end.UnixNano()), Step: uint64(step)}
	req.SetInstant(false)
	if req.Start >= req.End {
		return nil, ErrEndMustBeGreaterThanStart
	}
	maxEnd := uint64(now.Add(-fe.QueryEndCutoff).UnixNano())
	clamp := func() bool {
		if req.End > maxEnd {
			req.End = maxEnd
			return true
		}
		return false
	}
	if clamp() && req.Start >= req.End {
		return nil, ErrQueryWindowWithinEndCutoff
	}
	if err := fe.validate(req); err != nil {
		return nil, err
	}
	traceql.AlignRequest(req)
	if clamp() {
		traceql.AlignEndToLeft(req)
	}
	return req, nil
}

// validate is validateQueryRangeReq (modules/frontend/metrics_query_validation.go:18-54).
func (fe Frontend) validate(req *tempopb.QueryRangeRequest) error {
	if req.Start >= req.End {
		return ErrEndMustBeGreaterThanStart
	}
	if fe.MaxDuration < 0 || (fe.MaxDuration > 0 && req.End-req.Start > uint64(fe.MaxDuration)) {
		return fmt.Errorf("metrics query time range exceeds the maximum allowed duration of %s", fe.MaxDuration)
	}
	if fe.MaxIntervals != 0 && (req.Step == 0 || (req.End-req.Start)/req.Step > fe.MaxIntervals) {
		return fmt.Errorf("step of %s is too small, minimum step for given range is %s",
			time.Duration(req.Step).String(), time.Duration((req.End-req.Start)/fe.MaxIntervals).String())
	}
	return nil
}

// ParseFrontend reads the query_frontend settings above from a Tempo config file
// (the YAML Tempo reads with -config.file, cmd/tempo/main.go:190): keys
// query_frontend.query_end_cutoff, query_frontend.metrics.max_duration and
// query_frontend.metrics.max_intervals. A key that is absent keeps Tempo's default.
// Durations are Go durations ("30s") or integer nanoseconds, as Tempo's yaml.v2
// decoder accepts. Other keys are ignored: obs models only these.
func ParseFrontend(b []byte) (Frontend, error) {
	var doc struct {
		QueryFrontend struct {
			QueryEndCutoff any `yaml:"query_end_cutoff"`
			Metrics        struct {
				MaxDuration  any `yaml:"max_duration"`
				MaxIntervals any `yaml:"max_intervals"`
			} `yaml:"metrics"`
		} `yaml:"query_frontend"`
	}
	fe := DefaultFrontend
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fe, err
	}
	dur := func(key string, v any, dst *time.Duration) error {
		switch x := v.(type) {
		case nil:
		case string:
			d, err := time.ParseDuration(x)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*dst = d
		case int:
			*dst = time.Duration(x)
		default:
			return fmt.Errorf("%s: want a duration such as 30s, got %v", key, v)
		}
		return nil
	}
	qf := doc.QueryFrontend
	if err := dur("query_frontend.query_end_cutoff", qf.QueryEndCutoff, &fe.QueryEndCutoff); err != nil {
		return fe, err
	}
	if err := dur("query_frontend.metrics.max_duration", qf.Metrics.MaxDuration, &fe.MaxDuration); err != nil {
		return fe, err
	}
	switch x := qf.Metrics.MaxIntervals.(type) {
	case nil:
	case int:
		if x < 0 {
			return fe, fmt.Errorf("query_frontend.metrics.max_intervals: %d is negative", x)
		}
		fe.MaxIntervals = uint64(x)
	default:
		return fe, fmt.Errorf("query_frontend.metrics.max_intervals: want a whole number, got %v", x)
	}
	return fe, nil
}
