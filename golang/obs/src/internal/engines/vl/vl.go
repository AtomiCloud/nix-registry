// Package vl runs LogsQL on VictoriaLogs' own parser and storage (v1.54.0,
// lib/logstorage from the vendored third_party tree), inside this process.
package vl

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VictoriaMetrics/VictoriaLogs/lib/logstorage"

	"github.com/AtomiCloud/obs/internal/engines"
	"github.com/AtomiCloud/obs/internal/seed"
)

// Query is a parsed LogsQL query.
type Query struct{ q *logstorage.Query }

// Parse parses a LogsQL query at the timestamp t (relative `_time:5m` filters are
// anchored there, as VictoriaLogs does for the stats query endpoint).
func Parse(qs string, t time.Time) (*Query, error) {
	engines.Init()
	q, err := logstorage.ParseQueryAtTimestamp(qs, t.UnixNano())
	if err != nil {
		return nil, err
	}
	return &Query{q: q}, nil
}

// String is the query as VictoriaLogs prints it.
func (q *Query) String() string { return q.q.String() }

// StatsLabels returns the `by (...)` labels of the last `stats` pipe; it fails when
// the query has none, the way /select/logsql/stats_query does.
func (q *Query) StatsLabels() ([]string, error) { return q.q.ObsStatsLabels() }

// Fields returns the field names the query reads: filter fields (with `_stream:<tag>`
// for each stream tag) and the fields its pipes need from the stored rows. wildcard
// is true when the pipes keep every field.
func (q *Query) Fields() (filters, pipes []string, wildcard bool) { return q.q.ObsFieldNames() }

// HasTimeFilter reports whether the query already carries a `_time:` filter.
func (q *Query) HasTimeFilter() bool {
	s, e := q.q.GetFilterTimeRange()
	return s != math.MinInt64 || e != math.MaxInt64
}

// Result is one stats row or log row of a query.
type Result struct {
	Labels []seed.Label
	Value  string
	Name   string // stats output column; the HTTP stats endpoint emits this as __name__
}

// Engine is one in-process VictoriaLogs.
type Engine struct {
	dir         string
	s           *logstorage.Storage
	limits      Limits
	slots       chan struct{}
	concurrency uint // 0 = VictoriaLogs' default (all CPUs)
}

// SetQueryConcurrency runs every later query with n workers (options(concurrency=n)).
// Results do not change; callers that already run queries in parallel save the
// per-worker buffers each query allocates.
func (e *Engine) SetQueryConcurrency(n uint) { e.concurrency = n }

// New opens a storage in a temp dir and loads every stream of ds.
func New(ds *seed.Dataset) (*Engine, error) {
	return NewWithLimits(ds, DefaultLimits())
}

// NewWithLimits opens the store with the consumer's deployed query limits.
func NewWithLimits(ds *seed.Dataset, limits Limits) (*Engine, error) {
	if limits.MaxConcurrentRequests < 0 {
		return nil, fmt.Errorf("-search.maxConcurrentRequests must be nonnegative")
	}
	engines.Init()
	dir, err := os.MkdirTemp(engines.TmpBase(), "obs-vl-")
	if err != nil {
		return nil, err
	}
	s := logstorage.MustOpenStorage(dir, &logstorage.StorageConfig{
		Retention:       100 * 365 * 24 * time.Hour,
		FutureRetention: 2 * 24 * time.Hour,
		MaxBackfillAge:  100 * 365 * 24 * time.Hour,
	})
	e := &Engine{dir: dir, s: s, limits: limits}
	if limits.runtimeSet || limits.MaxConcurrentRequests > 0 {
		e.slots = make(chan struct{}, limits.MaxConcurrentRequests)
	}
	tenant := logstorage.TenantID{}
	for _, st := range ds.Streams {
		streamFields := make([]string, 0, len(st.Fields))
		for _, l := range st.Fields {
			streamFields = append(streamFields, l.Name)
		}
		lr := logstorage.GetLogRows(streamFields, nil, nil, nil, "")
		for _, row := range st.Rows {
			fs := make([]logstorage.Field, 0, len(st.Fields)+len(row.Fields)+1)
			for _, l := range st.Fields {
				fs = append(fs, logstorage.Field{Name: l.Name, Value: l.Value})
			}
			for _, l := range row.Fields {
				fs = append(fs, logstorage.Field{Name: l.Name, Value: l.Value})
			}
			fs = append(fs, logstorage.Field{Name: "_msg", Value: row.Msg})
			lr.MustAdd(tenant, row.TsNs, fs, -1)
			// Hand rows over in blocks, as vlinsert does: one LogRows holding a whole
			// seed grows (and the GC rescans) a slice of every row and field.
			if lr.NeedFlush() {
				e.s.MustAddRows(lr)
				lr.ResetKeepSettings()
			}
		}
		e.s.MustAddRows(lr)
		logstorage.PutLogRows(lr)
	}
	e.s.DebugFlush()
	return e, nil
}

