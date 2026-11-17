package vl

import (
	"os"
	"path/filepath"
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
		{"*", "search.maxQueryTimeRange=1h0m0s", time.Hour + time.Second},
	} {
		if err := l.Check(tc.query, at, at.Add(tc.duration)); err == nil || !strings.Contains(err.Error(), tc.token) || !strings.Contains(err.Error(), tc.query) {
			t.Fatalf("planted bad query %q: want token %s, got %v", tc.query, tc.token, err)
		}
	}
	if err := (Limits{}).Check("unlimited", at, at.Add(100*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestSettings(t *testing.T) {
	for _, input := range []string{
		"-search.maxQueryLen: 1KiB\n-search.maxQueryTimeRange: 7d\n-search.maxConcurrentRequests: 2\n-search.maxQueryDuration: 3s\n-search.maxQueueDuration: 4s\n-retentionPeriod: 10y\n",
		"- -search.maxQueryLen: 1KiB\n- -search.maxQueryTimeRange: 7d\n- -search.maxConcurrentRequests: 2\n- -search.maxQueryDuration: 3s\n- -search.maxQueueDuration: 4s\n",
		"- -search.maxQueryLen=1KiB\n- -search.maxQueryTimeRange=7d\n- -search.maxConcurrentRequests=2\n- -search.maxQueryDuration=3s\n- -search.maxQueueDuration=4s\n",
	} {
		l, err := ParseLimits([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if l.MaxQueryLen != 1024 || l.MaxQueryTimeRange != 7*24*time.Hour || l.MaxConcurrentRequests != 2 || l.MaxQueryDuration != 3*time.Second || l.MaxQueueDuration != 4*time.Second {
			t.Fatalf("settings lost: %+v", l)
		}
	}
	for _, input := range []string{"search.maxQueryLen: bad", "search.maxConcurrentRequests: -1", "search.maxQueryTimeRange: 2", "search.unknown: 1", "search.maxQueryLen: 1\n-search.maxQueryLen: 2", "- -search.maxQueryLen", "search.maxQueryLen: [1]", "{}\n---\n{}"} {
		if _, err := ParseLimits([]byte(input)); err == nil {
			t.Fatalf("planted bad settings accepted: %s", input)
		}
	}
	dir := t.TempDir()
	l, note, err := LoadLimits(dir, "")
	if err != nil || note == "" || l.MaxQueryLen != 16384 || l.MaxQueryTimeRange != 0 || l.MaxQueryDuration != 30*time.Second || l.MaxQueueDuration != 10*time.Second {
		t.Fatalf("default settings: %+v %q %v", l, note, err)
	}
	path := filepath.Join(dir, "victorialogs-settings.yaml")
	if _, _, err := LoadLimits(dir, path); err == nil {
		t.Fatal("explicit missing path accepted")
	}
	if err := os.WriteFile(path, []byte("search.maxQueryLen: 0"), 0600); err != nil {
		t.Fatal(err)
	}
	l, note, err = LoadLimits(dir, "")
	if err != nil || note != "" {
		t.Fatalf("implicit settings: %q %v", note, err)
	}
	if err := l.Check("*", time.Now(), time.Now()); err == nil {
		t.Fatal("explicit maxQueryLen=0 must refuse nonempty queries")
	}
	l = DefaultLimits()
	l.MaxQueryTimeRange = time.Minute
	at := time.Unix(1700000000, 0)
	if err := l.Check("_time:30s | stats count()", at.Add(-time.Hour), at); err != nil {
		t.Fatalf("authored time intersection refused: %v", err)
	}
}

func TestRuntimeLimits(t *testing.T) {
	l, err := ParseLimits([]byte("search.maxConcurrentRequests: 1\nsearch.maxQueueDuration: 1ms\nsearch.maxQueryDuration: 1h"))
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{limits: l, slots: make(chan struct{}, 1)}
	_, release, err := e.queryContext()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.queryContext(); err == nil || !strings.Contains(err.Error(), "-search.maxConcurrentRequests=1") || !strings.Contains(err.Error(), "-search.maxQueueDuration=1ms") {
		t.Fatalf("saturated queue: %v", err)
	}
	release()
	_, release, err = e.queryContext()
	if err != nil {
		t.Fatalf("released slot not reusable: %v", err)
	}
	release()
	l.MaxQueryDuration = time.Nanosecond
	e = &Engine{limits: l}
	ctx, release, err := e.queryContext()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	<-ctx.Done()
	if !strings.Contains(e.timeoutError().Error(), "-search.maxQueryDuration=1ns") {
		t.Fatal(e.timeoutError())
	}
}
