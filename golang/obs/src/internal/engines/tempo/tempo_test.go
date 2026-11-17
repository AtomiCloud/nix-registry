package tempo

import (
	"fmt"
	"github.com/AtomiCloud/obs/internal/seed"
	"strings"
	"testing"
	"time"
)

func testEngine() (*TempoEngine, time.Time) {
	at := time.Unix(1800000000, 0)
	root := &TSpan{SpanID: [8]byte{1}, Name: "root", Service: "checkout", Kind: 2, StartNs: uint64(at.UnixNano()), DurNs: 1000000000, Attrs: map[string]TAttrVal{"amount": seed.TInt(42), "serial": seed.TInt(9007199254740993)}, Resource: map[string]TAttrVal{"durian.cloud.service": seed.TStr("checkout")}}
	a := &TSpan{SpanID: [8]byte{2}, ParentID: root.SpanID, Name: "a", Service: "checkout", Kind: 3, Status: 2, StartNs: root.StartNs + 1000, DurNs: 500000000, Attrs: map[string]TAttrVal{"ratio": seed.TFloat(2.5)}}
	b := &TSpan{SpanID: [8]byte{3}, ParentID: root.SpanID, Name: "b", Service: "checkout", StartNs: root.StartNs + 2000, DurNs: 500000000}
	c := &TSpan{SpanID: [8]byte{4}, ParentID: a.SpanID, Name: "c", Service: "checkout", StartNs: root.StartNs + 3000, DurNs: 100000000}
	return NewTempoEngine([]*TTrace{{TraceID: [16]byte{1}, Spans: []*TSpan{c, b, a, root}}}), at
}

func TestSearchStructuralAndTypedColumns(t *testing.T) {
	e, at := testEngine()
	for _, tc := range []struct {
		q string
		n int
	}{
		{`{name="root"}`, 1}, {`{name="root"} >> {name="c"}`, 1}, {`{name="root"} > {name="a"}`, 1},
		{`{name="root"} > {name="c"}`, 0}, {`{name="a"} ~ {name="b"}`, 1}, {`{name="c"} << {name="root"}`, 1},
		{`{name="a"} < {name="root"}`, 1}, {`{name="a"} !> {name="root"}`, 1}, {`{name="root"} !>> {name="c"}`, 0},
		{`{name="root"} &>> {name="c"}`, 1}, {`{name="root"} &> {name="a"}`, 1}, {`{name="a"} &~ {name="b"}`, 1},
		{`{span.amount=42}`, 1}, {`{span.amount="42"}`, 0}, {`{span.ratio>2.0}`, 1}, {`{span.ratio>2}`, 0},
		{`{span.serial>9007199254740992}`, 1}, {`{span.serial<=9007199254740992}`, 0},
		{`{.amount=42}`, 1}, {`{.missing="x"}`, 0},
		{`{status=error}`, 1}, {`{kind=server && resource.service.name="checkout"}`, 1}, {`{span.missing="x"}`, 0},
	} {
		t.Run(tc.q, func(t *testing.T) {
			hits, err := e.Search(tc.q, at.Add(-time.Second), at.Add(2*time.Second), 20)
			if err != nil || len(hits) != tc.n {
				t.Fatalf("hits=%v err=%v want=%d", hits, err, tc.n)
			}
			if e.Inexact.Load() != 0 {
				t.Fatalf("inexact storage predicate: %d", e.Inexact.Load())
			}
		})
	}
	hits, err := e.Search(`{}`, at.Add(time.Hour), at.Add(2*time.Hour), 20)
	if err != nil || len(hits) != 0 {
		t.Fatalf("outside range: %v %v", hits, err)
	}
	if _, err := e.Search(`{span.broken=`, at.Add(time.Hour), at.Add(2*time.Hour), 20); err == nil {
		t.Fatal("invalid query passed on empty range")
	}
}

func TestMetricsAndParse(t *testing.T) {
	e, at := testEngine()
	for _, q := range []string{`{} | count_over_time()`, `{} | rate() by (resource.service.name)`, `{} | quantile_over_time(duration,.5)`} {
		rs, err := e.MetricsRange(q, at.Add(-time.Second), at.Add(2*time.Second), time.Second)
		if err != nil || len(rs) == 0 {
			t.Fatalf("%s: %v %v", q, rs, err)
		}
	}
	if _, err := e.MetricsRange(`{} | no_such_fn()`, at, at.Add(time.Second), time.Second); err == nil {
		t.Fatal("invalid metrics query passed")
	}
	p, err := Parse(`{span.known="x" || span.drifted="y"} | select(resource.service.name)`)
	if err != nil || len(p.Attributes) != 3 {
		t.Fatalf("names: %+v %v", p, err)
	}
	if _, err := Parse(`{span.provider =`); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("bad syntax: %v", err)
	}
	p, err = Parse(`{} | count_over_time() by (span.provider)`)
	if err != nil || !p.Metrics || len(p.Attributes) != 1 {
		t.Fatalf("metrics names: %+v %v", p, err)
	}
}

func TestIDLookup(t *testing.T) {
	e, _ := testEngine()
	hits, err := e.Lookup("01000000000000000000000000000000")
	if err != nil || len(hits) != 1 || len(hits[0].SpanIDs) != 4 {
		t.Fatalf("known ID: %v %v", hits, err)
	}
	hits, err = e.Lookup("02000000000000000000000000000000")
	if err != nil || len(hits) != 0 {
		t.Fatalf("unknown ID: %v %v", hits, err)
	}
	if _, err := e.Lookup("planted-not-hex"); err == nil {
		t.Fatal("invalid ID accepted")
	}
}

func TestParseProjectionsAreNotFilters(t *testing.T) {
	for _, tc := range []struct {
		q    string
		proj string
	}{
		{`{span.a = "x"} | select(span.b)`, "[span.b]"},
		{`{span.a = nil} | select(span.b)`, "[span.b]"},
		{`{} | count_over_time() by (span.b)`, "[span.b]"},
		{`{span.b = nil} | count_over_time() by (span.b)`, "[]"},
		{`{span.a = "x" && resource.c != nil}`, "[]"},
		{`{span.a = nil}`, "[]"},
	} {
		p, err := Parse(tc.q)
		if err != nil {
			t.Fatalf("%s: %v", tc.q, err)
		}
		if got := fmt.Sprint(p.Projections); got != tc.proj {
			t.Fatalf("%s: projections %s, want %s (attributes %v)", tc.q, got, tc.proj, p.Attributes)
		}
	}
}
