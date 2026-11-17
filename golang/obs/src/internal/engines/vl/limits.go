package vl

import (
	"fmt"
	"time"
)

// Limits are the deterministic query refusals enforced by VictoriaLogs' HTTP
// frontend. Callers supply their deployed settings; zero means unlimited.
// Runtime timeout and concurrent-request limits require a real server and are
// deliberately not modeled by this static guard.
type Limits struct {
	MaxQueryLen       int
	MaxQueryTimeRange time.Duration
}

// Check applies the same byte-length and time-range boundaries as vlselect.
func (l Limits) Check(query string, start, end time.Time) error {
	if l.MaxQueryLen > 0 && len(query) > l.MaxQueryLen {
		return fmt.Errorf("query %q: -search.maxQueryLen=%d exceeded (%d bytes)", query, l.MaxQueryLen, len(query))
	}
	if l.MaxQueryTimeRange > 0 && end.Sub(start) > l.MaxQueryTimeRange {
		return fmt.Errorf("query %q: -search.maxQueryTimeRange=%s exceeded (%s)", query, l.MaxQueryTimeRange, end.Sub(start))
	}
	return nil
}
