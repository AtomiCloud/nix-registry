package tempo

// Tempo's two metrics HTTP endpoints as the Grafana Tempo datasource calls them for
// an alert query (grafana-tempo-datasource v13.3.0 pkg/tempo/traceql_query.go:172-212):
// /api/metrics/query_range and /api/metrics/query. The responses are Tempo's own
// protobuf messages, so the caller sees OTLP-typed series labels exactly as the
// plugin decodes them. Exemplars are never computed (the engine runs with no
// exemplar budget); the alerts layer refuses range queries that would get them.

import (
	"errors"
	"time"

	"github.com/grafana/tempo/pkg/tempopb"
)

// ErrEndMustBeGreaterThanStart is the frontend's validation error
// (modules/frontend/metrics_query_validation.go:19-21, metrics_query_handler.go:231-233).
var ErrEndMustBeGreaterThanStart = errors.New("invalid start and end time: end must be greater than start")

// QueryRange answers /api/metrics/query_range with an explicit step (the caller
// applies traceql.DefaultQueryRangeStep when the request has none, as
// pkg/api/http.go:685-698 does).
func (e *TempoEngine) QueryRange(q string, start, end time.Time, step time.Duration) (*tempopb.QueryRangeResponse, error) {
	if !end.After(start) {
		return nil, ErrEndMustBeGreaterThanStart
	}
	return e.metricsRaw(q, start, end, step)
}

// QueryInstant answers /api/metrics/query the way Tempo 3.1.1's frontend does:
// rewrite to a query_range request with Step = End - Start and instant set
// (modules/frontend/metrics_query_handler.go:139-151), run and combine it, then
// keep each series' first sample (translateQueryRangeToInstant, :250-267).
func (e *TempoEngine) QueryInstant(q string, start, end time.Time) (*tempopb.QueryInstantResponse, error) {
	if !end.After(start) {
		return nil, ErrEndMustBeGreaterThanStart
	}
	req := &tempopb.QueryRangeRequest{Query: q, Start: uint64(start.UnixNano()), End: uint64(end.UnixNano())}
	req.Step = req.End - req.Start
	req.SetInstant(true)
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
