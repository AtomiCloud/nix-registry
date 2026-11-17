package tempo

// TempoEngine: Tempo's own TraceQL engine (github.com/grafana/tempo/pkg/traceql,
// pinned to the release that runs as the store) over in-memory TTrace data.
//
// - Spans carry nested-set numbers (left/right/parent) computed exactly like
//   Tempo's vParquet5 writer (assignNestedSetModelBoundsAndServiceStats), and
//   the structural operators (>>, >, ~, <<, <, !>>, !>, !~, &>>, &>, &~) are
//   Tempo's vParquet5 span methods copied verbatim (AGPL-3.0, Grafana Labs).
// - Traces are sorted by start; a query only touches traces that overlap its
//   window (binary search on start, bounded by the longest trace).
// - The storage side honours the fetch request like vParquet does: first-pass
//   conditions (AND or OR), then the engine's SecondPass callback. When the
//   engine optimised the SecondPass away, the first-pass filter is exact.
// - Work is split into contiguous trace shards run on Workers goroutines and
//   merged with Tempo's own combiners (search: by trace; metrics: the
//   query-frontend "final" stage, which is what makes quantiles work).

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tqregexp "github.com/grafana/tempo/pkg/regexp"
	"github.com/grafana/tempo/pkg/tempopb"
	tqv1common "github.com/grafana/tempo/pkg/tempopb/common/v1"
	"github.com/grafana/tempo/pkg/traceql"
	"github.com/grafana/tempo/pkg/util"
)

type TraceHit struct {
	TraceID string
	SpanIDs []string
}

type TempoEngine struct {
	traces   []*tqTrace // sorted by start
	starts   []uint64
	maxDur   uint64
	minStart uint64
	maxEnd   uint64
	spans    int
	ids      map[string]int32 // attribute name -> id (written only while indexing)

	// SpansPerSpanSet caps span IDs returned per matched trace (Tempo's spss).
	SpansPerSpanSet int
	// Workers is the number of goroutines a query may use (default GOMAXPROCS).
	Workers int
	// Inexact counts first-pass conditions the storage filter could not
	// evaluate exactly while the engine had dropped its SecondPass. Must stay 0.
	Inexact atomic.Int64
}

type tqTrace struct {
	id          [16]byte
	start, end  uint64
	rootName    string
	rootService string
	idHex       string // Tempo's trimmed hex form, used by trace:id
	spans       []traceql.Span
}

type tqAttr struct {
	name string
	id   int32 // interned name, for the storage filter
	val  traceql.Static
}

type tqSpan struct {
	tr         *tqTrace
	id         [8]byte
	parentID   [8]byte
	start, dur uint64
	left       int32
	right      int32
	nsParent   int32
	childCount int32
	name       string
	kind       traceql.Kind
	status     traceql.Status
	attrs      []tqAttr // span scope
	res        []tqAttr // resource scope, includes service.name
}

func tqStatic(v TAttrVal) traceql.Static {
	switch v.Kind {
	case 1:
		return traceql.NewStaticInt(int(v.I))
	case 2:
		return traceql.NewStaticFloat(v.F)
	case 3:
		return traceql.NewStaticBool(v.B)
	}
	return traceql.NewStaticString(v.S)
}

func tqKind(k int) traceql.Kind {
	switch k {
	case 1:
		return traceql.KindInternal
	case 2:
		return traceql.KindServer
	case 3:
		return traceql.KindClient
	case 4:
		return traceql.KindProducer
	case 5:
		return traceql.KindConsumer
	}
	return traceql.KindUnspecified
}

func tqStatus(s int) traceql.Status {
	switch s {
	case 1:
		return traceql.StatusOk
	case 2:
		return traceql.StatusError
	}
	return traceql.StatusUnset
}

func (e *TempoEngine) intern(name string) int32 {
	id, ok := e.ids[name]
	if !ok {
		id = int32(len(e.ids))
		e.ids[name] = id
	}
	return id
}

