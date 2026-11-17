package tempo

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// T is on the hour, so 1m and 30m steps align on it.
var frontT = time.Unix(1800000000, 0).UTC().Truncate(time.Hour)

func TestInstantRequestPullsTheEndInByWholeSeconds(t *testing.T) {
	for _, tc := range []struct {
		name           string
		now            time.Time
		start, end     time.Time
		wantEnd        time.Time
		wantErr        error
		cutoff         time.Duration
		cutoffOverride bool
	}{
		{name: "end at now: 30 s cut", now: frontT, start: frontT.Add(-10 * time.Minute), end: frontT, wantEnd: frontT.Add(-30 * time.Second)},
		// The request reaches Tempo a little after the tick: still whole seconds, same end.
		{name: "now 400 ms late", now: frontT.Add(400 * time.Millisecond), start: frontT.Add(-10 * time.Minute), end: frontT, wantEnd: frontT.Add(-30 * time.Second)},
		{name: "end already at the cutoff", now: frontT, start: frontT.Add(-10 * time.Minute), end: frontT.Add(-30 * time.Second), wantEnd: frontT.Add(-30 * time.Second)},
		{name: "window inside the cutoff", now: frontT, start: frontT.Add(-30 * time.Second), end: frontT, wantErr: ErrQueryWindowWithinEndCutoff},
		{name: "empty window", now: frontT, start: frontT, end: frontT, wantErr: ErrEndMustBeGreaterThanStart},
		{name: "cutoff 0", now: frontT, start: frontT.Add(-30 * time.Second), end: frontT, wantEnd: frontT, cutoffOverride: true},
	} {
		fe := DefaultFrontend
		if tc.cutoffOverride {
			fe.QueryEndCutoff = tc.cutoff
		}
		req, err := fe.instantRequest(tc.now, "{} | rate()", tc.start, tc.end)
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: err %v want %v", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := time.Unix(0, int64(req.End)).UTC(); !got.Equal(tc.wantEnd) || req.Step != req.End-req.Start {
			t.Errorf("%s: end %s step %d, want end %s and step = end - start", tc.name, got, req.Step, tc.wantEnd)
		}
	}
}

func TestRangeRequestClampsValidatesAlignsAndClampsAgain(t *testing.T) {
	for _, tc := range []struct {
		name               string
		now                time.Time
		from               time.Duration
		step               time.Duration
		wantStart, wantEnd time.Duration // offsets from frontT
		wantErr            error
	}{
		// end T -> T-30s; AlignRequest: start T-3m (aligned) - 1m, end up to T; clamp
		// again to T-30s and round down to the step: the (T-1m, T] bucket is gone.
		{name: "1m step", now: frontT, from: 3 * time.Minute, step: time.Minute, wantStart: -4 * time.Minute, wantEnd: -time.Minute},
		{name: "1m step, now 400 ms late", now: frontT.Add(400 * time.Millisecond), from: 3 * time.Minute, step: time.Minute, wantStart: -4 * time.Minute, wantEnd: -time.Minute},
		// start T-10m rounds down to T-30m, minus one step: two steps before now-from.
		{name: "30m step", now: frontT, from: 10 * time.Minute, step: 30 * time.Minute, wantStart: -60 * time.Minute, wantEnd: -30 * time.Minute},
		{name: "window inside the cutoff", now: frontT, from: 20 * time.Second, step: time.Second, wantErr: ErrQueryWindowWithinEndCutoff},
	} {
		req, err := DefaultFrontend.rangeRequest(tc.now, "{} | rate()", frontT.Add(-tc.from), frontT, tc.step)
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: err %v want %v", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		gs, ge := time.Unix(0, int64(req.Start)).Sub(frontT), time.Unix(0, int64(req.End)).Sub(frontT)
		if gs != tc.wantStart || ge != tc.wantEnd {
			t.Errorf("%s: (%s, %s] want (%s, %s]", tc.name, gs, ge, tc.wantStart, tc.wantEnd)
		}
	}
}

func TestFrontendValidatesDurationAndIntervals(t *testing.T) {
	if _, err := DefaultFrontend.rangeRequest(frontT, "{} | rate()", frontT.Add(-25*time.Hour), frontT, time.Hour); err == nil ||
		err.Error() != "metrics query time range exceeds the maximum allowed duration of 24h0m0s" {
		t.Fatalf("max_duration: %v", err)
	}
	if _, err := DefaultFrontend.instantRequest(frontT, "{} | rate()", frontT.Add(-25*time.Hour), frontT); err == nil ||
		!strings.Contains(err.Error(), "maximum allowed duration of 24h0m0s") {
		t.Fatalf("instant max_duration: %v", err)
	}
	if _, err := DefaultFrontend.rangeRequest(frontT, "{} | rate()", frontT.Add(-3*time.Hour), frontT, time.Second); err == nil ||
		!strings.HasPrefix(err.Error(), "step of 1s is too small, minimum step for given range is ") {
		t.Fatalf("max_intervals: %v", err)
	}
	off := Frontend{QueryEndCutoff: 30 * time.Second}
	if _, err := off.rangeRequest(frontT, "{} | rate()", frontT.Add(-25*time.Hour), frontT, time.Second); err != nil {
		t.Fatalf("0 turns both checks off: %v", err)
	}
}

func TestParseFrontendReadsTempoConfig(t *testing.T) {
	fe, err := ParseFrontend([]byte("server:\n  http_listen_port: 3200\nquery_frontend:\n  query_end_cutoff: 1m\n  metrics:\n    max_duration: 48h\n    max_intervals: 500\n"))
	if err != nil || fe != (Frontend{QueryEndCutoff: time.Minute, MaxDuration: 48 * time.Hour, MaxIntervals: 500}) {
		t.Fatalf("%+v %v", fe, err)
	}
	fe, err = ParseFrontend([]byte("server:\n  http_listen_port: 3200\n"))
	if err != nil || fe != DefaultFrontend {
		t.Fatalf("absent keys keep Tempo's defaults: %+v %v", fe, err)
	}
	// yaml.v2 (Tempo's decoder) reads an integer duration as nanoseconds.
	fe, err = ParseFrontend([]byte("query_frontend:\n  query_end_cutoff: 0\n"))
	if err != nil || fe.QueryEndCutoff != 0 || fe.MaxDuration != DefaultFrontend.MaxDuration {
		t.Fatalf("cutoff 0: %+v %v", fe, err)
	}
	for _, bad := range []string{"query_frontend:\n  query_end_cutoff: soon\n", "query_frontend:\n  metrics:\n    max_intervals: -1\n", "query_frontend: [\n"} {
		if _, err := ParseFrontend([]byte(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}
