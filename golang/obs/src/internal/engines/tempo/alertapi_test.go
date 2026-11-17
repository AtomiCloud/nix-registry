package tempo

import (
	"errors"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/seed"
	v1 "github.com/grafana/tempo/pkg/tempopb/common/v1"
)

// alertEngine: one server span per second for 2 minutes from at, every third one an
// error with status_code 502 (an OTLP int), plus one span an hour later.
func alertEngine() (*TempoEngine, time.Time) {
	at := time.Unix(1800000000, 0)
	var traces []*TTrace
	add := func(i int, start time.Time, code int64) {
		s := &TSpan{SpanID: [8]byte{byte(i), byte(i >> 8), 1}, Name: "POST /charge", Service: "checkout", Kind: 2, Status: 1,
			StartNs: uint64(start.UnixNano()), DurNs: uint64(100 * time.Millisecond), Attrs: map[string]TAttrVal{"code": seed.TInt(code)}}
		if code == 502 {
			s.Status = 2
		}
		traces = append(traces, &TTrace{TraceID: [16]byte{byte(i), byte(i >> 8), 7}, Spans: []*TSpan{s}})
	}
	for i := 0; i < 120; i++ {
		code := int64(200)
		if i%3 == 0 {
			code = 502
		}
		add(i, at.Add(time.Duration(i)*time.Second+500*time.Millisecond), code)
	}
	add(500, at.Add(time.Hour), 502)
	return NewTempoEngine(traces), at
}

// later is a frontend now far after every window these tests read: no cutoff applies.
var later = time.Unix(1800000000, 0).Add(48 * time.Hour)

func TestQueryInstantCountsTheWholeWindow(t *testing.T) {
	e, at := alertEngine()
	resp, err := e.QueryInstant(DefaultFrontend, later, `{ status = error } | count_over_time() by (span.code)`, at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Series) != 1 || resp.Series[0].Value != 20 {
		t.Fatalf("instant: want one series of 20 error spans in the first minute, got %+v", resp.Series)
	}
	l := resp.Series[0].Labels
	if len(l) != 1 || l[0].Key != "span.code" {
		t.Fatalf("labels: %+v", l)
	}
	if v, ok := l[0].Value.Value.(*v1.AnyValue_IntValue); !ok || v.IntValue != 502 {
		t.Fatalf("label keeps the OTLP int type: %+v", l[0].Value)
	}
	// rate() over the instant window is spans per second of the whole window.
	resp, err = e.QueryInstant(DefaultFrontend, later, `{ } | rate()`, at, at.Add(2*time.Minute))
	if err != nil || len(resp.Series) != 1 || resp.Series[0].Value != 1 {
		t.Fatalf("rate: %+v %v", resp, err)
	}
	// Nothing in the window: no series (Grafana sees no frames, i.e. NoData).
	resp, err = e.QueryInstant(DefaultFrontend, later, `{ status = error } | rate()`, at.Add(-time.Hour), at.Add(-time.Minute))
	if err != nil || len(resp.Series) != 0 {
		t.Fatalf("empty window: %+v %v", resp, err)
	}
	if _, err := e.QueryInstant(DefaultFrontend, later, `{} | rate()`, at, at); !errors.Is(err, ErrEndMustBeGreaterThanStart) {
		t.Fatalf("empty window must be Tempo's validation error: %v", err)
	}
	if _, err := e.QueryRange(DefaultFrontend, later, `{} | rate()`, at, at, time.Second); !errors.Is(err, ErrEndMustBeGreaterThanStart) {
		t.Fatalf("empty range must be Tempo's validation error: %v", err)
	}
	if _, err := e.QueryInstant(DefaultFrontend, later, `{} | no_such_fn()`, at, at.Add(time.Minute)); err == nil {
		t.Fatal("invalid query passed")
	}
}

func TestQueryRangeBucketsSumToInstant(t *testing.T) {
	e, at := alertEngine()
	q := `{ status = error } | count_over_time()`
	inst, err := e.QueryInstant(DefaultFrontend, later, q, at, at.Add(2*time.Minute))
	if err != nil || len(inst.Series) != 1 {
		t.Fatalf("instant: %+v %v", inst, err)
	}
	rr, err := e.QueryRange(DefaultFrontend, later, q, at, at.Add(2*time.Minute), time.Minute)
	if err != nil || len(rr.Series) != 1 {
		t.Fatalf("range: %+v %v", rr, err)
	}
	sum := 0.0
	for _, s := range rr.Series[0].Samples {
		if s.Value == s.Value { // skip NaN
			sum += s.Value
		}
	}
	if sum != inst.Series[0].Value || sum != 40 {
		t.Fatalf("range buckets sum %v, instant %v, want 40", sum, inst.Series[0].Value)
	}
}