func (e *TempoEngine) sortedAttrs(m map[string]TAttrVal, extra ...tqAttr) []tqAttr {
	out := make([]tqAttr, 0, len(m)+len(extra))
	for _, x := range extra {
		x.id = e.intern(x.name)
		out = append(out, x)
	}
	for k, v := range m {
		out = append(out, tqAttr{k, e.intern(k), tqStatic(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// NewTempoEngine indexes the traces. The input is not modified.
func NewTempoEngine(traces []*TTrace) *TempoEngine {
	e := &TempoEngine{SpansPerSpanSet: 100, Workers: runtime.GOMAXPROCS(0), ids: map[string]int32{}}
	e.traces = make([]*tqTrace, 0, len(traces))
	for _, in := range traces {
		if len(in.Spans) == 0 {
			continue
		}
		t := &tqTrace{id: in.TraceID}
		t.idHex = util.TraceIDToHexString(t.id[:])
		spans := make([]tqSpan, len(in.Spans))
		t.spans = make([]traceql.Span, len(in.Spans))
		for i, s := range in.Spans {
			sp := &spans[i]
			sp.tr = t
			sp.id, sp.parentID = s.SpanID, s.ParentID
			sp.start, sp.dur = s.StartNs, s.DurNs
			sp.name, sp.kind, sp.status = s.Name, tqKind(s.Kind), tqStatus(s.Status)
			sp.attrs = e.sortedAttrs(s.Attrs)
			sp.res = e.sortedAttrs(s.Resource, tqAttr{name: "service.name", val: traceql.NewStaticString(s.Service)})
			if t.start == 0 || s.StartNs < t.start {
				t.start = s.StartNs
			}
			if end := s.StartNs + s.DurNs; end > t.end {
				t.end = end
			}
			t.spans[i] = sp
		}
		tqAssignNestedSet(t, in.Spans, spans)
		e.spans += len(spans)
		e.traces = append(e.traces, t)
	}
	sort.Slice(e.traces, func(i, j int) bool { return e.traces[i].start < e.traces[j].start })
	e.starts = make([]uint64, len(e.traces))
	for i, t := range e.traces {
		e.starts[i] = t.start
		if d := t.end - t.start; d > e.maxDur {
			e.maxDur = d
		}
		if e.minStart == 0 || t.start < e.minStart {
			e.minStart = t.start
		}
		if t.end > e.maxEnd {
			e.maxEnd = t.end
		}
	}
	return e
}

func tqSameService(r []tqAttr, svc string) bool {
	for _, a := range r {
		if a.name == "service.name" {
			s, _ := tqStaticString(a.val)
			return s == svc
		}
	}
	return false
}

func tqStaticString(s traceql.Static) (string, bool) {
	if s.Type != traceql.TypeString {
		return "", false
	}
	return s.EncodeToString(false), true
}

// tqAssignNestedSet mirrors vparquet5 assignNestedSetModelBoundsAndServiceStats:
// same root detection, same zipkin shared-ID handling, same DFS numbering.
func tqAssignNestedSet(t *tqTrace, in []*TSpan, spans []tqSpan) {
	type node struct {
		sp        *tqSpan
		parent    int
		children  []int
		nextChild int
	}
	nodes := make([]node, len(spans))
	byID := make(map[[8]byte][]int, len(spans))
	var roots []int
	undo := false
	for i := range spans {
		nodes[i] = node{sp: &spans[i], parent: -1}
		if spans[i].parentID == ([8]byte{}) {
			roots = append(roots, i)
		}
		ids := append(byID[spans[i].id], i)
		byID[spans[i].id] = ids
		if len(ids) > 2 {
			undo = true
		}
	}
	// root name/service: first root span (vParquet picks the root span of the trace)
	for _, i := range roots {
		t.rootName = spans[i].name
		t.rootService = in[i].Service
		break
	}
	if len(roots) == 0 || undo {
		return
	}
	for i := range nodes {
		sp := nodes[i].sp
		if sp.parentID == ([8]byte{}) {
			continue
		}
		cands := byID[sp.parentID]
		p := -1
		switch len(cands) {
		case 1:
			p = cands[0]
		case 2:
			want := traceql.KindServer
			if sp.kind == traceql.KindServer {
				want = traceql.KindClient
			}
			if spans[cands[0]].kind == want {
				p = cands[0]
			} else if spans[cands[1]].kind == want {
				p = cands[1]
			}
		}
		if p < 0 {
			continue
		}
		nodes[i].parent = p
		nodes[p].children = append(nodes[p].children, i)
	}
	for i := range nodes {
		nodes[i].sp.childCount = int32(len(nodes[i].children))
	}
	bound := int32(1)
	for _, r := range roots {
		n := r
		nodes[n].sp.left = bound
		nodes[n].sp.nsParent = -1
		bound++
		for n >= 0 {
			nd := &nodes[n]
			if nd.nextChild < len(nd.children) {
				c := nd.children[nd.nextChild]
				nd.nextChild++
				nodes[c].sp.left = bound
				nodes[c].sp.nsParent = nd.sp.left
				bound++
				n = c
			} else {
				nd.sp.right = bound
				bound++
				n = nd.parent
			}
		}
	}
}

// ---------------------------------------------------------------- traceql.Span

func (s *tqSpan) ID() []byte                 { return s.id[:] }
func (s *tqSpan) StartTimeUnixNanos() uint64 { return s.start }
func (s *tqSpan) DurationNanos() uint64      { return s.dur }

func tqFind(attrs []tqAttr, name string) (traceql.Static, bool) {
	for i := range attrs {
		if attrs[i].name == name {
			return attrs[i].val, true
		}
	}
	return traceql.StaticNil, false
}

func (s *tqSpan) intrinsic(in traceql.Intrinsic) (traceql.Static, bool) {
	switch in {
	case traceql.IntrinsicDuration:
		return traceql.NewStaticDuration(time.Duration(s.dur)), true
	case traceql.IntrinsicName:
		return traceql.NewStaticString(s.name), true
	case traceql.IntrinsicStatus:
		return traceql.NewStaticStatus(s.status), true
	case traceql.IntrinsicStatusMessage:
		return traceql.NewStaticString(""), true
	case traceql.IntrinsicKind:
		return traceql.NewStaticKind(s.kind), true
	case traceql.IntrinsicChildCount:
		return traceql.NewStaticInt(int(s.childCount)), true
	case traceql.IntrinsicTraceRootService:
		return traceql.NewStaticString(s.tr.rootService), true
	case traceql.IntrinsicTraceRootSpan:
		return traceql.NewStaticString(s.tr.rootName), true
	case traceql.IntrinsicTraceDuration:
		return traceql.NewStaticDuration(time.Duration(s.tr.end - s.tr.start)), true
	case traceql.IntrinsicTraceID:
		return traceql.NewStaticString(s.tr.idHex), true
	case traceql.IntrinsicSpanID:
		return traceql.NewStaticString(util.SpanIDToHexString(s.id[:])), true
	case traceql.IntrinsicParentID:
		if s.parentID == ([8]byte{}) {
			return traceql.StaticNil, false
		}
		return traceql.NewStaticString(util.SpanIDToHexString(s.parentID[:])), true
	case traceql.IntrinsicNestedSetLeft:
		return traceql.NewStaticInt(int(s.left)), true
	case traceql.IntrinsicNestedSetRight:
		return traceql.NewStaticInt(int(s.right)), true
	case traceql.IntrinsicNestedSetParent:
		return traceql.NewStaticInt(int(s.nsParent)), true
	}
	return traceql.StaticNil, false
}

// AttributeFor follows vparquet5 span.AttributeFor: explicit scopes look only
// in that scope; intrinsics resolve to span/trace values; unscoped names look
// in span attributes first, then resource attributes.
func (s *tqSpan) AttributeFor(a traceql.Attribute) (traceql.Static, bool) {
	switch a.Scope {
	case traceql.AttributeScopeResource:
		return tqFind(s.res, a.Name)
	case traceql.AttributeScopeSpan:
		return tqFind(s.attrs, a.Name)
	case traceql.AttributeScopeEvent, traceql.AttributeScopeLink, traceql.AttributeScopeInstrumentation:
		return traceql.StaticNil, false
	}
	if a.Intrinsic != traceql.IntrinsicNone {
		return s.intrinsic(a.Intrinsic)
	}
	if v, ok := tqFind(s.attrs, a.Name); ok {
		return v, true
	}
	return tqFind(s.res, a.Name)
}

func (s *tqSpan) AllAttributes() map[traceql.Attribute]traceql.Static {
	m := make(map[traceql.Attribute]traceql.Static, len(s.attrs)+len(s.res)+1)
	s.AllAttributesFunc(func(a traceql.Attribute, v traceql.Static) { m[a] = v })
	return m
}

func (s *tqSpan) AllAttributesFunc(f func(traceql.Attribute, traceql.Static)) {
	for _, a := range s.res {
		f(traceql.NewScopedAttribute(traceql.AttributeScopeResource, false, a.name), a.val)
	}
	f(traceql.NewIntrinsic(traceql.IntrinsicName), traceql.NewStaticString(s.name))
	for _, a := range s.attrs {
		f(traceql.NewScopedAttribute(traceql.AttributeScopeSpan, false, a.name), a.val)
	}
}

// ------------------------------------------------------------- the time index

// window returns the index range of traces overlapping [start,end] (ns,
// inclusive, like vParquet's trace-level time filter). 0,0 = everything.
func (e *TempoEngine) window(start, end uint64) (int, int) {
	if start == 0 && end == 0 {
		return 0, len(e.traces)
	}
	lo := 0
	if start > e.maxDur {
		lo = sort.Search(len(e.starts), func(i int) bool { return e.starts[i] >= start-e.maxDur })
	}
	hi := sort.Search(len(e.starts), func(i int) bool { return e.starts[i] > end })
	return lo, hi
}

func tqShards(lo, hi, n int) [][2]int {
	if n < 1 {
		n = 1
	}
	total := hi - lo
	if total <= 0 {
		return nil
	}
	if total < 2000 || n == 1 {
		return [][2]int{{lo, hi}}
	}
	if n > total/1000 {
		n = total / 1000
	}
	out := make([][2]int, 0, n)
	for i := 0; i < n; i++ {
		a := lo + total*i/n
		b := lo + total*(i+1)/n
		out = append(out, [2]int{a, b})
	}
	return out
}

// ------------------------------------------------------- first-pass conditions

type tqCond struct {
	c   traceql.Condition
	id  int32 // interned attribute id for span/resource scope; -1 = never seen; -2 = not used
	re  *tqregexp.Regexp
	neg bool // regex compiled with shouldMatch=true, result negated (RegexMatchNone)
}

type tqFilter struct {
	conds   []tqCond
	all     bool
	exact   bool // SecondPass is nil: the filter alone decides
	inexact *atomic.Int64
	err     error
	start   uint64
	end     uint64
}

func tqNewFilter(req *traceql.FetchSpansRequest, e *TempoEngine) (*tqFilter, error) {
	inexact := &e.Inexact
	f := &tqFilter{all: req.AllConditions, exact: req.SecondPass == nil, inexact: inexact,
		start: req.StartTimeUnixNanos, end: req.EndTimeUnixNanos}
	for _, c := range req.Conditions {
		tc := tqCond{c: c, id: -2}
		if c.Attribute.Intrinsic == traceql.IntrinsicNone && (c.Attribute.Scope == traceql.AttributeScopeSpan || c.Attribute.Scope == traceql.AttributeScopeResource) {
			tc.id = -1
			if id, ok := e.ids[c.Attribute.Name]; ok {
				tc.id = id
			}
		}
		switch c.Op {
		case traceql.OpRegex, traceql.OpNotRegex, traceql.OpRegexMatchAny, traceql.OpRegexMatchNone:
			var pats []string
			for _, o := range c.Operands {
				if arr, ok := o.StringArray(); ok && o.Type != traceql.TypeString {
					pats = append(pats, arr...)
				} else {
					pats = append(pats, o.EncodeToString(false))
				}
			}
			should := c.Op == traceql.OpRegex || c.Op == traceql.OpRegexMatchAny || c.Op == traceql.OpRegexMatchNone
			re, err := tqregexp.NewRegexp(pats, should)
			if err != nil {
				return nil, err
			}
			tc.re = re
			tc.neg = c.Op == traceql.OpRegexMatchNone
		}
		f.conds = append(f.conds, tc)
	}
	return f, nil
}

func tqCmpNum(op traceql.Operator, a, b float64) (bool, bool) {
	switch op {
	case traceql.OpGreater:
		return a > b, true
	case traceql.OpGreaterEqual:
		return a >= b, true
	case traceql.OpLess:
		return a < b, true
	case traceql.OpLessEqual:
		return a <= b, true
	}
	return false, false
}

func tqNumeric(t traceql.StaticType) bool {
	return t == traceql.TypeInt || t == traceql.TypeFloat || t == traceql.TypeDuration
}

// match evaluates one condition on one span: (matches, exact).
func (tc *tqCond) match(s *tqSpan) (bool, bool) {
	c := &tc.c
	a := c.Attribute
	switch a.Intrinsic {
	case traceql.IntrinsicSpanStartTime, traceql.IntrinsicTraceStartTime:
		if c.Op == traceql.OpNone {
			return true, true
		}
		v := s.start
		if a.Intrinsic == traceql.IntrinsicTraceStartTime {
			v = s.tr.start
		}
		if len(c.Operands) != 1 {
			return true, false
		}
		n, ok := c.Operands[0].Int()
		if !ok {
			return true, false
		}
		if c.Op == traceql.OpEqual {
			return v == uint64(n), true
		}
		r, ok := tqCmpNum(c.Op, float64(v), float64(n))
		return r, ok
	case traceql.IntrinsicStructuralDescendant, traceql.IntrinsicStructuralChild, traceql.IntrinsicStructuralSibling,
		traceql.IntrinsicNestedSetLeft, traceql.IntrinsicNestedSetRight, traceql.IntrinsicNestedSetParent:
		if c.Op == traceql.OpNone {
			return true, true
		}
	case traceql.IntrinsicServiceStats, traceql.IntrinsicSpanMultiplier:
		return true, c.Op == traceql.OpNone
	}
	unscoped := a.Scope == traceql.AttributeScopeNone && a.Intrinsic == traceql.IntrinsicNone
	if c.Op == traceql.OpNone {
		if a.Intrinsic != traceql.IntrinsicNone {
			return true, true // intrinsic columns exist on every span: match-all
		}
		_, ok := tc.lookup(s)
		return ok, !unscoped
	}
	v, ok := tc.lookup(s)
	switch c.Op {
	case traceql.OpExists:
		return ok, !unscoped
	case traceql.OpNotExists:
		return !ok, !unscoped
	}
	if !ok {
		return false, !unscoped
	}
	if unscoped {
		// vParquet matches .x on span OR resource; the engine then prefers span. Keep it.
		return true, false
	}
	if a.Intrinsic == traceql.IntrinsicNone && len(c.Operands) > 0 && !tqSameColumnType(v.Type, c.Operands[0].Type) {
		// vParquet keeps one value column per type and builds the predicate for the
		// operand's type only: `span.amount > 0` never matches a float amount.
		return false, true
	}
	switch c.Op {
	case traceql.OpRegex, traceql.OpNotRegex, traceql.OpRegexMatchAny:
		if v.Type != traceql.TypeString {
			return true, false
		}
		return tc.re.MatchString(v.EncodeToString(false)), true
	case traceql.OpRegexMatchNone:
		if v.Type != traceql.TypeString {
			return true, false
		}
		return !tc.re.MatchString(v.EncodeToString(false)), true
	case traceql.OpIn, traceql.OpNotIn:
		hit := false
		for i := range c.Operands {
			o := c.Operands[i]
			for _, el := range o.Elements() {
				if v.Equals(&el) {
					hit = true
				}
			}
		}
		if c.Op == traceql.OpIn {
			return hit, true
		}
		return !hit, true
	}
	if len(c.Operands) != 1 {
		return true, false
	}
	o := c.Operands[0]
	switch c.Op {
	case traceql.OpEqual:
		return v.Equals(&o), true
	case traceql.OpNotEqual:
		return v.NotEquals(&o), true
	}
	switch {
	case tqNumeric(v.Type) && tqNumeric(o.Type):
		r, ok := tqCmpNum(c.Op, v.Float(), o.Float())
		return r || !ok, ok
	case v.Type == traceql.TypeString && o.Type == traceql.TypeString:
		cmp := strings.Compare(v.EncodeToString(false), o.EncodeToString(false))
		r, ok := tqCmpNum(c.Op, float64(cmp), 0)
		return r || !ok, ok
	}
	return true, false
}

// keep reports whether the span passes the first pass.
func (f *tqFilter) keep(s *tqSpan) bool {
	if len(f.conds) == 0 {
		return true
	}
	if f.all {
		for i := range f.conds {
			m, ex := f.conds[i].match(s)
			if !ex && f.exact {
				f.inexact.Add(1)
				f.err = fmt.Errorf("unsupported exact Tempo storage condition: %v", f.conds[i].c)
			}
			if !m {
				return false
			}
		}
		return true
	}
	for i := range f.conds {
		m, ex := f.conds[i].match(s)
		if !ex && f.exact {
			f.inexact.Add(1)
			f.err = fmt.Errorf("unsupported exact Tempo storage condition: %v", f.conds[i].c)
		}
		if m {
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------- the fetcher

type tqFetcher struct {
	e      *TempoEngine
	lo, hi int // shard bounds (trace indexes)
}

type tqSetIter struct {
	f       *tqFetcher
	req     traceql.FetchSpansRequest
	filt    *tqFilter
	i, hi   int
	pending []*traceql.Spanset
	nils    *tqNils
	err     error
	buf     []traceql.Span
}

func (it *tqSetIter) Next(context.Context) (*traceql.Spanset, error) {
	for {
		if len(it.pending) > 0 {
			ss := it.pending[0]
			it.pending = it.pending[1:]
			return ss, nil
		}
		if it.i >= it.hi {
			return nil, nil
		}
		t := it.f.e.traces[it.i]
		it.i++
		if it.filt.start != 0 || it.filt.end != 0 {
			if t.start > it.filt.end || t.end < it.filt.start {
				continue
			}
		}
		// one buffer per iterator: the engine consumes each spanset before asking for
		// the next (search converts it to metadata, metrics observe + Release)
		spans := it.buf[:0]
		for _, sp := range t.spans {
			if it.filt.keep(sp.(*tqSpan)) {
				if it.nils != nil {
					spans = append(spans, &tqNilSpan{tqSpan: sp.(*tqSpan), m: it.nils})
				} else {
					spans = append(spans, sp)
				}
			}
		}
		if it.filt.err != nil {
			return nil, it.filt.err
		}
		it.buf = spans
		if len(spans) == 0 {
			continue
		}
		ss := &traceql.Spanset{Spans: spans}
		tqFillMeta(ss, t)
		if it.req.SecondPass == nil {
			return ss, nil
		}
		out, err := it.req.SecondPass(ss)
		if err != nil {
			return nil, err
		}
		// like vParquet: spansets built by the pipeline (by(), coalesce()) get the
		// trace-level metadata of the trace they came from
		for _, o := range out {
			tqFillMeta(o, t)
		}
		it.pending = append(it.pending[:0], out...)
	}
}

func tqFillMeta(ss *traceql.Spanset, t *tqTrace) {
	if ss.TraceID == nil {
		ss.TraceID = t.id[:]
	}
	if ss.RootSpanName == "" {
		ss.RootSpanName = t.rootName
	}
	if ss.RootServiceName == "" {
		ss.RootServiceName = t.rootService
	}
	if ss.StartTimeUnixNanos == 0 {
		ss.StartTimeUnixNanos = t.start
	}
	if ss.DurationNanos == 0 {
		ss.DurationNanos = t.end - t.start
	}
}

// ------------------------------------------------- vParquet5 "nil" placeholders

// Tempo's default dedicated (typed) columns, tempodb/backend/block_meta.go.
var tqDedicated = map[traceql.AttributeScope][]string{
	traceql.AttributeScopeResource: {"k8s.cluster.name", "k8s.namespace.name", "k8s.pod.name", "k8s.container.name"},
	traceql.AttributeScopeSpan: {"http.request.method", "http.response.status_code", "url.path", "url.route", "server.address",
		"server.port", "http.method", "http.url", "http.route", "http.status_code"},
}

func tqIsDedicated(a traceql.Attribute) bool {
	if a.Intrinsic != traceql.IntrinsicNone {
		return false
	}
	for scope, names := range tqDedicated {
		if a.Scope != traceql.AttributeScopeNone && a.Scope != scope {
			continue
		}
		for _, n := range names {
			if n == a.Name {
				return true
			}
		}
	}
	return false
}

// tqNils: attributes vParquet would read without a predicate from a dedicated
// column; spans missing them get the string "nil" (block_traceql.go, "This is
// a null value, indicating the attribute doesn't exist"). selectAll also adds
// the intrinsics vParquet returns for SecondPassSelectAll (compare()).
type tqNils struct {
	attrs     []traceql.Attribute
	selectAll bool
}

func tqNilsFor(req *traceql.FetchSpansRequest) *tqNils {
	n := &tqNils{selectAll: req.SecondPassSelectAll}
	for _, cs := range [][]traceql.Condition{req.Conditions, req.SecondPassConditions} {
		for _, c := range cs {
			// OpNone on a dedicated column, or any `attr = nil` (OpNotExists): the engine
			// checks `= nil` against the placeholder string (ast_execute.go OpNotExists)
			if (c.Op == traceql.OpNone && tqIsDedicated(c.Attribute)) || (c.Op == traceql.OpNotExists && c.Attribute.Intrinsic == traceql.IntrinsicNone) {
				n.attrs = append(n.attrs, c.Attribute)
			}
		}
	}
	if len(n.attrs) == 0 && !n.selectAll {
		return nil
	}
	return n
}

type tqNilSpan struct {
	*tqSpan
	m *tqNils
}

func tqAs(s traceql.Span) *tqSpan {
	switch x := s.(type) {
	case *tqSpan:
		return x
	case *tqNilSpan:
		return x.tqSpan
	}
	return nil
}

func (s *tqNilSpan) AttributeFor(a traceql.Attribute) (traceql.Static, bool) {
	if v, ok := s.tqSpan.AttributeFor(a); ok {
		return v, ok
	}
	if s.m.selectAll && tqIsDedicated(a) {
		return traceql.NewStaticString("nil"), true
	}
	for _, n := range s.m.attrs {
		if n == a {
			return traceql.NewStaticString("nil"), true
		}
	}
	return traceql.StaticNil, false
}

func (s *tqNilSpan) AllAttributes() map[traceql.Attribute]traceql.Static {
	m := map[traceql.Attribute]traceql.Static{}
	s.AllAttributesFunc(func(a traceql.Attribute, v traceql.Static) { m[a] = v })
	return m
}

func (s *tqNilSpan) AllAttributesFunc(f func(traceql.Attribute, traceql.Static)) {
	s.tqSpan.AllAttributesFunc(f)
	if !s.m.selectAll {
		return
	}
	for _, in := range []traceql.Intrinsic{traceql.IntrinsicKind, traceql.IntrinsicStatus, traceql.IntrinsicStatusMessage,
		traceql.IntrinsicChildCount, traceql.IntrinsicTraceRootSpan, traceql.IntrinsicTraceRootService} {
		v, _ := s.tqSpan.intrinsic(in)
		f(traceql.NewIntrinsic(in), v)
	}
	for scope, names := range tqDedicated {
		for _, n := range names {
			a := traceql.NewScopedAttribute(scope, false, n)
			if _, ok := s.tqSpan.AttributeFor(a); !ok {
				f(a, traceql.NewStaticString("nil"))
			}
		}
	}
}

func tqSameColumnType(v, o traceql.StaticType) bool {
	switch o {
	case traceql.TypeString, traceql.TypeStringArray:
		return v == traceql.TypeString || v == traceql.TypeStringArray
	case traceql.TypeInt, traceql.TypeIntArray:
		return v == traceql.TypeInt || v == traceql.TypeIntArray
	case traceql.TypeFloat, traceql.TypeFloatArray:
		return v == traceql.TypeFloat || v == traceql.TypeFloatArray
	case traceql.TypeBoolean, traceql.TypeBooleanArray:
		return v == traceql.TypeBoolean || v == traceql.TypeBooleanArray
	}
	return true
}
func (it *tqSetIter) Close() {}

func (f *tqFetcher) Fetch(_ context.Context, req traceql.FetchSpansRequest) (traceql.FetchSpansResponse, error) {
	filt, err := tqNewFilter(&req, f.e)
	if err != nil {
		return traceql.FetchSpansResponse{}, err
	}
	lo, hi := f.lo, f.hi
	if req.StartTimeUnixNanos != 0 || req.EndTimeUnixNanos != 0 {
		wl, wh := f.e.window(req.StartTimeUnixNanos, req.EndTimeUnixNanos)
		lo, hi = max(lo, wl), min(hi, wh)
	}
	return traceql.FetchSpansResponse{Results: &tqSetIter{f: f, req: req, filt: filt, i: lo, hi: hi, nils: tqNilsFor(&req)}}, nil
}

// span-only fetch (used by metrics queries without structure/aggregates)
type tqSpanIter struct {
	sets *tqSetIter
	cur  []traceql.Span
}

func (it *tqSpanIter) Next(ctx context.Context) (traceql.Span, error) {
	for len(it.cur) == 0 {
		ss, err := it.sets.Next(ctx)
		if err != nil || ss == nil {
			return nil, err
		}
		it.cur = ss.Spans
	}
	s := it.cur[0]
	it.cur = it.cur[1:]
	return s, nil
}
func (it *tqSpanIter) Close() {}

func (f *tqFetcher) FetchSpans(ctx context.Context, req traceql.FetchSpansRequest) (traceql.FetchSpansOnlyResponse, error) {
	r, err := f.Fetch(ctx, req)
	if err != nil {
		return traceql.FetchSpansOnlyResponse{}, err
	}
	return traceql.FetchSpansOnlyResponse{Results: &tqSpanIter{sets: r.Results.(*tqSetIter)}}, nil
}

// ------------------------------------------------------------------ public API

func tqHex16(b []byte) string { return hex.EncodeToString(b) }

// Search runs a TraceQL search over traces overlapping [start,end] (Tempo
// semantics: trace start <= end && trace end >= start, second precision).
func (e *TempoEngine) Search(q string, start, end time.Time, limit int) ([]TraceHit, error) {
	if _, _, _, _, err := traceql.Compile(q); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	req := &tempopb.SearchRequest{Query: q, Limit: uint32(limit), SpansPerSpanSet: uint32(e.SpansPerSpanSet),
		Start: uint32(start.Unix()), End: uint32(end.Unix())}
	lo, hi := e.window(uint64(req.Start)*1e9, uint64(req.End)*1e9)
	shards := tqShards(lo, hi, e.Workers)
	type res struct {
		r   *tempopb.SearchResponse
		err error
	}
	out := make([]res, len(shards))
	var wg sync.WaitGroup
	for i, sh := range shards {
		wg.Add(1)
		go func(i int, sh [2]int) {
			defer wg.Done()
			// Tempo's combiner pre-sizes a map to Limit; a shard cannot match more traces than it holds
			sr := *req
			if n := uint32(sh[1] - sh[0]); sr.Limit > n {
				sr.Limit = n
			}
			r, err := traceql.NewEngine().ExecuteSearch(context.Background(), &sr, &tqFetcher{e: e, lo: sh[0], hi: sh[1]})
			out[i] = res{r, err}
		}(i, sh)
	}
	wg.Wait()
	byID := map[string]*TraceHit{}
	startOf := map[string]uint64{}
	for _, o := range out {
		if o.err != nil {
			return nil, o.err
		}
		for _, t := range o.r.Traces {
			id := tqPadHex(t.TraceID, 32)
			h := byID[id]
			if h == nil {
				h = &TraceHit{TraceID: id}
				byID[id] = h
				startOf[id] = t.StartTimeUnixNano
			}
			seen := map[string]bool{}
			for _, s := range h.SpanIDs {
				seen[s] = true
			}
			sets := t.SpanSets
			if len(sets) == 0 && t.SpanSet != nil {
				sets = []*tempopb.SpanSet{t.SpanSet}
			}
			for _, ss := range sets {
				for _, sp := range ss.Spans {
					sid := tqPadHex(sp.SpanID, 16)
					if !seen[sid] {
						seen[sid] = true
						h.SpanIDs = append(h.SpanIDs, sid)
					}
				}
			}
		}
	}
	hits := make([]TraceHit, 0, len(byID))
	for _, h := range byID {
		sort.Strings(h.SpanIDs)
		hits = append(hits, *h)
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := startOf[hits[i].TraceID], startOf[hits[j].TraceID]
		if a != b {
			return a > b
		}
		return hits[i].TraceID < hits[j].TraceID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func tqPadHex(s string, n int) string {
	s = strings.ToLower(strings.TrimLeft(s, "0"))
	if len(s) >= n {
		return s
	}
	return strings.Repeat("0", n-len(s)) + s
}

// MetricsRange runs a TraceQL metrics query like Tempo's query-frontend does
// for backend blocks: align the request, evaluate raw per shard, combine with
// the "final" aggregation. Points are keyed by unix ms (right edge of the step,
// Tempo's convention).
func (e *TempoEngine) MetricsRange(q string, start, end time.Time, step time.Duration) ([]Result, error) {
	res, err := e.metricsRaw(q, start, end, step)
	if err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return tqToResults(res.Series), nil
}

func (e *TempoEngine) metricsRaw(q string, start, end time.Time, step time.Duration) (*tempopb.QueryRangeResponse, error) {
	if step <= 0 {
		return nil, errors.New("step required")
	}
	req := &tempopb.QueryRangeRequest{Query: q, Start: uint64(start.UnixNano()), End: uint64(end.UnixNano()), Step: uint64(step)}
	req.SetInstant(false)
	traceql.AlignRequest(req)
	// spans counted must start in (req.Start, req.End]; their traces overlap that window
	lo, hi := e.window(req.Start, req.End)
	shards := tqShards(lo, hi, e.Workers)
	final, err := traceql.QueryRangeCombinerFor(req, traceql.AggregateModeFinal, 0)
	if err != nil {
		return nil, err
	}
	type res struct {
		r   *tempopb.QueryRangeResponse
		err error
	}
	out := make([]res, len(shards))
	var wg sync.WaitGroup
	for i, sh := range shards {
		wg.Add(1)
		go func(i int, sh [2]int) {
			defer wg.Done()
			r := *req
			ev, err := traceql.NewEngine().CompileMetricsQueryRange(&r)
			if err != nil {
				out[i] = res{nil, err}
				return
			}
			if err := ev.Do(context.Background(), &tqFetcher{e: e, lo: sh[0], hi: sh[1]}, 0, 0, 0); err != nil {
				out[i] = res{nil, err}
				return
			}
			out[i] = res{&tempopb.QueryRangeResponse{Series: ev.Results().ToProto(&r)}, nil}
		}(i, sh)
	}
	wg.Wait()
	if len(shards) == 0 { // still compile, so bad queries fail
		if _, err := traceql.NewEngine().CompileMetricsQueryRange(req); err != nil {
			return nil, err
		}
	}
	for _, o := range out {
		if o.err != nil {
			return nil, o.err
		}
		final.Combine(o.r)
	}
	return final.Response(), nil
}

func tqToResults(series []*tempopb.TimeSeries) []Result {
	out := make([]Result, 0, len(series))
	for _, s := range series {
		r := Result{Points: make(map[int64]float64, len(s.Samples))}
		for _, l := range s.Labels {
			r.Labels = append(r.Labels, Label{Name: l.Key, Value: tqAnyValue(l.Value)})
		}
		sort.Slice(r.Labels, func(i, j int) bool { return r.Labels[i].Name < r.Labels[j].Name })
		for _, p := range s.Samples {
			r.Points[p.TimestampMs] = p.Value
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return tqLabelKey(out[i].Labels) < tqLabelKey(out[j].Labels) })
	return out
}

func tqLabelKey(ls []Label) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Name)
		b.WriteByte('=')
		b.WriteString(l.Value)
		b.WriteByte(',')
	}
	return b.String()
}

func tqAnyValue(v *tqv1common.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.Value.(type) {
	case *tqv1common.AnyValue_StringValue:
		return x.StringValue
	case *tqv1common.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *tqv1common.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64)
	case *tqv1common.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	}
	return v.String()
}

var _ = io.EOF
var _ = math.NaN
var _ = fmt.Sprint

// lookup resolves the condition's attribute on a span; scoped span/resource
// attributes go by interned id (no string compares), the rest by AttributeFor.
func (tc *tqCond) lookup(s *tqSpan) (traceql.Static, bool) {
	switch tc.id {
	case -2:
		return s.AttributeFor(tc.c.Attribute)
	case -1:
		return traceql.StaticNil, false
	}
	attrs := s.attrs
	if tc.c.Attribute.Scope == traceql.AttributeScopeResource {
		attrs = s.res
	}
	for i := range attrs {
		if attrs[i].id == tc.id {
			return attrs[i].val, true
		}
	}
	return traceql.StaticNil, false
}