// Close closes the storage and removes its directory.
func (e *Engine) Close() {
	e.s.MustClose()
	_ = os.RemoveAll(e.dir)
}

// Stats runs a `stats` query the way the Grafana VictoriaLogs datasource v0.34.0 and
// VictoriaLogs' /select/logsql/stats_query do (FROM SOURCE: datasource
// pkg/plugin/query.go:179-205, VictoriaLogs app/vlselect/logsql/logsql.go:1509-1570):
// the query is parsed at end, end is exclusive, and _time:[start, end) is added.
func (e *Engine) Stats(qs string, start, end time.Time, withoutTrailingFilters bool) ([]Result, error) {
	if err := e.limits.Check(qs, start, end); err != nil {
		return nil, err
	}
	q, err := logstorage.ParseQueryAtTimestamp(qs, end.UnixNano())
	if err != nil {
		return nil, err
	}
	group, err := q.ObsStatsLabels()
	if err != nil {
		return nil, err
	}
	if withoutTrailingFilters {
		q, _ = q.ObsWithoutTrailingFilters()
		group = nil // read again from the changed query
	}
	q.AddTimeFilter(start.UnixNano(), end.UnixNano()-1)
	return e.run(q, true, group)
}

// Rows runs a log query (no `stats` pipe needed) over [start, end) and returns the rows.
func (e *Engine) Rows(qs string, start, end time.Time) ([]Result, error) {
	if err := e.limits.Check(qs, start, end); err != nil {
		return nil, err
	}
	q, err := logstorage.ParseQueryAtTimestamp(qs, end.UnixNano())
	if err != nil {
		return nil, err
	}
	q.AddTimeFilter(start.UnixNano(), end.UnixNano()-1)
	return e.run(q, false, nil)
}

// run executes q. group is the stats labels when the caller already read them from
// the same query (each read clones the query, a per-tick cost for alerts).
func (e *Engine) run(q *logstorage.Query, stats bool, group []string) ([]Result, error) {
	ctx, release, err := e.queryContext()
	if err != nil {
		return nil, err
	}
	defer release()
	if e.concurrency > 0 {
		q.ObsSetConcurrency(e.concurrency)
	}
	if stats && group == nil {
		var err error
		group, err = q.ObsStatsLabels()
		if err != nil {
			return nil, err
		}
	}
	var mu sync.Mutex
	var out []Result
	write := func(_ uint, db *logstorage.DataBlock) {
		// Column names and values point into the query's 64 KiB allocator chunks;
		// results outlive the query (the alert query cache keeps every tick), so copy
		// them out instead of pinning a chunk per kept value.
		// VictoriaLogs v1.54.0 app/vlselect/logsql/logsql.go:
		// ProcessStatsQueryRequest emits one series per non-group column;
		// stats_query_response.qtpl emits its column name as __name__.
		cols := db.GetColumns(false)
		for i := 0; i < db.RowsCount(); i++ {
			var labels []seed.Label
			for _, c := range cols {
				if !stats || slices.Contains(group, c.Name) {
					labels = append(labels, seed.Label{Name: strings.Clone(c.Name), Value: strings.Clone(c.Values[i])})
				}
			}
			var rows []Result
			if !stats {
				rows = append(rows, Result{Labels: labels})
			} else {
				for _, c := range cols {
					if slices.Contains(group, c.Name) {
						continue
					}
					v := strings.Clone(c.Values[i])
					if v == "[]" || strings.HasPrefix(v, `[{"vmrange":`) {
						var buckets []struct {
							VMRange string `json:"vmrange"`
							Hits    uint64 `json:"hits"`
						}
						if json.Unmarshal([]byte(v), &buckets) == nil {
							for _, b := range buckets {
								ls := append(append([]seed.Label{}, labels...), seed.Label{Name: "vmrange", Value: b.VMRange})
								rows = append(rows, Result{Name: c.Name + "_bucket", Labels: ls, Value: strconv.FormatUint(b.Hits, 10)})
							}
							continue
						}
					}
					rows = append(rows, Result{Name: strings.Clone(c.Name), Labels: labels, Value: v})
				}
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}
	}
	var qs logstorage.QueryStats
	qctx := logstorage.NewQueryContext(ctx, &qs, []logstorage.TenantID{{}}, q, false, nil)
	if err := e.s.RunQuery(qctx, write); err != nil {
		if ctx.Err() != nil {
			return nil, e.timeoutError()
		}
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, e.timeoutError()
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name+fmt.Sprint(out[i].Labels) < out[j].Name+fmt.Sprint(out[j].Labels)
	})
	return out, nil
}

