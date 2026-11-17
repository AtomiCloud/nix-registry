// Package vl runs LogsQL on VictoriaLogs' own parser and storage (v1.54.0,
// lib/logstorage from the vendored third_party tree), inside this process.
package vl

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
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
}

// Engine is one in-process VictoriaLogs.
type Engine struct {
	dir string
	s   *logstorage.Storage
}

// New opens a storage in a temp dir and loads every stream of ds.
func New(ds *seed.Dataset) (*Engine, error) {
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
	e := &Engine{dir: dir, s: s}
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
	q, err := logstorage.ParseQueryAtTimestamp(qs, end.UnixNano())
	if err != nil {
		return nil, err
	}
	if _, err := q.ObsStatsLabels(); err != nil {
		return nil, err
	}
	if withoutTrailingFilters {
		q, _ = q.ObsWithoutTrailingFilters()
	}
	q.AddTimeFilter(start.UnixNano(), end.UnixNano()-1)
	return e.run(q, true)
}

// Rows runs a log query (no `stats` pipe needed) over [start, end) and returns the rows.
func (e *Engine) Rows(qs string, start, end time.Time) ([]Result, error) {
	q, err := logstorage.ParseQueryAtTimestamp(qs, end.UnixNano())
	if err != nil {
		return nil, err
	}
	q.AddTimeFilter(start.UnixNano(), end.UnixNano()-1)
	return e.run(q, false)
}

func (e *Engine) run(q *logstorage.Query, stats bool) ([]Result, error) {
	var mu sync.Mutex
	var out []Result
	write := func(_ uint, db *logstorage.DataBlock) {
		// Pipe order: the by-fields first, the stats result last.
		cols := db.GetColumns(false)
		for i := 0; i < db.RowsCount(); i++ {
			var r Result
			for ci, c := range cols {
				if stats && ci == len(cols)-1 {
					r.Value = c.Values[i]
					continue
				}
				r.Labels = append(r.Labels, seed.Label{Name: c.Name, Value: c.Values[i]})
			}
			mu.Lock()
			out = append(out, r)
			mu.Unlock()
		}
	}
	var qs logstorage.QueryStats
	qctx := logstorage.NewQueryContext(context.Background(), &qs, []logstorage.TenantID{{}}, q, false, nil)
	if err := e.s.RunQuery(qctx, write); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i].Labels) < fmt.Sprint(out[j].Labels) })
	return out, nil
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
