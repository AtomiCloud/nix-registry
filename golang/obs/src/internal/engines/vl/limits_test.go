package vl

import (
	"strings"
	"testing"
	"time"
)

func TestLimits(t *testing.T) {
	at := time.Unix(1234567890, 0)
	l := Limits{MaxQueryLen: 5, MaxQueryTimeRange: time.Hour}
	if err := l.Check("abcde", at, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, token string
		duration     time.Duration
	}{
		{"abcdef", "search.maxQueryLen=5", time.Hour},
		{"界界", "search.maxQueryLen=5", time.Hour},
		{"abcde", "search.maxQueryTimeRange=1h0m0s", time.Hour + time.Nanosecond},
	} {
		if err := l.Check(tc.query, at, at.Add(tc.duration)); err == nil || !strings.Contains(err.Error(), tc.token) || !strings.Contains(err.Error(), tc.query) {
			t.Fatalf("planted bad query %q: want token %s, got %v", tc.query, tc.token, err)
		}
	}
	if err := (Limits{}).Check("unlimited", at, at.Add(100*time.Hour)); err != nil {
		t.Fatal(err)
	}
}