func (e *Engine) timeoutError() error {
	return refusal("query exceeded -search.maxQueryDuration=%s", e.limits.MaxQueryDuration)
}

// The server starts its execution deadline before waiting for a concurrency slot.
func (e *Engine) queryContext() (context.Context, func(), error) {
	ctx := context.Background()
	cancel := func() {}
	if e.limits.runtimeSet || e.limits.MaxQueryDuration > 0 {
		ctx, cancel = context.WithTimeout(ctx, e.limits.MaxQueryDuration)
	}
	if e.slots == nil {
		return ctx, cancel, nil
	}
	select {
	case e.slots <- struct{}{}:
		return ctx, func() { <-e.slots; cancel() }, nil
	default:
	}
	queue, cancelQueue := context.WithTimeout(ctx, e.limits.MaxQueueDuration)
	defer cancelQueue()
	select {
	case e.slots <- struct{}{}:
		return ctx, func() { <-e.slots; cancel() }, nil
	case <-queue.Done():
		cancel()
		return nil, nil, refusal("query refused: -search.maxConcurrentRequests=%d reached; -search.maxQueueDuration=%s; -search.maxQueryDuration=%s", e.limits.MaxConcurrentRequests, e.limits.MaxQueueDuration, e.limits.MaxQueryDuration)
	}
}

// Float parses a stats result value.
func (r Result) Float() (float64, bool) {
	v, err := strconv.ParseFloat(r.Value, 64)
	return v, err == nil
}

// WithoutTrailingFilters returns qs as VictoriaLogs prints it with the `filter` pipes
// after the last `stats` pipe removed (a threshold), the way Stats does for the live check.
func WithoutTrailingFilters(qs string, at time.Time) (string, error) {
	q, err := Parse(qs, at)
	if err != nil {
		return "", err
	}
	out, _ := q.q.ObsWithoutTrailingFilters()
	return out.String(), nil
}

// FieldFilter is one filter on a single field of a parsed query (see ObsFieldFilters).
type FieldFilter = logstorage.ObsFieldFilter

// FieldFilters returns every filter on field before the first pipe.
func (q *Query) FieldFilters(field string) []FieldFilter { return q.q.ObsFieldFilters(field) }

// FilterFields returns the field of every filter the query evaluates on stored rows: the main filter,
// the `if (...)` condition of a stats function or a transforming pipe, `filter` pipes before the stats,
// and the filters of every nested source (in(...) subqueries, union, join). A stream filter reports
// "_stream" and one "_stream:<tag>" per tag it names. A query form the walker cannot read (an opaque
// pipe) is an error: the caller decides what that means.
func (q *Query) FilterFields() ([]string, error) {
	set := map[string]bool{}
	var walk func(n logstorage.ObsFilterTree)
	walk = func(n logstorage.ObsFilterTree) {
		if n.Kind == "stream" {
			set["_stream"] = true
			for _, tag := range n.Values {
				set["_stream:"+tag] = true
			}
		} else if n.Field != "" {
			set[n.Field] = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	_, err := q.q.ObsAlertTransform(func(n logstorage.ObsFilterTree) (string, error) {
		walk(n)
		return n.Text, nil
	}, func(string, []string) error { return nil })
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// TrailingFilterFields returns the fields of the `filter` pipes right after the last `stats` pipe: a
// threshold on a computed value, or a filter on a group key the stats kept (`by (f)`).
func (q *Query) TrailingFilterFields() []string { return q.q.ObsTrailingFilterFields() }
