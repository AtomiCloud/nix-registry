// Package vm runs MetricsQL on VictoriaMetrics' own storage and evaluator, inside
// this process (the same code as the VictoriaMetrics server v1.156.0; the storage
// lives in a temp dir, no socket is opened).
package vm

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/app/vminsert/common"
	"github.com/VictoriaMetrics/VictoriaMetrics/app/vmselect/netstorage"
	vmpromql "github.com/VictoriaMetrics/VictoriaMetrics/app/vmselect/promql"
	"github.com/VictoriaMetrics/VictoriaMetrics/app/vmstorage"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/fs"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"

	"github.com/AtomiCloud/obs/internal/engines"
	"github.com/AtomiCloud/obs/internal/seed"
)

// Engine is one in-process VictoriaMetrics.
type Engine struct{ dir string }

// Series is one query result series.
type Series struct {
	Labels []seed.Label
	Points int
	// Values holds the non-NaN values, in time order.
	Values []float64
}

// New starts the storage and loads every series of ds.
func New(ds *seed.Dataset) (*Engine, error) {
	engines.Init()
	dir, err := os.MkdirTemp(engines.TmpBase(), "obs-vm-")
	if err != nil {
		return nil, err
	}
	for k, v := range map[string]string{
		"storageDataPath":      dir,
		"retentionPeriod":      "100y",
		"search.disableCache":  "true",
		"search.latencyOffset": "0s",
	} {
		if flag.Lookup(k) == nil {
			continue
		}
		if err := flag.Set(k, v); err != nil {
			return nil, fmt.Errorf("vm flag %s: %w", k, err)
		}
	}
	vmstorage.Init(4, 5*time.Second, vmpromql.ResetRollupResultCacheIfNeeded)
	netstorage.InitTmpBlocksDir(dir + "/tmp")
	vmpromql.InitRollupResultCache("")

	var ctx common.InsertCtx
	ctx.Reset(0)
	n := 0
	for _, s := range ds.Series {
		lbls := make([]prompb.Label, 0, len(s.Labels))
		for _, l := range s.Labels {
			lbls = append(lbls, prompb.Label{Name: l.Name, Value: l.Value})
		}
		for i := range s.Ts {
			if err := ctx.WriteDataPoint(nil, lbls, s.Ts[i], s.Vals[i]); err != nil {
				return nil, err
			}
			n++
			if n%10000 == 0 {
				if err := ctx.FlushBufs(); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := ctx.FlushBufs(); err != nil {
		return nil, err
	}
	vmstorage.DebugFlush()
	if err := checkDropped(); err != nil {
		return nil, err
	}
	return &Engine{dir: dir}, nil
}

// Close stops the storage and removes its directory.
func (e *Engine) Close() {
	vmpromql.StopRollupResultCache()
	vmstorage.Stop()
	fs.MustRemoveDir(e.dir)
}

// Instant evaluates q at t the way Grafana alerting does: an instant query with
// VictoriaMetrics' default step (5 min), which a rollup without a window takes.
func (e *Engine) Instant(q string, t time.Time) ([]Series, error) {
	return e.exec(q, t, t, 5*time.Minute, true)
}

// Range evaluates q at every step in [start, end] (a dashboard panel).
func (e *Engine) Range(q string, start, end time.Time, step time.Duration) ([]Series, error) {
	return e.exec(q, start, end, step, false)
}

func (e *Engine) exec(q string, start, end time.Time, step time.Duration, instant bool) ([]Series, error) {
	rs, err := e.execSamples(q, start, end, step, instant)
	if err != nil {
		return nil, err
	}
	var out []Series
	for _, r := range rs {
		var values []float64
		for _, v := range r.Vals {
			if !math.IsNaN(v) {
				values = append(values, v)
			}
		}
		if len(values) > 0 {
			out = append(out, Series{Labels: r.Labels, Points: len(values), Values: values})
		}
	}
	return out, nil
}

// SamplesRange preserves timestamps and gaps for alert evaluation and reduce.
func (e *Engine) SamplesRange(q string, start, end time.Time, step time.Duration) ([]*seed.Series, error) {
	return e.execSamples(q, start, end, step, false)
}

// SamplesInstant uses VictoriaMetrics' default five-minute instant step.
func (e *Engine) SamplesInstant(q string, at time.Time) ([]*seed.Series, error) {
	return e.execSamples(q, at, at, 5*time.Minute, true)
}

func (e *Engine) execSamples(q string, start, end time.Time, step time.Duration, instant bool) ([]*seed.Series, error) {
	ec := &vmpromql.EvalConfig{
		Context:            context.Background(),
		Start:              start.UnixMilli(),
		End:                end.UnixMilli(),
		Step:               step.Milliseconds(),
		MaxPointsPerSeries: 1_000_000,
		MayCache:           false,
		RoundDigits:        100,
	}
	ec.QueryStats = vmpromql.NewQueryStats(q, nil, ec)
	rs, err := vmpromql.Exec(nil, ec, q, instant)
	if err != nil {
		return nil, err
	}
	var out []*seed.Series
	for _, r := range rs {
		var lbls []seed.Label
		if len(r.MetricName.MetricGroup) > 0 {
			lbls = append(lbls, seed.Label{Name: "__name__", Value: string(r.MetricName.MetricGroup)})
		}
		for _, t := range r.MetricName.Tags {
			lbls = append(lbls, seed.Label{Name: string(t.Key), Value: string(t.Value)})
		}
		ts := append([]int64(nil), r.Timestamps...)
		if instant {
			for i := range ts {
				ts[i] = start.UnixMilli()
			}
		}
		out = append(out, &seed.Series{Labels: lbls, Ts: ts, Vals: append([]float64(nil), r.Values...)})
	}
	return out, nil
}

// Append makes recording-rule output visible to later recording rules and alerts.
func (e *Engine) Append(series []*seed.Series) error {
	var ctx common.InsertCtx
	ctx.Reset(0)
	n := 0
	for _, s := range series {
		labels := make([]prompb.Label, 0, len(s.Labels))
		for _, l := range s.Labels {
			labels = append(labels, prompb.Label{Name: l.Name, Value: l.Value})
		}
		for i, t := range s.Ts {
			if err := ctx.WriteDataPoint(nil, labels, t, s.Vals[i]); err != nil {
				return err
			}
			n++
			if n%10000 == 0 {
				if err := ctx.FlushBufs(); err != nil {
					return err
				}
			}
		}
	}
	if err := ctx.FlushBufs(); err != nil {
		return err
	}
	vmstorage.DebugFlush()
	return checkDropped()
}
