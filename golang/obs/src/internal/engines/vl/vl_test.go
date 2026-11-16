package vl

import (
	"reflect"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/seed"
)

func TestFieldWalker(t *testing.T) {
	at := time.Now()
	cases := []struct {
		q        string
		filters  []string
		pipes    []string
		wildcard bool
		labels   []string
	}{
		{`_time:5m {service.name="x"} severity_text:error "durian.cloud/event":="a.b" | stats by (user) count() n`,
			[]string{"_stream", "_stream:service.name", "_time", "durian.cloud/event", "severity_text"}, []string{"user"}, false, []string{"user"}},
		{`error -"timeout" | stats count() n`, []string{"_msg"}, nil, false, nil},
		{`_time:1h payment.provider:in("a","b") or user:=x`, []string{"_time", "payment.provider", "user"}, nil, true, nil},
	}
	for _, c := range cases {
		q, err := Parse(c.q, at)
		if err != nil {
			t.Fatalf("%s: %v", c.q, err)
		}
		f, p, w := q.Fields()
		if !reflect.DeepEqual(f, c.filters) || !reflect.DeepEqual(p, c.pipes) || w != c.wildcard {
			t.Errorf("%s:\n filters %v pipes %v wildcard %v", c.q, f, p, w)
		}
		labels, err := q.StatsLabels()
		if c.labels != nil && (err != nil || !reflect.DeepEqual(labels, c.labels)) {
			t.Errorf("%s: labels %v %v", c.q, labels, err)
		}
	}
	if q, _ := Parse(`error`, at); q != nil {
		if _, err := q.StatsLabels(); err == nil {
			t.Errorf("a query with no stats pipe must fail StatsLabels")
		}
	}
	if _, err := Parse(`{a="b"`, at); err == nil {
		t.Errorf("syntax error accepted")
	}
}

// The stats query copies the datasource: parsed at end, _time:[start, end) added.
func TestStatsWindow(t *testing.T) {
	end := time.Now().Add(-3 * time.Hour).Truncate(time.Minute)
	ds := &seed.Dataset{Streams: map[string]*seed.Stream{}}
	st := &seed.Stream{Fields: []seed.Label{{Name: "service.name", Value: "checkout"}}}
	for _, r := range []struct {
		ago   time.Duration
		level string
	}{{1 * time.Minute, "ERROR"}, {2 * time.Minute, "ERROR"}, {3 * time.Minute, "INFO"}, {4 * time.Minute, "ERROR"}, {9 * time.Minute, "ERROR"}} {
		st.Rows = append(st.Rows, seed.Row{TsNs: end.Add(-r.ago).UnixNano(), Msg: "charge",
			Fields: []seed.Label{{Name: "severity_text", Value: r.level}}})
	}
	ds.Streams["a"] = st
	e, err := New(ds)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	rs, err := e.Stats(`_time:5m severity_text:ERROR | stats by (service.name) count() errors`, end.Add(-10*time.Minute), end, false)
	if err != nil {
		t.Fatal(err)
	}
	// 3 of the 4 errors are inside the query's own 5 minute window: the same answer as the embed proof.
	if len(rs) != 1 || rs[0].Value != "3" || rs[0].Labels[0].Value != "checkout" {
		t.Fatalf("got %+v", rs)
	}
	// A trailing threshold filter is dropped for the live check only.
	rs, err = e.Stats(`_time:5m severity_text:ERROR | stats count() errors | filter errors:>100`, end.Add(-10*time.Minute), end, true)
	if err != nil || len(rs) != 1 || rs[0].Value != "3" {
		t.Fatalf("threshold dropped: %+v %v", rs, err)
	}
	rs, _ = e.Stats(`_time:5m severity_text:ERROR | stats count() errors | filter errors:>100`, end.Add(-10*time.Minute), end, false)
	if len(rs) != 0 {
		t.Fatalf("threshold kept: %+v", rs)
	}
}
